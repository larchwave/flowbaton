package dev.nohavewho.flowbaton;

import android.app.Activity;
import android.os.Bundle;
import android.webkit.WebView;

/**
 * A debuggable WebView fixture that publishes webview_devtools_remote_&lt;pid&gt;.
 *
 * <p>Plain Java keeps the standalone test-package process independent of a Kotlin runtime.
 *
 * <p>The page is inline rather than fetched, so the target of a merge assertion does not
 * depend on the network.
 */
public final class WebViewHostActivity extends Activity {
    // aria-hidden keeps the marker DOM-only, distinguishing merged DOM content
    // from native accessibility nodes.
    private static final String PAGE =
            "<html><body><h1>FlowBaton WebView</h1>"
                    + "<button id=\"sign-in\">Sign in</button>"
                    + "<p aria-hidden=\"true\">FLOWBATON-DOM-ONLY</p></body></html>";

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        WebView.setWebContentsDebuggingEnabled(true);
        WebView view = new WebView(this);
        setContentView(view);
        view.loadData(PAGE, "text/html", "utf-8");
    }
}
