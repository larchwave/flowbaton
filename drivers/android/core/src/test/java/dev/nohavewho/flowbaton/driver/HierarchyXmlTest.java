package dev.nohavewho.flowbaton.driver;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;

import java.util.ArrayList;
import java.util.List;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import org.junit.Test;

public final class HierarchyXmlTest {
    private static final List<String> SPEC_ATTRIBUTE_ORDER =
            List.of(
                    "index",
                    "hintText",
                    "text",
                    "resource-id",
                    "class",
                    "package",
                    "content-desc",
                    "checkable",
                    "checked",
                    "clickable",
                    "enabled",
                    "focusable",
                    "focused",
                    "scrollable",
                    "long-clickable",
                    "password",
                    "selected",
                    "visible-to-user",
                    "important-for-accessibility",
                    "error",
                    "bounds");

    @Test
    public void serializesTheFullDocumentExactly() {
        HierarchyNode button =
                HierarchyNode.builder()
                        .accessibilityUnfriendly(true)
                        .index(0)
                        .text("OK & <Go>")
                        .resourceId("com.example:id/ok")
                        .className("android.widget.Button")
                        .packageName("com.example")
                        .contentDescription("say \"go\"")
                        .clickable(true)
                        .enabled(true)
                        .focusable(true)
                        .visibleToUser(true)
                        .importantForAccessibility(true)
                        .bounds(-10, 5, 500, 100)
                        .build();
        HierarchyNode invisibleOutsideWebView =
                HierarchyNode.builder()
                        .index(1)
                        .className("android.widget.TextView")
                        .packageName("com.example")
                        .enabled(true)
                        .bounds(0, 0, 10, 10)
                        .addChild(
                                HierarchyNode.builder()
                                        .className("android.widget.TextView")
                                        .build())
                        .build();
        HierarchyNode invisibleInsideWebView =
                HierarchyNode.builder()
                        .index(0)
                        .className("android.view.View")
                        .packageName("com.example")
                        .enabled(true)
                        .bounds(2000, 2000, 3000, 3000)
                        .build();
        HierarchyNode webView =
                HierarchyNode.builder()
                        .index(2)
                        .className("android.webkit.WebView")
                        .packageName("com.example")
                        .enabled(true)
                        .visibleToUser(true)
                        .importantForAccessibility(true)
                        .bounds(0, 100, 1080, 800)
                        .addChild(invisibleInsideWebView)
                        .build();
        HierarchyNode root =
                HierarchyNode.builder()
                        .index(0)
                        .hintText("type here")
                        .className("android.widget.FrameLayout")
                        .packageName("com.example")
                        .enabled(true)
                        .visibleToUser(true)
                        .importantForAccessibility(true)
                        .bounds(0, 0, 1080, 1920)
                        .addChild(button)
                        .addChild(invisibleOutsideWebView)
                        .addChild(webView)
                        .build();

        String xml =
                HierarchyXml.serialize(
                        1,
                        List.of(root),
                        new ToastNode("android.widget.Toast", "Saved!"),
                        1080,
                        1920,
                        34);

        String expected =
                String.join(
                        "\n",
                        "<?xml version='1.0' encoding='UTF-8' standalone='yes' ?>",
                        "<hierarchy rotation=\"1\">",
                        "  <node index=\"0\" hintText=\"type here\" text=\"\" resource-id=\"\""
                                + " class=\"android.widget.FrameLayout\" package=\"com.example\""
                                + " content-desc=\"\" checkable=\"false\" checked=\"false\""
                                + " clickable=\"false\" enabled=\"true\" focusable=\"false\""
                                + " focused=\"false\" scrollable=\"false\" long-clickable=\"false\""
                                + " password=\"false\" selected=\"false\" visible-to-user=\"true\""
                                + " important-for-accessibility=\"true\" error=\"\""
                                + " bounds=\"[0,0][1080,1920]\">",
                        "    <node NAF=\"true\" index=\"0\" hintText=\"\""
                                + " text=\"OK &amp; &lt;Go&gt;\""
                                + " resource-id=\"com.example:id/ok\""
                                + " class=\"android.widget.Button\" package=\"com.example\""
                                + " content-desc=\"say &quot;go&quot;\" checkable=\"false\""
                                + " checked=\"false\" clickable=\"true\" enabled=\"true\""
                                + " focusable=\"true\" focused=\"false\" scrollable=\"false\""
                                + " long-clickable=\"false\" password=\"false\" selected=\"false\""
                                + " visible-to-user=\"true\" important-for-accessibility=\"true\""
                                + " error=\"\" bounds=\"[0,5][500,100]\" />",
                        "    <node index=\"2\" hintText=\"\" text=\"\" resource-id=\"\""
                                + " class=\"android.webkit.WebView\" package=\"com.example\""
                                + " content-desc=\"\" checkable=\"false\" checked=\"false\""
                                + " clickable=\"false\" enabled=\"true\" focusable=\"false\""
                                + " focused=\"false\" scrollable=\"false\" long-clickable=\"false\""
                                + " password=\"false\" selected=\"false\" visible-to-user=\"true\""
                                + " important-for-accessibility=\"true\" error=\"\""
                                + " bounds=\"[0,100][1080,800]\">",
                        "      <node index=\"0\" hintText=\"\" text=\"\" resource-id=\"\""
                                + " class=\"android.view.View\" package=\"com.example\""
                                + " content-desc=\"\" checkable=\"false\" checked=\"false\""
                                + " clickable=\"false\" enabled=\"true\" focusable=\"false\""
                                + " focused=\"false\" scrollable=\"false\" long-clickable=\"false\""
                                + " password=\"false\" selected=\"false\" visible-to-user=\"false\""
                                + " important-for-accessibility=\"false\" error=\"\""
                                + " bounds=\"[0,0][0,0]\" />",
                        "    </node>",
                        "  </node>",
                        "  <node index=\"0\" class=\"android.widget.Toast\" text=\"Saved!\""
                                + " visible-to-user=\"true\" checkable=\"false\""
                                + " clickable=\"false\" bounds=\"[0,0][1080,1920]\" />",
                        "</hierarchy>");
        assertEquals(expected, xml);
    }

