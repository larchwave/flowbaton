import Foundation
import XCTest

@testable import FlowBatonIOSRunner

/// The runner's entry point.
///
/// There is no `main` here, and there cannot be: XCUITest only drives other apps
/// from inside a UI-test bundle, so the process that serves the wire has to BE a
/// test. `xcodebuild test` launches it, it binds the port the host told it to,
/// and it serves until the host is done and kills it.
///
/// That is why this file is a test that deliberately does not finish quickly. It
/// is the one place in the project where a long-running test is the product
/// rather than a smell.
final class RunnerHostTests: XCTestCase {

  /// How long the runner serves before giving up on its own.
  ///
  /// The host normally kills the process when the suite ends. This is the
  /// backstop for when it cannot — a crashed host, a cancelled CI job — because
  /// an orphaned runner holding a port is worse than one that exits.
  static let lifetimeVariable = "FLOWBATON_RUNNER_LIFETIME_SECONDS"
  static let defaultLifetime: TimeInterval = 3600

  /// The opt-in. Serving is not something to do by accident, and the guard is
  /// HERE rather than as a skipped test in the scheme because a scheme skip
  /// cannot be overridden by -only-testing — the host's own launch became a
  /// silent no-op that reported success and served nothing.
  static let serveVariable = "FLOWBATON_RUNNER_SERVE"

  override func setUpWithError() throws {
    try super.setUpWithError()
    // A newly created Simulator reports XCUIDevice orientation as unknown until
    // the test runner establishes one. The serving test is the production
    // process, so initialize the real device before either route can run.
    try XCUITestAutomation().setOrientation("PORTRAIT")
    // WITHOUT this the runner dies on the first device-level failure. XCUITest
    // records a gesture it could not synthesize as an XCTIssue, and by default a
    // issue tears the test down — which here means the server goes
    // away and later commands fail with "connection refused". Continue serving
    // so the command that failed can return its own error.
    continueAfterFailure = true
  }

  /// XCTest funnels every recorded failure through here, which is the only
  /// place a serving runner can see one: XCUITest records an issue instead of
  /// throwing, so the command that caused it returns normally and answers 200
  /// for work that never happened.
  ///
  /// Command issues go to the host. Outside a captured command, XCTest must
  /// still see failures: this bundle also contains the release smoke test,
  /// and its assertions must be able to fail the release gate.
  override func record(_ issue: XCTIssue) {
    guard issue.type != .thrownError || !(issue.associatedError is XCTSkip) else {
      super.record(issue)
      return
    }
    if !AutomationIssues.shared.recordIfCapturing(issue.compactDescription) {
      super.record(issue)
    }
  }

  func testServeTheWireUntilTheHostIsDone() throws {
    let environment = ProcessInfo.processInfo.environment
    guard environment[Self.serveVariable] == "1" else {
      throw XCTSkip(
        "set \(Self.serveVariable)=1 to serve; this test is the runner, not an assertion")
    }
    let port = try RunnerPort.resolve(environment)
    let runner = try RunnerIdentity.resolve(environment)
    let lifetime = Self.lifetime(environment)

    let server = LoopbackHTTPServer(
      port: port, automation: XCUITestAutomation(), runner: runner)
    let bound = try server.start(timeout: 10)
    defer { server.stop() }

    // Printed, not just logged: the host reads the runner's output to know it is
    // up, and "which port did it actually get" is the first question when a
    // connection is refused.
    print("flowbaton-runner listening on 127.0.0.1:\(bound) as \(runner ?? "no id")")
    XCTAssertEqual(bound, port, "the runner bound a different port than the host was told")

    // Serving happens on the server's own thread; this one only has to stay
    // alive. A RunLoop rather than a sleep so the XCTest machinery keeps
    // running, which is what the automation needs to work at all.
    let deadline = Date().addingTimeInterval(lifetime)
    while Date() < deadline {
      RunLoop.current.run(until: Date().addingTimeInterval(0.25))
    }
    print("flowbaton-runner lifetime elapsed after \(lifetime)s")
  }

