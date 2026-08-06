package dev.nohavewho.flowbaton.fixture;

import android.app.Activity;
import android.os.Bundle;
import android.webkit.WebView;

/**
 * One WebView holding two markers: one the accessibility tree exposes, one it does not.
 *
 * <p>Modern WebView publishes ordinary text to the accessibility tree, so
 * {@code WEBVIEW-ONLY-MARKER} remains visible without the DOM merge.
 *
 * <p>{@code aria-hidden="true"} is the difference that survives. It removes a node from the
 * accessibility tree while leaving it in the DOM, so {@code DEVTOOLS-ONLY-MARKER} is reachable only
 * through the Chrome DevTools walk. Without {@code androidWebViewHierarchy: devtools}, the marker
 * must remain absent.
 *
 * <p>{@link WebView#setWebContentsDebuggingEnabled} is what publishes the abstract
 * {@code webview_devtools_remote_<pid>} socket the driver forwards. Without it there is nothing to
 * attach to, which is exactly why release Chrome on an emulator could not serve as this fixture.
 */
public final class WebViewActivity extends Activity {
    // Inlined rather than loaded from assets: the page has to be identical on every device and
    // every run, and a data: URL cannot be affected by what else is on the filesystem.
    private static final String PAGE =
            "<html><body style='font-family:sans-serif'>"
                    + "<h1>WEBVIEW-ONLY-MARKER</h1>"
                    + "<p aria-hidden='true'>DEVTOOLS-ONLY-MARKER</p>"
                    + "<button id='go'>Tap me</button>"
                    + "</body></html>";

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        WebView.setWebContentsDebuggingEnabled(true);
        WebView webView = new WebView(this);
        webView.getSettings().setJavaScriptEnabled(true);
        webView.loadData(PAGE, "text/html", "utf-8");
        setContentView(webView);
    }
}
