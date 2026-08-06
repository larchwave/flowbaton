import Foundation
import XCTest

@testable import FlowBatonIOSRunner

/// The parser and router are tested on their own. This proves they are
/// actually connected to the socket — that a real HTTP request over a real
/// loopback connection reaches the automation and comes back.
///
/// Without it, the server could still be answering /status to every path and
/// every unit test above would stay green.
final class ServerRoundTripTests: XCTestCase {

  func testARealRequestReachesTheAutomationAndComesBack() throws {
    let automation = RecordingAutomation()
    let server = LoopbackHTTPServer(port: 0, automation: automation)
    let port = try server.start(timeout: 5)
    defer { server.stop() }

    let (status, body, contentType) = try send(
      port: port, method: "POST", target: "/touch", body: #"{"x":12,"y":34,"duration":3}"#)

    XCTAssertEqual(status, 200)
    XCTAssertEqual(contentType, "application/json")
    XCTAssertEqual(String(decoding: body, as: UTF8.self), "{}")
    XCTAssertEqual(automation.touches.count, 1)
    XCTAssertEqual(automation.touches[0].x, 12)
    XCTAssertEqual(automation.touches[0].duration, 3)
  }

  func testABodyLargerThanOneReadStillArrivesWhole() throws {
    // A 200 KB request body exceeds a single receive chunk. The server must
    // assemble the full body before parsing it.
    let automation = RecordingAutomation()
    let server = LoopbackHTTPServer(port: 0, automation: automation)
    let port = try server.start(timeout: 5)
    defer { server.stop() }

    let long = String(repeating: "abcdefghij", count: 20_000)
    let requestBody = #"{"text":"\#(long)","appIds":[]}"#

    let (status, _, _) = try send(
      port: port, method: "POST", target: "/inputText", body: requestBody)

    XCTAssertEqual(status, 200)
    XCTAssertEqual(automation.inputs.first?.text.count, long.count)
  }

  func testAnAutomationFailureArrivesWithItsContractStatus() throws {
    let automation = RecordingAutomation()
    automation.failure = .timeout("XCUITest gave up")
    let server = LoopbackHTTPServer(port: 0, automation: automation)
    let port = try server.start(timeout: 5)
    defer { server.stop() }

    let (status, body, _) = try send(
      port: port, method: "POST", target: "/touch", body: #"{"x":1,"y":1}"#)

    XCTAssertEqual(status, 408)
    let decoded = try JSONDecoder().decode(ErrorResponse.self, from: body)
    XCTAssertEqual(decoded.code, "timeout")
    XCTAssertEqual(decoded.errorMessage, "XCUITest gave up")
  }

  func testTheQueryStringSurvivesTheSocket() throws {
    let automation = RecordingAutomation()
    let server = LoopbackHTTPServer(port: 0, automation: automation)
    let port = try server.start(timeout: 5)
    defer { server.stop() }

    let (status, body, contentType) = try send(
      port: port, method: "GET", target: "/screenshot?compressed=true", body: nil)

    XCTAssertEqual(status, 200)
    XCTAssertEqual(contentType, "image/jpeg")
    XCTAssertEqual(body, Data("IMAGE".utf8))
    XCTAssertEqual(automation.screenshotCompressed, [true])
  }

  func testTheDefaultServerStillOnlyAnswersStatus() throws {
    // The no-automation initializer is what runs before a UI session exists.
    // It must keep answering the health check and must not pretend to serve
    // routes it has no automation for.
    let server = LoopbackHTTPServer(port: 0)
    let port = try server.start(timeout: 5)
    defer { server.stop() }

    let (ok, body, _) = try send(port: port, method: "GET", target: "/status", body: nil)
    XCTAssertEqual(ok, 200)
    XCTAssertEqual(String(decoding: body, as: UTF8.self), #"{"status":"ok"}"#)

    let (missing, _, _) = try send(port: port, method: "POST", target: "/touch", body: "{}")
    XCTAssertEqual(missing, 404)
  }

  /// Sends one request over a real socket and reads the whole response.
  private func send(
    port: UInt16, method: String, target: String, body: String?
  ) throws -> (Int, Data, String) {
    var request = URLRequest(url: URL(string: "http://127.0.0.1:\(port)\(target)")!)
    request.httpMethod = method
    if let body {
      request.httpBody = Data(body.utf8)
      request.setValue("application/json", forHTTPHeaderField: "Content-Type")
    }
    request.timeoutInterval = 10

    var result: (Int, Data, String)?
    var failure: Error?
    let finished = XCTestExpectation(description: "response")
    URLSession.shared.dataTask(with: request) { data, response, error in
      if let error {
        failure = error
      } else if let http = response as? HTTPURLResponse {
        result = (
          http.statusCode, data ?? Data(),
          http.value(forHTTPHeaderField: "Content-Type") ?? ""
        )
      }
      finished.fulfill()
    }.resume()
    wait(for: [finished], timeout: 15)

    if let failure { throw failure }
    return try XCTUnwrap(result)
  }
}
