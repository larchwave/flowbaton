package dev.nohavewho.flowbaton.driver;

import static org.junit.Assert.assertArrayEquals;
import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertThrows;
import static org.junit.Assert.assertTrue;

import java.util.List;
import org.junit.Test;

public final class WireCodecTest {
    @Test
    public void deviceInfoMatchesTheFrozenGoldenBytes() {
        byte[] encoded = WireCodec.encodeDeviceInfo(1080, 1920);
        assertArrayEquals(
                new byte[] {0x08, (byte) 0xb8, 0x08, 0x10, (byte) 0x80, 0x0f}, encoded);

        DeviceDimensions decoded = WireCodec.decodeDeviceInfo(encoded);
        assertEquals(1080, decoded.widthPixels());
        assertEquals(1920, decoded.heightPixels());
    }

    @Test
    public void tapMatchesGoldenBytesAndRoundTrips() {
        byte[] encoded = WireCodec.encodeTap(100, 200);
        assertArrayEquals(new byte[] {0x08, 0x64, 0x10, (byte) 0xc8, 0x01}, encoded);

        WireCodec.Tap decoded = WireCodec.decodeTap(encoded);
        assertEquals(100, decoded.x());
        assertEquals(200, decoded.y());
    }

    @Test
    public void setLocationUsesLittleEndianFixed64Doubles() {
        byte[] encoded = WireCodec.encodeSetLocation(1.5, -2.25);
        assertArrayEquals(
                new byte[] {
                    0x09, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, (byte) 0xf8, 0x3f,
                    0x11, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, (byte) 0xc0,
                },
                encoded);

        WireCodec.Location decoded = WireCodec.decodeSetLocation(encoded);
        assertEquals(1.5, decoded.latitude(), 0.0);
        assertEquals(-2.25, decoded.longitude(), 0.0);
    }

    @Test
    public void eraseAllTextMatchesGoldenBytesAndRoundTrips() {
        byte[] encoded = WireCodec.encodeEraseAllText(300);
        assertArrayEquals(new byte[] {0x08, (byte) 0xac, 0x02}, encoded);
        assertEquals(300, WireCodec.decodeEraseAllText(encoded));
    }

    @Test
    public void isWindowUpdatingEncodesProto3BoolPresence() {
        assertArrayEquals(new byte[] {0x08, 0x01}, WireCodec.encodeIsWindowUpdating(true));
        assertArrayEquals(new byte[0], WireCodec.encodeIsWindowUpdating(false));
        assertTrue(WireCodec.decodeIsWindowUpdating(new byte[] {0x08, 0x01}));
        assertEquals(false, WireCodec.decodeIsWindowUpdating(new byte[0]));
    }

    // The four single-field wire messages below pin the same golden bytes the
    // Go host pins in internal/android/pbwire/golden_test.go. The round-trip
    // tests alone go through this codec on both sides, so a coordinated field
    // renumber would pass them while silently breaking wire compat; the hex
    // literals pin the cross-language wire contract (contracts/v0/android-grpc.json
    // pins every field at number 1).

    @Test
    public void inputTextMatchesGoldenBytesAndRoundTrips() {
        byte[] encoded = WireCodec.encodeInputText("hi");
        assertArrayEquals(new byte[] {0x0a, 0x02, 'h', 'i'}, encoded);
        assertEquals("hi", WireCodec.decodeInputText(encoded));
    }

    @Test
    public void checkWindowUpdatingMatchesGoldenBytesAndRoundTrips() {
        byte[] encoded = WireCodec.encodeCheckWindowUpdating("a");
        assertArrayEquals(new byte[] {0x0a, 0x01, 'a'}, encoded);
        assertEquals("a", WireCodec.decodeCheckWindowUpdating(encoded));
    }

    @Test
    public void viewHierarchyMatchesGoldenBytesAndRoundTrips() {
        byte[] encoded = WireCodec.encodeViewHierarchy("<h>");
        assertArrayEquals(new byte[] {0x0a, 0x03, '<', 'h', '>'}, encoded);
        assertEquals("<h>", WireCodec.decodeViewHierarchy(encoded));
    }

    @Test
    public void screenshotMatchesGoldenBytesAndRoundTrips() {
        byte[] encoded = WireCodec.encodeScreenshot(new byte[] {(byte) 0x89, 0x50});
        assertArrayEquals(new byte[] {0x0a, 0x02, (byte) 0x89, 0x50}, encoded);
        assertArrayEquals(new byte[] {(byte) 0x89, 0x50}, WireCodec.decodeScreenshot(encoded));
    }

