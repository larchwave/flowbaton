import Foundation

/// The seam between the wire and XCUITest.
///
/// Everything that needs a UI test session lives behind this protocol, which
/// is what lets the router — the part that decides what a request means — be
/// tested with `swift test` on a Mac, without a simulator or a test host.
///
/// The method set is the eighteen routes of the frozen contract and nothing
/// else. A method here that no route reaches would be code no wire can call.
public protocol DeviceAutomation: Sendable {
  func runningApp(appIDs: [String]) throws -> String
  func swipe(
    startX: Double, startY: Double, endX: Double, endY: Double, duration: Double, appID: String?)
    throws
  func swipeV2(
    startX: Double, startY: Double, endX: Double, endY: Double, duration: Double, appIDs: [String])
    throws
  func inputText(_ text: String, appIDs: [String]) throws
  /// A nil duration is a tap; a present one is a long press.
  func touch(x: Double, y: Double, duration: Double?) throws
  func screenshot(compressed: Bool) throws -> Data
  func isScreenStatic() throws -> Bool
  func pressKey(_ key: String, appIDs: [String]) throws
  func pressButton(_ button: String) throws
  func eraseText(charactersToErase: Int, appIDs: [String]) throws
  func deviceInfo() throws -> DeviceInfoPayload
  func setOrientation(_ orientation: String) throws
  func setPermissions(_ permissions: [String: String]) throws
  func viewHierarchy(appIDs: [String], excludeKeyboardElements: Bool) throws -> Data
  func isKeyboardVisible(appIDs: [String]) throws -> Bool
  func launchApp(bundleID: String) throws
  func terminateApp(appID: String) throws
}

/// The screen geometry the contract's deviceInfo route returns.
public struct DeviceInfoPayload: Codable, Equatable, Sendable {
  public let widthPoints: Double
  public let heightPoints: Double
  public let widthPixels: Double
  public let heightPixels: Double
  public let orientation: String

  public init(
    widthPoints: Double, heightPoints: Double, widthPixels: Double, heightPixels: Double,
    orientation: String
  ) {
    self.widthPoints = widthPoints
    self.heightPoints = heightPoints
    self.widthPixels = widthPixels
    self.heightPixels = heightPixels
    self.orientation = orientation
  }
}

/// A failure with a place on the wire.
///
/// The three cases are the contract's own error codes, and each carries the
/// status the contract maps it to. An automation failure that reached the wire
/// as a bare 500 would tell the host to retry something that can never
/// succeed — the host treats precondition and timeout as non-retryable.
public enum AutomationError: Error, Equatable {
  /// The request asked for something that cannot be done in the current state:
  /// a bundle id that is not installed, an element that is not there.
  case precondition(String)
  /// XCUITest stopped waiting. The contract pins both timeout signatures as
  /// non-retryable.
  case timeout(String)
  /// Anything else.
  case internalFailure(String)

  public var code: String {
    switch self {
    case .precondition: return "precondition"
    case .timeout: return "timeout"
    case .internalFailure: return "internal"
    }
  }

  public var httpStatus: Int {
    switch self {
    case .precondition: return 400
    case .timeout: return 408
    case .internalFailure: return 500
    }
  }

  public var message: String {
    switch self {
    case .precondition(let message), .timeout(let message), .internalFailure(let message):
      return message
    }
  }
}
