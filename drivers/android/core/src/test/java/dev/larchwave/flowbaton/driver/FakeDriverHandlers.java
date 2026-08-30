package dev.larchwave.flowbaton.driver;

import java.io.IOException;
import java.io.InputStream;
import java.util.List;

/** Recording fake for server contract tests; set {@link #failure} to make every handler throw. */
final class FakeDriverHandlers implements FlowBatonDriverHandlers {
    DeviceDimensions dimensions = new DeviceDimensions(1080, 1920);
    String hierarchy = "<hierarchy rotation=\"0\" />";
    byte[] png = new byte[] {(byte) 0x89, 'P', 'N', 'G'};
    boolean windowUpdating = true;
    RuntimeException failure;

    Integer tappedX;
    Integer tappedY;
    String typedText;
    Integer erasedCharacters;
    Double latitude;
    Double longitude;
    String windowAppId;
    String launchedPackage;
    List<LaunchArgument> launchedArguments;
    String mediaName;
    String mediaExt;
    byte[] mediaData;
    int enableMockLocationCalls;
    int disableLocationUpdateCalls;

    @Override
    public DeviceDimensions deviceInfo() {
        failIfArmed();
        return dimensions;
    }

    /** What the last viewHierarchy call was asked to leave out. */
    public Boolean excludedKeyboard;

    @Override
    public String viewHierarchy(boolean excludeKeyboardElements) {
        failIfArmed();
        excludedKeyboard = excludeKeyboardElements;
        return hierarchy;
    }

    @Override
    public byte[] screenshot() {
        failIfArmed();
        return png;
    }

    @Override
    public void tap(int x, int y) {
        failIfArmed();
        tappedX = x;
        tappedY = y;
    }

    @Override
    public void inputText(String text) {
        failIfArmed();
        typedText = text;
    }

    @Override
    public void eraseAllText(int charactersToErase) {
        failIfArmed();
        erasedCharacters = charactersToErase;
    }

    @Override
    public void setLocation(double latitude, double longitude) {
        failIfArmed();
        this.latitude = latitude;
        this.longitude = longitude;
    }

    @Override
    public boolean isWindowUpdating(String appId) {
        failIfArmed();
        windowAppId = appId;
        return windowUpdating;
    }

    @Override
    public void launchApp(String packageName, List<LaunchArgument> arguments) {
        failIfArmed();
        launchedPackage = packageName;
        launchedArguments = arguments;
    }

    @Override
    public void addMedia(String mediaName, String mediaExt, InputStream data) throws IOException {
        failIfArmed();
        this.mediaName = mediaName;
        this.mediaExt = mediaExt;
        this.mediaData = data.readAllBytes();
    }

    @Override
    public void enableMockLocationProviders() {
        failIfArmed();
        enableMockLocationCalls++;
    }

    @Override
    public void disableLocationUpdates() {
        failIfArmed();
        disableLocationUpdateCalls++;
    }

    private void failIfArmed() {
        if (failure != null) {
            throw failure;
        }
    }
}
