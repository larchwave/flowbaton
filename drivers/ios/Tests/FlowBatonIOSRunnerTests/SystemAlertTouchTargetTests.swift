import CoreGraphics
import XCTest

@testable import FlowBatonIOSRunner

final class SystemAlertTouchTargetTests: XCTestCase {
  func testNoAlertLeavesAnOrdinaryTouchAlone() {
    XCTAssertEqual(
      SystemAlertTouchTarget.decide(at: CGPoint(x: 20, y: 30), buttonFramesByAlert: []),
      .noAlert)
  }

  func testUniqueButtonAtThePointIsTargetedDirectly() {
    let decision = SystemAlertTouchTarget.decide(
      at: CGPoint(x: 275, y: 518),
      buttonFramesByAlert: [
        [
          CGRect(x: 57, y: 492, width: 154, height: 52),
          CGRect(x: 218, y: 492, width: 154, height: 52),
        ]
      ])

    XCTAssertEqual(decision, .button(alert: 0, button: 1))
  }

  func testAlertWithoutAButtonAtThePointBlocksTheTouch() {
    XCTAssertEqual(
      SystemAlertTouchTarget.decide(
        at: CGPoint(x: 275, y: 610),
        buttonFramesByAlert: [[CGRect(x: 218, y: 492, width: 154, height: 52)]]),
      .blocked)
  }

  func testOverlappingButtonMatchesAreAmbiguousAndBlocked() {
    let overlapping = CGRect(x: 200, y: 490, width: 160, height: 60)
    XCTAssertEqual(
      SystemAlertTouchTarget.decide(
        at: CGPoint(x: 275, y: 518), buttonFramesByAlert: [[overlapping], [overlapping]]),
      .blocked)
  }
}
