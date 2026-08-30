import Testing

@testable import FlowBatonIOSRunner

@Suite struct KeyboardKeyNameTests {
  /// contracts/v0/ios-http.json declares the pressKey enum. Every value in
  /// it has to resolve, or the runner refuses a request its own contract
  /// promises to serve.
  @Test func everyContractValueResolves() {
    for wire in ["delete", "return", "enter", "tab", "space", "escape"] {
      #expect(KeyboardKeyName.from(wire: wire) != nil, "the contract value \(wire) did not resolve")
    }
  }

  /// Session mmx25 spent a whole scenario on `unsupported key enter`: the
  /// host sends the contract's lower-case spelling and the table happened to
  /// hold "Enter". Case is not part of the vocabulary.
  @Test func caseIsNotPartOfTheVocabulary() {
    #expect(KeyboardKeyName.from(wire: "ENTER") == .enter)
    #expect(KeyboardKeyName.from(wire: "Enter") == .enter)
    #expect(KeyboardKeyName.from(wire: "enter") == .enter)
  }

  @Test func aValueOutsideTheContractIsRefused() {
    #expect(KeyboardKeyName.from(wire: "f13") == nil)
    #expect(KeyboardKeyName.from(wire: "") == nil)
  }
}
