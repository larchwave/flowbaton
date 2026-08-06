package dev.larchwave.flowbaton.driver;

import java.util.ArrayList;
import java.util.List;

/**
 * One accessibility node as captured on the device, carrying exactly the attribute set the
 * spec 04 §2 hierarchy XML emits. Bounds are the RAW node bounds in screen pixels; clipping to
 * the display happens at serialization time in {@link HierarchyXml}.
 */
public final class HierarchyNode {
    private final boolean accessibilityUnfriendly;
    private final int index;
    private final String hintText;
    private final String text;
    private final String resourceId;
    private final String className;
    private final String packageName;
    private final String contentDescription;
    private final String error;
    private final boolean checkable;
    private final boolean checked;
    private final boolean clickable;
    private final boolean enabled;
    private final boolean focusable;
    private final boolean focused;
    private final boolean scrollable;
    private final boolean longClickable;
    private final boolean password;
    private final boolean selected;
    private final boolean visibleToUser;
    private final boolean importantForAccessibility;
    private final int left;
    private final int top;
    private final int right;
    private final int bottom;
    private final List<HierarchyNode> children;

    private HierarchyNode(Builder builder) {
        this.accessibilityUnfriendly = builder.accessibilityUnfriendly;
        this.index = builder.index;
        this.hintText = builder.hintText;
        this.text = builder.text;
        this.resourceId = builder.resourceId;
        this.className = builder.className;
        this.packageName = builder.packageName;
        this.contentDescription = builder.contentDescription;
        this.error = builder.error;
        this.checkable = builder.checkable;
        this.checked = builder.checked;
        this.clickable = builder.clickable;
        this.enabled = builder.enabled;
        this.focusable = builder.focusable;
        this.focused = builder.focused;
        this.scrollable = builder.scrollable;
        this.longClickable = builder.longClickable;
        this.password = builder.password;
        this.selected = builder.selected;
        this.visibleToUser = builder.visibleToUser;
        this.importantForAccessibility = builder.importantForAccessibility;
        this.left = builder.left;
        this.top = builder.top;
        this.right = builder.right;
        this.bottom = builder.bottom;
        this.children = List.copyOf(builder.children);
    }

    public static Builder builder() {
        return new Builder();
    }

    public boolean accessibilityUnfriendly() {
        return accessibilityUnfriendly;
    }

    public int index() {
        return index;
    }

    public String hintText() {
        return hintText;
    }

    public String text() {
        return text;
    }

    public String resourceId() {
        return resourceId;
    }

    public String className() {
        return className;
    }

    public String packageName() {
        return packageName;
    }

    public String contentDescription() {
        return contentDescription;
    }

    public String error() {
        return error;
    }

    public boolean checkable() {
        return checkable;
    }

    public boolean checked() {
        return checked;
    }

    public boolean clickable() {
        return clickable;
    }

    public boolean enabled() {
        return enabled;
    }

    public boolean focusable() {
        return focusable;
    }

    public boolean focused() {
        return focused;
    }

    public boolean scrollable() {
        return scrollable;
    }

    public boolean longClickable() {
        return longClickable;
    }

    public boolean password() {
        return password;
    }

    public boolean selected() {
        return selected;
    }

    public boolean visibleToUser() {
        return visibleToUser;
    }

    public boolean importantForAccessibility() {
        return importantForAccessibility;
    }

    public int left() {
        return left;
    }

    public int top() {
        return top;
    }

    public int right() {
        return right;
    }

    public int bottom() {
        return bottom;
    }

    public List<HierarchyNode> children() {
        return children;
    }

    /** Fluent construction; every field defaults to empty/false/zero. */
    public static final class Builder {
        private boolean accessibilityUnfriendly;
        private int index;
        private String hintText = "";
        private String text = "";
        private String resourceId = "";
        private String className = "";
        private String packageName = "";
        private String contentDescription = "";
        private String error = "";
        private boolean checkable;
        private boolean checked;
        private boolean clickable;
        private boolean enabled;
        private boolean focusable;
        private boolean focused;
        private boolean scrollable;
        private boolean longClickable;
        private boolean password;
        private boolean selected;
        private boolean visibleToUser;
        private boolean importantForAccessibility;
        private int left;
        private int top;
        private int right;
        private int bottom;
        private final List<HierarchyNode> children = new ArrayList<>();

        private Builder() {}

        public Builder accessibilityUnfriendly(boolean value) {
            this.accessibilityUnfriendly = value;
            return this;
        }

        public Builder index(int value) {
            this.index = value;
            return this;
        }

        public Builder hintText(String value) {
            this.hintText = value;
            return this;
        }

        public Builder text(String value) {
            this.text = value;
            return this;
        }

        public Builder resourceId(String value) {
            this.resourceId = value;
            return this;
        }

        public Builder className(String value) {
            this.className = value;
            return this;
        }

        public Builder packageName(String value) {
            this.packageName = value;
            return this;
        }

        public Builder contentDescription(String value) {
            this.contentDescription = value;
            return this;
        }

        public Builder error(String value) {
            this.error = value;
            return this;
        }

        public Builder checkable(boolean value) {
            this.checkable = value;
            return this;
        }

        public Builder checked(boolean value) {
            this.checked = value;
            return this;
        }

        public Builder clickable(boolean value) {
            this.clickable = value;
            return this;
        }

        public Builder enabled(boolean value) {
            this.enabled = value;
            return this;
        }

        public Builder focusable(boolean value) {
            this.focusable = value;
            return this;
        }

        public Builder focused(boolean value) {
            this.focused = value;
            return this;
        }

        public Builder scrollable(boolean value) {
            this.scrollable = value;
            return this;
        }

        public Builder longClickable(boolean value) {
            this.longClickable = value;
            return this;
        }

        public Builder password(boolean value) {
            this.password = value;
            return this;
        }

        public Builder selected(boolean value) {
            this.selected = value;
            return this;
        }

        public Builder visibleToUser(boolean value) {
            this.visibleToUser = value;
            return this;
        }

        public Builder importantForAccessibility(boolean value) {
            this.importantForAccessibility = value;
            return this;
        }

        public Builder bounds(int left, int top, int right, int bottom) {
            this.left = left;
            this.top = top;
            this.right = right;
            this.bottom = bottom;
            return this;
        }

        public Builder addChild(HierarchyNode child) {
            this.children.add(child);
            return this;
        }

        public HierarchyNode build() {
            return new HierarchyNode(this);
        }
    }
}
