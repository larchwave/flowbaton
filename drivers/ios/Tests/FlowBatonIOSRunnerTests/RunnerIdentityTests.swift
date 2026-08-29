import Foundation
import Testing

@testable import FlowBatonIOSRunner

/// The host hands a managed runner an id in `FLOWBATON_RUNNER_ID` and accepts
/// a `/status` answer as its child only when the id comes back. A runner that
/// ignored the variable would look like a stranger to the host that started it.
struct RunnerIdentityTests {
  @Test func isAbsentWhenTheHostSetNone() throws {
    #expect(try RunnerIdentity.resolve([:]) == nil)
    #expect(try RunnerIdentity.resolve(["FLOWBATON_RUNNER_ID": "  "]) == nil)
  }

  @Test func readsTheEnvironmentWhenSet() throws {
    #expect(try RunnerIdentity.resolve(["FLOWBATON_RUNNER_ID": "0f3a9c"]) == "0f3a9c")
  }

  @Test func refusesAnIdThatCannotGoIntoTheStatusBodyVerbatim() {
    // The status answer is built by concatenation, not an encoder, so a
    // quote or backslash here would corrupt the one route that must never
    // fail. Refusing at startup keeps that property.
    #expect(throws: RunnerIdentity.Failure.malformedIdentity(#"a"b"#)) {
      try RunnerIdentity.resolve(["FLOWBATON_RUNNER_ID": #"a"b"#])
    }
  }
}
