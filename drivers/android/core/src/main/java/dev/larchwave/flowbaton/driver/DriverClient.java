package dev.larchwave.flowbaton.driver;

import io.grpc.CallOptions;
import io.grpc.Channel;
import io.grpc.ManagedChannel;
import io.grpc.MethodDescriptor;
import io.grpc.netty.shaded.io.grpc.netty.NettyChannelBuilder;
import io.grpc.stub.ClientCalls;
import io.grpc.stub.StreamObserver;
import java.util.Arrays;
import java.util.List;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;

/**
 * Blocking client for the frozen {@code flowbaton_android.FlowBatonDriver} service over the same
 * hand-rolled codec the server uses. Backs the JVM contract tests and the on-device smoke test;
 * the production host client lives on the Go side.
 */
public final class DriverClient {
    private final Channel channel;

    public DriverClient(Channel channel) {
        this.channel = channel;
    }

    /**
     * Plaintext loopback channel over the pinned shaded Netty transport. Built explicitly
     * because on Android gRPC's provider discovery only knows the unshaded class names, so
     * {@code ManagedChannelBuilder.forAddress} cannot find the shaded transport.
     */
    public static ManagedChannel loopbackChannel(int port) {
        return NettyChannelBuilder.forAddress("127.0.0.1", port).usePlaintext().build();
    }

    public DeviceDimensions deviceInfo() {
        return WireCodec.decodeDeviceInfo(call(FlowBatonDriverServer.DEVICE_INFO_METHOD, new byte[0]));
    }

    public String viewHierarchy(boolean excludeKeyboardElements) {
        return WireCodec.decodeViewHierarchy(
                call(
                        FlowBatonDriverServer.VIEW_HIERARCHY_METHOD,
                        WireCodec.encodeViewHierarchyRequest(excludeKeyboardElements)));
    }

    public byte[] screenshot() {
        return WireCodec.decodeScreenshot(call(FlowBatonDriverServer.SCREENSHOT_METHOD, new byte[0]));
    }

    public void tap(int x, int y) {
        call(FlowBatonDriverServer.TAP_METHOD, WireCodec.encodeTap(x, y));
    }

    public void inputText(String text) {
        call(FlowBatonDriverServer.INPUT_TEXT_METHOD, WireCodec.encodeInputText(text));
    }

    public void eraseAllText(int charactersToErase) {
        call(FlowBatonDriverServer.ERASE_ALL_TEXT_METHOD,
                WireCodec.encodeEraseAllText(charactersToErase));
    }

    public void setLocation(double latitude, double longitude) {
        call(FlowBatonDriverServer.SET_LOCATION_METHOD,
                WireCodec.encodeSetLocation(latitude, longitude));
    }

    public boolean isWindowUpdating(String appId) {
        return WireCodec.decodeIsWindowUpdating(
                call(
                        FlowBatonDriverServer.IS_WINDOW_UPDATING_METHOD,
                        WireCodec.encodeCheckWindowUpdating(appId)));
    }

    public void launchApp(String packageName, List<LaunchArgument> arguments) {
        call(FlowBatonDriverServer.LAUNCH_APP_METHOD,
                WireCodec.encodeLaunchApp(packageName, arguments));
    }

    public void enableMockLocationProviders() {
        call(FlowBatonDriverServer.ENABLE_MOCK_LOCATION_PROVIDERS_METHOD, new byte[0]);
    }

    public void disableLocationUpdates() {
        call(FlowBatonDriverServer.DISABLE_LOCATION_UPDATES_METHOD, new byte[0]);
    }

    public void addMedia(String mediaName, String mediaExt, byte[] data, int chunkSize)
            throws InterruptedException {
        if (chunkSize < 1) {
            throw new IllegalArgumentException("chunkSize must be positive: " + chunkSize);
        }
        Throwable[] failure = new Throwable[1];
        CountDownLatch done = new CountDownLatch(1);
        StreamObserver<byte[]> responses =
                new StreamObserver<>() {
                    @Override
                    public void onNext(byte[] response) {}

                    @Override
                    public void onError(Throwable error) {
                        failure[0] = error;
                        done.countDown();
                    }

                    @Override
                    public void onCompleted() {
                        done.countDown();
                    }
                };
        StreamObserver<byte[]> requests =
                ClientCalls.asyncClientStreamingCall(
                        channel.newCall(FlowBatonDriverServer.ADD_MEDIA_METHOD, CallOptions.DEFAULT),
                        responses);
        int offset = 0;
        do {
            int end = Math.min(data.length, offset + chunkSize);
            requests.onNext(
                    WireCodec.encodeAddMediaChunk(
                            Arrays.copyOfRange(data, offset, end), mediaName, mediaExt));
            offset = end;
        } while (offset < data.length);
        requests.onCompleted();

        if (!done.await(30, TimeUnit.SECONDS)) {
            throw new IllegalStateException("addMedia did not complete within thirty seconds");
        }
        if (failure[0] instanceof RuntimeException runtime) {
            throw runtime;
        }
        if (failure[0] != null) {
            throw new IllegalStateException("addMedia failed", failure[0]);
        }
    }

    private byte[] call(MethodDescriptor<byte[], byte[]> method, byte[] request) {
        return ClientCalls.blockingUnaryCall(channel, method, CallOptions.DEFAULT, request);
    }
}
