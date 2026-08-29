import XCTest

@testable import FlowBatonIOSRunner

final class StatusEndpointTests: XCTestCase {
  func testGetStatusReturnsExactHealthResponse() throws {
    let response = StatusEndpoint.route(method: "GET", path: "/status")

    XCTAssertEqual(response.statusCode, 200)
    XCTAssertEqual(response.contentType, "application/json")
    XCTAssertEqual(
      String(decoding: response.body, as: UTF8.self),
      #"{"status":"ok"}"#
    )
  }

  func testGetStatusEchoesTheIdTheHostLaunchedTheRunnerWith() throws {
    // Two runners on one port answer the bare form identically; the id is
    // how a host tells the child it just started from a stranger.
    let response = StatusEndpoint.route(method: "GET", path: "/status", runner: "a1b2c3")
    XCTAssertEqual(
      String(decoding: response.body, as: UTF8.self),
      #"{"status":"ok","runner":"a1b2c3"}"#
    )
  }
}
