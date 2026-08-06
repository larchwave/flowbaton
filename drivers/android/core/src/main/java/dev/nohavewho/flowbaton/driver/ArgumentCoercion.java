package dev.nohavewho.flowbaton.driver;

/**
 * Coerces a wire {@code ArgumentValue} into the typed extra {@code launchApp} puts on the
 * launcher intent. {@code type} is one of the five Java class FQNs spec 04 §1 names; anything
 * else is refused so a typo surfaces as an INTERNAL error instead of a silently mistyped extra.
 */
public final class ArgumentCoercion {
    private ArgumentCoercion() {}

    public static Object coerce(String value, String type) {
        return switch (type) {
            case "java.lang.String" -> value;
            case "java.lang.Boolean" -> Boolean.parseBoolean(value);
            case "java.lang.Integer" -> Integer.parseInt(value);
            case "java.lang.Double" -> Double.parseDouble(value);
            case "java.lang.Long" -> Long.parseLong(value);
            default ->
                    throw new IllegalArgumentException(
                            "unsupported launch argument type "
                                    + type
                                    + " (spec 04 §1 allows java.lang.String|Boolean|Integer"
                                    + "|Double|Long)");
        };
    }
}
