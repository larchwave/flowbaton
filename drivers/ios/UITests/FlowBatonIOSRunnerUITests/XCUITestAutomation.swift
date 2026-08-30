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

  /// typingTarget is the app to type into, once typed text has somewhere to land.
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
    guard canReceiveTyping(in: SnapshotAdapter(try app.snapshot())) else {
      throw AutomationError.precondition(
        "nothing on screen accepts typed text and no keyboard is open, so there "
          + "is nowhere to type; tap a text field first")
    }
    return app
  }

  /// keyboardIsUp answers the question the route's name asks. Existence is
  /// not the answer: a dismissed keyboard stays in the hierarchy parked
  /// below the screen, so `keyboards.firstMatch.exists` is true everywhere.
  /// KeyboardPresence decides on geometry instead.
  @MainActor
  static func keyboardIsUp(in app: XCUIApplication) -> Bool {
    let keyboard = app.keyboards.firstMatch
    guard keyboard.exists else { return false }
    // The app's own frame stands in for the screen: the runner drives one
    // full-screen app at a time, and XCUIScreen exposes no bounds.
    return KeyboardPresence.isPresented(keyboard: keyboard.frame, screen: app.frame)
  }

  func isKeyboardVisible(appIDs: [String]) throws -> Bool {
    try onMain { Self.keyboardIsUp(in: try Self.foregroundApp(among: appIDs)) }
  }

  /// A key press needs a keyboard, which is a stricter demand than
  /// typingTarget's: that one asks whether anything on screen could accept
  /// text, and a Reminders screen with an unfocused search field says yes.
  /// Pressing Return there answered 200 and took the whole runner process
  /// down with it -- typeText without keyboard focus raises an XCTIssue, not
  /// a Swift error, so nothing here can catch it. The host saw the death as
  /// a connection refused on its next request (sessions mmx22 and mmx23,
  /// both right after hide_keyboard). Reproduced and pinned live.
  func pressKey(_ key: String, appIDs: [String]) throws {
    guard let keyboardKey = Self.keyboardKeys[key] else {
      throw AutomationError.precondition("unsupported key \(key)")
    }
    try onMain {
      let app = try Self.foregroundApp(among: appIDs)
      _ = app.keyboards.firstMatch.waitForExistence(timeout: Self.keyboardWaitSeconds)
      guard Self.keyboardIsUp(in: app) else {
        throw AutomationError.precondition(
          "no keyboard is on screen, so there is no key to press; focus a text field first")
      }
      app.typeText(keyboardKey.rawValue)
    }
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
        heightPixels: screenshot.size.height * screenshot.scale,
        orientation: Self.wireOrientation(
          XCUIDevice.shared.orientation, screenPoints: points.size))
    }
  }

  func setOrientation(_ orientation: String) throws {
    guard let value = Self.orientations[orientation.uppercased()] else {
      throw AutomationError.precondition("unsupported orientation \(orientation)")
    }
    try onMain { XCUIDevice.shared.orientation = value }
  }

  /// setPermissions auto-answers springboard permission alerts.
  ///
  /// On a SIMULATOR the host owns permissions through `simctl privacy` and
  /// never calls this route. On HARDWARE there is no host-side TCC write —
  /// every tool answers the system dialog like a person would — so the rules
  /// arrive here and a main-RunLoop timer keeps answering matching alerts as
  /// they appear (`RunnerHostTests` pumps that RunLoop while serving).
  ///
  /// Rule keys are the flow's permission names ("camera", "location",
  /// "notifications", …) matched against the alert text; "all" matches any
  /// permission alert. Values: "allow" and "deny" ("unset" cannot exist on
  /// hardware and is refused).
  func setPermissions(_ permissions: [String: String]) throws {
    for (permission, grant) in permissions where grant != "allow" && grant != "deny" {
      throw AutomationError.precondition(
        "hardware answers permission dialogs, so \(permission) supports allow or deny, not \(grant)"
      )
    }
    try onMain {
      Self.permissionRules.merge(permissions) { _, newest in newest }
      Self.answerVisiblePermissionAlerts()
      Self.startPermissionWatcherIfNeeded()
    }
  }

  @MainActor private static var permissionRules: [String: String] = [:]
  @MainActor private static var permissionWatcher: Timer?

  /// Buttons that grant, in the order iOS tends to present them.
  private static let allowButtons = [
    "Allow While Using App", "Allow Once", "Allow", "OK", "Continue",
  ]
  private static let denyButtons = ["Don't Allow", "Don’t Allow", "Deny"]

  @MainActor private static func startPermissionWatcherIfNeeded() {
    guard permissionWatcher == nil else { return }
    let timer = Timer(timeInterval: 0.5, repeats: true) { _ in
      MainActor.assumeIsolated { answerVisiblePermissionAlerts() }
    }
    RunLoop.main.add(timer, forMode: .common)
    permissionWatcher = timer
  }

  /// Answers every springboard alert a rule matches. Unmatched alerts stay
  /// untouched: an unrelated system dialog is not this route's to dismiss.
  @MainActor private static func answerVisiblePermissionAlerts() {
    guard !permissionRules.isEmpty else { return }
    let springboard = XCUIApplication(bundleIdentifier: springboardID)
    let alerts = springboard.alerts
    for index in 0..<alerts.count {
      let alert = alerts.element(boundBy: index)
      guard alert.exists, let grant = rule(for: alert.label) else { continue }
      let titles = grant == "allow" ? allowButtons : denyButtons
      for title in titles where alert.buttons[title].exists {
        alert.buttons[title].tap()
        break
      }
    }
  }

  @MainActor private static func rule(for alertText: String) -> String? {
    let text = alertText.lowercased()
    for (permission, grant) in permissionRules where permission != "all" {
      if text.contains(permission.lowercased()) {
        return grant
      }
    }
    return permissionRules["all"]
  }

  // MARK: - Hierarchy

  /// viewHierarchy serves the app under test AND the system chrome around it.
  ///
  /// The status bar and any system alert belong to the springboard, not to the
  /// app, so an app-only snapshot cannot see the clock, the signal bars, the
  /// battery, or a permission dialog sitting over the app. They are therefore a
  /// second subtree under the public zero-sized root.
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
        systemChrome: Self.isSpringboard(app)
          ? [] : Self.statusBarSnapshots() + Self.systemAlertSnapshots(),
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

  /// systemAlertSnapshots takes the springboard's alerts and nothing else.
  ///
  /// A permission alert belongs to the springboard, so an app-only snapshot
  /// cannot see it while it covers the app: the served tree looks unchanged,
  /// every tap lands on a dialog nobody reported, and an exploring agent reads
  /// the screen as frozen. Serving the alert beside the status bar puts the
  /// blocking dialog and its buttons where a selector can reach them.
  ///
  /// Failures are swallowed for the same reason as the status bar: an alert
  /// that cannot be snapshot is worth less than the app.
  @MainActor
  static func systemAlertSnapshots() -> [any AccessibilitySnapshot] {
    let alerts = XCUIApplication(bundleIdentifier: springboardID).alerts
    return alerts.allElementsBoundByAccessibilityElement.compactMap {
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
    "Space": .space, "space": .space,
    "Escape": .escape, "escape": .escape,
  ]

  private static let orientations: [String: UIDeviceOrientation] = [
    "PORTRAIT": .portrait,
    "LANDSCAPE_LEFT": .landscapeLeft,
    "LANDSCAPE_RIGHT": .landscapeRight,
    "UPSIDE_DOWN": .portraitUpsideDown,
  ]

  private static func wireOrientation(
    _ orientation: UIDeviceOrientation, screenPoints: CGSize
  ) -> String {
    switch orientation {
    case .portrait:
      return "portrait"
    case .portraitUpsideDown:
      return "portrait-upside-down"
    case .landscapeLeft:
      return "landscape-left"
    case .landscapeRight:
      return "landscape-right"
    default:
      // A freshly created simulator reports .unknown until its first rotation,
      // and hardware lying flat reports faceUp/faceDown. The screen itself
      // still renders one way, so answer from its geometry instead of
      // refusing the whole session before any command ran.
      return screenPoints.width > screenPoints.height ? "landscape-left" : "portrait"
    }
  }
}