    @Test
    public void stringsCarryUtf8BothWays() {
        String text = "héllo\n👍";
        assertEquals(text, WireCodec.decodeInputText(WireCodec.encodeInputText(text)));
        assertEquals(
                "com.example.app",
                WireCodec.decodeCheckWindowUpdating(
                        WireCodec.encodeCheckWindowUpdating("com.example.app")));
        assertEquals(
                "<hierarchy rotation=\"0\" />",
                WireCodec.decodeViewHierarchy(
                        WireCodec.encodeViewHierarchy("<hierarchy rotation=\"0\" />")));
    }

    @Test
    public void screenshotBytesRoundTrip() {
        byte[] png = new byte[] {(byte) 0x89, 'P', 'N', 'G', 0x00, 0x7f};
        assertArrayEquals(png, WireCodec.decodeScreenshot(WireCodec.encodeScreenshot(png)));
    }

    @Test
    public void launchAppWithoutArgumentsMatchesGoldenBytes() {
        byte[] encoded = WireCodec.encodeLaunchApp("a", List.of());
        assertArrayEquals(new byte[] {0x0a, 0x01, 0x61}, encoded);

        WireCodec.LaunchApp decoded = WireCodec.decodeLaunchApp(encoded);
        assertEquals("a", decoded.packageName());
        assertEquals(List.of(), decoded.arguments());
    }

    @Test
    public void launchAppCarriesTypedArgumentsInOrder() {
        List<LaunchArgument> arguments =
                List.of(
                        new LaunchArgument("flag", "true", "java.lang.Boolean"),
                        new LaunchArgument("name", "flow", "java.lang.String"));
        WireCodec.LaunchApp decoded =
                WireCodec.decodeLaunchApp(WireCodec.encodeLaunchApp("com.example", arguments));
        assertEquals("com.example", decoded.packageName());
        assertEquals(arguments, decoded.arguments());
    }

    @Test
    public void addMediaChunkMatchesGoldenBytesAndRoundTrips() {
        byte[] encoded = WireCodec.encodeAddMediaChunk(new byte[] {1, 2, 3}, "photo", "png");
        assertArrayEquals(
                new byte[] {
                    0x0a, 0x05, 0x0a, 0x03, 0x01, 0x02, 0x03,
                    0x12, 0x05, 'p', 'h', 'o', 't', 'o',
                    0x1a, 0x03, 'p', 'n', 'g',
                },
                encoded);

        WireCodec.MediaChunk decoded = WireCodec.decodeAddMediaChunk(encoded);
        assertArrayEquals(new byte[] {1, 2, 3}, decoded.payload());
        assertEquals("photo", decoded.mediaName());
        assertEquals("png", decoded.mediaExt());
    }

    @Test
    public void emptyMessagesDecodeToProto3Defaults() {
        assertEquals("", WireCodec.decodeInputText(new byte[0]));
        assertEquals(0, WireCodec.decodeEraseAllText(new byte[0]));
        WireCodec.Tap tap = WireCodec.decodeTap(new byte[0]);
        assertEquals(0, tap.x());
        assertEquals(0, tap.y());
        WireCodec.LaunchApp launch = WireCodec.decodeLaunchApp(new byte[0]);
        assertEquals("", launch.packageName());
        assertEquals(List.of(), launch.arguments());
        WireCodec.MediaChunk chunk = WireCodec.decodeAddMediaChunk(new byte[0]);
        assertArrayEquals(new byte[0], chunk.payload());
        assertEquals("", chunk.mediaName());
        assertEquals("", chunk.mediaExt());
    }

    @Test
    public void unknownFieldsAreSkippedNotFatal() {
        // field 15 varint, then field 14 length-delimited, then the real tap fields.
        byte[] encoded =
                new byte[] {
                    0x78, 0x01,
                    0x72, 0x02, 0x61, 0x62,
                    0x08, 0x64, 0x10, (byte) 0xc8, 0x01,
                };
        WireCodec.Tap decoded = WireCodec.decodeTap(encoded);
        assertEquals(100, decoded.x());
        assertEquals(200, decoded.y());
    }

    @Test
    public void truncatedInputIsRefused() {
        assertThrows(
                IllegalArgumentException.class, () -> WireCodec.decodeTap(new byte[] {0x08}));
        assertThrows(
                IllegalArgumentException.class,
                () -> WireCodec.decodeInputText(new byte[] {0x0a, 0x05, 0x61}));
    }
}
