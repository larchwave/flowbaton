import CoreGraphics
import Testing

@testable import FlowBatonIOSRunner

@Suite struct KeyboardPresenceTests {
  static let screen = CGRect(x: 0, y: 0, width: 402, height: 874)

  /// The frame observed live on a Reminders screen with no keyboard: parked
  /// exactly at the bottom edge, so it touches nothing on screen.
  @Test func aDismissedKeyboardParkedBelowTheScreenIsNotPresented() {
    let dismissed = CGRect(x: 0, y: 874, width: 402, height: 233)
    #expect(KeyboardPresence.isPresented(keyboard: dismissed, screen: Self.screen) == false)
  }

  @Test func aKeyboardRestingOnTheLowerScreenIsPresented() {
    let up = CGRect(x: 0, y: 641, width: 402, height: 233)
    #expect(KeyboardPresence.isPresented(keyboard: up, screen: Self.screen))
  }

  @Test func anEmptyFrameIsNeverPresented() {
    #expect(KeyboardPresence.isPresented(keyboard: .zero, screen: Self.screen) == false)
  }
}
