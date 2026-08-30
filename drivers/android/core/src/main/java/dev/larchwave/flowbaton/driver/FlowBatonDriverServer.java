package dev.larchwave.flowbaton.driver;

import io.grpc.Metadata;
import io.grpc.MethodDescriptor;
import io.grpc.Server;
import io.grpc.ServerMethodDefinition;
import io.grpc.ServerServiceDefinition;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import io.grpc.netty.shaded.io.grpc.netty.NettyServerBuilder;
import io.grpc.stub.ServerCalls;
import io.grpc.stub.StreamObserver;
import java.io.ByteArrayInputStream;
import java.io.IOException;
import java.io.InputStream;
import java.net.InetSocketAddress;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.TimeUnit;

/**
 * Plaintext Netty gRPC server for the frozen {@code flowbaton_android.FlowBatonDriver} service.
 * All twelve RPCs are wired to an injected {@link FlowBatonDriverHandlers}; any throwable a
 * handler raises is reported per the spec 04 §1 error contract (INTERNAL + description, with
 * {@code error-type} / {@code error-message} / {@code error-cause} trailers).
 *
 * <p>{@link GrpcDeviceServer} stays untouched beside this class: it is the frozen
 * {@code android-g001-runtime} contract artifact, pinned by SHA-256 in
 * {@code contracts/v0/contract-set.json}, so the full service lives here instead of growing
 * inside the pinned file.</p>
 */
public final class FlowBatonDriverServer implements AutoCloseable {
    public static final String SERVICE_NAME = "flowbaton_android.FlowBatonDriver";

    /** Spec 04 §1 server settings: keepalive permit 30s / timeout 20s / idle 30min. */
    public static final int KEEPALIVE_PERMIT_SECONDS = 30;

    public static final int KEEPALIVE_TIMEOUT_SECONDS = 20;
    public static final int MAX_CONNECTION_IDLE_MINUTES = 30;
    public static final long MAX_MEDIA_BYTES = 512L * 1024L * 1024L;

    public static final Metadata.Key<String> ERROR_TYPE_TRAILER = trailerKey("error-type");
    public static final Metadata.Key<String> ERROR_MESSAGE_TRAILER = trailerKey("error-message");
    public static final Metadata.Key<String> ERROR_CAUSE_TRAILER = trailerKey("error-cause");

    private static final ByteArrayMarshaller BYTE_ARRAY_MARSHALLER = new ByteArrayMarshaller();
    private static final byte[] EMPTY_MESSAGE = new byte[0];

    public static final MethodDescriptor<byte[], byte[]> DEVICE_INFO_METHOD = unary("deviceInfo");
    public static final MethodDescriptor<byte[], byte[]> VIEW_HIERARCHY_METHOD =
            unary("viewHierarchy");
    public static final MethodDescriptor<byte[], byte[]> SCREENSHOT_METHOD = unary("screenshot");
    public static final MethodDescriptor<byte[], byte[]> TAP_METHOD = unary("tap");
    public static final MethodDescriptor<byte[], byte[]> INPUT_TEXT_METHOD = unary("inputText");
    public static final MethodDescriptor<byte[], byte[]> ERASE_ALL_TEXT_METHOD =
            unary("eraseAllText");
    public static final MethodDescriptor<byte[], byte[]> SET_LOCATION_METHOD =
            unary("setLocation");
    public static final MethodDescriptor<byte[], byte[]> IS_WINDOW_UPDATING_METHOD =
            unary("isWindowUpdating");
    public static final MethodDescriptor<byte[], byte[]> LAUNCH_APP_METHOD = unary("launchApp");
    public static final MethodDescriptor<byte[], byte[]> ADD_MEDIA_METHOD =
            descriptor("addMedia", MethodDescriptor.MethodType.CLIENT_STREAMING);
    public static final MethodDescriptor<byte[], byte[]> ENABLE_MOCK_LOCATION_PROVIDERS_METHOD =
            unary("enableMockLocationProviders");
    public static final MethodDescriptor<byte[], byte[]> DISABLE_LOCATION_UPDATES_METHOD =
            unary("disableLocationUpdates");

    private final Server server;
    private final InetSocketAddress listenAddress;

    private FlowBatonDriverServer(Server server, InetSocketAddress listenAddress) {
        this.server = server;
        this.listenAddress = listenAddress;
    }

