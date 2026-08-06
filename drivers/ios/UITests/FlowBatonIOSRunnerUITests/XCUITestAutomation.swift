import Foundation
import XCTest

@testable import FlowBatonIOSRunner

/// The real `DeviceAutomation`, backed by XCUITest.
///
/// This is the half of the runner that cannot live in the framework: XCTest only
/// links into a test bundle, and XCUIApplication only works inside a running
/// test on a simulator. Everything above it — parsing, routing, the wire
/// contract, the port, the hierarchy shape — is in the framework precisely so it
/// can be tested with `swift test` on a Mac. What is left here is binding, and
/// binding is what a booted simulator is for.
///
/// EVERY method hops to the main actor. XCUITest's types are main-actor isolated
/// under Swift 6, and the HTTP server calls this from its own connection thread,
/// so the hop is not ceremony: it is why `RunnerHostTests` keeps a RunLoop
/// running on the main thread while it serves. Without a main thread pumping
/// work, every request would deadlock.
///
/// Coordinates are taken as given. The host resolves elements itself from the
/// hierarchy it fetched — that is what internal/engine's lookup and stability
/// logic is for — so a runner that re-resolved by label would answer a different
/// question than the one asked and disagree with the host's own view.
final class XCUITestAutomation: DeviceAutomation, @unchecked Sendable {

  /// The springboard is the contract's answer to "which app is in front" when
  /// none of the requested ones is.
  static let springboardID = "com.apple.springboard"

  // MARK: - Apps

  func runningApp(appIDs: [String]) throws -> String {
    try onMain {
      for appID in appIDs
      where XCUIApplication(bundleIdentifier: appID).state == .runningForeground {
        return appID
      }
      return Self.springboardID
    }
  }

  func launchApp(bundleID: String) throws {
    try onMain {
      let app = XCUIApplication(bundleIdentifier: bundleID)
      app.launch()
      // launch() returns before the app is necessarily interactive. Waiting here
      // rather than letting the next command fail keeps a launch failure
      // reported as a launch failure.
      if !app.wait(for: .runningForeground, timeout: 30) {
        throw AutomationError.timeout("\(bundleID) did not come to the foreground")
      }
    }
  }

  func terminateApp(appID: String) throws {
    try onMain { XCUIApplication(bundleIdentifier: appID).terminate() }
  }

  // MARK: - Touch

  func touch(x: Double, y: Double, duration: Double?) throws {
    try onMain {
      let target = Self.coordinate(x: x, y: y)
      guard let duration else {
        target.tap()
        return
      }
      target.press(forDuration: duration)
    }
  }

  func swipe(
    startX: Double, startY: Double, endX: Double, endY: Double, duration: Double, appID: String?
  ) throws {
    try swipeV2(
      startX: startX, startY: startY, endX: endX, endY: endY, duration: duration,
      appIDs: appID.map { [$0] } ?? [])
  }

  func swipeV2(
    startX: Double, startY: Double, endX: Double, endY: Double, duration: Double, appIDs: [String]
  ) throws {
    // appIDs is deliberately unused for the gesture: a swipe is a screen-space
    // drag, and pinning it to one app's coordinate space would move the gesture
    // whenever that app is not full-screen.
    _ = appIDs
    try onMain {
      Self.coordinate(x: startX, y: startY)
        .press(forDuration: max(duration, 0), thenDragTo: Self.coordinate(x: endX, y: endY))
    }
  }

  // MARK: - Text

  func inputText(_ text: String, appIDs: [String]) throws {
    // typeText on the application types into whatever has focus, which is what
    // the host means: it has already tapped the field. The focus is CHECKED
    // first — see typingTarget.
    try onMain { try Self.typingTarget(among: appIDs).typeText(text) }
  }