    @Test
    public void emitsEveryAttributeInTheSpecOrder() {
        String xml = serializeSingle(HierarchyNode.builder().visibleToUser(true).build(), 34);
        assertEquals(SPEC_ATTRIBUTE_ORDER, attributeNames(nodeLine(xml)));
    }

    @Test
    public void nafIsEmittedOnlyWhenAccessibilityUnfriendlyAndComesFirst() {
        String friendly =
                serializeSingle(HierarchyNode.builder().visibleToUser(true).build(), 34);
        assertFalse(friendly, friendly.contains("NAF="));

        String unfriendly =
                serializeSingle(
                        HierarchyNode.builder()
                                .accessibilityUnfriendly(true)
                                .visibleToUser(true)
                                .build(),
                        34);
        assertTrue(unfriendly, nodeLine(unfriendly).startsWith("  <node NAF=\"true\" index="));
    }

    @Test
    public void hintTextIsBlankBelowApi26() {
        HierarchyNode node =
                HierarchyNode.builder().hintText("secret hint").visibleToUser(true).build();
        assertTrue(serializeSingle(node, 26).contains("hintText=\"secret hint\""));
        assertTrue(serializeSingle(node, 25).contains("hintText=\"\""));
        assertFalse(serializeSingle(node, 25).contains("secret hint"));
    }

    @Test
    public void boundsAreClippedToTheDisplay() {
        HierarchyNode node =
                HierarchyNode.builder().visibleToUser(true).bounds(-50, -50, 2000, 100).build();
        assertTrue(serializeSingle(node, 34).contains("bounds=\"[0,0][1080,100]\""));
    }

    @Test
    public void controlCharactersInAttributeValuesAreEscaped() {
        HierarchyNode node =
                HierarchyNode.builder().text("line one\nline\ttwo").visibleToUser(true).build();
        assertTrue(serializeSingle(node, 34).contains("text=\"line one&#10;line&#9;two\""));
    }

    @Test
    public void toastCarriesExactlyItsSevenSpecAttributes() {
        String xml =
                HierarchyXml.serialize(
                        0,
                        List.of(),
                        new ToastNode("android.widget.Toast", "done"),
                        1080,
                        1920,
                        34);
        String toastLine = nodeLine(xml);
        assertEquals(
                List.of(
                        "index",
                        "class",
                        "text",
                        "visible-to-user",
                        "checkable",
                        "clickable",
                        "bounds"),
                attributeNames(toastLine));
    }

    private static String serializeSingle(HierarchyNode node, int apiLevel) {
        return HierarchyXml.serialize(0, List.of(node), null, 1080, 1920, apiLevel);
    }

    private static String nodeLine(String xml) {
        for (String line : xml.split("\n")) {
            if (line.trim().startsWith("<node")) {
                return line;
            }
        }
        throw new AssertionError("no <node> line in:\n" + xml);
    }

    private static List<String> attributeNames(String nodeLine) {
        Matcher matcher = Pattern.compile("([A-Za-z-]+)=\"").matcher(nodeLine);
        List<String> names = new ArrayList<>();
        while (matcher.find()) {
            names.add(matcher.group(1));
        }
        return names;
    }
}
