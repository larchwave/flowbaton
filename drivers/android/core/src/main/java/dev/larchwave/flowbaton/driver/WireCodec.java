package dev.larchwave.flowbaton.driver;

import java.io.ByteArrayOutputStream;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;

/**
 * Hand-rolled proto3 codec for the frozen {@code flowbaton_android} messages. The messages are
 * small and the field set is pinned by {@code AndroidWireContractV0}, so a few dozen lines of
 * varint arithmetic replace a protobuf toolchain dependency. Unknown fields are skipped;
 * truncated input is refused.
 */
public final class WireCodec {
    private WireCodec() {}

    public record Tap(int x, int y) {}

    public record Location(double latitude, double longitude) {}

    public record LaunchApp(String packageName, List<LaunchArgument> arguments) {}

    public record MediaChunk(byte[] payload, String mediaName, String mediaExt) {}

    public static byte[] encodeDeviceInfo(int widthPixels, int heightPixels) {
        Writer writer = new Writer();
        writer.varintField(1, Integer.toUnsignedLong(widthPixels));
        writer.varintField(2, Integer.toUnsignedLong(heightPixels));
        return writer.bytes();
    }

    public static DeviceDimensions decodeDeviceInfo(byte[] encoded) {
        Reader reader = new Reader(encoded);
        int width = 0;
        int height = 0;
        while (reader.hasMore()) {
            int tag = reader.readTag();
            switch (tag) {
                case 0x08 -> width = (int) reader.readVarint();
                case 0x10 -> height = (int) reader.readVarint();
                default -> reader.skip(tag);
            }
        }
        return new DeviceDimensions(width, height);
    }

    public static byte[] encodeViewHierarchy(String hierarchy) {
        Writer writer = new Writer();
        writer.stringField(1, hierarchy);
        return writer.bytes();
    }

    public static String decodeViewHierarchy(byte[] encoded) {
        return decodeSingleString(encoded);
    }

    public static byte[] encodeScreenshot(byte[] png) {
        Writer writer = new Writer();
        writer.bytesField(1, png);
        return writer.bytes();
    }

    public static byte[] decodeScreenshot(byte[] encoded) {
        Reader reader = new Reader(encoded);
        byte[] bytes = new byte[0];
        while (reader.hasMore()) {
            int tag = reader.readTag();
            if (tag == 0x0a) {
                bytes = reader.readBytes();
            } else {
                reader.skip(tag);
            }
        }
        return bytes;
    }

    public static byte[] encodeTap(int x, int y) {
        Writer writer = new Writer();
        writer.varintField(1, Integer.toUnsignedLong(x));
        writer.varintField(2, Integer.toUnsignedLong(y));
        return writer.bytes();
    }

    public static Tap decodeTap(byte[] encoded) {
        Reader reader = new Reader(encoded);
        int x = 0;
        int y = 0;
        while (reader.hasMore()) {
            int tag = reader.readTag();
            switch (tag) {
                case 0x08 -> x = (int) reader.readVarint();
                case 0x10 -> y = (int) reader.readVarint();
                default -> reader.skip(tag);
            }
        }
        return new Tap(x, y);
    }

    public static byte[] encodeInputText(String text) {
        Writer writer = new Writer();
        writer.stringField(1, text);
        return writer.bytes();
    }

    public static String decodeInputText(byte[] encoded) {
        return decodeSingleString(encoded);
    }

    public static byte[] encodeEraseAllText(int charactersToErase) {
        Writer writer = new Writer();
        writer.varintField(1, Integer.toUnsignedLong(charactersToErase));
        return writer.bytes();
    }

    public static int decodeEraseAllText(byte[] encoded) {
        Reader reader = new Reader(encoded);
        int characters = 0;
        while (reader.hasMore()) {
            int tag = reader.readTag();
            if (tag == 0x08) {
                characters = (int) reader.readVarint();
            } else {
                reader.skip(tag);
            }
        }
        return characters;
    }

