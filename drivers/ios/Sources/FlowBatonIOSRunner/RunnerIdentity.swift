import Foundation

/// Who this runner is, as far as the host is concerned.
///
/// A host that starts a runner hands it an id in `FLOWBATON_RUNNER_ID`, and
/// the runner echoes it in `/status`. Two runners on one port answer the health
/// check identically otherwise, and a host once took another simulator's runner
/// for the child it had just started. The id is opaque to the runner.
public enum RunnerIdentity {
  /// The name of the variable both sides read.
  public static let environmentVariable = "FLOWBATON_RUNNER_ID"

  public enum Failure: Error, Equatable {
    /// The id carries characters that cannot go into the status body verbatim.
    /// Reported rather than escaped: the status answer is a constant, not an
    /// encode, so it keeps answering when everything else is failing.
    case malformedIdentity(String)
  }

  /// Resolves the id from an environment; nil when the host set none.
  public static func resolve(
    _ environment: [String: String] = ProcessInfo.processInfo.environment
  ) throws -> String? {
    guard let raw = environment[environmentVariable] else {
      return nil
    }
    let trimmed = raw.trimmingCharacters(in: .whitespaces)
    if trimmed.isEmpty {
      return nil
    }
    let allowed = CharacterSet.alphanumerics.union(CharacterSet(charactersIn: "-_"))
    guard trimmed.unicodeScalars.allSatisfy(allowed.contains) else {
      throw Failure.malformedIdentity(raw)
    }
    return trimmed
  }
}
