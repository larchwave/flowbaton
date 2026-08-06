package dev.nohavewho.flowbaton

import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import dev.nohavewho.flowbaton.driver.AgentBootstrap
import org.junit.Assume.assumeTrue
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class FlowBatonDriverService {
    @Test
    fun grpcServer() {
        val arguments = InstrumentationRegistry.getArguments()
        // The host targets this test explicitly (spec 02 §2.2:
        // -e class '...FlowBatonDriverService#grpcServer' -e port N). A blanket connected
        // run must not hang on a server that serves until killed, so without that class
        // filter the test is skipped -- the same lesson as the iOS runner's serve guard.
        assumeTrue(
            "grpcServer serves until killed; run it via -e class ...FlowBatonDriverService#grpcServer",
            arguments.getString("class")?.contains("grpcServer") == true,
        )

        val instrumentation = InstrumentationRegistry.getInstrumentation()
        val runnerArguments = buildMap {
            arguments.getString("port")?.let { put("port", it) }
        }
        val server = AgentBootstrap.start(runnerArguments, AndroidDriverHandlers(instrumentation))
        server.use { it.awaitTermination() }
    }
}
