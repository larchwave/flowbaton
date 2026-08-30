package dev.larchwave.flowbaton.driver.contract;

import java.util.List;

/**
 * Complete declaration-only JVM mirror of the FlowBaton v0 Android gRPC wire contract.
 *
 * <p>This type does not claim that {@code GrpcDeviceServer} implements these RPCs. Runtime
 * coverage remains tracked separately and is currently limited to the G001 {@code deviceInfo}
 * feasibility endpoint.</p>
 */
public final class AndroidWireContractV0 {
    public static final int SCHEMA_VERSION = 1;
    public static final String CONTRACT_VERSION = "v0";
    public static final String DESCRIPTOR_SHA256 = "dabeade15724b90a5e7e547f9d8933b0538b90b64ae80896d3b573c14e78ac3a";
    public static final String SEMANTIC_MANIFEST = String.join(
            "\n",
            "descriptor|1|v0",
            "proto|proto/flowbaton_android.proto|proto3|flowbaton_android|FlowBatonDriver",
            "rpc|0|deviceInfo|DeviceInfoRequest|DeviceInfo|false|false",
            "rpc|1|viewHierarchy|ViewHierarchyRequest|ViewHierarchyResponse|false|false",
            "rpc|2|screenshot|ScreenshotRequest|ScreenshotResponse|false|false",
            "rpc|3|tap|TapRequest|TapResponse|false|false",
            "rpc|4|inputText|InputTextRequest|InputTextResponse|false|false",
            "rpc|5|eraseAllText|EraseAllTextRequest|EraseAllTextResponse|false|false",
            "rpc|6|setLocation|SetLocationRequest|SetLocationResponse|false|false",
            "rpc|7|isWindowUpdating|CheckWindowUpdatingRequest|CheckWindowUpdatingResponse|false|false",
            "rpc|8|launchApp|LaunchAppRequest|LaunchAppResponse|false|false",
            "rpc|9|addMedia|AddMediaRequest|AddMediaResponse|true|false",
            "rpc|10|enableMockLocationProviders|EmptyRequest|EmptyResponse|false|false",
            "rpc|11|disableLocationUpdates|EmptyRequest|EmptyResponse|false|false",
            "message|0|AddMediaRequest",
            "field|AddMediaRequest|0|payload|Payload|1|false",
            "field|AddMediaRequest|1|media_name|string|2|false",
            "field|AddMediaRequest|2|media_ext|string|3|false",
            "message|1|AddMediaResponse",
            "message|2|ArgumentValue",
            "field|ArgumentValue|0|key|string|1|false",
            "field|ArgumentValue|1|value|string|2|false",
            "field|ArgumentValue|2|type|string|3|false",
            "message|3|CheckWindowUpdatingRequest",
            "field|CheckWindowUpdatingRequest|0|appId|string|1|false",
            "message|4|CheckWindowUpdatingResponse",
            "field|CheckWindowUpdatingResponse|0|isWindowUpdating|bool|1|false",
            "message|5|DeviceInfo",
            "field|DeviceInfo|0|widthPixels|uint32|1|false",
            "field|DeviceInfo|1|heightPixels|uint32|2|false",
            "message|6|DeviceInfoRequest",
            "message|7|EmptyRequest",
            "message|8|EmptyResponse",
            "message|9|EraseAllTextRequest",
            "field|EraseAllTextRequest|0|charactersToErase|uint32|1|false",
            "message|10|EraseAllTextResponse",
            "message|11|InputTextRequest",
            "field|InputTextRequest|0|text|string|1|false",
            "message|12|InputTextResponse",
            "message|13|LaunchAppRequest",
            "field|LaunchAppRequest|0|packageName|string|1|false",
            "field|LaunchAppRequest|1|arguments|ArgumentValue|2|true",
            "message|14|LaunchAppResponse",
            "message|15|Payload",
            "field|Payload|0|data|bytes|1|false",
            "message|16|ScreenshotRequest",
            "message|17|ScreenshotResponse",
            "field|ScreenshotResponse|0|bytes|bytes|1|false",
            "message|18|SetLocationRequest",
            "field|SetLocationRequest|0|latitude|double|1|false",
            "field|SetLocationRequest|1|longitude|double|2|false",
            "message|19|SetLocationResponse",
            "message|20|TapRequest",
            "field|TapRequest|0|x|uint32|1|false",
            "field|TapRequest|1|y|uint32|2|false",
            "message|21|TapResponse",
            "message|22|ViewHierarchyRequest",
            "field|ViewHierarchyRequest|0|excludeKeyboardElements|bool|1|false",
            "message|23|ViewHierarchyResponse",
            "field|ViewHierarchyResponse|0|hierarchy|string|1|false",
            "error-handler-status|INTERNAL",
            "error-trailer|0|error-type",
            "error-trailer|1|error-message",
            "error-trailer|2|error-cause",
            "error-transport-status|0|UNAVAILABLE",
            "error-transport-status|1|DEADLINE_EXCEEDED");

