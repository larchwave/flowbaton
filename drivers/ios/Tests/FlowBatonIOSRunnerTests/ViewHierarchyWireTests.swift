import Foundation
import XCTest

@testable import FlowBatonIOSRunner

/// The viewHierarchy payload is the only response the runner builds that the
/// host decodes into a structured tree rather than passing through, so it is the
/// one where a wrong field name is a decode error on the host with no clue where
/// it came from. `Frame`'s keys are capitalized (X, Y, Width, Height) and the
/// element's are not, which is exactly the kind of detail that rots.
///
/// The important test READS `IOSWireContractV0` rather than restating the field
/// list, so a contract change that this encoder does not follow fails here.
final class ViewHierarchyWireTests: XCTestCase {

  func testEveryFieldTheContractRequiresIsEncoded() throws {
    let encoded = try encodedRoot(from: TestSnapshot.simple())

    var missing: [String] = []
    for name in Self.requiredFields(of: "AXElement") where encoded[name] == nil {
      missing.append("AXElement.\(name)")
    }
    let frame = try XCTUnwrap(encoded["frame"] as? [String: Any])
    for name in Self.requiredFields(of: "Frame") where frame[name] == nil {
      missing.append("Frame.\(name)")
    }
    XCTAssertEqual(
      missing.sorted(), [],
      "fields the frozen contract requires that this encoder does not send")
  }

  func testNoFieldIsSentThatTheContractDoesNotDeclare() throws {
    // The other direction. An extra key is not a decode failure on the host, so
    // nothing would ever notice it — until the field it was meant to be got
    // added under the right name and the two disagreed.
    let encoded = try encodedRoot(from: TestSnapshot.simple())
    let declared = Set(Self.declaredFields(of: "AXElement"))
    let undeclared = Set(encoded.keys).subtracting(declared)
    XCTAssertEqual(undeclared.sorted(), [], "fields the contract does not declare")

    let frame = try XCTUnwrap(encoded["frame"] as? [String: Any])
    let declaredFrame = Set(Self.declaredFields(of: "Frame"))
    XCTAssertEqual(
      Set(frame.keys).subtracting(declaredFrame).sorted(), [],
      "Frame fields the contract does not declare")
  }

  func testTheFrameKeysAreCapitalized() throws {
    // Worth its own test because it is the one difference between this shape and
    // every other shape on the wire, and a lowercase x decodes as zero rather
    // than failing — a tap at the top-left corner instead of on the button.
    let encoded = try encodedRoot(from: TestSnapshot.simple())
    let frame = try XCTUnwrap(encoded["frame"] as? [String: Any])
    XCTAssertEqual(frame["X"] as? Double, 11)
    XCTAssertEqual(frame["Y"] as? Double, 22)
    XCTAssertEqual(frame["Width"] as? Double, 33)
    XCTAssertEqual(frame["Height"] as? Double, 44)
    XCTAssertNil(frame["x"], "a lowercase key decodes as zero rather than failing")
  }

  func testAbsentOptionalFieldsAreOmittedRatherThanNull() throws {
    // The host decodes value, title and placeholderValue as pointers. A null is
    // not the same as absent to every decoder in the chain, and the runner
    // already shipped one null-versus-empty defect on the appIds field.
    let payload = hierarchyPayload(from: TestSnapshot.bare(), excludeKeyboardElements: false)
    let bytes = try JSONEncoder().encode(payload)
    let text = String(decoding: bytes, as: UTF8.self)
    for key in ["value", "title", "placeholderValue", "children"] {
      XCTAssertFalse(text.contains("\"\(key)\":null"), "\(key) was sent as null")
    }
  }

