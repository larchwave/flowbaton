import XCTest

@testable import FlowBatonIOSRunner

final class WireContractV0Tests: XCTestCase {
  func testFreezesAllEighteenRouteDeclarations() {
    XCTAssertEqual(IOSWireContractV0.contractVersion, "v0")
    XCTAssertEqual(
      IOSWireContractV0.descriptorSHA256,
      "352b5136f165510a741cb2277e09cc5b3c4c540c5d32b3c2429d6bf07a721f3a")
    XCTAssertEqual(IOSWireContractV0.bindHost, "127.0.0.1")
    XCTAssertEqual(IOSWireContractV0.defaultPort, 22_087)
    XCTAssertEqual(
      IOSWireContractV0.routes,
      [
        route("runningApp", "GET", "json_body", "RunningAppRequest", "RunningAppResponse"),
        route("swipe", "POST", "json_body", "SwipeRequest", "EmptyResponse"),
        route("swipeV2", "POST", "json_body", "SwipeV2Request", "EmptyResponse"),
        route("inputText", "POST", "json_body", "InputTextRequest", "EmptyResponse"),
        route("touch", "POST", "json_body", "TouchRequest", "EmptyResponse"),
        route("screenshot", "GET", "query", "ScreenshotQuery", "ScreenshotResponse"),
        route("isScreenStatic", "GET", "none", "NoBody", "ScreenStaticResponse"),
        route("pressKey", "POST", "json_body", "PressKeyRequest", "EmptyResponse"),
        route("pressButton", "POST", "json_body", "PressButtonRequest", "EmptyResponse"),
        route("eraseText", "POST", "json_body", "EraseTextRequest", "EmptyResponse"),
        route("deviceInfo", "GET", "none", "NoBody", "DeviceInfoResponse"),
        route("setOrientation", "POST", "json_body", "SetOrientationRequest", "EmptyResponse"),
        route("setPermissions", "POST", "json_body", "SetPermissionsRequest", "EmptyResponse"),
        route(
          "viewHierarchy", "POST", "json_body", "ViewHierarchyRequest", "ViewHierarchyResponse"),
        route("status", "GET", "none", "NoBody", "StatusResponse"),
        route("keyboard", "GET", "json_body", "KeyboardRequest", "KeyboardResponse"),
        route("launchApp", "POST", "json_body", "LaunchAppRequest", "EmptyResponse"),
        route("terminateApp", "POST", "json_body", "TerminateAppRequest", "EmptyResponse"),
      ])
  }

