import Testing

@testable import FlowBatonIOSRunner

/// XCUITest reports a gesture it could not perform by recording an XCTIssue,
/// which is not a Swift error: nothing at the call site can catch it, so the
/// command returns 200 for work that never happened, and the runner has
/// already died once this way (sessions mmx22, mmx23). Predicting the refusal
/// per command was tried and failed -- keyboard geometry refused every typing
/// command on the Simulator. Catching the issue after the fact needs no
/// prediction at all.
@Suite struct AutomationIssuesTests {

  @Test func aRunThatRaisesNothingReturnsItsValue() throws {
    let issues = AutomationIssues()
    #expect(try issues.capture { 7 } == 7)
  }

  @Test func anIssueRaisedDuringTheRunBecomesTheCommandsError() {
    let issues = AutomationIssues()
    #expect(throws: AutomationError.self) {
      try issues.capture {
        issues.record("Neither element nor any descendant has keyboard focus")
      }
    }
  }

  /// The message is the only account of what went wrong that reaches the
  /// host: XCUITest's own wording is more precise than anything this file
  /// could invent about a gesture it did not see.
  @Test func theIssueTextReachesTheHost() {
    let issues = AutomationIssues()
    do {
      try issues.capture { issues.record("Neither element nor any descendant has keyboard focus") }
      Issue.record("capture accepted a run that raised an issue")
    } catch let error as AutomationError {
      #expect(error.message.contains("Neither element nor any descendant has keyboard focus"))
      // Non-retryable: a gesture XCUITest could not synthesize will not
      // start working on a second identical attempt, and the host retries
      // anything that is not a precondition or a timeout.
      #expect(error.code == "precondition")
    } catch {
      Issue.record("unexpected error \(error)")
    }
  }

  @Test func everyIssueOfOneRunIsReported() {
    let issues = AutomationIssues()
    do {
      try issues.capture {
        issues.record("first")
        issues.record("second")
      }
      Issue.record("capture accepted a run that raised two issues")
    } catch let error as AutomationError {
      #expect(error.message.contains("first"))
      #expect(error.message.contains("second"))
    } catch {
      Issue.record("unexpected error \(error)")
    }
  }

  /// A command must not inherit the failure of the one before it. The runner
  /// serves for an hour; one stale issue would otherwise refuse every command
  /// left in the session.
  @Test func anIssueLeftOverFromAnEarlierRunDoesNotFailTheNextOne() throws {
    let issues = AutomationIssues()
    issues.record("something that happened outside any command")
    #expect(try issues.capture { 1 } == 1)
    #expect(try issues.capture { 2 } == 2)
  }

  /// A throwing run keeps its own error: the automation code raises errors
  /// that say more than "XCUITest recorded something".
  @Test func aRunThatThrowsKeepsItsOwnError() {
    let issues = AutomationIssues()
    #expect(throws: AutomationError.precondition("no such app")) {
      try issues.capture { throw AutomationError.precondition("no such app") }
    }
  }
}
