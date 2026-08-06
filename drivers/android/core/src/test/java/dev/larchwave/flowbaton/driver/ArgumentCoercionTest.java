package dev.larchwave.flowbaton.driver;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertThrows;
import static org.junit.Assert.assertTrue;

import org.junit.Test;

public final class ArgumentCoercionTest {
    @Test
    public void coercesEachSpecNamedJavaTypeToItsClass() {
        assertEquals("flow", ArgumentCoercion.coerce("flow", "java.lang.String"));
        assertEquals(Boolean.TRUE, ArgumentCoercion.coerce("true", "java.lang.Boolean"));
        assertEquals(Integer.valueOf(42), ArgumentCoercion.coerce("42", "java.lang.Integer"));
        assertEquals(Double.valueOf(1.5), ArgumentCoercion.coerce("1.5", "java.lang.Double"));
        assertEquals(
                Long.valueOf(9_999_999_999L),
                ArgumentCoercion.coerce("9999999999", "java.lang.Long"));
    }

    @Test
    public void booleanCoercionFollowsParseBoolean() {
        assertEquals(Boolean.TRUE, ArgumentCoercion.coerce("TRUE", "java.lang.Boolean"));
        assertEquals(Boolean.FALSE, ArgumentCoercion.coerce("banana", "java.lang.Boolean"));
    }

    @Test
    public void unknownTypeIsRefusedByName() {
        IllegalArgumentException refusal =
                assertThrows(
                        IllegalArgumentException.class,
                        () -> ArgumentCoercion.coerce("x", "java.lang.Character"));
        assertTrue(refusal.getMessage(), refusal.getMessage().contains("java.lang.Character"));
    }

    @Test
    public void unparsableNumbersSurfaceAsNumberFormatErrors() {
        assertThrows(
                NumberFormatException.class,
                () -> ArgumentCoercion.coerce("abc", "java.lang.Integer"));
        assertThrows(
                NumberFormatException.class, () -> ArgumentCoercion.coerce("", "java.lang.Long"));
    }
}
