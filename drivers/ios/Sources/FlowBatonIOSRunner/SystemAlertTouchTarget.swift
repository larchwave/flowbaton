import CoreGraphics

/// Decides whether a screen point can be delivered while a system alert is
/// visible.
///
/// A selector can resolve a SpringBoard-owned alert button from the combined
/// hierarchy while still carrying the tested application's bundle id. Sending
/// that point through the application's coordinate anchor lets XCTest dismiss
/// the system alert and deliver the same touch to the app underneath. A unique
/// system button is safe to target directly. Every other point is refused while
/// an alert is present because the runner cannot prove which control should
/// receive it.
enum SystemAlertTouchTarget: Equatable {
  case noAlert
  case button(alert: Int, button: Int)
  case blocked

  static func decide(at point: CGPoint, buttonFramesByAlert: [[CGRect]]) -> Self {
    guard !buttonFramesByAlert.isEmpty else { return .noAlert }

    var matches: [(alert: Int, button: Int)] = []
    for (alertIndex, buttonFrames) in buttonFramesByAlert.enumerated() {
      for (buttonIndex, frame) in buttonFrames.enumerated() where frame.contains(point) {
        matches.append((alert: alertIndex, button: buttonIndex))
      }
    }

    guard matches.count == 1, let match = matches.first else { return .blocked }
    return .button(alert: match.alert, button: match.button)
  }
}