    public static byte[] encodeSetLocation(double latitude, double longitude) {
        Writer writer = new Writer();
        writer.doubleField(1, latitude);
        writer.doubleField(2, longitude);
        return writer.bytes();
    }

    public static Location decodeSetLocation(byte[] encoded) {
        Reader reader = new Reader(encoded);
        double latitude = 0;
        double longitude = 0;
        while (reader.hasMore()) {
            int tag = reader.readTag();
            switch (tag) {
                case 0x09 -> latitude = Double.longBitsToDouble(reader.readFixed64());
                case 0x11 -> longitude = Double.longBitsToDouble(reader.readFixed64());
                default -> reader.skip(tag);
            }
        }
        return new Location(latitude, longitude);
    }

    public static byte[] encodeCheckWindowUpdating(String appId) {
        Writer writer = new Writer();
        writer.stringField(1, appId);
        return writer.bytes();
    }

    public static String decodeCheckWindowUpdating(byte[] encoded) {
        return decodeSingleString(encoded);
    }

    public static byte[] encodeIsWindowUpdating(boolean isWindowUpdating) {
        Writer writer = new Writer();
        writer.varintField(1, isWindowUpdating ? 1 : 0);
        return writer.bytes();
    }

    public static boolean decodeIsWindowUpdating(byte[] encoded) {
        Reader reader = new Reader(encoded);
        boolean updating = false;
        while (reader.hasMore()) {
            int tag = reader.readTag();
            if (tag == 0x08) {
                updating = reader.readVarint() != 0;
            } else {
                reader.skip(tag);
            }
        }
        return updating;
    }

    public static byte[] encodeLaunchApp(String packageName, List<LaunchArgument> arguments) {
        Writer writer = new Writer();
        writer.stringField(1, packageName);
        for (LaunchArgument argument : arguments) {
            writer.messageField(2, encodeArgumentValue(argument));
        }
        return writer.bytes();
    }

    public static LaunchApp decodeLaunchApp(byte[] encoded) {
        Reader reader = new Reader(encoded);
        String packageName = "";
        List<LaunchArgument> arguments = new ArrayList<>();
        while (reader.hasMore()) {
            int tag = reader.readTag();
            switch (tag) {
                case 0x0a -> packageName = reader.readString();
                case 0x12 -> arguments.add(decodeArgumentValue(reader.readBytes()));
                default -> reader.skip(tag);
            }
        }
        return new LaunchApp(packageName, List.copyOf(arguments));
    }

    public static byte[] encodeAddMediaChunk(byte[] payload, String mediaName, String mediaExt) {
        Writer payloadWriter = new Writer();
        payloadWriter.bytesField(1, payload);

        Writer writer = new Writer();
        writer.messageField(1, payloadWriter.bytes());
        writer.stringField(2, mediaName);
        writer.stringField(3, mediaExt);
        return writer.bytes();
    }

    public static MediaChunk decodeAddMediaChunk(byte[] encoded) {
        Reader reader = new Reader(encoded);
        byte[] payload = new byte[0];
        String mediaName = "";
        String mediaExt = "";
        while (reader.hasMore()) {
            int tag = reader.readTag();
            switch (tag) {
                case 0x0a -> payload = decodePayload(reader.readBytes());
                case 0x12 -> mediaName = reader.readString();
                case 0x1a -> mediaExt = reader.readString();
                default -> reader.skip(tag);
            }
        }
        return new MediaChunk(payload, mediaName, mediaExt);
    }

    private static byte[] encodeArgumentValue(LaunchArgument argument) {
        Writer writer = new Writer();
        writer.stringField(1, argument.key());
        writer.stringField(2, argument.value());
        writer.stringField(3, argument.type());
        return writer.bytes();
    }

    private static LaunchArgument decodeArgumentValue(byte[] encoded) {
        Reader reader = new Reader(encoded);
        String key = "";
        String value = "";
        String type = "";
        while (reader.hasMore()) {
            int tag = reader.readTag();
            switch (tag) {
                case 0x0a -> key = reader.readString();
                case 0x12 -> value = reader.readString();
                case 0x1a -> type = reader.readString();
                default -> reader.skip(tag);
            }
        }
        return new LaunchArgument(key, value, type);
    }