    private AndroidWireContractV0() {}

    public record Proto(String file, String syntax, String packageName, String service) {}

    public record Rpc(
            String name,
            String request,
            String response,
            boolean clientStreaming,
            boolean serverStreaming) {}

    public record Field(String name, String type, int number, boolean repeated) {}

    public record Message(String name, List<Field> fields) {
        public Message {
            fields = List.copyOf(fields);
        }
    }

    public record ErrorContract(
            String handlerStatus,
            List<String> trailers,
            List<String> transportStatuses) {
        public ErrorContract {
            trailers = List.copyOf(trailers);
            transportStatuses = List.copyOf(transportStatuses);
        }
    }

    private static final Proto PROTO = new Proto(
            "proto/flowbaton_android.proto", "proto3", "flowbaton_android", "FlowBatonDriver");

    private static final List<Rpc> RPCS = List.of(
            rpc("deviceInfo", "DeviceInfoRequest", "DeviceInfo", false),
            rpc("viewHierarchy", "ViewHierarchyRequest", "ViewHierarchyResponse", false),
            rpc("screenshot", "ScreenshotRequest", "ScreenshotResponse", false),
            rpc("tap", "TapRequest", "TapResponse", false),
            rpc("inputText", "InputTextRequest", "InputTextResponse", false),
            rpc("eraseAllText", "EraseAllTextRequest", "EraseAllTextResponse", false),
            rpc("setLocation", "SetLocationRequest", "SetLocationResponse", false),
            rpc("isWindowUpdating", "CheckWindowUpdatingRequest", "CheckWindowUpdatingResponse", false),
            rpc("launchApp", "LaunchAppRequest", "LaunchAppResponse", false),
            rpc("addMedia", "AddMediaRequest", "AddMediaResponse", true),
            rpc("enableMockLocationProviders", "EmptyRequest", "EmptyResponse", false),
            rpc("disableLocationUpdates", "EmptyRequest", "EmptyResponse", false));

    private static final List<Message> MESSAGES = List.of(
            message("AddMediaRequest", field("payload", "Payload", 1, false), field("media_name", "string", 2, false), field("media_ext", "string", 3, false)),
            message("AddMediaResponse"),
            message("ArgumentValue", field("key", "string", 1, false), field("value", "string", 2, false), field("type", "string", 3, false)),
            message("CheckWindowUpdatingRequest", field("appId", "string", 1, false)),
            message("CheckWindowUpdatingResponse", field("isWindowUpdating", "bool", 1, false)),
            message("DeviceInfo", field("widthPixels", "uint32", 1, false), field("heightPixels", "uint32", 2, false)),
            message("DeviceInfoRequest"),
            message("EmptyRequest"),
            message("EmptyResponse"),
            message("EraseAllTextRequest", field("charactersToErase", "uint32", 1, false)),
            message("EraseAllTextResponse"),
            message("InputTextRequest", field("text", "string", 1, false)),
            message("InputTextResponse"),
            message("LaunchAppRequest", field("packageName", "string", 1, false), field("arguments", "ArgumentValue", 2, true)),
            message("LaunchAppResponse"),
            message("Payload", field("data", "bytes", 1, false)),
            message("ScreenshotRequest"),
            message("ScreenshotResponse", field("bytes", "bytes", 1, false)),
            message("SetLocationRequest", field("latitude", "double", 1, false), field("longitude", "double", 2, false)),
            message("SetLocationResponse"),
            message("TapRequest", field("x", "uint32", 1, false), field("y", "uint32", 2, false)),
            message("TapResponse"),
            message("ViewHierarchyRequest", field("excludeKeyboardElements", "bool", 1, false)),
            message("ViewHierarchyResponse", field("hierarchy", "string", 1, false)));

    private static final ErrorContract ERROR_CONTRACT = new ErrorContract(
            "INTERNAL",
            List.of("error-type", "error-message", "error-cause"),
            List.of("UNAVAILABLE", "DEADLINE_EXCEEDED"));

    public static Proto proto() {
        return PROTO;
    }

    public static List<Rpc> rpcs() {
        return RPCS;
    }

    public static List<Message> messages() {
        return MESSAGES;
    }

    public static ErrorContract errorContract() {
        return ERROR_CONTRACT;
    }

    private static Rpc rpc(String name, String request, String response, boolean clientStreaming) {
        return new Rpc(name, request, response, clientStreaming, false);
    }

    private static Field field(String name, String type, int number, boolean repeated) {
        return new Field(name, type, number, repeated);
    }

    private static Message message(String name, Field... fields) {
        return new Message(name, List.of(fields));
    }
}
