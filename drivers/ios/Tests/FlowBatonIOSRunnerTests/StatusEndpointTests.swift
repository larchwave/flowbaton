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
}
