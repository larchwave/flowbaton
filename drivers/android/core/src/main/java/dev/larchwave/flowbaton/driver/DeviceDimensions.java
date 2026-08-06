package dev.larchwave.flowbaton.driver;

/** Pixel dimensions returned by the canonical Android deviceInfo RPC. */
public final class DeviceDimensions {
    private final int widthPixels;
    private final int heightPixels;

    public DeviceDimensions(int widthPixels, int heightPixels) {
        this.widthPixels = widthPixels;
        this.heightPixels = heightPixels;
    }

    public int widthPixels() {
        return widthPixels;
    }

    public int heightPixels() {
        return heightPixels;
    }
}
