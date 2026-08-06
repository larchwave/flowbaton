package dev.larchwave.flowbaton.driver;

import java.util.List;

/**
 * The twelve device-side operations behind the frozen {@code flowbaton_android.FlowBatonDriver}
 * service. {@link GrpcDeviceServer} owns the wire; implementations own the device. Any throwable
 * escaping a handler is reported to the client per the spec 04 §1 error contract.
 */
public interface FlowBatonDriverHandlers {
    DeviceDimensions deviceInfo() throws Exception;

    String viewHierarchy() throws Exception;

    byte[] screenshot() throws Exception;

    void tap(int x, int y) throws Exception;

    void inputText(String text) throws Exception;

    void eraseAllText(int charactersToErase) throws Exception;

    void setLocation(double latitude, double longitude) throws Exception;

    boolean isWindowUpdating(String appId) throws Exception;

    void launchApp(String packageName, List<LaunchArgument> arguments) throws Exception;

    void addMedia(String mediaName, String mediaExt, byte[] data) throws Exception;

    void enableMockLocationProviders() throws Exception;

    void disableLocationUpdates() throws Exception;
}
