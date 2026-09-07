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

  func touch(x: Double, y: Double, duration: Double?, appID: String?) throws {
    try onMain {
      let anchor = try Self.anchor(appIDs: appID.map { [$0] } ?? [])
      let mapping = Self.screenMapping(of: anchor)
      let target = Self.coordinate(x: x, y: y, in: anchor, mapping: mapping)
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
    // Without appIDs a swipe is a screen-space drag. With them the host is
    // saying the points came from that app's hierarchy (an element-anchored
    // scroll or swipe), so the drag is anchored in the app's own space — see
    // anchor(appIDs:).
    // `duration` on the wire is how long the drag takes. XCUITest's
    // press(forDuration:thenDragTo:) spends that time HOLDING at the start
    // point before it moves, and half a second of hold over a SwiftUI Link
    // activated the link instead of scrolling the page beneath it (issue #12).
    // So the hold stays at zero and the duration becomes the drag's velocity.
    let distance = hypot(endX - startX, endY - startY)
    let velocity: XCUIGestureVelocity =
      duration > 0 && distance > 0
      ? XCUIGestureVelocity(rawValue: CGFloat(distance / duration)) : .default
    try onMain {
      let anchor = try Self.anchor(appIDs: appIDs)
      let mapping = Self.screenMapping(of: anchor)
      Self.coordinate(x: startX, y: startY, in: anchor, mapping: mapping)
        .press(
          forDuration: 0,
          thenDragTo: Self.coordinate(x: endX, y: endY, in: anchor, mapping: mapping),
          withVelocity: velocity, thenHoldForDuration: 0)
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
    guard try waitUntil({ canReceiveTyping(in: SnapshotAdapter(try app.snapshot())) }) else {
      throw AutomationError.precondition(
        "nothing on screen accepts typed text and no keyboard is open, so there "
          + "is nowhere to type; tap a text field first")
    }
    return app
  }

  /// pollSeconds is how often a settle condition is re-read while waiting.
  static let pollSeconds: TimeInterval = 0.05

  /// waitUntil polls a condition until keyboardWaitSeconds have passed.
  ///
  /// waitForExistence on the keyboard element cannot do this job: the element
  /// exists before, during and after the animation -- that is the whole
  /// finding this file is built on -- so it returns at once and measures
  /// nothing. A tap that focuses a field returns before the keyboard has
  /// finished animating in, and reading the frame right then says the
  /// keyboard is still parked, refusing a command that was about to be valid.
  @MainActor
  static func waitUntil(_ condition: () throws -> Bool) rethrows -> Bool {
    let deadline = Date().addingTimeInterval(keyboardWaitSeconds)
    while true {
      if try condition() {
        return true
      }
      if Date() >= deadline {
        return false
      }
      // An expectation nobody fulfills is the sanctioned XCUITest pause; it
      // keeps the main run loop turning instead of blocking it.
      _ = XCTWaiter().wait(
        for: [XCTestExpectation(description: "settle")], timeout: pollSeconds)
    }
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
    guard let name = KeyboardKeyName.from(wire: key),
      let keyboardKey = Self.keyboardKeys[name]
    else {
      throw AutomationError.precondition("unsupported key \(key)")
    }
    try onMain {
      let app = try Self.foregroundApp(among: appIDs)
      guard try Self.waitUntil({ Self.keyboardIsUp(in: app) }) else {
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
    // Points and pixels both come from one screenshot: its size is the screen
    // in points as currently rotated, its scale turns that into pixels.
    // specs/02-device-drivers.md:28 has iOS reporting points as the grid unit and
    // pixels separately, and the host scales crops between them. The
    // springboard's frame is not used because it stays portrait after a
    // rotation while the hierarchy and the screenshot have turned (issue #18).
    try onMain {
      let screenshot = XCUIScreen.main.screenshot().image
      let points = screenshot.size
      return DeviceInfoPayload(
        widthPoints: points.width,
        heightPoints: points.height,
        widthPixels: points.width * screenshot.scale,
        heightPixels: points.height * screenshot.scale,
        orientation: Self.wireOrientation(XCUIDevice.shared.orientation, screenPoints: points))
    }
  }

  func setOrientation(_ orientation: String) throws {
    guard let value = Self.orientations[orientation] else {
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

  // MARK: - Hit testing

  /// hittable is the one place the runner resolves an element itself, and it
  /// resolves the host's element, not its own: the query is narrowed by the
  /// identifier or label the host observed and then to the element whose
  /// frame renders as the host's bounds do, so the answer is about the same
  /// node the host is looking at. Anything but one match is reported as such
  /// and the host falls back to geometry. A field under an opaque footer is
  /// fully inside the screen and not hittable; bounds alone said it was
  /// visible (issue #14).
  func hittable(appID: String, frame: WireFrame, identifier: String?, label: String?) throws
    -> HittablePayload
  {
    try onMain {
      let app = XCUIApplication(bundleIdentifier: appID)
      guard app.state == .runningForeground else {
        throw AutomationError.precondition("\(appID) is not in the foreground")
      }
      var query = app.descendants(matching: .any)
      if let identifier, !identifier.isEmpty {
        query = query.matching(identifier: identifier)
      } else if let label, !label.isEmpty {
        query = query.matching(NSPredicate(format: "label == %@", label))
      }
      // Frames come from each element's snapshot, the same source as the
      // hierarchy the host read. Inside an iPhone-compatibility window on an
      // iPad an element's `frame` property is in another grid than its
      // snapshot's, and no element matched.
      let matches = query.allElementsBoundByIndex.filter {
        Self.rendersAs(frame: (try? $0.snapshot().frame) ?? $0.frame, wire: frame)
      }
      return HittablePayload(
        hittable: matches.count == 1 && matches[0].isHittable, matches: matches.count)
    }
  }

  /// rendersAs mirrors the host's bounds rendering: each edge is floored at
  /// single precision on both sides first, so the frame the host sends back
  /// matches the element it came from and no other.
  static func rendersAs(frame: CGRect, wire: WireFrame) -> Bool {
    func floored(_ value: Double) -> Int { Int(floor(Double(Float(value)))) }
    return floored(frame.minX) == floored(wire.x) && floored(frame.minY) == floored(wire.y)
      && floored(frame.maxX) == floored(wire.x + wire.width)
      && floored(frame.maxY) == floored(wire.y + wire.height)
  }

  // MARK: - Helpers

  /// onMain runs XCUITest work where XCUITest expects to be run.
  ///
  /// The result must be Sendable because it crosses back to the caller's thread.
  /// That constraint is load-bearing rather than incidental: it is what keeps
  /// XCUIApplication and XCUIElement instances from escaping the main actor,
  /// which is the mistake this helper exists to prevent.
  private func onMain<T: Sendable>(_ work: @MainActor () throws -> T) throws -> T {
    // Every XCUITest call in this file goes through here, which makes it the
    // one place that can turn an issue XCUITest recorded into the error of
    // the command that caused it. See AutomationIssues.
    try AutomationIssues.shared.capture {
      if Thread.isMainThread {
        return try MainActor.assumeIsolated(work)
      }
      return try DispatchQueue.main.sync { try MainActor.assumeIsolated(work) }
    }
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
    var states: [(appID: String, rawValue: Int)] = []
    for appID in appIDs {
      let app = XCUIApplication(bundleIdentifier: appID)
      let state = app.state
      if state == .runningForeground {
        return app
      }
      states.append((appID: appID, rawValue: Int(state.rawValue)))
    }
    // What each app was doing instead, not merely that none of them was in
    // front: an app in the background is a flow that navigated away, an app
    // that is not running is a crash, and the caller has to tell them apart.
    throw AutomationError.precondition(ForegroundMiss.message(states))
  }

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

  /// anchor is the application whose coordinate space a gesture's points are
  /// in.
  ///
  /// Without appIDs that is the springboard, whose space is the screen
  /// deviceInfo reports, so an authored screen point lands where the author
  /// measured it. With appIDs it is the first of them in front: a point the
  /// host took from an app's hierarchy is in that app's own space, which is
  /// not the screen's inside an iPhone-compatibility window on iPad (issue
  /// #15), and a touch anchored elsewhere while that app shows its own alert
  /// is handled by XCTest as an interruption and lands under the alert (issue
  /// #17). A named app that is not in front is refused rather than tapped
  /// through whatever is: that would report success for the wrong app.
  @MainActor
  static func anchor(appIDs: [String]) throws -> XCUIApplication {
    if appIDs.isEmpty {
      return XCUIApplication(bundleIdentifier: springboardID)
    }
    return try foregroundApp(among: appIDs)
  }

  /// screenMapping is the map from the anchor's coordinate space onto the
  /// screen XCTest synthesises events on.
  ///
  /// It is the identity for the springboard and for an app that fills the
  /// screen. An iPhone-only app on an iPad runs inside a compatibility
  /// window: its hierarchy, and XCTest's own anchor for it, stay in the app's
  /// 390x844-style grid, while the window is drawn scaled to fit the screen
  /// and centred on it. Anchoring alone left a tap at the grid point, off the
  /// window (issue #15), so the point is scaled and shifted the way the
  /// window is. The screen size comes from a screenshot, the one reading
  /// that turns with the device (see deviceInfo).
  @MainActor
  static func screenMapping(of anchor: XCUIApplication) -> CGAffineTransform {
    let app = anchor.frame
    guard !isSpringboard(anchor), app.origin == .zero, app.width > 0, app.height > 0 else {
      return .identity
    }
    let screen = XCUIScreen.main.screenshot().image.size
    guard app.size != screen else { return .identity }
    let scale = min(screen.width / app.width, screen.height / app.height)
    return CGAffineTransform(
      translationX: (screen.width - app.width * scale) / 2,
      y: (screen.height - app.height * scale) / 2
    ).scaledBy(x: scale, y: scale)
  }

  /// coordinate turns the contract's absolute points into an XCUICoordinate in
  /// the anchor's coordinate space, mapped onto the screen.
  @MainActor
  static func coordinate(
    x: Double, y: Double, in anchor: XCUIApplication, mapping: CGAffineTransform
  ) -> XCUICoordinate {
    let point = CGPoint(x: x, y: y).applying(mapping)
    return
      anchor
      .coordinate(withNormalizedOffset: .zero)
      .withOffset(CGVector(dx: point.x, dy: point.y))
  }

  // Keyed on the contract vocabulary rather than on spellings: a string
  // table drifts from the wire values one entry at a time, and every entry
  // it misses is a request the runner refuses against its own contract.
  private static let keyboardKeys: [KeyboardKeyName: XCUIKeyboardKey] = [
    .delete: .delete,
    .return: .return,
    .enter: .return,
    .tab: .tab,
    .space: .space,
    .escape: .escape,
  ]

  // The exact SetOrientationRequest enum from contracts/v0/ios-http.json.
  // Keyed on other spellings, this table refused every landscape request the
  // host sent, against the runner's own contract (issue #18).
  private static let orientations: [String: UIDeviceOrientation] = [
    "portrait": .portrait,
    "landscapeLeft": .landscapeLeft,
    "landscapeRight": .landscapeRight,
    "upsideDown": .portraitUpsideDown,
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
