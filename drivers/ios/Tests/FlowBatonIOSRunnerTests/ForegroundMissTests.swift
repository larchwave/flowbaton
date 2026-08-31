import Testing

@testable import FlowBatonIOSRunner

@Suite("Foreground miss")
struct ForegroundMissTests {
  /// A replay on 2026-08-30 failed with "none of com.apple.reminders is in the
  /// foreground" and left no way to tell a flow that navigated away from an app
  /// that had died. Both readings had to be chased by hand through a
  /// screenshot.
  @Test("names what each application was doing instead")
  func namesWhatEachApplicationWasDoing() {
    let backgrounded = ForegroundMiss.message([
      (appID: "com.apple.reminders", rawValue: ForegroundMiss.runningBackground)
    ])
    #expect(
      backgrounded
        == "no named application is in the foreground: com.apple.reminders is running in the background"
    )

    let dead = ForegroundMiss.message([
      (appID: "com.apple.reminders", rawValue: ForegroundMiss.notRunning)
    ])
    #expect(dead == "no named application is in the foreground: com.apple.reminders is not running")
    // The two readings must not collapse into one another; that is the whole
    // point of asking.
    #expect(backgrounded != dead)
  }

  @Test("reports every application the caller asked about")
  func reportsEveryApplication() {
    let message = ForegroundMiss.message([
      (appID: "com.example.one", rawValue: ForegroundMiss.notRunning),
      (appID: "com.example.two", rawValue: ForegroundMiss.runningBackground),
    ])
    #expect(message.contains("com.example.one is not running"))
    #expect(message.contains("com.example.two is running in the background"))
  }

  /// An unrecognized raw value must still produce a sentence: XCTest may add a
  /// state, and a crash or an empty message would be worse than a vague one.
  @Test("survives a state it does not know")
  func survivesAnUnknownState() {
    #expect(ForegroundMiss.describeState(99) == "is in an unknown state")
    #expect(ForegroundMiss.describeState(ForegroundMiss.unknown) == "is in an unknown state")
  }

  @Test("says so when no application was named")
  func saysSoWhenNoApplicationWasNamed() {
    #expect(
      ForegroundMiss.message([])
        == "no application was named, so none could be in the foreground")
  }
}
