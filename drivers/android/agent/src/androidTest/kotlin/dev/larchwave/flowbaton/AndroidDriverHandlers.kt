package dev.larchwave.flowbaton

import android.accessibilityservice.AccessibilityServiceInfo
import android.app.Instrumentation
import android.app.UiAutomation
import android.content.Context
import android.content.Intent
import android.graphics.Bitmap
import android.os.Build
import android.os.Bundle
import android.os.SystemClock
import android.util.DisplayMetrics
import android.view.InputDevice
import android.view.KeyCharacterMap
import android.view.KeyEvent
import android.view.MotionEvent
import android.view.WindowManager
import android.view.accessibility.AccessibilityEvent
import android.view.accessibility.AccessibilityNodeInfo
import dev.larchwave.flowbaton.driver.ArgumentCoercion
import dev.larchwave.flowbaton.driver.DeviceDimensions
import dev.larchwave.flowbaton.driver.HierarchyXml
import dev.larchwave.flowbaton.driver.LaunchArgument
import dev.larchwave.flowbaton.driver.FlowBatonDriverHandlers
import dev.larchwave.flowbaton.driver.WindowOrder
import java.io.ByteArrayOutputStream
import java.io.InputStream
import java.util.concurrent.TimeoutException

/** Device glue behind the twelve FlowBatonDriver RPCs, on raw UiAutomation. */
class AndroidDriverHandlers(
    private val instrumentation: Instrumentation,
) : FlowBatonDriverHandlers {
    private val toastWatcher = ToastWatcher()
    private val mockLocation = MockLocationEngine(instrumentation.targetContext)

    private val uiAutomation: UiAutomation
        get() = instrumentation.uiAutomation

    private val context: Context
        get() = instrumentation.targetContext

    init {
        uiAutomation.serviceInfo?.let { info ->
            info.flags = info.flags or AccessibilityServiceInfo.FLAG_RETRIEVE_INTERACTIVE_WINDOWS
            uiAutomation.serviceInfo = info
        }
        uiAutomation.setOnAccessibilityEventListener(toastWatcher::observe)
    }

    override fun deviceInfo(): DeviceDimensions {
        val metrics = realMetrics()
        return DeviceDimensions(metrics.widthPixels, metrics.heightPixels)
    }

    override fun viewHierarchy(): String {
        try {
            uiAutomation.waitForIdle(500, 1_000)
        } catch (_: TimeoutException) {
            // A busy screen is still dumpable; the wait is only cache hygiene.
        }
        // Re-setting serviceInfo flushes the cached node tree before the dump (spec 04 §2).
        uiAutomation.serviceInfo?.let { uiAutomation.serviceInfo = it }

        // Bottom-up by window layer, because the platform's own order is not stable and the
        // flakiness reaches the flow author through selection order. Put the app under test
        // ahead of overlays such as the status bar; WindowOrder defines the ordering.
        val windows = uiAutomation.windows
        val roots =
            WindowOrder.ascendingLayerOrder(IntArray(windows.size) { windows[it].layer })
                .toList()
                .mapNotNull { windows[it].root }
                .ifEmpty { listOfNotNull(uiAutomation.rootInActiveWindow) }
        val metrics = realMetrics()
        return HierarchyXml.serialize(
            rotation(),
            roots.mapIndexed { index, root -> AccessibilityHierarchy.convert(root, index) },
            toastWatcher.current(),
            metrics.widthPixels,
            metrics.heightPixels,
            Build.VERSION.SDK_INT,
        )
    }

    override fun screenshot(): ByteArray {
        repeat(3) {
            val bitmap: Bitmap? = uiAutomation.takeScreenshot()
            if (bitmap != null) {
                val out = ByteArrayOutputStream()
                bitmap.compress(Bitmap.CompressFormat.PNG, 100, out)
                return out.toByteArray()
            }
            SystemClock.sleep(100)
        }
        throw IllegalStateException("could not capture a screenshot in three attempts")
    }

    override fun tap(x: Int, y: Int) {
        val downTime = SystemClock.uptimeMillis()
        injectMotion(MotionEvent.ACTION_DOWN, downTime, downTime, x, y)
        injectMotion(MotionEvent.ACTION_UP, downTime, SystemClock.uptimeMillis(), x, y)
    }

    override fun inputText(text: String) {
        val keyMap = KeyCharacterMap.load(KeyCharacterMap.VIRTUAL_KEYBOARD)
        if (containsUnmappedCharacter(text, keyMap)) {
            // Mixing key injection with an accessibility read-modify-write can
            // overwrite a key whose text event has not reached the node cache.
            // One action keeps the requested mixed text atomic.
            appendToFocusedNode(text)
            return
        }
        var offset = 0
        while (offset < text.length) {
            val chars = Character.toChars(text.codePointAt(offset))
            if (offset > 0) {
                SystemClock.sleep(75) // spec 04 §1: 75ms between characters
            }
            val events =
                keyMap.getEvents(chars)
                    ?: throw IllegalStateException("inputText: key mapping changed during input")
            events.forEach { injectKey(it) }
            offset += chars.size
        }
    }

    private fun containsUnmappedCharacter(
        text: String,
        keyMap: KeyCharacterMap,
    ): Boolean {
        var offset = 0
        while (offset < text.length) {
            val chars = Character.toChars(text.codePointAt(offset))
            if (chars.size != 1 || keyMap.getEvents(chars) == null) return true
            offset += chars.size
        }
        return false
    }

    private fun appendToFocusedNode(text: String) {
        val deadline = SystemClock.uptimeMillis() + 1_000
        var node: AccessibilityNodeInfo? = null
        while (SystemClock.uptimeMillis() < deadline) {
            node = uiAutomation.findFocus(AccessibilityNodeInfo.FOCUS_INPUT)
            if (node != null) break
            SystemClock.sleep(50)
        }
        val target =
            node
                ?: throw IllegalStateException(
                    "inputText: no focused field to receive \"$text\"",
                )
        if (!target.isEditable) {
            throw IllegalStateException("inputText: the focused node is not editable")
        }
        target.refresh()
        val existing = if (target.isShowingHintText) "" else target.text?.toString().orEmpty()
        val arguments = Bundle()
        arguments.putCharSequence(
            AccessibilityNodeInfo.ACTION_ARGUMENT_SET_TEXT_CHARSEQUENCE,
            existing + text,
        )
        if (!target.performAction(AccessibilityNodeInfo.ACTION_SET_TEXT, arguments)) {
            throw IllegalStateException("inputText: the focused field rejected ACTION_SET_TEXT")
        }
    }

    override fun eraseAllText(charactersToErase: Int) {
        repeat(charactersToErase) { pressKey(KeyEvent.KEYCODE_DEL) }
    }

    override fun setLocation(latitude: Double, longitude: Double) {
        mockLocation.push(latitude, longitude)
    }

    override fun isWindowUpdating(appId: String): Boolean =
        try {
            uiAutomation.executeAndWaitForEvent(
                {},
                { event ->
                    event.eventType == AccessibilityEvent.TYPE_WINDOW_CONTENT_CHANGED &&
                        (appId.isEmpty() || event.packageName?.toString() == appId)
                },
                500,
            )
            true
        } catch (_: TimeoutException) {
            false
        }

    override fun launchApp(packageName: String, arguments: List<LaunchArgument>) {
        val intent =
            context.packageManager.getLaunchIntentForPackage(packageName)
                ?: throw IllegalArgumentException("no launcher intent for package $packageName")
        intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        arguments.forEach { argument ->
            when (val value = ArgumentCoercion.coerce(argument.value(), argument.type())) {
                is String -> intent.putExtra(argument.key(), value)
                is Boolean -> intent.putExtra(argument.key(), value)
                is Int -> intent.putExtra(argument.key(), value)
                is Double -> intent.putExtra(argument.key(), value)
                is Long -> intent.putExtra(argument.key(), value)
                else -> throw IllegalStateException("unreachable coercion ${value.javaClass}")
            }
        }
        context.startActivity(intent)
    }

    override fun addMedia(mediaName: String, mediaExt: String, data: InputStream) {
        MediaStoreWriter.write(context.contentResolver, mediaName, mediaExt, data)
    }

    override fun enableMockLocationProviders() {
        mockLocation.enableProviders()
    }

    override fun disableLocationUpdates() {
        mockLocation.disable()
    }

    private fun realMetrics(): DisplayMetrics {
        val metrics = DisplayMetrics()
        @Suppress("DEPRECATION")
        windowManager().defaultDisplay.getRealMetrics(metrics)
        return metrics
    }

    private fun rotation(): Int {
        @Suppress("DEPRECATION")
        return windowManager().defaultDisplay.rotation
    }

    private fun windowManager(): WindowManager =
        context.getSystemService(Context.WINDOW_SERVICE) as WindowManager

    private fun injectMotion(action: Int, downTime: Long, eventTime: Long, x: Int, y: Int) {
        val event = MotionEvent.obtain(downTime, eventTime, action, x.toFloat(), y.toFloat(), 0)
        event.source = InputDevice.SOURCE_TOUCHSCREEN
        try {
            if (!uiAutomation.injectInputEvent(event, true)) {
                throw IllegalStateException("input injection was rejected for action $action")
            }
        } finally {
            event.recycle()
        }
    }

    private fun pressKey(keyCode: Int) {
        val time = SystemClock.uptimeMillis()
        injectKey(KeyEvent(time, time, KeyEvent.ACTION_DOWN, keyCode, 0))
        injectKey(KeyEvent(time, time, KeyEvent.ACTION_UP, keyCode, 0))
    }

    private fun injectKey(event: KeyEvent) {
        if (!uiAutomation.injectInputEvent(event, true)) {
            throw IllegalStateException("key injection was rejected for keyCode ${event.keyCode}")
        }
    }
}
