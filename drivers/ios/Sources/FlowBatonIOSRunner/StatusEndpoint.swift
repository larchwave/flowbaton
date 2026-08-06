import Foundation

public struct HTTPResponse: Equatable, Sendable {
  public let statusCode: Int
  public let contentType: String
  public let body: Data

  public init(statusCode: Int, contentType: String, body: Data) {
    self.statusCode = statusCode
    self.contentType = contentType
    self.body = body
  }
}

public enum StatusEndpoint {
  public static func route(method: String, path: String) -> HTTPResponse {
    guard method == "GET", path == "/status" else {
      return HTTPResponse(
        statusCode: 404,
        contentType: "text/plain; charset=utf-8",
        body: Data()
      )
    }

    return HTTPResponse(
      statusCode: 200,
      contentType: "application/json",
      body: Data(#"{"status":"ok"}"#.utf8)
    )
  }
}
