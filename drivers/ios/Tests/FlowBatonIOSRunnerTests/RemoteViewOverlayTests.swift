import XCTest

@testable import FlowBatonIOSRunner

/// The StoreKit rating prompt is drawn by another process. On an iPhone its
/// contents show up inside the app's own tree; over an iPad compatibility
/// window they do not, and the app's tree is empty (issue #43).
final class RemoteViewOverlayTests: XCTestCase {
  let prompt = TestSnapshot(
    label: "iTunes",
    children: [
      TestSnapshot(label: "Enjoying FlowBatonSettleFixture?", elementTypeCode: 48),
      TestSnapshot(label: "Not Now", elementTypeCode: 9),
    ])

  func testAPromptMissingFromTheAppIsServed() {
    let emptyApp = TestSnapshot(label: "FlowBatonSettleFixture")
    XCTAssertTrue(RemoteViewOverlay.serves(prompt, over: emptyApp))
  }

  func testAPromptTheAppAlreadyShowsIsNotServedTwice() {
    let app = TestSnapshot(
      label: "FlowBatonSettleFixture",
      children: [
        TestSnapshot(label: "Rate", elementTypeCode: 9),
        TestSnapshot(label: "Enjoying FlowBatonSettleFixture?", elementTypeCode: 48),
        TestSnapshot(label: "Not Now", elementTypeCode: 9),
      ])
    XCTAssertFalse(RemoteViewOverlay.serves(prompt, over: app))
  }

  func testAServiceWithNothingOnScreenIsNotServed() {
    let idle = TestSnapshot(label: "iTunes")
    XCTAssertFalse(RemoteViewOverlay.serves(idle, over: TestSnapshot(label: "App")))
  }
}
