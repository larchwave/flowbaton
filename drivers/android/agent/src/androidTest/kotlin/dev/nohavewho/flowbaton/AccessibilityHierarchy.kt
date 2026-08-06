package dev.nohavewho.flowbaton

import android.graphics.Rect
import android.os.SystemClock
import android.view.accessibility.AccessibilityEvent
import android.view.accessibility.AccessibilityNodeInfo
import dev.nohavewho.flowbaton.driver.HierarchyNode
import dev.nohavewho.flowbaton.driver.ToastNode

/** Converts a live AccessibilityNodeInfo tree into the pure model HierarchyXml serializes. */
object AccessibilityHierarchy {
    fun convert(node: AccessibilityNodeInfo, index: Int): HierarchyNode {
        val bounds = Rect()
        node.getBoundsInScreen(bounds)
        val builder =
            HierarchyNode.builder()
                .index(index)
                .accessibilityUnfriendly(isUnfriendly(node))
                .hintText(node.hintText?.toString() ?: "")
                .text(node.text?.toString() ?: "")
                .resourceId(node.viewIdResourceName ?: "")
                .className(node.className?.toString() ?: "")
                .packageName(node.packageName?.toString() ?: "")
                .contentDescription(node.contentDescription?.toString() ?: "")
                .error(node.error?.toString() ?: "")
                .checkable(node.isCheckable)
                .checked(node.isChecked)
                .clickable(node.isClickable)
                .enabled(node.isEnabled)
                .focusable(node.isFocusable)
                .focused(node.isFocused)
                .scrollable(node.isScrollable)
                .longClickable(node.isLongClickable)
                .password(node.isPassword)
                .selected(node.isSelected)
                .visibleToUser(node.isVisibleToUser)
                .importantForAccessibility(node.isImportantForAccessibility)
                .bounds(bounds.left, bounds.top, bounds.right, bounds.bottom)
        for (childIndex in 0 until node.childCount) {
            val child = node.getChild(childIndex) ?: continue
            builder.addChild(convert(child, childIndex))
        }
        return builder.build()
    }

    /** NAF the way uiautomator means it: actionable, but nothing to find it by. */
    private fun isUnfriendly(node: AccessibilityNodeInfo): Boolean =
        node.isClickable &&
            node.isEnabled &&
            node.text.isNullOrEmpty() &&
            node.contentDescription.isNullOrEmpty()
}

/** Remembers the most recent toast so the dump can append it as its own node. */
class ToastWatcher {
    private val lock = Any()
    private var text = ""
    private var className = ""
    private var seenAtMillis = 0L

    fun observe(event: AccessibilityEvent) {
        if (event.eventType != AccessibilityEvent.TYPE_NOTIFICATION_STATE_CHANGED) return
        // Notifications arrive on the same event type but carry a Notification parcelable.
        if (event.parcelableData != null) return
        val toastText = event.text.joinToString(" ") { it?.toString() ?: "" }.trim()
        if (toastText.isEmpty()) return
        synchronized(lock) {
            text = toastText
            className = event.className?.toString() ?: "android.widget.Toast"
            seenAtMillis = SystemClock.uptimeMillis()
        }
    }

    /** ponytail: 3500ms is a LONG toast's on-screen lifetime; no dismissal signal exists. */
    fun current(): ToastNode? =
        synchronized(lock) {
            if (text.isEmpty() || SystemClock.uptimeMillis() - seenAtMillis > 3_500) {
                null
            } else {
                ToastNode(className, text)
            }
        }
}
