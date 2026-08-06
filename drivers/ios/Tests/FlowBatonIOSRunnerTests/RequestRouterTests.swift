import Foundation
import XCTest

@testable import FlowBatonIOSRunner

/// The router decides what a request MEANS. Everything that needs a UI test
/// session sits behind `DeviceAutomation`, which is what lets this run under
/// `swift test` on a Mac with no simulator involved.
///
/// Route coverage is derived from the wire contract so every declared route
/// must have a handler.
final class RequestRouterTests: XCTestCase {

  func testEveryDeclaredRouteIsServed() throws {
    // Not a restatement of the eighteen routes — this READS them. A route
    // added to the contract without a handler reds here.
    let automation = RecordingAutomation()
    let router = RequestRouter(automation: automation)

    var unserved: [String] = []
    for route in IOSWireContractV0.routes {
      let response = router.route(
        HTTPRequest(
          method: route.method,
          path: route.path,
          query: "",
          headers: [:],
          body: Self.minimalBody(for: route.name)
        ))
      if response.statusCode == 404 {
        unserved.append("\(route.method) \(route.path)")
      }
      if response.statusCode == 500 {
        unserved.append("\(route.method) \(route.path) (no handler)")
      }
    }
    XCTAssertEqual(unserved, [], "contract routes the router does not serve")
  }

  func testAPathTheContractDoesNotDeclareIsNotFound() throws {
    let response = RequestRouter(automation: RecordingAutomation())
      .route(request("GET", "/nonsense"))
    XCTAssertEqual(response.statusCode, 404)
  }

  func testAKnownPathWithTheWrongMethodSaysSoRatherThanReporting404() throws {
    // /touch exists but only as POST. "Not found" would send someone checking
    // their spelling instead of their verb.
    let response = RequestRouter(automation: RecordingAutomation())
      .route(request("GET", "/touch"))
    XCTAssertEqual(response.statusCode, 400)
    XCTAssertTrue(Self.message(response).contains("does not accept GET"))
  }

