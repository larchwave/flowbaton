package dev.nohavewho.flowbaton.driver;

/**
 * A toast observed while the hierarchy was captured. Serialized as its own {@code <node>} with
 * exactly the spec 04 §2 toast attribute subset.
 */
public record ToastNode(String className, String text) {}
