import CoreGraphics

/// Whether the on-screen keyboard is actually presented.
///
/// XCUITest keeps a keyboard element in the hierarchy after the keyboard is
/// dismissed: on a Reminders screen with no keyboard, `app.keyboards` still
/// matches `UIKeyboardLayoutStar Preview`, parked at `{0, 874, 402, 233}` on
/// a 402x874 screen -- the full height of the keyboard, entirely below the
/// bottom edge. Asking whether that element exists therefore answers "yes"
/// on every screen, which made `/keyboard` report a keyboard nobody could
/// see and let typing go to a screen with nowhere to put it (sessions mmx22
/// and mmx23; the attempt then took the whole runner process down).
///
/// Presence is geometry, and the threshold is not "touches the screen": the
/// keyboard slides out over about a quarter of a second, so a keyboard with
/// one row still showing is one the next command must not type into.
public enum KeyboardPresence {
  /// The share of the keyboard that has to be on screen to count. Half is
  /// far enough through either animation to be unambiguous, and a keyboard
  /// resting in place is wholly on screen, nowhere near the edge of it.
  public static let presentedFraction: CGFloat = 0.5

  public static func isPresented(keyboard: CGRect, screen: CGRect) -> Bool {
    guard keyboard.height > 0, keyboard.width > 0 else { return false }
    let overlap = keyboard.intersection(screen)
    guard !overlap.isNull else { return false }
    return overlap.height >= keyboard.height * presentedFraction
  }
}
