package dev.nohavewho.flowbaton.driver;

/**
 * One typed {@code launchApp} extra as carried by the wire {@code ArgumentValue} message:
 * {@code type} is the Java class FQN controlling putExtra coercion (spec 04 §1).
 */
public record LaunchArgument(String key, String value, String type) {}