  func testFreezesEverySchemaDeclaration() {
    XCTAssertEqual(
      IOSWireContractV0.schemas,
      [
        schema("NoBody", "none"),
        schema("EmptyResponse", "empty"),
        schema("ScreenshotResponse", "binary", contentTypes: ["image/png", "image/jpeg"]),
        schema(
          "RunningAppRequest", "object", required: ["appIds"],
          fields: [field("appIds", "array<string>")]),
        schema(
          "RunningAppResponse", "object", required: ["runningAppBundleId"],
          fields: [field("runningAppBundleId", "string")]),
        schema(
          "SwipeRequest", "object", required: ["startX", "startY", "endX", "endY", "duration"],
          fields: [
            field("appId", "string"), field("startX", "number"), field("startY", "number"),
            field("endX", "number"), field("endY", "number"), field("duration", "number"),
          ]),
        schema(
          "SwipeV2Request", "object", required: ["startX", "startY", "endX", "endY", "duration"],
          fields: [
            field("startX", "number"), field("startY", "number"), field("endX", "number"),
            field("endY", "number"), field("duration", "number"), field("appIds", "array<string>"),
          ]),
        schema(
          "InputTextRequest", "object", required: ["text", "appIds"],
          fields: [field("text", "string"), field("appIds", "array<string>")]),
        schema(
          "TouchRequest", "object", required: ["x", "y"],
          fields: [field("x", "number"), field("y", "number"), field("duration", "number")]),
        schema("ScreenshotQuery", "object", fields: [field("compressed", "boolean")]),
        schema(
          "ScreenStaticResponse", "object", required: ["isScreenStatic"],
          fields: [field("isScreenStatic", "boolean")]),
        schema(
          "PressKeyRequest", "object", required: ["key", "appIds"],
          fields: [
            field("key", "string{delete,return,enter,tab,space,escape}"),
            field("appIds", "array<string>"),
          ]),
        schema(
          "PressButtonRequest", "object", required: ["button"],
          fields: [field("button", "string{home,lock}")]),
        schema(
          "EraseTextRequest", "object", required: ["charactersToErase", "appIds"],
          fields: [
            field("charactersToErase", "integer(minimum=0)"), field("appIds", "array<string>"),
          ]),
        schema(
          "DeviceInfoResponse", "object",
          required: ["widthPoints", "heightPoints", "widthPixels", "heightPixels", "orientation"],
          fields: [
            field("widthPoints", "number"), field("heightPoints", "number"),
            field("widthPixels", "number"), field("heightPixels", "number"),
            field(
              "orientation",
              "string{portrait,portrait-upside-down,landscape-left,landscape-right}"),
          ]),
        schema(
          "SetOrientationRequest", "object", required: ["orientation"],
          fields: [field("orientation", "string{portrait,landscapeLeft,landscapeRight,upsideDown}")]
        ),
        schema(
          "SetPermissionsRequest", "object", required: ["permissions"],
          fields: [field("permissions", "object<additional-properties:string>")]),
        schema(
          "ViewHierarchyRequest", "object", required: ["appIds", "excludeKeyboardElements"],
          fields: [field("appIds", "array<string>"), field("excludeKeyboardElements", "boolean")]),
        schema(
          "Frame", "object", required: ["X", "Y", "Width", "Height"],
          fields: [
            field("X", "number"), field("Y", "number"), field("Width", "number"),
            field("Height", "number"),
          ]),
        schema(
          "AXElement", "object",
          required: [
            "identifier", "frame", "label", "elementType", "enabled", "horizontalSizeClass",
            "verticalSizeClass", "selected", "hasFocus", "windowContextID", "displayID",
          ],
          fields: [
            field("identifier", "string"), field("frame", "ref:Frame"), field("value", "string"),
            field("title", "string"), field("label", "string"), field("elementType", "integer"),
            field("enabled", "boolean"), field("horizontalSizeClass", "integer"),
            field("verticalSizeClass", "integer"), field("placeholderValue", "string"),
            field("selected", "boolean"), field("hasFocus", "boolean"),
            field("children", "array<ref:AXElement>"), field("windowContextID", "number"),
            field("displayID", "integer"),
          ]),
        schema(
          "ViewHierarchyResponse", "object", required: ["axElement", "depth"],
          fields: [field("axElement", "ref:AXElement"), field("depth", "integer")]),
        schema(
          "StatusResponse", "object", required: ["status"],
          fields: [field("runner", "string"), field("status", "string{ok}")]),
        schema(
          "KeyboardRequest", "object", required: ["appIds"],
          fields: [field("appIds", "array<string>")]),
        schema(
          "KeyboardResponse", "object", required: ["isKeyboardVisible"],
          fields: [field("isKeyboardVisible", "boolean")]),
        schema(
          "LaunchAppRequest", "object", required: ["bundleId"],
          fields: [field("bundleId", "string")]),
        schema(
          "TerminateAppRequest", "object", required: ["appId"], fields: [field("appId", "string")]),
        schema(
          "ErrorResponse", "object", required: ["code", "errorMessage"],
          fields: [
            field("code", "string{internal,precondition,timeout}"), field("errorMessage", "string"),
          ]),
      ])
  }

