import Foundation

/// Where the runner listens.
///
/// specs/02-device-drivers.md:68 — "HTTP server: FlyingFox, binds 127.0.0.1,
/// port = env `PORT` default 22087." The host reads the same variable
/// (`internal/cli/driver_ports.go`), because a sharded run gives each shard its
/// own port and a host and runner that disagree about the number simply never
/// meet.
public enum RunnerPort {
  /// The contract's port, used when nothing says otherwise.
  public static let contractDefault: UInt16 = 22087

  /// The name of the variable both sides read.
  public static let environmentVariable = "PORT"

  public enum Failure: Error, Equatable {
    /// PORT was set to something that is not a port. Reported rather than
    /// ignored: silently falling back would bind a port the host is not
    /// talking to, and the failure would surface much later as "the runner
    /// never came up".
    case malformedPort(String)
  }

  /// Resolves the port from an environment, defaulting to the contract's.
  public static func resolve(
    _ environment: [String: String] = ProcessInfo.processInfo.environment
  ) throws -> UInt16 {
    guard let raw = environment[environmentVariable] else {
      return contractDefault
    }
    let trimmed = raw.trimmingCharacters(in: .whitespaces)
    if trimmed.isEmpty {
      // An exported-but-empty variable is how a shell says "unset" often
      // enough that treating it as a mistake would be hostile.
      return contractDefault
    }
    // UInt16 alone would accept 0, which means "any free port" to the kernel —
    // the one value that makes the agreed number unknowable to the host.
    guard let port = UInt16(trimmed), port > 0 else {
      throw Failure.malformedPort(raw)
    }
    return port
  }
}
