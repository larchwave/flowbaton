package dev.nohavewho.flowbaton.driver;

import java.util.List;

/**
 * Serializes captured accessibility roots into the spec 04 §2 hierarchy XML: exact attribute
 * names and order, bounds clipped to the display, the WebView always-descend rule, the toast
 * appended as its own node, and hintText blank below API 26.
 */
public final class HierarchyXml {
    private static final int HINT_TEXT_MIN_API = 26;

    private HierarchyXml() {}

    public static String serialize(
            int rotation,
            List<HierarchyNode> roots,
            ToastNode toast,
            int displayWidthPixels,
            int displayHeightPixels,
            int apiLevel) {
        StringBuilder out = new StringBuilder();
        out.append("<?xml version='1.0' encoding='UTF-8' standalone='yes' ?>\n");
        out.append("<hierarchy rotation=\"").append(rotation).append("\">");
        for (HierarchyNode root : roots) {
            appendNode(out, root, 1, false, displayWidthPixels, displayHeightPixels, apiLevel);
        }
        if (toast != null) {
            appendToast(out, toast, displayWidthPixels, displayHeightPixels);
        }
        out.append("\n</hierarchy>");
        return out.toString();
    }

    private static void appendNode(
            StringBuilder out,
            HierarchyNode node,
            int depth,
            boolean insideWebView,
            int displayWidth,
            int displayHeight,
            int apiLevel) {
        out.append('\n').append("  ".repeat(depth)).append("<node");
        if (node.accessibilityUnfriendly()) {
            attribute(out, "NAF", "true");
        }
        attribute(out, "index", String.valueOf(node.index()));
        attribute(out, "hintText", apiLevel >= HINT_TEXT_MIN_API ? node.hintText() : "");
        attribute(out, "text", node.text());
        attribute(out, "resource-id", node.resourceId());
        attribute(out, "class", node.className());
        attribute(out, "package", node.packageName());
        attribute(out, "content-desc", node.contentDescription());
        attribute(out, "checkable", String.valueOf(node.checkable()));
        attribute(out, "checked", String.valueOf(node.checked()));
        attribute(out, "clickable", String.valueOf(node.clickable()));
        attribute(out, "enabled", String.valueOf(node.enabled()));
        attribute(out, "focusable", String.valueOf(node.focusable()));
        attribute(out, "focused", String.valueOf(node.focused()));
        attribute(out, "scrollable", String.valueOf(node.scrollable()));
        attribute(out, "long-clickable", String.valueOf(node.longClickable()));
        attribute(out, "password", String.valueOf(node.password()));
        attribute(out, "selected", String.valueOf(node.selected()));
        attribute(out, "visible-to-user", String.valueOf(node.visibleToUser()));
        attribute(
                out,
                "important-for-accessibility",
                String.valueOf(node.importantForAccessibility()));
        attribute(out, "error", node.error());
        attribute(out, "bounds", clippedBounds(node, displayWidth, displayHeight));

        // Inside a WebView the walk always descends (spec 04 §2); elsewhere invisible
        // children are skipped together with their subtrees.
        boolean webViewHere = insideWebView || isWebView(node);
        List<HierarchyNode> emitted =
                node.children().stream()
                        .filter(child -> webViewHere || child.visibleToUser())
                        .toList();
        if (emitted.isEmpty()) {
            out.append(" />");
            return;
        }
        out.append('>');
        for (HierarchyNode child : emitted) {
            appendNode(out, child, depth + 1, webViewHere, displayWidth, displayHeight, apiLevel);
        }
        out.append('\n').append("  ".repeat(depth)).append("</node>");
    }

    private static void appendToast(
            StringBuilder out, ToastNode toast, int displayWidth, int displayHeight) {
        out.append("\n  <node");
        attribute(out, "index", "0");
        attribute(out, "class", toast.className());
        attribute(out, "text", toast.text());
        attribute(out, "visible-to-user", "true");
        attribute(out, "checkable", "false");
        attribute(out, "clickable", "false");
        attribute(out, "bounds", "[0,0][" + displayWidth + "," + displayHeight + "]");
        out.append(" />");
    }

    private static boolean isWebView(HierarchyNode node) {
        // ponytail: substring match covers android.webkit.WebView and vendor subclasses;
        // tighten to a class-registry check if a false positive ever shows up.
        return node.className().contains("WebView");
    }

    private static String clippedBounds(HierarchyNode node, int displayWidth, int displayHeight) {
        int left = Math.max(0, node.left());
        int top = Math.max(0, node.top());
        int right = Math.min(displayWidth, node.right());
        int bottom = Math.min(displayHeight, node.bottom());
        if (left >= right || top >= bottom) {
            return "[0,0][0,0]";
        }
        return "[" + left + "," + top + "][" + right + "," + bottom + "]";
    }

    private static void attribute(StringBuilder out, String name, String value) {
        out.append(' ').append(name).append("=\"");
        escapeInto(out, value);
        out.append('"');
    }

    private static void escapeInto(StringBuilder out, String value) {
        for (int i = 0; i < value.length(); i++) {
            char c = value.charAt(i);
            switch (c) {
                case '&' -> out.append("&amp;");
                case '<' -> out.append("&lt;");
                case '>' -> out.append("&gt;");
                case '"' -> out.append("&quot;");
                case '\n' -> out.append("&#10;");
                case '\t' -> out.append("&#9;");
                case '\r' -> out.append("&#13;");
                default -> {
                    if (c >= 0x20 || Character.isHighSurrogate(c) || Character.isLowSurrogate(c)) {
                        out.append(c);
                    }
                }
            }
        }
    }
}
