package dev.nohavewho.flowbaton.driver;

import java.util.Map;

/** Validated arguments passed to the FlowBaton instrumentation runner. */
public final class RunnerArguments {
    public static final int DEFAULT_PORT = 7001;
    private final int port;

    public RunnerArguments(int port) {
        this.port = port;
    }

    public int port() {
        return port;
    }

    public static RunnerArguments from(Map<String, String> arguments) {
        String rawPort = arguments.get("port");
        if (rawPort == null) {
            return new RunnerArguments(DEFAULT_PORT);
        }

        final int parsed;
        try {
            parsed = Integer.parseInt(rawPort);
        } catch (NumberFormatException error) {
            throw new IllegalArgumentException("port must be a decimal TCP port: " + rawPort, error);
        }

        if (parsed < 1 || parsed > 65_535) {
            throw new IllegalArgumentException("port must be in range 1..65535: " + parsed);
        }
        return new RunnerArguments(parsed);
    }
}
