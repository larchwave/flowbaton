package dev.nohavewho.flowbaton.driver.contract;

import static org.junit.Assert.assertEquals;

import java.util.ArrayList;
import java.util.List;
import java.util.StringJoiner;
import org.junit.Test;

public final class AndroidWireContractV0Test {
    @Test
    public void freezesAllTwelveRPCDeclarations() {
        assertEquals("v0", AndroidWireContractV0.CONTRACT_VERSION);
        assertEquals("d0b1b630a6c96c717d3824bc60098c46f00f90c3c3f614bbad0635182eb3a640", AndroidWireContractV0.DESCRIPTOR_SHA256);
        assertEquals(
                List.of(
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
                        rpc("disableLocationUpdates", "EmptyRequest", "EmptyResponse", false)),
                AndroidWireContractV0.rpcs());
    }

    @Test
    public void freezesEveryMessageAndFieldDeclaration() {
        assertEquals(
                List.of(
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
                        message("ViewHierarchyRequest"),
                        message("ViewHierarchyResponse", field("hierarchy", "string", 1, false))),
                AndroidWireContractV0.messages());
    }

    @Test
    public void freezesErrorAndTransportDeclarations() {
        assertEquals(
                new AndroidWireContractV0.ErrorContract(
                        "INTERNAL",
                        List.of("error-type", "error-message", "error-cause"),
                        List.of("UNAVAILABLE", "DEADLINE_EXCEEDED")),
                AndroidWireContractV0.errorContract());
    }

    @Test
    public void semanticManifestMatchesLiveTypedDeclarations() {
        List<String> lines = new ArrayList<>();
        lines.add(semanticLine("descriptor", AndroidWireContractV0.SCHEMA_VERSION, AndroidWireContractV0.CONTRACT_VERSION));

        AndroidWireContractV0.Proto proto = AndroidWireContractV0.proto();
        lines.add(semanticLine("proto", proto.file(), proto.syntax(), proto.packageName(), proto.service()));

        List<AndroidWireContractV0.Rpc> rpcs = AndroidWireContractV0.rpcs();
        for (int index = 0; index < rpcs.size(); index++) {
            AndroidWireContractV0.Rpc rpc = rpcs.get(index);
            lines.add(semanticLine("rpc", index, rpc.name(), rpc.request(), rpc.response(), rpc.clientStreaming(), rpc.serverStreaming()));
        }

        List<AndroidWireContractV0.Message> messages = AndroidWireContractV0.messages();
        for (int messageIndex = 0; messageIndex < messages.size(); messageIndex++) {
            AndroidWireContractV0.Message message = messages.get(messageIndex);
            lines.add(semanticLine("message", messageIndex, message.name()));
            for (int fieldIndex = 0; fieldIndex < message.fields().size(); fieldIndex++) {
                AndroidWireContractV0.Field field = message.fields().get(fieldIndex);
                lines.add(semanticLine("field", message.name(), fieldIndex, field.name(), field.type(), field.number(), field.repeated()));
            }
        }

        AndroidWireContractV0.ErrorContract error = AndroidWireContractV0.errorContract();
        lines.add(semanticLine("error-handler-status", error.handlerStatus()));
        for (int index = 0; index < error.trailers().size(); index++) {
            lines.add(semanticLine("error-trailer", index, error.trailers().get(index)));
        }
        for (int index = 0; index < error.transportStatuses().size(); index++) {
            lines.add(semanticLine("error-transport-status", index, error.transportStatuses().get(index)));
        }

        assertEquals(String.join("\n", lines), AndroidWireContractV0.SEMANTIC_MANIFEST);
    }

    private static AndroidWireContractV0.Rpc rpc(String name, String request, String response, boolean clientStreaming) {
        return new AndroidWireContractV0.Rpc(name, request, response, clientStreaming, false);
    }

    private static AndroidWireContractV0.Field field(String name, String type, int number, boolean repeated) {
        return new AndroidWireContractV0.Field(name, type, number, repeated);
    }

    private static AndroidWireContractV0.Message message(String name, AndroidWireContractV0.Field... fields) {
        return new AndroidWireContractV0.Message(name, List.of(fields));
    }

    private static String semanticLine(Object... values) {
        StringJoiner line = new StringJoiner("|");
        for (Object value : values) {
            line.add(escapeSemanticToken(String.valueOf(value)));
        }
        return line.toString();
    }

    private static String escapeSemanticToken(String value) {
        return value
                .replace("%", "%25")
                .replace("|", "%7C")
                .replace("\r", "%0D")
                .replace("\n", "%0A");
    }
}
