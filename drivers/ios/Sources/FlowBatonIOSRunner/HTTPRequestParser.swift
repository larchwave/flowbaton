import Foundation

/// One parsed HTTP request.
public struct HTTPRequest: Equatable, Sendable {
  public let method: String
  public let path: String
  public let query: String
  public let headers: [String: String]
  public let body: Data

  public init(method: String, path: String, query: String, headers: [String: String], body: Data) {
    self.method = method
    self.path = path
    self.query = query
    self.headers = headers
    self.body = body
  }
}

/// What a parse attempt concluded about the bytes so far.
///
/// `needMoreData` is the case worth naming. A request that has not fully
/// arrived is not an error — it is the normal shape of a TCP read — and
/// treating it as malformed would fail requests that were perfectly valid and
/// merely split across packets.
public enum HTTPParseResult: Sendable {
  case parsed(HTTPRequest)
  case needMoreData
  case malformed(String)
}

public enum HTTPRequestParser {
  private static let headerTerminator = Data("\r\n\r\n".utf8)

  /// Parses an accumulated read buffer.
  ///
  /// The caller keeps appending and re-parsing until this returns something
  /// other than `needMoreData`, which is what makes a body larger than one
  /// packet work at all.
  public static func parse(_ buffer: Data) -> HTTPParseResult {
    guard let terminator = range(of: headerTerminator, in: buffer) else {
      return .needMoreData
    }
    let headerText = String(
      decoding: buffer[buffer.startIndex..<terminator.lowerBound], as: UTF8.self)
    var lines = headerText.components(separatedBy: "\r\n")
    guard let requestLine = lines.first, !requestLine.isEmpty else {
      return .malformed("empty request line")
    }
    lines.removeFirst()

    let parts = requestLine.split(separator: " ", omittingEmptySubsequences: true)
    guard parts.count >= 3 else {
      return .malformed("request line is not METHOD PATH VERSION")
    }
    let method = String(parts[0])
    let target = String(parts[1])
    let (path, query) = splitTarget(target)

    var headers: [String: String] = [:]
    for line in lines where !line.isEmpty {
      guard let separator = line.firstIndex(of: ":") else { continue }
      // Header names are case-insensitive, and a client that sends
      // content-length in lowercase would otherwise look like it sent no body.
      let name = line[line.startIndex..<separator].trimmingCharacters(in: .whitespaces).lowercased()
      let value = line[line.index(after: separator)...].trimmingCharacters(in: .whitespaces)
      headers[name] = value
    }

    var declaredLength = 0
    if let raw = headers["content-length"] {
      guard let parsed = Int(raw), parsed >= 0 else {
        return .malformed("Content-Length \(raw) is not a length")
      }
      declaredLength = parsed
    }

    let bodyStart = terminator.upperBound
    let available = buffer.distance(from: bodyStart, to: buffer.endIndex)
    if available < declaredLength {
      return .needMoreData
    }
    // Anything past Content-Length belongs to the next request on the
    // connection, not to this one.
    let bodyEnd = buffer.index(bodyStart, offsetBy: declaredLength)
    return .parsed(
      HTTPRequest(
        method: method,
        path: path,
        query: query,
        headers: headers,
        body: Data(buffer[bodyStart..<bodyEnd])
      ))
  }

  private static func splitTarget(_ target: String) -> (String, String) {
    guard let mark = target.firstIndex(of: "?") else {
      return (target, "")
    }
    return (String(target[target.startIndex..<mark]), String(target[target.index(after: mark)...]))
  }

  /// Finds the header terminator. Data slices do not index from zero, so this
  /// works in the buffer's own index space rather than assuming it.
  private static func range(of needle: Data, in haystack: Data) -> Range<Data.Index>? {
    guard !needle.isEmpty, haystack.count >= needle.count else { return nil }
    let last = haystack.index(haystack.endIndex, offsetBy: -needle.count)
    var start = haystack.startIndex
    while start <= last {
      let end = haystack.index(start, offsetBy: needle.count)
      if haystack[start..<end].elementsEqual(needle) {
        return start..<end
      }
      start = haystack.index(after: start)
    }
    return nil
  }
}
