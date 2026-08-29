import Foundation

/// Dispatches a parsed request to the automation behind it.
///
/// The route table is not restated here: `IOSWireContractV0.routes` is the
/// wire contract and this reads it, so every declared route remains covered by
/// the router.
public struct RequestRouter: Sendable {
  private let automation: any DeviceAutomation
  private let runner: String?

  /// `runner` is the id the host launched this process with; see
  /// `RunnerIdentity`.
  public init(automation: any DeviceAutomation, runner: String? = nil) {
    self.runner = runner
    self.automation = automation
  }

  public func route(_ request: HTTPRequest) -> HTTPResponse {
    guard let declared = Self.declaredRoute(method: request.method, path: request.path) else {
      // A path the contract declares under a different method is a different
      // failure from a path it does not declare at all, and saying so saves
      // an afternoon.
      if IOSWireContractV0.routes.contains(where: { $0.path == request.path }) {
        return Self.errorResponse(
          .precondition("\(request.path) does not accept \(request.method)"))
      }
      return Self.notFound(request.path)
    }

    do {
      return try dispatch(declared.name, request)
    } catch let error as AutomationError {
      return Self.errorResponse(error)
    } catch let error as DecodingError {
      // A body the contract's schema does not describe is the caller's
      // mistake, which is a precondition failure and not retryable.
      return Self.errorResponse(.precondition("\(declared.name): \(Self.describe(error))"))
    } catch {
      return Self.errorResponse(.internalFailure("\(declared.name): \(error)"))
    }
  }

  // MARK: - Dispatch

  private func dispatch(_ name: String, _ request: HTTPRequest) throws -> HTTPResponse {
    switch name {
    case "status":
      return StatusEndpoint.route(method: "GET", path: "/status", runner: runner)

    case "runningApp":
      let decoded: RunningAppRequest = try Self.decode(request)
      return try Self.json(["runningAppBundleId": automation.runningApp(appIDs: decoded.appIds)])

    case "swipe":
      let decoded: SwipeRequest = try Self.decode(request)
      try automation.swipe(
        startX: decoded.startX, startY: decoded.startY,
        endX: decoded.endX, endY: decoded.endY,
        duration: decoded.duration, appID: decoded.appId)
      return Self.empty()

    case "swipeV2":
      let decoded: SwipeV2Request = try Self.decode(request)
      try automation.swipeV2(
        startX: decoded.startX, startY: decoded.startY,
        endX: decoded.endX, endY: decoded.endY,
        duration: decoded.duration, appIDs: decoded.appIds ?? [])
      return Self.empty()

    case "inputText":
      let decoded: InputTextRequest = try Self.decode(request)
      try automation.inputText(decoded.text, appIDs: decoded.appIds)
      return Self.empty()

    case "touch":
      let decoded: TouchRequest = try Self.decode(request)
      try automation.touch(x: decoded.x, y: decoded.y, duration: decoded.duration)
      return Self.empty()

    case "screenshot":
      // The only route whose request is a query string and whose response is
      // bytes rather than JSON.
      let compressed = Self.queryFlag("compressed", in: request.query)
      let image = try automation.screenshot(compressed: compressed)
      return HTTPResponse(
        statusCode: 200,
        contentType: compressed ? "image/jpeg" : "image/png",
        body: image)

    case "isScreenStatic":
      return try Self.json(["isScreenStatic": automation.isScreenStatic()])

    case "pressKey":
      let decoded: PressKeyRequest = try Self.decode(request)
      try automation.pressKey(decoded.key, appIDs: decoded.appIds)
      return Self.empty()

    case "pressButton":
      let decoded: PressButtonRequest = try Self.decode(request)
      try automation.pressButton(decoded.button)
      return Self.empty()

    case "eraseText":
      let decoded: EraseTextRequest = try Self.decode(request)
      try automation.eraseText(charactersToErase: decoded.charactersToErase, appIDs: decoded.appIds)
      return Self.empty()

    case "deviceInfo":
      return try Self.encode(automation.deviceInfo())

    case "setOrientation":
      let decoded: SetOrientationRequest = try Self.decode(request)
      try automation.setOrientation(decoded.orientation)
      return Self.empty()

    case "setPermissions":
      let decoded: SetPermissionsRequest = try Self.decode(request)
      try automation.setPermissions(decoded.permissions)
      return Self.empty()

    case "viewHierarchy":
      let decoded: ViewHierarchyRequest = try Self.decode(request)
      // The hierarchy is passed through as encoded bytes rather than decoded
      // and re-encoded: it is deep, and a round trip through this layer would
      // only add a way to lose a field.
      let payload = try automation.viewHierarchy(
        appIDs: decoded.appIds, excludeKeyboardElements: decoded.excludeKeyboardElements)
      return HTTPResponse(statusCode: 200, contentType: "application/json", body: payload)

    case "keyboard":
      let decoded: KeyboardRequest = try Self.decode(request)
      return try Self.json([
        "isKeyboardVisible": automation.isKeyboardVisible(appIDs: decoded.appIds)
      ])

    case "launchApp":
      let decoded: LaunchAppRequest = try Self.decode(request)
      try automation.launchApp(bundleID: decoded.bundleId)
      return Self.empty()

    case "terminateApp":
      let decoded: TerminateAppRequest = try Self.decode(request)
      try automation.terminateApp(appID: decoded.appId)
      return Self.empty()

    default:
      // Unreachable while the coverage test passes; kept so a route added to
      // the contract fails loudly here rather than silently returning 200.
      return Self.errorResponse(.internalFailure("route \(name) has no handler"))
    }
  }

