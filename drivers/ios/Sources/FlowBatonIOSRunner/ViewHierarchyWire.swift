import Foundation

/// The viewHierarchy response shape, and the walk that builds it.
///
/// This lives in the framework rather than beside the XCUITest code for one
/// reason: it is the half that can be tested without a simulator. The tree walk,
/// the keyboard exclusion, the depth, and the exact field names the host decodes
/// are all pure, and all of them are places a mistake would surface as a decode
/// error on the host with no clue where it came from.
///
/// `AccessibilitySnapshot` is the seam. XCUITest's `XCUIElementSnapshot`
/// conforms to it in the UI-test target; a struct conforms to it in the unit
/// tests. Nothing here imports XCTest.

/// AccessibilitySnapshot is the subset of an accessibility node the wire needs.
public protocol AccessibilitySnapshot {
  var identifier: String { get }
  var frameOrigin: (x: Double, y: Double) { get }
  var frameSize: (width: Double, height: Double) { get }
  var stringValue: String? { get }
  var title: String? { get }
  var label: String { get }
  var elementTypeCode: Int { get }
  var enabled: Bool { get }
  var placeholder: String? { get }
  var selected: Bool { get }
  var focused: Bool { get }
  /// Whether this node is the on-screen keyboard. Answered by the adapter
  /// rather than checked against a raw element-type number here, because the
  /// number is XCUITest's to know and a hardcoded one would rot silently.
  var isKeyboard: Bool { get }
  var childSnapshots: [any AccessibilitySnapshot] { get }
}

/// WireFrame is the contract's Frame. Its keys are capitalized — X, Y, Width,
/// Height — which is the single most likely thing to get wrong here, so the
/// CodingKeys are explicit and a test reads the frozen contract to check them.
public struct WireFrame: Codable, Equatable, Sendable {
  public let x: Double
  public let y: Double
  public let width: Double
  public let height: Double

  enum CodingKeys: String, CodingKey {
    case x = "X"
    case y = "Y"
    case width = "Width"
    case height = "Height"
  }

  public init(x: Double, y: Double, width: Double, height: Double) {
    self.x = x
    self.y = y
    self.width = width
    self.height = height
  }
}

/// WireAXElement is the contract's AXElement, field for field.
///
/// The optional fields are optional in the contract too, and omitting them
/// rather than sending null matters: the host decodes them as pointers and a
/// null is not the same as absent to every decoder in the chain.
public struct WireAXElement: Codable, Equatable, Sendable {
  public let identifier: String
  public let frame: WireFrame
  public let value: String?
  public let title: String?
  public let label: String
  public let elementType: Int
  public let enabled: Bool
  public let horizontalSizeClass: Int
  public let verticalSizeClass: Int
  public let placeholderValue: String?
  public let selected: Bool
  public let hasFocus: Bool
  public let children: [WireAXElement]?
  public let windowContextID: Double
  public let displayID: Int
}

/// WireHierarchy is the contract's ViewHierarchyResponse.
public struct WireHierarchy: Codable, Equatable, Sendable {
  public let axElement: WireAXElement
  public let depth: Int
}

/// hierarchyPayload walks a snapshot into the wire shape.
///
/// excludeKeyboardElements drops the keyboard subtree entirely rather than just
/// the keyboard node: the host asks for this when it wants the app's own
/// elements, and a keyboard's hundred key nodes are what makes the answer
/// unusable.
public func hierarchyPayload(
  from snapshot: any AccessibilitySnapshot,
  excludeKeyboardElements: Bool
) -> WireHierarchy {
  let root = wireElement(from: snapshot, excludeKeyboardElements: excludeKeyboardElements)
  return WireHierarchy(axElement: root, depth: hierarchyDepth(of: root))
}

/// hierarchyPayload(app:systemChrome:) builds the public hierarchy tree:
///
///	root          bounds=[0,0][0,0], no enabled
///	  app         bounds=[0,0][402,874]
///	  wrapper     bounds=[0,0][0,0], no enabled
///	    statusBar bounds=[0,0][402,54]
///	    statusBar bounds=[0,0][402,54]
///
/// Exactly two of its 178 rows carry no `enabled` attribute, and they are the
/// root and that wrapper. The whole chrome subtree is nine nodes — snapshotting
/// the springboard wholesale instead puts the home screen behind the app into
/// the tree, 298 nodes against 178, and makes every home icon a candidate.
///
/// The zero-sized nodes are not decoration. They are the only elements on that
/// screen a selector with `enabled: false` can resolve, and the wrapper is where
/// the status bar hangs — without it no flow can assert on the clock, the
/// battery or a system alert.
public func hierarchyPayload(
  app: any AccessibilitySnapshot,
  systemChrome: [any AccessibilitySnapshot],
  excludeKeyboardElements: Bool
) -> WireHierarchy {
  var children = [wireElement(from: app, excludeKeyboardElements: excludeKeyboardElements)]
  if !systemChrome.isEmpty {
    children.append(
      zeroSizedNode(
        wrapping: systemChrome.map {
          wireElement(from: $0, excludeKeyboardElements: excludeKeyboardElements)
        }))
  }
  let root = zeroSizedNode(wrapping: children)
  return WireHierarchy(axElement: root, depth: hierarchyDepth(of: root))
}

