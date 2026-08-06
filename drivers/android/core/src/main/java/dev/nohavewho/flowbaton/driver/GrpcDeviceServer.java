package dev.nohavewho.flowbaton.driver;

import io.grpc.MethodDescriptor;
import io.grpc.Server;
import io.grpc.ServerServiceDefinition;
import io.grpc.netty.shaded.io.grpc.netty.NettyServerBuilder;
import io.grpc.stub.ServerCalls;
import io.grpc.stub.StreamObserver;
import java.io.ByteArrayInputStream;
import java.io.IOException;
import java.io.InputStream;
import java.net.InetSocketAddress;
import java.util.concurrent.TimeUnit;

/** Minimal G001 server for the canonical Android deviceInfo method. */
public final class GrpcDeviceServer implements AutoCloseable {
    private static final ByteArrayMarshaller BYTE_ARRAY_MARSHALLER = new ByteArrayMarshaller();

    public static final MethodDescriptor<byte[], byte[]> DEVICE_INFO_METHOD =
            MethodDescriptor.<byte[], byte[]>newBuilder()
                    .setType(MethodDescriptor.MethodType.UNARY)
                    .setFullMethodName(
                            MethodDescriptor.generateFullMethodName(
                                    "flowbaton_android.FlowBatonDriver", "deviceInfo"))
                    .setRequestMarshaller(BYTE_ARRAY_MARSHALLER)
                    .setResponseMarshaller(BYTE_ARRAY_MARSHALLER)
                    .build();

    private final Server server;
    private final InetSocketAddress listenAddress;

    private GrpcDeviceServer(Server server, InetSocketAddress listenAddress) {
        this.server = server;
        this.listenAddress = listenAddress;
    }

    public static GrpcDeviceServer start(int port, DeviceDimensionsProvider dimensionsProvider)
            throws IOException {
        ServerServiceDefinition service =
                ServerServiceDefinition.builder("flowbaton_android.FlowBatonDriver")
                        .addMethod(
                                DEVICE_INFO_METHOD,
                                ServerCalls.asyncUnaryCall(
                                        (byte[] ignored, StreamObserver<byte[]> observer) -> {
                                            observer.onNext(
                                                    encodeDeviceInfo(
                                                            dimensionsProvider.dimensions()));
                                            observer.onCompleted();
                                        }))
                        .build();

        InetSocketAddress requestedAddress = new InetSocketAddress("127.0.0.1", port);
        Server server =
                NettyServerBuilder.forAddress(requestedAddress)
                        .addService(service)
                        .build()
                        .start();
        InetSocketAddress boundAddress =
                new InetSocketAddress(requestedAddress.getAddress(), server.getPort());
        return new GrpcDeviceServer(server, boundAddress);
    }

    public int port() {
        return server.getPort();
    }

    public InetSocketAddress listenAddress() {
        return listenAddress;
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

    private static byte[] encodeDeviceInfo(DeviceDimensions dimensions) {
        byte[] width = encodeVarint(dimensions.widthPixels());
        byte[] height = encodeVarint(dimensions.heightPixels());
        byte[] response = new byte[2 + width.length + height.length];
        int offset = 0;
        response[offset++] = 0x08;
        System.arraycopy(width, 0, response, offset, width.length);
        offset += width.length;
        response[offset++] = 0x10;
        System.arraycopy(height, 0, response, offset, height.length);
        return response;
    }

    private static byte[] encodeVarint(int value) {
        byte[] scratch = new byte[5];
        int length = 0;
        do {
            int next = value & 0x7f;
            value >>>= 7;
            scratch[length++] = (byte) (value == 0 ? next : next | 0x80);
        } while (value != 0);

        byte[] encoded = new byte[length];
        System.arraycopy(scratch, 0, encoded, 0, length);
        return encoded;
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
