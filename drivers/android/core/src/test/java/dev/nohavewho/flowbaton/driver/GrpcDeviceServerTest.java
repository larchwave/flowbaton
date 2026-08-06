package dev.nohavewho.flowbaton.driver;

import static org.junit.Assert.assertArrayEquals;
import static org.junit.Assert.assertTrue;

import io.grpc.CallOptions;
import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.grpc.stub.ClientCalls;
import java.util.concurrent.TimeUnit;
import org.junit.Test;

public final class GrpcDeviceServerTest {
    @Test
    public void servesCanonicalDeviceInfoRpcOverPlaintextGrpc() throws Exception {
        try (GrpcDeviceServer server =
                GrpcDeviceServer.start(0, () -> new DeviceDimensions(1080, 1920))) {
            assertTrue(server.port() > 0);

            ManagedChannel channel =
                    ManagedChannelBuilder.forAddress("127.0.0.1", server.port())
                            .usePlaintext()
                            .build();
            try {
                byte[] response =
                        ClientCalls.blockingUnaryCall(
                                channel,
                                GrpcDeviceServer.DEVICE_INFO_METHOD,
                                CallOptions.DEFAULT,
                                new byte[0]);

                assertArrayEquals(
                        new byte[] {0x08, (byte) 0xb8, 0x08, 0x10, (byte) 0x80, 0x0f},
                        response);
            } finally {
                channel.shutdownNow();
                assertTrue(channel.awaitTermination(5, TimeUnit.SECONDS));
            }
        }
    }
}
