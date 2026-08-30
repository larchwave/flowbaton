import Foundation

/// AutomationIssues turns XCUITest's own failure channel into a command error.
///
/// XCUITest reports a gesture it could not perform by recording an XCTIssue on
/// the running test case. An issue is not a Swift error: the call that caused
/// it returns normally, so the request answers 200 for work that never
/// happened, and the serving test can be torn down under the server (sessions
/// mmx22 and mmx23 ended as connection refused on the host's next request).
///
/// The alternative was to predict each refusal before making the call, and
/// that was measured and failed: gating typing on the keyboard's on-screen
/// geometry refused every typing command on the Simulator, where the software
/// keyboard does not present at all while typing still reaches the focused
/// field. Reading what XCUITest reports needs no prediction, and it covers
/// every gesture rather than the one command a guard was written for.
///
/// The runner's test case forwards `record(_:)` here instead of letting XCTest
/// fail the test; `capture` then reports anything raised during one command as
/// that command's error.
public final class AutomationIssues: @unchecked Sendable {
  /// The instance the runner's test case reports into. A singleton because
  /// XCTest's funnel is a method on the test case, with nowhere to pass one.
  public static let shared = AutomationIssues()

  private let lock = NSLock()
  private var pending: [String] = []

  public init() {}

  /// record files one issue against whatever command is running.
  public func record(_ description: String) {
    lock.lock()
    defer { lock.unlock() }
    pending.append(description)
  }

  /// capture runs one command's work and fails it with anything XCUITest
  /// recorded while it ran.
  ///
  /// Issues left over from outside any command are dropped at entry: the
  /// runner serves for an hour, and one stale issue would otherwise refuse
  /// every command left in the session. The failure is a precondition, which
  /// the host does not retry -- a gesture XCUITest could not synthesize does
  /// not start working on a second identical attempt.
  public func capture<T>(_ work: () throws -> T) throws -> T {
    _ = drain()
    let value = try work()
    let raised = drain()
    if !raised.isEmpty {
      throw AutomationError.precondition(raised.joined(separator: "; "))
    }
    return value
  }

  private func drain() -> [String] {
    lock.lock()
    defer { lock.unlock() }
    let taken = pending
    pending = []
    return taken
  }
}