    public static FlowBatonDriverServer start(int port, FlowBatonDriverHandlers handlers)
            throws IOException {
        InetSocketAddress requestedAddress = new InetSocketAddress("127.0.0.1", port);
        Server server =
                NettyServerBuilder.forAddress(requestedAddress)
                        .permitKeepAliveTime(KEEPALIVE_PERMIT_SECONDS, TimeUnit.SECONDS)
                        .keepAliveTimeout(KEEPALIVE_TIMEOUT_SECONDS, TimeUnit.SECONDS)
                        .maxConnectionIdle(MAX_CONNECTION_IDLE_MINUTES, TimeUnit.MINUTES)
                        .addService(serviceFor(handlers))
                        .build()
                        .start();
        InetSocketAddress boundAddress =
                new InetSocketAddress(requestedAddress.getAddress(), server.getPort());
        return new FlowBatonDriverServer(server, boundAddress);
    }

    public int port() {
        return server.getPort();
    }

    public InetSocketAddress listenAddress() {
        return listenAddress;
    }

    /** The live registration, read off the running server rather than a parallel list. */
    public List<MethodDescriptor<?, ?>> registeredMethods() {
        List<MethodDescriptor<?, ?>> methods = new ArrayList<>();
        server.getServices()
                .forEach(
                        service ->
                                service.getMethods().stream()
                                        .map(ServerMethodDefinition::getMethodDescriptor)
                                        .forEach(methods::add));
        return List.copyOf(methods);
    }

    public void awaitTermination() throws InterruptedException {
        server.awaitTermination();
    }

    @Override
    public void close() throws InterruptedException {
        server.shutdownNow();
        if (!server.awaitTermination(5, TimeUnit.SECONDS)) {
            throw new IllegalStateException("gRPC server did not terminate within five seconds");
        }
    }

    private static ServerServiceDefinition serviceFor(FlowBatonDriverHandlers handlers) {
        return ServerServiceDefinition.builder(SERVICE_NAME)
                .addMethod(
                        DEVICE_INFO_METHOD,
                        unaryCall(
                                request -> {
                                    DeviceDimensions dimensions = handlers.deviceInfo();
                                    return WireCodec.encodeDeviceInfo(
                                            dimensions.widthPixels(), dimensions.heightPixels());
                                }))
                .addMethod(
                        VIEW_HIERARCHY_METHOD,
                        unaryCall(
                                request ->
                                        WireCodec.encodeViewHierarchy(
                                                handlers.viewHierarchy(
                                                        WireCodec.decodeViewHierarchyRequest(
                                                                request)))))
                .addMethod(
                        SCREENSHOT_METHOD,
                        unaryCall(request -> WireCodec.encodeScreenshot(handlers.screenshot())))
                .addMethod(
                        TAP_METHOD,
                        unaryCall(
                                request -> {
                                    WireCodec.Tap tap = WireCodec.decodeTap(request);
                                    handlers.tap(tap.x(), tap.y());
                                    return EMPTY_MESSAGE;
                                }))
                .addMethod(
                        INPUT_TEXT_METHOD,
                        unaryCall(
                                request -> {
                                    handlers.inputText(WireCodec.decodeInputText(request));
                                    return EMPTY_MESSAGE;
                                }))
                .addMethod(
                        ERASE_ALL_TEXT_METHOD,
                        unaryCall(
                                request -> {
                                    handlers.eraseAllText(WireCodec.decodeEraseAllText(request));
                                    return EMPTY_MESSAGE;
                                }))
                .addMethod(
                        SET_LOCATION_METHOD,
                        unaryCall(
                                request -> {
                                    WireCodec.Location location =
                                            WireCodec.decodeSetLocation(request);
                                    handlers.setLocation(
                                            location.latitude(), location.longitude());
                                    return EMPTY_MESSAGE;
                                }))
                .addMethod(
                        IS_WINDOW_UPDATING_METHOD,
                        unaryCall(
                                request ->
                                        WireCodec.encodeIsWindowUpdating(
                                                handlers.isWindowUpdating(
                                                        WireCodec.decodeCheckWindowUpdating(
                                                                request)))))
                .addMethod(
                        LAUNCH_APP_METHOD,
                        unaryCall(
                                request -> {
                                    WireCodec.LaunchApp launch =
                                            WireCodec.decodeLaunchApp(request);
                                    handlers.launchApp(
                                            launch.packageName(), launch.arguments());
                                    return EMPTY_MESSAGE;
                                }))
                .addMethod(ADD_MEDIA_METHOD, addMediaCall(handlers))
                .addMethod(
                        ENABLE_MOCK_LOCATION_PROVIDERS_METHOD,
                        unaryCall(
                                request -> {
                                    handlers.enableMockLocationProviders();
                                    return EMPTY_MESSAGE;
                                }))
                .addMethod(
                        DISABLE_LOCATION_UPDATES_METHOD,
                        unaryCall(
                                request -> {
                                    handlers.disableLocationUpdates();
                                    return EMPTY_MESSAGE;
                                }))
                .build();
    }

