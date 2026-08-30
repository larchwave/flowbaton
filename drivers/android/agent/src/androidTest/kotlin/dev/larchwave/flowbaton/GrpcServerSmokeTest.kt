package dev.larchwave.flowbaton

import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import dev.larchwave.flowbaton.driver.AgentBootstrap
import dev.larchwave.flowbaton.driver.DriverClient
import java.util.concurrent.TimeUnit
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

/**
 * Connected smoke: the server binds the instrumentation-arg port (default 7001) and answers
 * deviceInfo + viewHierarchy over a real localhost grpc-java channel — the same pinned Netty
 * artifact the host-facing server uses.
 */
@RunWith(AndroidJUnit4::class)
class GrpcServerSmokeTest {
    @Test
    fun answersDeviceInfoAndViewHierarchyOverLoopbackGrpc() {
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        val arguments = InstrumentationRegistry.getArguments()
        val runnerArguments = buildMap {
            arguments.getString("port")?.let { put("port", it) }
        }
        val expectedPort = runnerArguments["port"]?.toInt() ?: 7001
        val server = AgentBootstrap.start(runnerArguments, AndroidDriverHandlers(instrumentation))
        server.use {
            assertTrue(
                "the instrumentation-arg port must be bound, wanted $expectedPort got ${it.port()}",
                it.port() == expectedPort,
            )
            val channel = DriverClient.loopbackChannel(it.port())
            try {
                val client = DriverClient(channel)

                val info = client.deviceInfo()
                assertTrue("width must be real pixels: ${info.widthPixels()}", info.widthPixels() > 0)
                assertTrue("height must be real pixels: ${info.heightPixels()}", info.heightPixels() > 0)

                val hierarchy = client.viewHierarchy(false)
                assertTrue(hierarchy, hierarchy.startsWith("<?xml version='1.0'"))
                assertTrue(hierarchy, hierarchy.contains("<hierarchy rotation="))
                assertTrue(hierarchy, hierarchy.contains("bounds=\""))
            } finally {
                channel.shutdownNow()
                channel.awaitTermination(5, TimeUnit.SECONDS)
            }
        }
    }
}