    private static byte[] decodePayload(byte[] encoded) {
        Reader reader = new Reader(encoded);
        byte[] data = new byte[0];
        while (reader.hasMore()) {
            int tag = reader.readTag();
            if (tag == 0x0a) {
                data = reader.readBytes();
            } else {
                reader.skip(tag);
            }
        }
        return data;
    }

    private static String decodeSingleString(byte[] encoded) {
        Reader reader = new Reader(encoded);
        String value = "";
        while (reader.hasMore()) {
            int tag = reader.readTag();
            if (tag == 0x0a) {
                value = reader.readString();
            } else {
                reader.skip(tag);
            }
        }
        return value;
    }

    private static final class Writer {
        private final ByteArrayOutputStream out = new ByteArrayOutputStream();

        void varintField(int field, long value) {
            if (value == 0) {
                return;
            }
            tag(field, 0);
            varint(value);
        }

        void doubleField(int field, double value) {
            long bits = Double.doubleToLongBits(value);
            if (bits == 0) {
                return;
            }
            tag(field, 1);
            for (int shift = 0; shift < 64; shift += 8) {
                out.write((int) (bits >>> shift) & 0xff);
            }
        }

        void stringField(int field, String value) {
            bytesField(field, value.getBytes(StandardCharsets.UTF_8));
        }

        void bytesField(int field, byte[] value) {
            if (value.length == 0) {
                return;
            }
            tag(field, 2);
            varint(value.length);
            out.write(value, 0, value.length);
        }

        /** Message and repeated-element fields keep presence, so they emit even when empty. */
        void messageField(int field, byte[] encoded) {
            tag(field, 2);
            varint(encoded.length);
            out.write(encoded, 0, encoded.length);
        }

        byte[] bytes() {
            return out.toByteArray();
        }

        private void tag(int field, int wireType) {
            varint(((long) field << 3) | wireType);
        }

        private void varint(long value) {
            long rest = value;
            do {
                int next = (int) (rest & 0x7f);
                rest >>>= 7;
                out.write(rest == 0 ? next : next | 0x80);
            } while (rest != 0);
        }
    }

    private static final class Reader {
        private final byte[] data;
        private int position;

        Reader(byte[] data) {
            this.data = data;
        }

        boolean hasMore() {
            return position < data.length;
        }

        int readTag() {
            return (int) readVarint();
        }

        long readVarint() {
            long value = 0;
            for (int shift = 0; shift < 64; shift += 7) {
                if (position >= data.length) {
                    throw malformed("varint runs past the end of the message");
                }
                byte next = data[position++];
                value |= (long) (next & 0x7f) << shift;
                if ((next & 0x80) == 0) {
                    return value;
                }
            }
            throw malformed("varint is longer than 64 bits");
        }

        long readFixed64() {
            if (position + 8 > data.length) {
                throw malformed("fixed64 runs past the end of the message");
            }
            long value = 0;
            for (int shift = 0; shift < 64; shift += 8) {
                value |= (long) (data[position++] & 0xff) << shift;
            }
            return value;
        }

        byte[] readBytes() {
            long length = readVarint();
            if (length < 0 || position + length > data.length) {
                throw malformed("length-delimited field runs past the end of the message");
            }
            byte[] value = Arrays.copyOfRange(data, position, position + (int) length);
            position += (int) length;
            return value;
        }

        String readString() {
            return new String(readBytes(), StandardCharsets.UTF_8);
        }

        void skip(int tag) {
            switch (tag & 7) {
                case 0 -> readVarint();
                case 1 -> readFixed64();
                case 2 -> readBytes();
                case 5 -> {
                    if (position + 4 > data.length) {
                        throw malformed("fixed32 runs past the end of the message");
                    }
                    position += 4;
                }
                default -> throw malformed("unsupported wire type in tag " + tag);
            }
        }

        private static IllegalArgumentException malformed(String reason) {
            return new IllegalArgumentException("malformed wire message: " + reason);
        }
    }
}
