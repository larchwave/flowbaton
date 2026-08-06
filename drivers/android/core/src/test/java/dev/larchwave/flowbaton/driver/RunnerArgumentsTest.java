package dev.larchwave.flowbaton.driver;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertThrows;

import java.util.Map;
import org.junit.Test;

public final class RunnerArgumentsTest {
    @Test
    public void defaultsToContractPortWhenArgumentIsMissing() {
        assertEquals(7001, RunnerArguments.from(Map.of()).port());
    }

    @Test
    public void acceptsExplicitInstrumentationPort() {
        assertEquals(7123, RunnerArguments.from(Map.of("port", "7123")).port());
    }

    @Test
    public void rejectsNonNumericPort() {
        assertThrows(
                IllegalArgumentException.class,
                () -> RunnerArguments.from(Map.of("port", "not-a-port")));
    }

    @Test
    public void rejectsPortOutsideTcpRange() {
        assertThrows(
                IllegalArgumentException.class,
                () -> RunnerArguments.from(Map.of("port", "65536")));
    }
}