  func testAKeyboardSubtreeIsDroppedWhenExcluded() throws {
    let payload = hierarchyPayload(from: TestSnapshot.withKeyboard(), excludeKeyboardElements: true)
    let labels = Self.labels(of: payload.axElement)
    XCTAssertTrue(labels.contains("field"), "the app's own elements were dropped: \(labels)")
    XCTAssertFalse(labels.contains("keyboard"), "the keyboard survived exclusion")
    XCTAssertFalse(labels.contains("Q"), "a key under the keyboard survived exclusion")
  }

  func testAKeyboardSubtreeIsKeptWhenNotExcluded() throws {
    // The control. Dropping the keyboard unconditionally would satisfy the test
    // above and lose every keyboard assertion a flow can make.
    let payload = hierarchyPayload(
      from: TestSnapshot.withKeyboard(), excludeKeyboardElements: false)
    let labels = Self.labels(of: payload.axElement)
    XCTAssertTrue(labels.contains("keyboard"), "the keyboard was dropped anyway: \(labels)")
    XCTAssertTrue(labels.contains("Q"), "the keys were dropped anyway")
  }

  func testDepthCountsLevelsNotNodes() throws {
    XCTAssertEqual(
      hierarchyPayload(from: TestSnapshot.bare(), excludeKeyboardElements: false).depth, 1,
      "a lone root is one level, not zero")
    XCTAssertEqual(
      hierarchyPayload(from: TestSnapshot.withKeyboard(), excludeKeyboardElements: false).depth, 3,
      "root, keyboard, key")
  }

  // MARK: - Reading the frozen contract

  private static func requiredFields(of schema: String) -> [String] {
    contractSchema(schema)?.required ?? []
  }

  private static func declaredFields(of schema: String) -> [String] {
    (contractSchema(schema)?.fields ?? []).map(\.name)
  }

  private static func contractSchema(_ name: String) -> IOSSchemaV0? {
    IOSWireContractV0.schemas.first { $0.name == name }
  }

  private func encodedRoot(from snapshot: any AccessibilitySnapshot) throws -> [String: Any] {
    let payload = hierarchyPayload(from: snapshot, excludeKeyboardElements: false)
    let bytes = try JSONEncoder().encode(payload)
    let decoded = try JSONSerialization.jsonObject(with: bytes) as? [String: Any]
    return try XCTUnwrap(try XCTUnwrap(decoded)["axElement"] as? [String: Any])
  }

  private static func labels(of element: WireAXElement) -> [String] {
    [element.label] + (element.children ?? []).flatMap(labels(of:))
  }
}

/// TestSnapshot is what stands in for XCUIElementSnapshot on a Mac. Its
/// existence is the reason the walk above is testable at all.
struct TestSnapshot: AccessibilitySnapshot {
  var identifier: String = ""
  var frameOrigin: (x: Double, y: Double) = (0, 0)
  var frameSize: (width: Double, height: Double) = (0, 0)
  var stringValue: String?
  var title: String?
  var label: String = ""
  var elementTypeCode: Int = 0
  var enabled: Bool = true
  var placeholder: String?
  var selected: Bool = false
  var focused: Bool = false
  var isKeyboard: Bool = false
  var children: [TestSnapshot] = []

  var childSnapshots: [any AccessibilitySnapshot] { children }

  static func simple() -> TestSnapshot {
    TestSnapshot(
      identifier: "root", frameOrigin: (11, 22), frameSize: (33, 44),
      stringValue: "v", title: "t", label: "l", elementTypeCode: 1,
      placeholder: "p", selected: true, focused: true)
  }

  static func bare() -> TestSnapshot {
    TestSnapshot(label: "bare")
  }

  static func withKeyboard() -> TestSnapshot {
    TestSnapshot(
      label: "window",
      children: [
        TestSnapshot(label: "field", elementTypeCode: 49),
        TestSnapshot(
          label: "keyboard", elementTypeCode: 50, isKeyboard: true,
          children: [TestSnapshot(label: "Q")]),
      ])
  }
}

extension ViewHierarchyWireTests {

