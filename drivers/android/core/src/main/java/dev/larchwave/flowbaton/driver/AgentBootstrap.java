package dev.larchwave.flowbaton.driver;

import java.io.IOException;
import java.util.Map;

/** Starts the device service from Android instrumentation arguments. */
public final class AgentBootstrap {
    private AgentBootstrap() {}

    public static FlowBatonDriverServer start(
            Map<String, String> arguments, FlowBatonDriverHandlers handlers) throws IOException {
        RunnerArguments runnerArguments = RunnerArguments.from(arguments);
        return FlowBatonDriverServer.start(runnerArguments.port(), handlers);
    }
}
