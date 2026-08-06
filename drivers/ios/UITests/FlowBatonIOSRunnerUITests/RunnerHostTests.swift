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

  override func setUp() {
    super.setUp()
    // WITHOUT this the runner dies on the first device-level failure. XCUITest
    // records a gesture it could not synthesize as an XCTIssue, and by default a
    // issue tears the test down — which here means the server goes
    // away and later commands fail with "connection refused". Continue serving
    // so the command that failed can return its own error.
    continueAfterFailure = true
  }

  func testServeTheWireUntilTheHostIsDone() throws {
    let environment = ProcessInfo.processInfo.environment
    guard environment[Self.serveVariable] == "1" else {
      throw XCTSkip(
        "set \(Self.serveVariable)=1 to serve; this test is the runner, not an assertion")
    }
    let port = try RunnerPort.resolve(environment)
    let lifetime = Self.lifetime(environment)

    let server = LoopbackHTTPServer(port: port, automation: XCUITestAutomation())
    let bound = try server.start(timeout: 10)
    defer { server.stop() }

    // Printed, not just logged: the host reads the runner's output to know it is
    // up, and "which port did it actually get" is the first question when a
    // connection is refused.
    print("flowbaton-runner listening on 127.0.0.1:\(bound)")
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

  private static func lifetime(_ environment: [String: String]) -> TimeInterval {
    guard let raw = environment[lifetimeVariable], let value = TimeInterval(raw), value > 0 else {
      return defaultLifetime
    }
    return value
  }
}