  func testFocusIsFoundAnywhereInTheSubtree() {
    // Depth matters: the focused node is a text field several levels inside the
    // app, never the root, so a check that only looks at the top always says no.
    let focused = TestSnapshot(identifier: "field", label: "Search", focused: true)
    let tree = TestSnapshot(
      identifier: "app", label: "Settings",
      children: [TestSnapshot(identifier: "table", label: "", children: [focused])])
    XCTAssertTrue(canReceiveTyping(in: tree))
  }

  func testAnOpenKeyboardCountsWhileNothingReportsFocus() {
    // An iOS text field with the keyboard open reports hasFocus false, and the
    // public attributes carry no per-node keyboard focus. Reading the flag
    // alone refuses every typing command on the platform.
    let tree = TestSnapshot(
      identifier: "app", label: "Reminders",
      children: [
        TestSnapshot(identifier: "field", label: "Search", elementTypeCode: 45),
        TestSnapshot(
          label: "keyboard", elementTypeCode: 19, isKeyboard: true,
          children: [TestSnapshot(label: "Q")]),
      ])
    XCTAssertTrue(canReceiveTyping(in: tree))
  }

  func testNoFocusAnywhereIsReportedAsNoFocus() {
    let tree = TestSnapshot(
      identifier: "app", label: "Settings",
      children: [
        TestSnapshot(
          identifier: "table", label: "",
          children: [
            TestSnapshot(identifier: "cell", label: "General")
          ])
      ])
    XCTAssertFalse(canReceiveTyping(in: tree))
  }

  func testAFocusedRootCountsWithoutAnyChildren() {
    XCTAssertTrue(canReceiveTyping(in: TestSnapshot(identifier: "a", label: "", focused: true)))
  }
}

extension ViewHierarchyWireTests {

  /// The contract roots every tree it serves in a zero-sized node and hangs the
  /// system chrome under a SECOND zero-sized node beside the app.
  ///
  /// The wrapper contains status bars only. Home-screen content must never
  /// become a selector candidate while another app is active.
  ///
  /// The root earns its place twice over. It is the only element on that screen a
  /// selector with `enabled: false` can resolve, and it is where the chrome
  /// hangs — without it no flow can assert on the clock, the battery or a system
  /// alert.
  func testChromeHangsUnderItsOwnZeroSizedWrapper() {
    let app = TestSnapshot(
      identifier: "app", frameOrigin: (0, 0), frameSize: (402, 874), label: "Settings")
    let bar = TestSnapshot(
      identifier: "bar", frameOrigin: (0, 0), frameSize: (402, 54), label: "status")

    let payload = hierarchyPayload(
      app: app, systemChrome: [bar, bar], excludeKeyboardElements: false)

    let root = payload.axElement
    XCTAssertEqual(root.frame.width, 0)
    XCTAssertEqual(root.frame.height, 0)
    XCTAssertFalse(root.enabled)
    XCTAssertEqual(root.children?.count, 2)
    XCTAssertEqual(root.children?.first?.label, "Settings")

    let wrapper = root.children?.last
    XCTAssertEqual(wrapper?.frame.width, 0)
    XCTAssertEqual(wrapper?.frame.height, 0)
    XCTAssertFalse(wrapper?.enabled ?? true)
    XCTAssertEqual(wrapper?.children?.count, 2)
    XCTAssertEqual(wrapper?.children?.first?.label, "status")
    XCTAssertEqual(payload.depth, 3)
  }

  /// No chrome means no wrapper — an empty zero-sized node would be one more
  /// element that `enabled: false` and `index` can both see, and the contract
  /// does not serve one when there is nothing behind the app.
  func testNoChromeLeavesTheAppAloneUnderTheRoot() {
    let payload = hierarchyPayload(
      app: TestSnapshot.bare(), systemChrome: [], excludeKeyboardElements: false)
    XCTAssertEqual(payload.axElement.children?.count, 1)
    XCTAssertEqual(payload.depth, 2)
  }
}