  /// testTheAutomationCanSeeTheDevice is the smallest claim that needs a real
  /// simulator, and it is separate from the serving test so it can be run and
  /// finish. If this passes, XCUITest is attached and the automation's screen
  /// geometry and hierarchy paths work on a device.
  func testTheAutomationCanSeeTheDevice() throws {
    let automation = XCUITestAutomation()

    let info = try automation.deviceInfo()
    XCTAssertGreaterThan(info.widthPoints, 0, "no screen width in points")
    XCTAssertGreaterThan(info.heightPoints, 0, "no screen height in points")
    // Pixels come from a screenshot's own scale rather than points times a
    // guessed factor, so this inequality checks values read from both paths
    // agreeing about the same screen.
    XCTAssertGreaterThanOrEqual(info.widthPixels, info.widthPoints)
    XCTAssertTrue(
      ["portrait", "portrait-upside-down", "landscape-left", "landscape-right"]
        .contains(info.orientation),
      "the device orientation must use the wire vocabulary")

    XCTAssertEqual(
      try automation.runningApp(appIDs: []), XCUITestAutomation.springboardID,
      "an empty filter must fall back to the springboard")

    let hierarchy = try automation.viewHierarchy(appIDs: [], excludeKeyboardElements: false)
    let decoded = try JSONDecoder().decode(WireHierarchy.self, from: hierarchy)
    XCTAssertGreaterThanOrEqual(decoded.depth, 1, "a real screen has at least one level")
    XCTAssertEqual(decoded.axElement.frame.width, 0, "the public root must stay zero-sized")
    XCTAssertEqual(decoded.axElement.frame.height, 0, "the public root must stay zero-sized")
    let app = try XCTUnwrap(decoded.axElement.children?.first)
    XCTAssertGreaterThan(
      app.frame.width, 0, "the app child has no width, so its frame did not decode")
    XCTAssertGreaterThan(
      app.frame.height, 0, "the app child has no height, so its frame did not decode")

    let shot = try automation.screenshot(compressed: false)
    XCTAssertGreaterThan(shot.count, 0, "an empty screenshot")
  }

  /// The settle fixture (UITests/SettleFixture) keeps redrawing a decoration
  /// that accessibility never sees while its Continue button stands still.
  static let settleFixtureID = "dev.larchwave.flowbaton.settlefixture"

  /// Set by scripts/ci/ios-simulator-test.sh after it installs the fixture.
  /// XCUITest records a missing app as a failure rather than throwing, so the
  /// test cannot probe for the app itself and skips unless told it is there.
  static let settleFixtureVariable = "FLOWBATON_SETTLE_FIXTURE_INSTALLED"

  /// testAHiddenAnimationDoesNotHideAStableHierarchy pins the device facts
  /// behind issue #7: the screen probe keeps reporting motion, the hierarchy
  /// the host settles on does not change, and the button it exposes is
  /// tappable. A settle gated on the screen probe would never sample here.
  func testAHiddenAnimationDoesNotHideAStableHierarchy() throws {
    guard ProcessInfo.processInfo.environment[Self.settleFixtureVariable] == "1" else {
      throw XCTSkip("set \(Self.settleFixtureVariable)=1 once the settle fixture is installed")
    }
    let automation = XCUITestAutomation()
    try automation.launchApp(bundleID: Self.settleFixtureID)
    defer { try? automation.terminateApp(appID: Self.settleFixtureID) }

    var sawMotion = false
    for _ in 0..<10 where !sawMotion {
      sawMotion = try !automation.isScreenStatic()
    }
    XCTAssertTrue(
      sawMotion,
      "the fixture never moved a pixel, so it no longer reproduces the animated screen")

    let first = try Self.appTree(automation)
    let second = try Self.appTree(automation)
    XCTAssertEqual(first, second, "two consecutive hierarchy samples differ on a still screen")
    let button = try XCTUnwrap(
      Self.element("fixture.continue", in: first), "the Continue button is not in the hierarchy")
    XCTAssertTrue(button.enabled, "the Continue button is not enabled")

    try automation.touch(
      x: button.frame.x + button.frame.width / 2,
      y: button.frame.y + button.frame.height / 2,
      duration: nil)
    var continued = false
    for _ in 0..<20 where !continued {
      continued = try Self.element("fixture.continued", in: Self.appTree(automation)) != nil
      if !continued { Thread.sleep(forTimeInterval: 0.25) }
    }
    XCTAssertTrue(continued, "tapping Continue did not reveal the confirmation")
  }

  /// appTree is the fixture's own subtree: the served root also carries the
  /// status bar, whose clock may tick between two samples.
  private static func appTree(_ automation: XCUITestAutomation) throws -> WireAXElement {
    let payload = try automation.viewHierarchy(
      appIDs: [settleFixtureID], excludeKeyboardElements: false)
    let decoded = try JSONDecoder().decode(WireHierarchy.self, from: payload)
    return try XCTUnwrap(decoded.axElement.children?.first, "the served root has no app child")
  }

  private static func element(_ identifier: String, in root: WireAXElement) -> WireAXElement? {
    if root.identifier == identifier {
      return root
    }
    for child in root.children ?? [] {
      if let found = element(identifier, in: child) {
        return found
      }
    }
    return nil
  }

  private static func lifetime(_ environment: [String: String]) -> TimeInterval {
    guard let raw = environment[lifetimeVariable], let value = TimeInterval(raw), value > 0 else {
      return defaultLifetime
    }
    return value
  }
}
