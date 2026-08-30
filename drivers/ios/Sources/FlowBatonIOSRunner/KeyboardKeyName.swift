/// The pressKey vocabulary of `contracts/v0/ios-http.json`, which spells
/// every value in lower case.
///
/// The host translates the flow keyword to one of these and puts it on the
/// wire; a runner that answers only to some of them refuses a request its
/// own contract promises to serve. Session mmx25 spent a scenario on
/// `unsupported key enter` because the lookup table held "Enter" instead --
/// so case is deliberately not part of the vocabulary here, the way
/// pressButton has always folded its own.
// Sendable is explicit: a public type does not get the conformance inferred
// across a module boundary, and the runner keys a static table on this.
public enum KeyboardKeyName: String, CaseIterable, Sendable {
  case delete
  case `return`
  case enter
  case tab
  case space
  case escape

  public static func from(wire: String) -> KeyboardKeyName? {
    KeyboardKeyName(rawValue: wire.lowercased())
  }
}
