package dev.nohavewho.flowbaton

import android.os.ParcelFileDescriptor
import android.os.SystemClock
import android.view.accessibility.AccessibilityNodeInfo
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

/**
 * Connected coverage for the unicode half of inputText: characters without a virtual-keyboard
 * mapping must land in the field through in-process typing, not throw.
 *
 * The test APK has its own uid, so the launched activity lives in another process; both the
 * focus wait and the assertion therefore go through the agent's own accessibility surface —
 * the same one the host reads.
 */
@RunWith(AndroidJUnit4::class)
class InputTextTest {
    @Test
    fun typesUnicodeTextIntoTheFocusedField() {
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        val handlers = AndroidDriverHandlers(instrumentation)

        // Launched through the shell: API 34 silently drops activity starts from
        // a background process, which is what the instrumented process is.
        val component =
            "${instrumentation.context.packageName}/${TextEntryActivity::class.java.name}"
        val descriptor =
            instrumentation.uiAutomation.executeShellCommand("am start -W -n $component")
        ParcelFileDescriptor.AutoCloseInputStream(descriptor).use { it.readBytes() }

        val deadline = SystemClock.uptimeMillis() + 10_000
        var focused: AccessibilityNodeInfo? = null
        while (SystemClock.uptimeMillis() < deadline) {
            focused = instrumentation.uiAutomation.findFocus(AccessibilityNodeInfo.FOCUS_INPUT)
            if (focused != null) break
            SystemClock.sleep(100)
        }
        assertNotNull("no focused text field appeared", focused)

        // Unique per run: inputText appends to whatever the field holds, so a
        // rerun against a still-open activity must not pass on stale text. The
        // closing quote anchors the match to the END of the field's contents.
        val text = "héllo Ж👍 ${SystemClock.uptimeMillis() % 100_000}"
        handlers.inputText(text)

        val hierarchy = handlers.viewHierarchy()
        assertTrue(
            "typed text is missing from the hierarchy:\n$hierarchy",
            hierarchy.contains("$text\""),
        )
    }
}
