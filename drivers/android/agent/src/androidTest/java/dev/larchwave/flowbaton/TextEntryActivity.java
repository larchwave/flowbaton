package dev.larchwave.flowbaton;

import android.app.Activity;
import android.os.Bundle;
import android.view.WindowManager;
import android.widget.EditText;

/**
 * A single focused EditText: the target the inputText connected test types into. Plain Java
 * on purpose — the shell launches it in the test package's own standalone process, which has
 * no Kotlin runtime (the stdlib rides in the instrumented target APK).
 */
public final class TextEntryActivity extends Activity {
    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        // Ask for the soft keyboard rather than hoping for it: an emulator with a hardware
        // keyboard leaves the IME hidden on plain focus, and leavesTheKeyboardOutWhenAsked
        // has nothing to exclude without it.
        getWindow()
                .setSoftInputMode(WindowManager.LayoutParams.SOFT_INPUT_STATE_ALWAYS_VISIBLE);
        EditText field = new EditText(this);
        setContentView(field);
        field.requestFocus();
    }
}