  // MARK: - Contract lookup

  static func declaredRoute(method: String, path: String) -> IOSRouteV0? {
    IOSWireContractV0.routes.first { $0.method == method && $0.path == path }
  }

  // MARK: - Encoding

  private static func decode<T: Decodable>(_ request: HTTPRequest) throws -> T {
    try JSONDecoder().decode(T.self, from: request.body)
  }

  private static func json(_ value: [String: some Encodable & Sendable]) throws -> HTTPResponse {
    HTTPResponse(
      statusCode: 200, contentType: "application/json",
      body: try JSONEncoder().encode(value))
  }

  private static func encode(_ value: some Encodable) throws -> HTTPResponse {
    HTTPResponse(
      statusCode: 200, contentType: "application/json",
      body: try JSONEncoder().encode(value))
  }

  private static func empty() -> HTTPResponse {
    HTTPResponse(statusCode: 200, contentType: "application/json", body: Data("{}".utf8))
  }

  static func errorResponse(_ error: AutomationError) -> HTTPResponse {
    let payload = ErrorResponse(code: error.code, errorMessage: error.message)
    let body = (try? JSONEncoder().encode(payload)) ?? Data(#"{"code":"internal"}"#.utf8)
    return HTTPResponse(statusCode: error.httpStatus, contentType: "application/json", body: body)
  }

  private static func notFound(_ path: String) -> HTTPResponse {
    // 404 is deliberately outside the contract's three mapped statuses: it
    // means the runner does not serve this at all, which is a different thing
    // from an operation that failed.
    let payload = ErrorResponse(code: "internal", errorMessage: "no route for \(path)")
    let body = (try? JSONEncoder().encode(payload)) ?? Data()
    return HTTPResponse(statusCode: 404, contentType: "application/json", body: body)
  }

  private static func queryFlag(_ name: String, in query: String) -> Bool {
    for pair in query.split(separator: "&") {
      let parts = pair.split(separator: "=", maxSplits: 1)
      if parts.count == 2, parts[0] == name {
        return parts[1] == "true"
      }
    }
    return false
  }

  private static func describe(_ error: DecodingError) -> String {
    switch error {
    case .keyNotFound(let key, _): return "missing field \(key.stringValue)"
    case .typeMismatch(_, let context), .valueNotFound(_, let context):
      return context.debugDescription
    case .dataCorrupted(let context): return context.debugDescription
    @unknown default: return "\(error)"
    }
  }
}

// MARK: - Wire shapes
//
// The names are the contract's, not Swift's conventions: this is where the two
// spellings meet, and renaming a field here would rename it on the wire.

struct ErrorResponse: Codable, Equatable {
  let code: String
  let errorMessage: String
}
struct RunningAppRequest: Codable { let appIds: [String] }
struct SwipeRequest: Codable {
  let appId: String?
  let startX: Double
  let startY: Double
  let endX: Double
  let endY: Double
  let duration: Double
}
struct SwipeV2Request: Codable {
  let startX: Double
  let startY: Double
  let endX: Double
  let endY: Double
  let duration: Double
  let appIds: [String]?
}
struct InputTextRequest: Codable {
  let text: String
  let appIds: [String]
}
/// duration is optional because its PRESENCE is what makes a touch a long
/// press; a tap that sent zero would be a different gesture.
struct TouchRequest: Codable {
  let x: Double
  let y: Double
  let duration: Double?
}
struct PressKeyRequest: Codable {
  let key: String
  let appIds: [String]
}
struct PressButtonRequest: Codable { let button: String }
struct EraseTextRequest: Codable {
  let charactersToErase: Int
  let appIds: [String]
}
struct SetOrientationRequest: Codable { let orientation: String }
struct SetPermissionsRequest: Codable { let permissions: [String: String] }
struct ViewHierarchyRequest: Codable {
  let appIds: [String]
  let excludeKeyboardElements: Bool
}
struct KeyboardRequest: Codable { let appIds: [String] }
struct LaunchAppRequest: Codable { let bundleId: String }
struct TerminateAppRequest: Codable { let appId: String }
