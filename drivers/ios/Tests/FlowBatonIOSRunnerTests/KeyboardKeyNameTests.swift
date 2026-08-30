import Foundation
import Testing

@testable import FlowBatonIOSRunner

@Suite struct KeyboardKeyNameTests {
  /// contracts/v0/ios-http.json declares the pressKey enum. Every value in
  /// it has to resolve, or the runner refuses a request its own contract
  /// promises to serve.
  /// The vocabulary the contract publishes, read out of the contract rather
  /// than restated here. A list written down a third time is a list that can
  /// drift from both of the other two without any test noticing.
  static var contractKeys: [String] {
    guard
      let schema = IOSWireContractV0.schemas.first(where: { $0.name == "PressKeyRequest" }),
      let key = schema.fields.first(where: { $0.name == "key" }),
      let open = key.descriptor.firstIndex(of: "{"),
      let close = key.descriptor.lastIndex(of: "}")
    else { return [] }
    return key.descriptor[key.descriptor.index(after: open)..<close]
      .split(separator: ",").map { String($0).trimmingCharacters(in: .whitespaces) }
  }

  @Test func everyContractValueResolves() {
    let published = Self.contractKeys
    #expect(!published.isEmpty, "the contract publishes no pressKey vocabulary to check against")
    for wire in published {
      #expect(KeyboardKeyName.from(wire: wire) != nil, "the contract value \(wire) did not resolve")
    }
  }

  /// The other direction, which is the one that bites: a key the runner
  /// answers to but the contract never promised is a route the host cannot
  /// know to call, and it drifts silently because every request the host
  /// actually sends still works.
  @Test func theVocabularyIsExactlyWhatTheContractPublishes() {
    let published = Set(Self.contractKeys)
    let served = Set(KeyboardKeyName.allCases.map(\.rawValue))
    #expect(
      served == published,
      "the enum serves \(served.sorted()) and the contract publishes \(published.sorted())")
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
