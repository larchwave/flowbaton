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
  /// Answers the health check. `runner` is the id the host launched this
  /// process with (`RunnerIdentity`); it is echoed so the host can tell its
  /// child from a stranger on the same port. Nil answers the bare form.
  public static func route(method: String, path: String, runner: String? = nil) -> HTTPResponse {
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
      body: Data(healthBody(runner: runner).utf8)
    )
  }

  /// Health is a constant, not an encode: it has to answer even when
  /// everything that could fail is failing. `RunnerIdentity.resolve` admits
  /// only characters that are literal inside a JSON string.
  public static func healthBody(runner: String?) -> String {
    guard let runner else {
      return #"{"status":"ok"}"#
    }
    return #"{"status":"ok","runner":""# + runner + #""}"#
  }
}
