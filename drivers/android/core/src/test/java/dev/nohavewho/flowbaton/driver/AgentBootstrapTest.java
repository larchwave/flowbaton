package dev.nohavewho.flowbaton.driver;

import static org.junit.Assert.assertArrayEquals;

import io.grpc.CallOptions;
import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.grpc.stub.ClientCalls;
import java.net.ServerSocket;
import java.util.Map;
import java.util.concurrent.TimeUnit;
import org.junit.Test;

public final class AgentBootstrapTest {
    @Test
    public void startsDeviceServiceOnInstrumentationPort() throws Exception {
        int port;
        try (ServerSocket socket = new ServerSocket(0)) {
            port = socket.getLocalPort();
        }

        FakeDriverHandlers handlers = new FakeDriverHandlers();
        handlers.dimensions = new DeviceDimensions(720, 1280);
        try (FlowBatonDriverServer server =
                AgentBootstrap.start(Map.of("port", Integer.toString(port)), handlers)) {
            ManagedChannel channel =
                    ManagedChannelBuilder.forAddress("127.0.0.1", port).usePlaintext().build();
            try {
                byte[] response =
                        ClientCalls.blockingUnaryCall(
                                channel,
                                GrpcDeviceServer.DEVICE_INFO_METHOD,
                                CallOptions.DEFAULT,
                                new byte[0]);

                assertArrayEquals(
                        new byte[] {0x08, (byte) 0xd0, 0x05, 0x10, (byte) 0x80, 0x0a},
                        response);
            } finally {
                channel.shutdownNow();
                channel.awaitTermination(5, TimeUnit.SECONDS);
            }
        }
    }
}
