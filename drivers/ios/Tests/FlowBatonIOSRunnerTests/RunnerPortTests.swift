import Foundation
import Testing

@testable import FlowBatonIOSRunner

/// specs/02-device-drivers.md:68 gives the runner's port as env `PORT`,
/// defaulting to 22087. The runner ignored the variable entirely and always
/// took its port from a Swift default argument, so a sharded run — where the
/// host assigns each shard its own port — had no way to tell the runner where
/// to listen.
struct RunnerPortTests {
  @Test func defaultsToTheContractPortWhenUnset() throws {
    #expect(try RunnerPort.resolve([:]) == 22087)
  }

  @Test func readsTheEnvironmentWhenSet() throws {
    // The control for the test above: a resolver that always returned the
    // default would satisfy it and ignore every host-assigned port.
    #expect(try RunnerPort.resolve(["PORT": "41001"]) == 41001)
  }

  @Test func treatsAnEmptyValueAsUnset() throws {
    #expect(try RunnerPort.resolve(["PORT": ""]) == 22087)
    #expect(try RunnerPort.resolve(["PORT": "   "]) == 22087)
  }

  @Test func refusesAValueThatIsNotAPort() {
    // Falling back silently would bind a port the host is not talking to, and
    // the failure would surface much later as "the runner never came up".
    for raw in ["not-a-number", "-1", "70000", "22087.5", "0"] {
      #expect(throws: RunnerPort.Failure.malformedPort(raw)) {
        try RunnerPort.resolve(["PORT": raw])
      }
    }
  }

  @Test func aResolvedPortIsWhatTheServerActuallyBinds() throws {
    // The end of the chain: resolving is only useful if the server binds it.
    let server = LoopbackHTTPServer(port: try RunnerPort.resolve(["PORT": "41207"]))
    let bound = try server.start(timeout: 5)
    defer { server.stop() }
    #expect(bound == 41207)
  }
}