  func eraseText(charactersToErase: Int, appIDs: [String]) throws {
    guard charactersToErase > 0 else { return }
    try onMain {
      try Self.typingTarget(among: appIDs)
        .typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: charactersToErase))
    }
  }

  /// keyboardWaitSeconds is the wait specs/04-wire-protocols.md §3 records for
  /// inputText and eraseText: the keyboard animates in after the tap that
  /// focused the field, and typing into the gap types nowhere.
  static let keyboardWaitSeconds: TimeInterval = 1

  /// typingTarget is the app to type into, once something is actually focused.
  ///
  /// Typing with nothing focused makes XCUITest record "Neither element nor any
  /// descendant has keyboard focus" — an XCTIssue, not a Swift error, so it
  /// cannot be caught and the request would answer 200 for a command that typed
  /// nothing. Refusing here turns that into an error the host can report against
  /// the command that caused it.
  @MainActor
  static func typingTarget(among appIDs: [String]) throws -> XCUIApplication {
    let app = try foregroundApp(among: appIDs)
    _ = app.keyboards.firstMatch.waitForExistence(timeout: keyboardWaitSeconds)
    guard hasKeyboardFocus(in: SnapshotAdapter(try app.snapshot())) else {
      throw AutomationError.precondition(
        "nothing on screen has keyboard focus, so there is nowhere to type; "
          + "tap a text field first")
    }
    return app
  }

  func isKeyboardVisible(appIDs: [String]) throws -> Bool {
    try onMain { try Self.foregroundApp(among: appIDs).keyboards.firstMatch.exists }
  }

  func pressKey(_ key: String) throws {
    guard let keyboardKey = Self.keyboardKeys[key] else {
      throw AutomationError.precondition("unsupported key \(key)")
    }
    try onMain { try Self.foregroundApp(among: []).typeText(keyboardKey.rawValue) }
  }

  func pressButton(_ button: String) throws {
    switch button.uppercased() {
    case "HOME":
      try onMain { XCUIDevice.shared.press(.home) }
    case "VOLUME_UP", "VOLUMEUP", "VOLUME_DOWN", "VOLUMEDOWN", "LOCK":
      // Not reachable through XCUITest on a simulator. Refusing beats a silent
      // no-op, which the host would record as a completed step.
      throw AutomationError.precondition("\(button) is not available on the simulator")
    default:
      throw AutomationError.precondition("unsupported button \(button)")
    }
  }

  // MARK: - Screen

  func screenshot(compressed: Bool) throws -> Data {
    try onMain {
      let image = XCUIScreen.main.screenshot().image
      // compressed selects JPEG, which is the point of the flag: a PNG of a
      // retina screen is megabytes per step and the host keeps every one.
      if compressed, let jpeg = image.jpegData(compressionQuality: 0.7) {
        return jpeg
      }
      guard let png = image.pngData() else {
        throw AutomationError.internalFailure("the screenshot could not be encoded")
      }
      return png
    }
  }

  func isScreenStatic() throws -> Bool {
    // Two frames a beat apart. The host polls this and owns the settle policy,
    // so the answer here only has to be "did anything move just now".
    let first = try onMain { XCUIScreen.main.screenshot().pngRepresentation }
    Thread.sleep(forTimeInterval: 0.1)
    return try first == onMain { XCUIScreen.main.screenshot().pngRepresentation }
  }

  func deviceInfo() throws -> DeviceInfoPayload {
    // Points from the springboard's frame, pixels from a screenshot's own scale.
    // specs/02-device-drivers.md:28 has iOS reporting points as the grid unit and
    // pixels separately, and the host scales crops between them. Each value is
    // read from its native coordinate space.
    try onMain {
      let screenshot = XCUIScreen.main.screenshot().image
      let points = XCUIApplication(bundleIdentifier: Self.springboardID).frame
      return DeviceInfoPayload(
        widthPoints: points.width,
        heightPoints: points.height,
        widthPixels: screenshot.size.width * screenshot.scale,
        heightPixels: screenshot.size.height * screenshot.scale)
    }
  }

  func setOrientation(_ orientation: String) throws {
    guard let value = Self.orientations[orientation.uppercased()] else {
      throw AutomationError.precondition("unsupported orientation \(orientation)")
    }
    try onMain { XCUIDevice.shared.orientation = value }
  }

  func setPermissions(_ permissions: [String: String]) throws {
    // Simulator permissions are granted through simctl by the HOST, which owns
    // the udid and already shells out to install and launch. Accepting the
    // request here and doing nothing would report a permission as granted when
    // it is not, so this refuses and names where the work belongs.
    _ = permissions
    throw AutomationError.precondition(
      "permissions are set by the host through simctl privacy, not by the runner")
  }

  // MARK: - Hierarchy

  /// viewHierarchy serves the app under test AND the system chrome behind it.
  ///
  /// The status bar belongs to the springboard, not to the app, so an app-only
  /// snapshot cannot see the clock, the signal bars or the battery. The status
  /// bar is therefore a second subtree under the public zero-sized root.
  ///
  /// The springboard is deliberately NOT required to be in the foreground — it
  /// never is while an app is up, which is exactly when its status bar matters.
  /// A springboard that cannot be snapshot is dropped rather than fatal: losing
  /// the clock is worth less than losing the app.
  func viewHierarchy(appIDs: [String], excludeKeyboardElements: Bool) throws -> Data {
    let payload: WireHierarchy = try onMain {
      let app = try Self.foregroundApp(among: appIDs)
      let snapshot: XCUIElementSnapshot
      do {
        snapshot = try app.snapshot()
      } catch {
        throw AutomationError.timeout("the accessibility hierarchy could not be captured: \(error)")
      }
      return hierarchyPayload(
        app: SnapshotAdapter(snapshot),
        systemChrome: Self.isSpringboard(app) ? [] : Self.statusBarSnapshots(),
        excludeKeyboardElements: excludeKeyboardElements)
    }
    do {
      return try JSONEncoder().encode(payload)
    } catch {
      throw AutomationError.internalFailure("the hierarchy could not be encoded: \(error)")
    }
  }

  // MARK: - Helpers

  /// onMain runs XCUITest work where XCUITest expects to be run.
  ///
  /// The result must be Sendable because it crosses back to the caller's thread.
  /// That constraint is load-bearing rather than incidental: it is what keeps
  /// XCUIApplication and XCUIElement instances from escaping the main actor,
  /// which is the mistake this helper exists to prevent.
  private func onMain<T: Sendable>(_ work: @MainActor () throws -> T) throws -> T {
    if Thread.isMainThread {
      return try MainActor.assumeIsolated(work)
    }
    return try DispatchQueue.main.sync { try MainActor.assumeIsolated(work) }
  }

  /// foregroundApp resolves which app a request is about.
  ///
  /// An empty filter resolves to the springboard, which is NOT the same as
  /// "whatever is in front" — XCUITest offers no way to ask that from here, and
  /// a springboard snapshot contains the springboard, not the app on top of it.
  /// The caller therefore names the application whose hierarchy it needs; the
  /// host reports when no application filter is available.
  ///
  /// A filter that names apps and matches none is a precondition failure rather
  /// than a fallback: acting on a different app is the worst outcome available,
  /// because the step then appears to succeed.
  @MainActor
  static func foregroundApp(among appIDs: [String]) throws -> XCUIApplication {
    if appIDs.isEmpty {
      return XCUIApplication(bundleIdentifier: springboardID)
    }
    for appID in appIDs {
      let app = XCUIApplication(bundleIdentifier: appID)
      if app.state == .runningForeground {
        return app
      }
    }
    throw AutomationError.precondition(
      "none of \(appIDs.joined(separator: ", ")) is in the foreground")
  }

  /// coordinate turns the contract's absolute points into an XCUICoordinate,
  /// anchored on the springboard so the space matches what deviceInfo reports.
  /// statusBarSnapshots takes the springboard's status bars and nothing else.
  ///
  /// Only status bars are included. Snapshotting the whole springboard would
  /// expose home-screen icons to selectors while another app is active.
  ///
  /// Failures are swallowed on purpose. The status bar is a bonus; a flow that
  /// loses the clock still works, and one that loses the app does not.
  @MainActor
  static func statusBarSnapshots() -> [any AccessibilitySnapshot] {
    let bars = XCUIApplication(bundleIdentifier: springboardID).statusBars
    return bars.allElementsBoundByAccessibilityElement.compactMap {
      guard let snapshot = try? $0.snapshot() else { return nil }
      return SnapshotAdapter(snapshot)
    }
  }

  /// isSpringboard keeps the springboard from being snapshot twice when it is
  /// itself the app under test — the home screen would then appear in the tree
  /// once as the app and once as the chrome behind it.
  @MainActor
  static func isSpringboard(_ app: XCUIApplication) -> Bool {
    app == XCUIApplication(bundleIdentifier: springboardID)
  }

  @MainActor
  static func coordinate(x: Double, y: Double) -> XCUICoordinate {
    XCUIApplication(bundleIdentifier: springboardID)
      .coordinate(withNormalizedOffset: .zero)
      .withOffset(CGVector(dx: x, dy: y))
  }

  private static let keyboardKeys: [String: XCUIKeyboardKey] = [
    "Enter": .return, "Return": .return, "return": .return,
    "Backspace": .delete, "delete": .delete,
    "Tab": .tab, "tab": .tab,
    "Escape": .escape, "escape": .escape,
  ]

  private static let orientations: [String: UIDeviceOrientation] = [
    "PORTRAIT": .portrait,
    "LANDSCAPE_LEFT": .landscapeLeft,
    "LANDSCAPE_RIGHT": .landscapeRight,
    "UPSIDE_DOWN": .portraitUpsideDown,
  ]
}

