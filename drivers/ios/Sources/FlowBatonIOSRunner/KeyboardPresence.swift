import CoreGraphics

/// Whether the on-screen keyboard is actually presented.
///
/// XCUITest keeps a keyboard element in the hierarchy after the keyboard is
/// dismissed: on a Reminders screen with no keyboard, `app.keyboards` still
/// matches `UIKeyboardLayoutStar Preview`, parked at `{0, 874, 402, 233}` on
/// a 402x874 screen -- the full height of the keyboard, entirely below the
/// bottom edge. Asking whether that element exists therefore answers "yes"
/// on every screen, which made `/keyboard` report a keyboard nobody could
/// see and let `pressKey` type into nothing (sessions mmx22 and mmx23; the
/// press then took the whole runner process down).
///
/// Presence is geometry: the keyboard is up when its frame reaches into the
/// screen.
public enum KeyboardPresence {
  public static func isPresented(keyboard: CGRect, screen: CGRect) -> Bool {
    guard keyboard.height > 0, keyboard.width > 0 else { return false }
    return keyboard.intersects(screen)
  }
}
