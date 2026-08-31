import Foundation

/// Why no requested application was in the foreground.
///
/// "none of com.apple.reminders is in the foreground" says the check failed and
/// nothing about which failure it was. An app sitting in the background is a
/// flow that navigated away; an app that is not running at all is a crash. They
/// send an operator to different places, and XCUITest already knows which one
/// it is -- the state was being discarded.
///
/// The state arrives as `XCUIApplication.State.rawValue` because XCTest's UI
/// types are not linked into this module. The raw values are frozen API.
public enum ForegroundMiss {
  /// Raw values of `XCUIApplication.State`.
  public static let unknown = 0
  public static let notRunning = 1
  public static let runningBackgroundSuspended = 2
  public static let runningBackground = 3
  public static let runningForeground = 4

  /// describeState names one application's state in the words an operator
  /// reading a failed step needs.
  public static func describeState(_ rawValue: Int) -> String {
    switch rawValue {
    case notRunning: return "is not running"
    case runningBackgroundSuspended: return "is suspended in the background"
    case runningBackground: return "is running in the background"
    case runningForeground: return "is in the foreground"
    default: return "is in an unknown state"
    }
  }

  /// message reports every application that was asked for and what each was
  /// doing instead. The order is the caller's, which is the flow's.
  public static func message(_ states: [(appID: String, rawValue: Int)]) -> String {
    if states.isEmpty {
      return "no application was named, so none could be in the foreground"
    }
    let described = states.map { "\($0.appID) \(describeState($0.rawValue))" }
    return "no named application is in the foreground: "
      + described.joined(separator: "; ")
  }
}