  func testAutomationErrorsCarryTheirContractStatus() throws {
    // contracts/v0/ios-http.json maps 400 precondition, 408 timeout, 500
    // internal. A timeout that arrived as 500 would tell the host to retry
    // something it has pinned as non-retryable.
    for (failure, wantStatus, wantCode) in [
      (AutomationError.precondition("nope"), 400, "precondition"),
      (AutomationError.timeout("waited"), 408, "timeout"),
      (AutomationError.internalFailure("boom"), 500, "internal"),
    ] {
      let automation = RecordingAutomation()
      automation.failure = failure
      let response = RequestRouter(automation: automation).route(
        request("POST", "/touch", body: #"{"x":1,"y":2}"#))
      XCTAssertEqual(response.statusCode, wantStatus)
      XCTAssertEqual(Self.decodeError(response).code, wantCode)
    }
  }

  func testAMalformedBodyIsAPreconditionFailureNotAnInternalOne() throws {
    // The caller sent something the schema does not describe. Reporting it as
    // internal would make the host retry a request that can never succeed.
    let response = RequestRouter(automation: RecordingAutomation())
      .route(request("POST", "/touch", body: #"{"x":"not-a-number"}"#))
    XCTAssertEqual(response.statusCode, 400)
    XCTAssertEqual(Self.decodeError(response).code, "precondition")
  }

  func testTouchWithoutADurationIsATapAndWithOneIsALongPress() throws {
    // TouchRequest.duration is optional precisely because its presence is the
    // difference between the two gestures.
    let automation = RecordingAutomation()
    let router = RequestRouter(automation: automation)

    _ = router.route(request("POST", "/touch", body: #"{"x":3,"y":4}"#))
    XCTAssertEqual(automation.touches.count, 1)
    XCTAssertNil(automation.touches[0].duration)

    _ = router.route(request("POST", "/touch", body: #"{"x":3,"y":4,"duration":3}"#))
    XCTAssertEqual(automation.touches[1].duration, 3)
  }

  func testScreenshotReadsItsFlagFromTheQueryAndReturnsBytes() throws {
    // The one route whose request is a query string and whose response is not
    // JSON. A compressed flag read as false would return a PNG where the host
    // expects a JPEG.
    let automation = RecordingAutomation()
    let router = RequestRouter(automation: automation)

    let compressed = router.route(request("GET", "/screenshot", query: "compressed=true"))
    XCTAssertEqual(automation.screenshotCompressed, [true])
    XCTAssertEqual(compressed.contentType, "image/jpeg")
    XCTAssertEqual(compressed.body, Data("IMAGE".utf8))

    _ = router.route(request("GET", "/screenshot", query: "compressed=false"))
    XCTAssertEqual(automation.screenshotCompressed, [true, false])

    // An absent flag is not a true flag.
    _ = router.route(request("GET", "/screenshot"))
    XCTAssertEqual(automation.screenshotCompressed, [true, false, false])
  }

  func testDeviceInfoReportsPointsAndPixelsSeparately() throws {
    let response = RequestRouter(automation: RecordingAutomation())
      .route(request("GET", "/deviceInfo"))
    let decoded = try JSONDecoder().decode(DeviceInfoPayload.self, from: response.body)
    XCTAssertEqual(decoded.widthPoints, 390)
    XCTAssertEqual(decoded.widthPixels, 1170)
  }

  func testTheHierarchyIsPassedThroughWithoutARoundTrip() throws {
    // Re-encoding a deep tree through this layer would only add a way to lose
    // a field, so the bytes the automation produced are the bytes sent.
    let automation = RecordingAutomation()
    automation.hierarchy = Data(#"{"axElement":{"identifier":"root"},"depth":1}"#.utf8)
    let response = RequestRouter(automation: automation).route(
      request("POST", "/viewHierarchy", body: #"{"appIds":["a"],"excludeKeyboardElements":false}"#))
    XCTAssertEqual(response.body, automation.hierarchy)
  }

  func testRequestFieldsReachTheAutomationUnchanged() throws {
    let automation = RecordingAutomation()
    let router = RequestRouter(automation: automation)

    _ = router.route(request("POST", "/inputText", body: #"{"text":"hello","appIds":["a","b"]}"#))
    XCTAssertEqual(automation.inputs.first?.text, "hello")
    XCTAssertEqual(automation.inputs.first?.appIDs, ["a", "b"])

    _ = router.route(
      request(
        "POST", "/swipeV2",
        body: #"{"startX":1,"startY":2,"endX":3,"endY":4,"duration":0.5,"appIds":["a"]}"#))
    XCTAssertEqual(automation.swipes.first?.endY, 4)
    XCTAssertEqual(automation.swipes.first?.duration, 0.5)

    _ = router.route(
      request("POST", "/setPermissions", body: #"{"permissions":{"camera":"allow"}}"#))
    XCTAssertEqual(automation.permissions.first?["camera"], "allow")

    _ = router.route(request("POST", "/eraseText", body: #"{"charactersToErase":9,"appIds":[]}"#))
    XCTAssertEqual(automation.erasures.first, 9)
  }

  func testStatusIsAnsweredWithoutTheAutomation() throws {
    // Health has to work when the automation cannot: the host polls /status to
    // decide the runner is up at all.
    let automation = RecordingAutomation()
    automation.failure = .internalFailure("everything is broken")
    let response = RequestRouter(automation: automation).route(request("GET", "/status"))
    XCTAssertEqual(response.statusCode, 200)
    XCTAssertEqual(String(decoding: response.body, as: UTF8.self), #"{"status":"ok"}"#)
  }

  // MARK: - Helpers

  func testAnEmptyAppFilterIsAcceptedAndNullIsNot() throws {
    // appIds is non-optional on the wire. The host sends `[]` for an empty
    // filter, while `null` remains a precondition error.
    //
    // Method and path come from IOSWireContractV0, not from a list here:
    // runningApp and keyboard are GET, and a hand-written POST would 400 for
    // the wrong reason — which reads exactly like the defect.
    let router = RequestRouter(automation: RecordingAutomation())
    let bodies = [
      ("runningApp", #"{"appIds":[]}"#),
      ("inputText", #"{"text":"hello","appIds":[]}"#),
      ("eraseText", #"{"charactersToErase":3,"appIds":[]}"#),
      ("viewHierarchy", #"{"appIds":[],"excludeKeyboardElements":false}"#),
      ("keyboard", #"{"appIds":[]}"#),
    ]

    for (name, body) in bodies {
      guard let route = IOSWireContractV0.routes.first(where: { $0.name == name }) else {
        XCTFail("the wire contract does not declare \(name)")
        continue
      }
      let accepted = router.route(request(route.method, route.path, body: body))
      XCTAssertEqual(
        accepted.statusCode, route.successStatus,
        "\(route.path) refused an empty app filter: \(Self.message(accepted))")

      // The control. If the decoder had been relaxed to tolerate null instead
      // of the host being fixed, the check above would pass and the contract
      // would be silently wider than it says.
      let nulled = body.replacingOccurrences(of: #""appIds":[]"#, with: #""appIds":null"#)
      let refused = router.route(request(route.method, route.path, body: nulled))
      XCTAssertEqual(refused.statusCode, 400, "\(route.path) accepted a null app filter")
    }
  }

  private func request(
    _ method: String, _ path: String, query: String = "", body: String = "{}"
  ) -> HTTPRequest {
    HTTPRequest(
      method: method, path: path, query: query, headers: [:], body: Data(body.utf8))
  }

  private static func message(_ response: HTTPResponse) -> String {
    decodeError(response).errorMessage
  }

  private static func decodeError(_ response: HTTPResponse) -> ErrorResponse {
    (try? JSONDecoder().decode(ErrorResponse.self, from: response.body))
      ?? ErrorResponse(code: "", errorMessage: "")
  }

  /// The smallest body each route will decode. The coverage test cares about
  /// reachability, not about the values.
  private static func minimalBody(for route: String) -> Data {
    let bodies: [String: String] = [
      "runningApp": #"{"appIds":[]}"#,
      "swipe": #"{"startX":0,"startY":0,"endX":1,"endY":1,"duration":1}"#,
      "swipeV2": #"{"startX":0,"startY":0,"endX":1,"endY":1,"duration":1}"#,
      "inputText": #"{"text":"a","appIds":[]}"#,
      "touch": #"{"x":0,"y":0}"#,
      "pressKey": #"{"key":"enter"}"#,
      "pressButton": #"{"button":"home"}"#,
      "eraseText": #"{"charactersToErase":1,"appIds":[]}"#,
      "setOrientation": #"{"orientation":"portrait"}"#,
      "setPermissions": #"{"permissions":{}}"#,
      "viewHierarchy": #"{"appIds":[],"excludeKeyboardElements":false}"#,
      "keyboard": #"{"appIds":[]}"#,
      "launchApp": #"{"bundleId":"com.example.a"}"#,
      "terminateApp": #"{"appId":"com.example.a"}"#,
    ]
    return Data((bodies[route] ?? "{}").utf8)
  }
}

/// Records what reached it and answers everything, unless a failure is set.
final class RecordingAutomation: DeviceAutomation, @unchecked Sendable {
  var failure: AutomationError?
  var hierarchy = Data(#"{"axElement":{},"depth":0}"#.utf8)
  var touches: [(x: Double, y: Double, duration: Double?)] = []
  var swipes: [(endY: Double, duration: Double)] = []
  var inputs: [(text: String, appIDs: [String])] = []
  var permissions: [[String: String]] = []
  var erasures: [Int] = []
  var screenshotCompressed: [Bool] = []

  private func check() throws {
    if let failure { throw failure }
  }

  func runningApp(appIDs: [String]) throws -> String {
    try check()
    return "com.apple.springboard"
  }

  func swipe(
    startX: Double, startY: Double, endX: Double, endY: Double, duration: Double, appID: String?
  ) throws {
    try check()
    swipes.append((endY: endY, duration: duration))
  }

  func swipeV2(
    startX: Double, startY: Double, endX: Double, endY: Double, duration: Double, appIDs: [String]
  ) throws {
    try check()
    swipes.append((endY: endY, duration: duration))
  }

  func inputText(_ text: String, appIDs: [String]) throws {
    try check()
    inputs.append((text: text, appIDs: appIDs))
  }

  func touch(x: Double, y: Double, duration: Double?) throws {
    try check()
    touches.append((x: x, y: y, duration: duration))
  }

  func screenshot(compressed: Bool) throws -> Data {
    try check()
    screenshotCompressed.append(compressed)
    return Data("IMAGE".utf8)
  }

  func isScreenStatic() throws -> Bool {
    try check()
    return true
  }

  func pressKey(_ key: String) throws { try check() }
  func pressButton(_ button: String) throws { try check() }

  func eraseText(charactersToErase: Int, appIDs: [String]) throws {
    try check()
    erasures.append(charactersToErase)
  }

  func deviceInfo() throws -> DeviceInfoPayload {
    try check()
    return DeviceInfoPayload(
      widthPoints: 390, heightPoints: 844, widthPixels: 1170, heightPixels: 2532)
  }

  func setOrientation(_ orientation: String) throws { try check() }

  func setPermissions(_ permissions: [String: String]) throws {
    try check()
    self.permissions.append(permissions)
  }

  func viewHierarchy(appIDs: [String], excludeKeyboardElements: Bool) throws -> Data {
    try check()
    return hierarchy
  }

  func isKeyboardVisible(appIDs: [String]) throws -> Bool {
    try check()
    return false
  }

  func launchApp(bundleID: String) throws { try check() }
  func terminateApp(appID: String) throws { try check() }
}
