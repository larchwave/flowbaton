import Foundation
import XCTest

@testable import FlowBatonIOSRunner

/// The request parser handles bodies split across TCP reads, enforces
/// Content-Length, and preserves query strings.
final class HTTPRequestParserTests: XCTestCase {

  func testParsesMethodPathAndQuerySeparately() throws {
    let request = try parse("GET /screenshot?compressed=true HTTP/1.1\r\nHost: x\r\n\r\n")
    XCTAssertEqual(request.method, "GET")
    XCTAssertEqual(request.path, "/screenshot")
    XCTAssertEqual(request.query, "compressed=true")
  }

  func testAPathWithNoQueryHasAnEmptyQueryRatherThanAQuestionMark() throws {
    let request = try parse("GET /status HTTP/1.1\r\n\r\n")
    XCTAssertEqual(request.path, "/status")
    XCTAssertEqual(request.query, "")
  }

  func testReadsABodyOfTheDeclaredLength() throws {
    let body = #"{"x":1,"y":2}"#
    let request = try parse(
      "POST /touch HTTP/1.1\r\nContent-Type: application/json\r\nContent-Length: \(body.utf8.count)\r\n\r\n\(body)"
    )
    XCTAssertEqual(String(decoding: request.body, as: UTF8.self), body)
  }

  func testHeaderNamesAreCaseInsensitive() throws {
    // A client is free to send content-length in any case, and treating a
    // lowercase one as absent would silently drop the body.
    let body = #"{"x":1}"#
    let request = try parse(
      "POST /touch HTTP/1.1\r\ncontent-length: \(body.utf8.count)\r\n\r\n\(body)")
    XCTAssertEqual(String(decoding: request.body, as: UTF8.self), body)
  }

  func testAnIncompleteBodyIsReportedAsIncompleteRatherThanTruncated() throws {
    // An incomplete body asks for more data instead of decoding a partial JSON
    // document as malformed.
    let body = #"{"x":1,"y":2}"#
    let partial =
      "POST /touch HTTP/1.1\r\nContent-Length: \(body.utf8.count)\r\n\r\n{\"x\":1,"
    switch HTTPRequestParser.parse(Data(partial.utf8)) {
    case .needMoreData:
      break
    case .parsed(let request):
      XCTFail("a partial body parsed as complete: \(String(decoding: request.body, as: UTF8.self))")
    case .malformed(let reason):
      XCTFail("a partial body was called malformed: \(reason)")
    }
  }

  func testHeadersThatHaveNotFullyArrivedAlsoAskForMore() throws {
    switch HTTPRequestParser.parse(Data("POST /touch HTTP/1.1\r\nContent-Len".utf8)) {
    case .needMoreData:
      break
    default:
      XCTFail("an incomplete header block did not ask for more data")
    }
  }

  func testABodyArrivingAcrossSeveralReadsIsAssembled() throws {
    // A valid request may arrive in several TCP reads.
    let body = #"{"text":"a longer string that will not fit in one packet"}"#
    let head = "POST /inputText HTTP/1.1\r\nContent-Length: \(body.utf8.count)\r\n\r\n"
    var buffer = Data(head.utf8)

    XCTAssertEqual(HTTPRequestParser.parse(buffer).isNeedMoreData, true)
    for chunk in Array(body.utf8).chunked(into: 8) {
      buffer.append(contentsOf: chunk)
    }
    let request = try XCTUnwrap(HTTPRequestParser.parse(buffer).parsedRequest)
    XCTAssertEqual(String(decoding: request.body, as: UTF8.self), body)
  }

  func testARequestLineWithoutThreePartsIsMalformed() throws {
    switch HTTPRequestParser.parse(Data("NOTAREQUEST\r\n\r\n".utf8)) {
    case .malformed:
      break
    default:
      XCTFail("a garbage request line was not rejected")
    }
  }

  func testABodyLongerThanContentLengthIsTruncatedToIt() throws {
    // Trailing bytes belong to the next request on a keep-alive connection,
    // not to this one. Handing them to a JSON decoder would fail a request
    // that was perfectly valid.
    let body = #"{"x":1}"#
    let request = try parse(
      "POST /touch HTTP/1.1\r\nContent-Length: \(body.utf8.count)\r\n\r\n\(body)EXTRA")
    XCTAssertEqual(String(decoding: request.body, as: UTF8.self), body)
  }

  func testANegativeOrUnparsableContentLengthIsMalformed() throws {
    for value in ["-1", "abc"] {
      switch HTTPRequestParser.parse(
        Data("POST /touch HTTP/1.1\r\nContent-Length: \(value)\r\n\r\n".utf8))
      {
      case .malformed:
        break
      default:
        XCTFail("Content-Length \(value) was accepted")
      }
    }
  }

  private func parse(_ text: String) throws -> HTTPRequest {
    try XCTUnwrap(HTTPRequestParser.parse(Data(text.utf8)).parsedRequest)
  }
}

extension HTTPParseResult {
  fileprivate var parsedRequest: HTTPRequest? {
    if case .parsed(let request) = self { return request }
    return nil
  }

  fileprivate var isNeedMoreData: Bool {
    if case .needMoreData = self { return true }
    return false
  }
}

extension Array {
  fileprivate func chunked(into size: Int) -> [[Element]] {
    stride(from: 0, to: count, by: size).map { Array(self[$0..<Swift.min($0 + size, count)]) }
  }
}
