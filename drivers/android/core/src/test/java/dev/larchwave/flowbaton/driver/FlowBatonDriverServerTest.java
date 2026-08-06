package dev.larchwave.flowbaton.driver;

import static org.junit.Assert.assertArrayEquals;
import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertNotNull;
import static org.junit.Assert.assertTrue;
import static org.junit.Assert.fail;

import dev.larchwave.flowbaton.driver.contract.AndroidWireContractV0;
import io.grpc.CallOptions;
import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.grpc.Metadata;
import io.grpc.MethodDescriptor;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import io.grpc.stub.ClientCalls;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.TimeUnit;
import org.junit.Test;

public final class FlowBatonDriverServerTest {
    @Test
    public void servesCanonicalDeviceInfoBytesAndBindsOnlyToLoopback() throws Exception {
        FakeDriverHandlers handlers = new FakeDriverHandlers();
        try (FlowBatonDriverServer server = FlowBatonDriverServer.start(0, handlers)) {
            assertTrue(server.port() > 0);
            assertTrue(server.listenAddress().getAddress().isLoopbackAddress());

            withChannel(
                    server,
                    channel -> {
                        byte[] response =
                                ClientCalls.blockingUnaryCall(
                                        channel,
                                        FlowBatonDriverServer.DEVICE_INFO_METHOD,
                                        CallOptions.DEFAULT,
                                        new byte[0]);
                        assertArrayEquals(
                                new byte[] {0x08, (byte) 0xb8, 0x08, 0x10, (byte) 0x80, 0x0f},
                                response);
                    });
        }
    }

    @Test
    public void servesEveryUnaryRpcThroughTheHandCodec() throws Exception {
        FakeDriverHandlers handlers = new FakeDriverHandlers();
        try (FlowBatonDriverServer server = FlowBatonDriverServer.start(0, handlers)) {
            withChannel(
                    server,
                    channel -> {
                        DriverClient client = new DriverClient(channel);

                        DeviceDimensions info = client.deviceInfo();
                        assertEquals(1080, info.widthPixels());
                        assertEquals(1920, info.heightPixels());

                        assertEquals("<hierarchy rotation=\"0\" />", client.viewHierarchy());
                        assertArrayEquals(handlers.png, client.screenshot());

                        client.tap(37, 73);
                        assertEquals(Integer.valueOf(37), handlers.tappedX);
                        assertEquals(Integer.valueOf(73), handlers.tappedY);

                        client.inputText("héllo 👍");
                        assertEquals("héllo 👍", handlers.typedText);

                        client.eraseAllText(12);
                        assertEquals(Integer.valueOf(12), handlers.erasedCharacters);

                        client.setLocation(59.3293, 18.0686);
                        assertEquals(59.3293, handlers.latitude, 0.0);
                        assertEquals(18.0686, handlers.longitude, 0.0);

                        assertTrue(client.isWindowUpdating("com.example.app"));
                        assertEquals("com.example.app", handlers.windowAppId);

                        List<LaunchArgument> arguments =
                                List.of(
                                        new LaunchArgument("count", "3", "java.lang.Integer"),
                                        new LaunchArgument("name", "flow", "java.lang.String"));
                        client.launchApp("com.example.app", arguments);
                        assertEquals("com.example.app", handlers.launchedPackage);
                        assertEquals(arguments, handlers.launchedArguments);

                        client.enableMockLocationProviders();
                        client.disableLocationUpdates();
                        assertEquals(1, handlers.enableMockLocationCalls);
                        assertEquals(1, handlers.disableLocationUpdateCalls);
                    });
        }
    }

    @Test
    public void addMediaStreamsChunksIntoOneMediaItem() throws Exception {
        FakeDriverHandlers handlers = new FakeDriverHandlers();
        try (FlowBatonDriverServer server = FlowBatonDriverServer.start(0, handlers)) {
            withChannel(
                    server,
                    channel -> {
                        byte[] data = new byte[] {0, 1, 2, 3, 4, 5, 6, 7, 8, 9};
                        new DriverClient(channel).addMedia("clip", "mp4", data, 4);
                        assertEquals("clip", handlers.mediaName);
                        assertEquals("mp4", handlers.mediaExt);
                        assertArrayEquals(data, handlers.mediaData);
                    });
        }
    }

