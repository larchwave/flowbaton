package dev.larchwave.flowbaton

import android.app.Instrumentation
import android.os.Build
import android.os.ParcelFileDescriptor
import android.os.SystemClock
import android.view.accessibility.AccessibilityNodeInfo
import android.view.accessibility.AccessibilityWindowInfo
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
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
        typeAndRequireExactTail("héllo Ж👍")
    }

    @Test
    fun typesMappedASCIITextIntoTheFocusedField() {
        typeAndRequireExactTail("ascii text")
    }

    /**
     * The iOS runner has always answered excludeKeyboardElements; the Android agent ignored it and
     * dumped every window, so the explore element table carried one tappable row per key on
     * Android and none on iOS. The window type is the signal, and only a device can say whether
     * reading it works: this asserts both directions on a screen that has a keyboard up, so a
     * filter that dropped nothing and a filter that dropped everything both fail.
     */
    @Test
    fun leavesTheKeyboardOutWhenAsked() {
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        val handlers = AndroidDriverHandlers(instrumentation)
        focusTheTextField(instrumentation, handlers)

        val whole = handlers.viewHierarchy(false)
        val withoutKeyboard = handlers.viewHierarchy(true)
        val keyboard =
            instrumentation.uiAutomation.windows.firstOrNull {
                it.type == AccessibilityWindowInfo.TYPE_INPUT_METHOD
            }
        // Not a skip: a focused text field with no input-method window means the dump this test
        // is about cannot be exercised, and passing quietly would be the same as not running.
        val keyboardPackage =
            keyboard?.root?.packageName?.toString()
                ?: fail("no input-method window while a text field has focus:\n$whole").let { "" }

        assertTrue(
            "the input-method package $keyboardPackage is missing from the whole screen:\n$whole",
            whole.contains("package=\"$keyboardPackage\""),
        )
        assertTrue(
            "the keyboard survived the exclusion:\n$withoutKeyboard",
            !withoutKeyboard.contains("package=\"$keyboardPackage\""),
        )
        // The app itself must still be there; dropping every window would satisfy the line above.
        val appPackage = instrumentation.context.packageName
        assertTrue(
            "the app under test went with the keyboard:\n$withoutKeyboard",
            withoutKeyboard.contains("package=\"$appPackage\""),
        )
    }

    private companion object {
        /**
         * A cold activity launch on a swiftshader emulator is not a fast thing, and the previous
         * 10s deadline failed on CI while passing on a rerun. Waiting longer costs nothing when
         * the launch is quick.
         */
        const val FOCUS_TIMEOUT_MS = 30_000L
    }

    /**
     * Runs a shell command and answers everything it printed.
     *
     * `executeShellCommand` hands back stdout ALONE, and `am` reports a failed launch on stderr —
     * so on the one run where the report was worth reading it came back empty, three times over
     * on CI. The Rwe form carries both streams; below API 31 there is only stdout, which is what
     * this always had.
     *
     * Drains stdout fully before stderr, which would deadlock on a command that fills the stderr
     * pipe buffer first. `am start -W` prints a few lines, far under it. A command that can say
     * more needs a reader thread per stream.
     */
    private fun launchReport(instrumentation: Instrumentation, command: String): String {
        fun read(descriptor: ParcelFileDescriptor?): String =
            descriptor?.let {
                ParcelFileDescriptor.AutoCloseInputStream(it).use { stream ->
                    String(stream.readBytes()).trim()
                }
            }.orEmpty()

        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.S) {
            return read(instrumentation.uiAutomation.executeShellCommand(command))
        }
        val streams = instrumentation.uiAutomation.executeShellCommandRwe(command)
        // executeShellCommandRwe answers [stdout, stdin, stderr].
        streams.getOrNull(1)?.close()
        val out = read(streams.getOrNull(0))
        val err = read(streams.getOrNull(2))
        return listOf("stdout: $out", "stderr: $err").joinToString("\n")
    }

    /** Starts the text-entry activity and waits until its field holds input focus. */
    private fun focusTheTextField(
        instrumentation: Instrumentation,
        handlers: AndroidDriverHandlers,
    ) {
        // Launched through the shell: API 34 silently drops activity starts from
        // a background process, which is what the instrumented process is.
        val component =
            "${instrumentation.context.packageName}/${TextEntryActivity::class.java.name}"
        val launch = launchReport(instrumentation, "am start -W -n $component")

        val deadline = SystemClock.uptimeMillis() + FOCUS_TIMEOUT_MS
        var focused: AccessibilityNodeInfo? = null
        while (SystemClock.uptimeMillis() < deadline) {
            focused = instrumentation.uiAutomation.findFocus(AccessibilityNodeInfo.FOCUS_INPUT)
            if (focused != null) break
            SystemClock.sleep(100)
        }
        // Built only on the failing branch: an assertNotNull message is an
        // eager argument, so dumping the screen there ran a full hierarchy
        // pass on every green run, and a dump that threw would have failed
        // the test for something other than what it tests.
        if (focused == null) {
            fail(
                "no focused text field appeared within ${FOCUS_TIMEOUT_MS}ms.\n" +
                    "am start -W said:\n$launch\n" +
                    // The production dump, not uiAutomation.windows: that list
                    // is empty unless flagRetrieveInteractiveWindows is set,
                    // and viewHierarchy() is the path that sets it. An empty
                    // list said nothing about the screen, only about the flag.
                    "screen at the deadline:\n" + handlers.viewHierarchy(false),
            )
        }
    }

    private fun typeAndRequireExactTail(prefix: String) {
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        val handlers = AndroidDriverHandlers(instrumentation)
        focusTheTextField(instrumentation, handlers)

        // Unique per run: inputText appends to whatever the field holds, so a
        // rerun against a still-open activity must not pass on stale text. The
        // closing quote anchors the match to the END of the field's contents.
        val text = "$prefix ${SystemClock.uptimeMillis() % 100_000}"
        handlers.inputText(text)

        val hierarchy = handlers.viewHierarchy(false)
        assertTrue(
            "typed text is missing from the hierarchy:\n$hierarchy",
            hierarchy.contains("$text\""),
        )
    }
}