func zeroSizedNode(wrapping children: [WireAXElement]) -> WireAXElement {
  WireAXElement(
    identifier: "", frame: WireFrame(x: 0, y: 0, width: 0, height: 0),
    value: nil, title: nil, label: "", elementType: 0, enabled: false,
    horizontalSizeClass: 0, verticalSizeClass: 0, placeholderValue: nil,
    selected: false, hasFocus: false, children: children,
    windowContextID: 0, displayID: 0)
}

func wireElement(
  from snapshot: any AccessibilitySnapshot,
  excludeKeyboardElements: Bool
) -> WireAXElement {
  var children: [WireAXElement] = []
  for child in snapshot.childSnapshots {
    if excludeKeyboardElements && child.isKeyboard {
      continue
    }
    children.append(wireElement(from: child, excludeKeyboardElements: excludeKeyboardElements))
  }
  return WireAXElement(
    identifier: snapshot.identifier,
    frame: WireFrame(
      x: snapshot.frameOrigin.x, y: snapshot.frameOrigin.y,
      width: snapshot.frameSize.width, height: snapshot.frameSize.height),
    value: snapshot.stringValue,
    title: snapshot.title,
    label: snapshot.label,
    elementType: snapshot.elementTypeCode,
    enabled: snapshot.enabled,
    // XCUITest exposes no size class on a snapshot. Zero is the contract's
    // integer and the host reads these only to group windows.
    horizontalSizeClass: 0,
    verticalSizeClass: 0,
    placeholderValue: snapshot.placeholder,
    selected: snapshot.selected,
    hasFocus: snapshot.focused,
    children: children.isEmpty ? nil : children,
    windowContextID: 0,
    displayID: 0)
}

/// hierarchyDepth counts levels, so a lone root is 1 rather than 0. The host
/// reports it and nothing branches on it, but a zero for a real tree would look
/// like an empty screen.
func hierarchyDepth(of element: WireAXElement) -> Int {
  guard let children = element.children, !children.isEmpty else { return 1 }
  return 1 + (children.map(hierarchyDepth(of:)).max() ?? 0)
}

/// canReceiveTyping reports whether this subtree has somewhere for typed text
/// to land: a focused node, or an open keyboard.
///
/// Typing into a screen where nothing has focus makes XCUITest record "Neither
/// element nor any descendant has keyboard focus". That is an XCTIssue, not a
/// Swift error — so it cannot be caught, the request answers 200 for a command
/// that typed nothing, and with continueAfterFailure off it tears down the
/// serving test. Returning the issue as a command error keeps the server alive
/// for later commands.
///
/// The `focused` flag alone is not enough: it carries XCUITest's `hasFocus`,
/// which is UI focus, and an iOS text field with the keyboard open reports it
/// as false. The public element attributes expose no per-node keyboard focus,
/// so the open keyboard is the signal that typing has a destination.
///
/// So the check happens first, here, where it is a value a request handler can
/// refuse on. It lives in the framework rather than beside the XCUITest code
/// because that makes it testable without a simulator.
///
/// The parked-keyboard problem is NOT this function's, whatever this comment
/// said before 2026-08-30. A dismissed keyboard does stay matchable below
/// the screen, but through `app.keyboards` -- an element query that reaches
/// the system-wide tree -- which is why /keyboard has to gate on geometry
/// (KeyboardPresence.swift). This walks `app.snapshot()`, the app's OWN
/// tree, and the keyboard belongs to another process, so it is not in there.
/// Measured on iOS 26.2: on a Reminders screen with nothing focused,
/// inputText is refused with this gate's precondition, and on a focused
/// field typing lands (session mmx39, step 3).
///
/// One wrong way to close a gate here, kept because it cost two sessions to
/// learn: gating on the keyboard's geometry -- the fix that works for
/// /keyboard and pressKey -- REFUSED EVERY TYPING COMMAND on the Simulator,
/// measured across two apps (sessions mmx27 and mmx31). The software
/// keyboard does not present there, while typeText still reaches the focused
/// field. Screen geometry is not the signal for this question.
///
/// A gate that guesses wrong is no longer fatal either: AutomationIssues
/// catches what XCUITest records when typing lands nowhere and returns it as
/// the command's error, so the cost is a failed command rather than the
/// serving process.
public func canReceiveTyping(in snapshot: any AccessibilitySnapshot) -> Bool {
  if snapshot.focused || snapshot.isKeyboard {
    return true
  }
  return snapshot.childSnapshots.contains { canReceiveTyping(in: $0) }
}
