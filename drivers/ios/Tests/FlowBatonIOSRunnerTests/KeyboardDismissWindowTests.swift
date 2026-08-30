import CoreGraphics
import Testing

@testable import FlowBatonIOSRunner

/// The runner dies when XCUITest types with no keyboard focus: the issue it
/// raises is an XCTIssue, not a Swift error, so nothing can catch it. Every
/// guard against that has to be right about a keyboard that is MOVING --
/// hideKeyboard presses Return and the next command lands inside the dismiss
/// animation.
@Suite struct KeyboardDismissWindowTests {
  static let screen = CGRect(x: 0, y: 0, width: 402, height: 874)

  /// One point of overlap is the tail of the dismiss animation, not a usable
  /// keyboard. Accepting it leaves the crash window open at exactly the
  /// moment sessions mmx22 and mmx23 hit it.
  @Test func aKeyboardAlmostEntirelyGoneIsNotPresented() {
    for y in [873.0, 860.0, 800.0] {
      let leaving = CGRect(x: 0, y: y, width: 402, height: 233)
      #expect(
        KeyboardPresence.isPresented(keyboard: leaving, screen: Self.screen) == false,
        "a keyboard at y=\(y) has \(874 - y) of 233 points on screen")
    }
  }

  @Test func aKeyboardMostlyOnScreenIsPresented() {
    for y in [641.0, 700.0, 750.0] {
      let arriving = CGRect(x: 0, y: y, width: 402, height: 233)
      #expect(KeyboardPresence.isPresented(keyboard: arriving, screen: Self.screen))
    }
  }
}