  func testFreezesErrorAndTimeoutDeclarations() {
    XCTAssertEqual(
      IOSWireContractV0.errorMappings,
      [
        IOSHTTPErrorMappingV0(httpStatus: 400, code: "precondition"),
        IOSHTTPErrorMappingV0(httpStatus: 408, code: "timeout"),
        IOSHTTPErrorMappingV0(httpStatus: 500, code: "internal"),
      ])
    XCTAssertEqual(
      IOSWireContractV0.timeoutSignatures,
      [
        IOSTimeoutSignatureV0(
          domain: "com.apple.dt.XCTest.XCTFuture", code: 1_000, retryable: false),
        IOSTimeoutSignatureV0(
          domain: "com.apple.dt.xctest.automation-support.error", code: 6, retryable: false),
      ])
  }

  func testSemanticManifestMatchesLiveTypedDeclarations() {
    var lines = [
      semanticLine(
        "descriptor", IOSWireContractV0.schemaVersion, IOSWireContractV0.contractVersion),
      semanticLine(
        "transport", IOSWireContractV0.scheme, IOSWireContractV0.bindHost,
        IOSWireContractV0.defaultPort),
    ]

    for (index, route) in IOSWireContractV0.routes.enumerated() {
      lines.append(
        semanticLine(
          "route", index, route.name, route.method, route.path, route.requestLocation,
          route.requestSchema, route.responseSchema, route.successStatus, route.errorSchema))
      for (statusIndex, status) in route.errorStatuses.enumerated() {
        lines.append(semanticLine("route-error-status", route.name, statusIndex, status))
      }
    }

    for schema in IOSWireContractV0.schemas.sorted(by: { $0.name < $1.name }) {
      lines.append(semanticLine("schema", schema.name, schema.kind))
      for (index, required) in schema.required.enumerated() {
        lines.append(semanticLine("schema-required", schema.name, index, required))
      }
      for field in schema.fields.sorted(by: { $0.name < $1.name }) {
        lines.append(semanticLine("schema-field", schema.name, field.name, field.descriptor))
      }
      for (index, contentType) in schema.contentTypes.enumerated() {
        lines.append(semanticLine("schema-content-type", schema.name, index, contentType))
      }
    }

    lines.append(semanticLine("error-schema", IOSWireContractV0.errorSchema))
    for (index, mapping) in IOSWireContractV0.errorMappings.enumerated() {
      lines.append(semanticLine("error-mapping", index, mapping.httpStatus, mapping.code))
    }
    for (index, signature) in IOSWireContractV0.timeoutSignatures.enumerated() {
      lines.append(
        semanticLine(
          "timeout-signature", index, signature.domain, signature.code, signature.retryable))
    }

    XCTAssertEqual(IOSWireContractV0.semanticManifest, lines.joined(separator: "\n"))
  }

  private func route(
    _ name: String, _ method: String, _ requestLocation: String, _ requestSchema: String,
    _ responseSchema: String
  ) -> IOSRouteV0 {
    IOSRouteV0(
      name: name, method: method, path: "/\(name)", requestLocation: requestLocation,
      requestSchema: requestSchema, responseSchema: responseSchema, successStatus: 200,
      errorSchema: "ErrorResponse", errorStatuses: [400, 408, 500])
  }

  private func field(_ name: String, _ descriptor: String) -> IOSSchemaFieldV0 {
    IOSSchemaFieldV0(name: name, descriptor: descriptor)
  }

  private func schema(
    _ name: String, _ kind: String, required: [String] = [], fields: [IOSSchemaFieldV0] = [],
    contentTypes: [String] = []
  ) -> IOSSchemaV0 {
    IOSSchemaV0(
      name: name, kind: kind, required: required, fields: fields, contentTypes: contentTypes)
  }

  private func semanticLine(_ values: Any...) -> String {
    values.map { escapeSemanticToken(String(describing: $0)) }.joined(separator: "|")
  }

  private func escapeSemanticToken(_ value: String) -> String {
    value
      .replacingOccurrences(of: "%", with: "%25")
      .replacingOccurrences(of: "|", with: "%7C")
      .replacingOccurrences(of: "\r", with: "%0D")
      .replacingOccurrences(of: "\n", with: "%0A")
  }
}
