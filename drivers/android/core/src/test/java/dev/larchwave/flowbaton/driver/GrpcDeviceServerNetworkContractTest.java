package dev.larchwave.flowbaton.driver;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertTrue;

import org.junit.Test;

public final class GrpcDeviceServerNetworkContractTest {
    @Test
    public void usesFrozenMethodIdentityAndBindsOnlyToLoopback() throws Exception {
        assertEquals(
                "flowbaton_android.FlowBatonDriver/deviceInfo",
                GrpcDeviceServer.DEVICE_INFO_METHOD.getFullMethodName());

        try (GrpcDeviceServer server =
                GrpcDeviceServer.start(0, () -> new DeviceDimensions(1, 1))) {
            assertTrue(server.listenAddress().getAddress().isLoopbackAddress());
        }
    }
}
