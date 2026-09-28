/// RemoteViewOverlay decides whether a system service's tree belongs beside
/// the app's.
///
/// Some system views are drawn by another process over the app. The StoreKit
/// rating prompt is one: on an iPhone its contents appear inside the app's own
/// snapshot, but over an iPhone-compatibility window on an iPad they do not,
/// and the app's tree comes back empty while the prompt is on screen (issue
/// #43). Measured on iOS 26.2, the prompt is then only in the snapshot of
/// `com.apple.ios.StoreKitUIService`, in the same grid as the app.
///
/// The service is served only when it shows something the app's tree does not
/// already carry, so the iPhone case does not match every label twice.
public enum RemoteViewOverlay {
  /// The processes known to draw a remote view over an app.
  public static let services = ["com.apple.ios.StoreKitUIService"]

  public static func serves(
    _ service: any AccessibilitySnapshot, over app: any AccessibilitySnapshot
  ) -> Bool {
    let shown = descendantLabels(of: service)
    return !shown.isEmpty && !shown.isSubset(of: descendantLabels(of: app))
  }

  /// The root's own label is the application's name, not something on screen.
  static func descendantLabels(of root: any AccessibilitySnapshot) -> Set<String> {
    var labels = Set<String>()
    var pending = root.childSnapshots
    while let node = pending.popLast() {
      if !node.label.isEmpty {
        labels.insert(node.label)
      }
      pending.append(contentsOf: node.childSnapshots)
    }
    return labels
  }
}