    @Test
    public void anyHandlerFailureCarriesTheSpecErrorContract() throws Exception {
        FakeDriverHandlers handlers = new FakeDriverHandlers();
        handlers.failure =
                new IllegalStateException("boom", new IllegalArgumentException("root cause"));
        try (FlowBatonDriverServer server = FlowBatonDriverServer.start(0, handlers)) {
            withChannel(
                    server,
                    channel -> {
                        try {
                            new DriverClient(channel).tap(1, 2);
                            fail("tap should have carried the handler failure");
                        } catch (StatusRuntimeException failure) {
                            assertEquals(
                                    Status.Code.INTERNAL, failure.getStatus().getCode());
                            String description = failure.getStatus().getDescription();
                            assertNotNull(description);
                            assertTrue(description, description.contains("boom"));

                            Metadata trailers = Status.trailersFromThrowable(failure);
                            assertNotNull(trailers);
                            assertEquals(
                                    "java.lang.IllegalStateException",
                                    trailers.get(FlowBatonDriverServer.ERROR_TYPE_TRAILER));
                            assertEquals(
                                    "boom",
                                    trailers.get(FlowBatonDriverServer.ERROR_MESSAGE_TRAILER));
                            String cause =
                                    trailers.get(FlowBatonDriverServer.ERROR_CAUSE_TRAILER);
                            assertNotNull(cause);
                            assertTrue(cause, cause.contains("root cause"));
                        }
                    });
        }
    }

    @Test
    public void registeredMethodsMatchTheFrozenContractBothWays() throws Exception {
        try (FlowBatonDriverServer server =
                FlowBatonDriverServer.start(0, new FakeDriverHandlers())) {
            Map<String, MethodDescriptor<?, ?>> registered = new HashMap<>();
            for (MethodDescriptor<?, ?> method : server.registeredMethods()) {
                registered.put(method.getFullMethodName(), method);
            }

            List<AndroidWireContractV0.Rpc> rpcs = AndroidWireContractV0.rpcs();
            assertEquals(
                    "the live server must register exactly the frozen rpc set",
                    rpcs.size(),
                    registered.size());
            for (AndroidWireContractV0.Rpc rpc : rpcs) {
                String fullName = FlowBatonDriverServer.SERVICE_NAME + "/" + rpc.name();
                MethodDescriptor<?, ?> method = registered.get(fullName);
                assertNotNull("no live registration for " + fullName, method);
                MethodDescriptor.MethodType expected =
                        rpc.clientStreaming()
                                ? MethodDescriptor.MethodType.CLIENT_STREAMING
                                : MethodDescriptor.MethodType.UNARY;
                assertEquals(rpc.name(), expected, method.getType());
            }
        }
    }

    @Test
    public void serverTuningMatchesTheSpecSettings() {
        assertEquals(30, FlowBatonDriverServer.KEEPALIVE_PERMIT_SECONDS);
        assertEquals(20, FlowBatonDriverServer.KEEPALIVE_TIMEOUT_SECONDS);
        assertEquals(30, FlowBatonDriverServer.MAX_CONNECTION_IDLE_MINUTES);
    }

    private interface ChannelProbe {
        void run(ManagedChannel channel) throws Exception;
    }

    private static void withChannel(FlowBatonDriverServer server, ChannelProbe probe)
            throws Exception {
        ManagedChannel channel =
                ManagedChannelBuilder.forAddress("127.0.0.1", server.port())
                        .usePlaintext()
                        .build();
        try {
            probe.run(channel);
        } finally {
            channel.shutdownNow();
            assertTrue(channel.awaitTermination(5, TimeUnit.SECONDS));
        }
    }
}