    private static io.grpc.ServerCallHandler<byte[], byte[]> unaryCall(WireOperation operation) {
        return ServerCalls.asyncUnaryCall(
                (byte[] request, StreamObserver<byte[]> observer) -> {
                    final byte[] response;
                    try {
                        response = operation.apply(request);
                    } catch (Throwable failure) {
                        observer.onError(internalError(failure));
                        return;
                    }
                    observer.onNext(response);
                    observer.onCompleted();
                });
    }

    private static io.grpc.ServerCallHandler<byte[], byte[]> addMediaCall(
            FlowBatonDriverHandlers handlers) {
        return ServerCalls.asyncClientStreamingCall(
                (StreamObserver<byte[]> observer) ->
                        new StreamObserver<byte[]>() {
                            private final IncomingMedia data =
                                    new IncomingMedia(MAX_MEDIA_BYTES);
                            private String mediaName = "";
                            private String mediaExt = "";
                            private boolean failed;

                            @Override
                            public void onNext(byte[] request) {
                                try {
                                    WireCodec.MediaChunk chunk =
                                            WireCodec.decodeAddMediaChunk(request);
                                    if (mediaName.isEmpty()) {
                                        mediaName = chunk.mediaName();
                                    }
                                    if (mediaExt.isEmpty()) {
                                        mediaExt = chunk.mediaExt();
                                    }
                                    data.append(chunk.payload());
                                } catch (Throwable failure) {
                                    fail(failure, true);
                                }
                            }

                            @Override
                            public void onError(Throwable failure) {
                                fail(failure, false);
                            }

                            @Override
                            public void onCompleted() {
                                if (failed) {
                                    return;
                                }
                                try (data; InputStream input = data.openInputStream()) {
                                    handlers.addMedia(mediaName, mediaExt, input);
                                } catch (Throwable failure) {
                                    observer.onError(internalError(failure));
                                    return;
                                }
                                observer.onNext(EMPTY_MESSAGE);
                                observer.onCompleted();
                            }

                            private void fail(Throwable failure, boolean report) {
                                if (failed) {
                                    return;
                                }
                                failed = true;
                                try {
                                    data.close();
                                } catch (Throwable cleanupFailure) {
                                    failure.addSuppressed(cleanupFailure);
                                }
                                if (report) {
                                    observer.onError(internalError(failure));
                                }
                            }
                        });
    }

    private static StatusRuntimeException internalError(Throwable failure) {
        Metadata trailers = new Metadata();
        trailers.put(ERROR_TYPE_TRAILER, failure.getClass().getName());
        trailers.put(
                ERROR_MESSAGE_TRAILER,
                asciiSafe(failure.getMessage() == null ? "" : failure.getMessage()));
        trailers.put(
                ERROR_CAUSE_TRAILER,
                asciiSafe(failure.getCause() == null ? "" : failure.getCause().toString()));
        return Status.INTERNAL
                .withDescription(failure.toString())
                .asRuntimeException(trailers);
    }

    /** Trailer values ride HTTP/2 headers; anything outside printable ASCII would corrupt them. */
    private static String asciiSafe(String value) {
        StringBuilder safe = new StringBuilder(value.length());
        for (int i = 0; i < value.length(); i++) {
            char c = value.charAt(i);
            safe.append(c >= 0x20 && c <= 0x7e ? c : '?');
        }
        return safe.toString();
    }

    private static Metadata.Key<String> trailerKey(String name) {
        return Metadata.Key.of(name, Metadata.ASCII_STRING_MARSHALLER);
    }

    private static MethodDescriptor<byte[], byte[]> unary(String name) {
        return descriptor(name, MethodDescriptor.MethodType.UNARY);
    }

    private static MethodDescriptor<byte[], byte[]> descriptor(
            String name, MethodDescriptor.MethodType type) {
        return MethodDescriptor.<byte[], byte[]>newBuilder()
                .setType(type)
                .setFullMethodName(MethodDescriptor.generateFullMethodName(SERVICE_NAME, name))
                .setRequestMarshaller(BYTE_ARRAY_MARSHALLER)
                .setResponseMarshaller(BYTE_ARRAY_MARSHALLER)
                .build();
    }

    @FunctionalInterface
    private interface WireOperation {
        byte[] apply(byte[] request) throws Exception;
    }

    private static final class ByteArrayMarshaller implements MethodDescriptor.Marshaller<byte[]> {
        @Override
        public InputStream stream(byte[] value) {
            return new ByteArrayInputStream(value);
        }

        @Override
        public byte[] parse(InputStream stream) {
            try {
                return stream.readAllBytes();
            } catch (IOException error) {
                throw new IllegalStateException("could not read gRPC payload", error);
            }
        }
    }
}