/// SnapshotAdapter is the whole reason the hierarchy walk is testable.
///
/// It is the only place that knows XCUITest's element-type numbering, and it
/// exists so `hierarchyPayload` can be exercised against a plain struct in
/// `swift test` rather than only against a booted simulator.
struct SnapshotAdapter: AccessibilitySnapshot {
  let snapshot: XCUIElementSnapshot

  init(_ snapshot: XCUIElementSnapshot) {
    self.snapshot = snapshot
  }

  var identifier: String { snapshot.identifier }
  var frameOrigin: (x: Double, y: Double) { (snapshot.frame.origin.x, snapshot.frame.origin.y) }
  var frameSize: (width: Double, height: Double) { (snapshot.frame.width, snapshot.frame.height) }
  // XCUITest types value as Any?; the contract types it as a string.
  var stringValue: String? { snapshot.value.map { String(describing: $0) } }
  var title: String? { snapshot.title.isEmpty ? nil : snapshot.title }
  var label: String { snapshot.label }
  var elementTypeCode: Int { Int(snapshot.elementType.rawValue) }
  var enabled: Bool { snapshot.isEnabled }
  var placeholder: String? { snapshot.placeholderValue }
  var selected: Bool { snapshot.isSelected }
  var focused: Bool { snapshot.hasFocus }
  var isKeyboard: Bool { snapshot.elementType == .keyboard }
  var childSnapshots: [any AccessibilitySnapshot] { snapshot.children.map(SnapshotAdapter.init) }
}