/// SnapshotAdapter is the whole reason the hierarchy walk is testable.
///
/// It is the only place that knows XCUITest's element-type numbering, and it
/// exists so `hierarchyPayload` can be exercised against a plain struct in
/// `swift test` rather than only against a booted simulator.
struct SnapshotAdapter: AccessibilitySnapshot {
  let identifier: String
  let frameOrigin: (x: Double, y: Double)
  let frameSize: (width: Double, height: Double)
  let stringValue: String?
  let title: String?
  let label: String
  let elementTypeCode: Int
  let enabled: Bool
  let placeholder: String?
  let selected: Bool
  let focused: Bool
  let isKeyboard: Bool
  let childSnapshots: [any AccessibilitySnapshot]

  @MainActor
  init(_ snapshot: XCUIElementSnapshot) {
    identifier = snapshot.identifier
    frameOrigin = (snapshot.frame.origin.x, snapshot.frame.origin.y)
    frameSize = (snapshot.frame.width, snapshot.frame.height)
    // XCUITest types value as Any?; the contract types it as a string.
    stringValue = snapshot.value.map { String(describing: $0) }
    title = snapshot.title.isEmpty ? nil : snapshot.title
    label = snapshot.label
    elementTypeCode = Int(snapshot.elementType.rawValue)
    enabled = snapshot.isEnabled
    placeholder = snapshot.placeholderValue
    selected = snapshot.isSelected
    focused = snapshot.hasFocus
    isKeyboard = snapshot.elementType == .keyboard
    childSnapshots = snapshot.children.map(SnapshotAdapter.init)
  }
}
