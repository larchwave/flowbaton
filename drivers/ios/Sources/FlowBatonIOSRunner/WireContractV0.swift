/// Complete declaration-only Swift mirror of the FlowBaton v0 iOS HTTP wire contract.
///
/// These values describe the frozen routes and schemas. They do not claim that the G001
/// loopback server implements them; runtime coverage remains separately limited to `/status`.
public struct IOSRouteV0: Equatable, Sendable {
  public let name: String
  public let method: String
  public let path: String
  public let requestLocation: String
  public let requestSchema: String
  public let responseSchema: String
  public let successStatus: Int
  public let errorSchema: String
  public let errorStatuses: [Int]

  public init(
    name: String, method: String, path: String, requestLocation: String, requestSchema: String,
    responseSchema: String, successStatus: Int, errorSchema: String, errorStatuses: [Int]
  ) {
    self.name = name
    self.method = method
    self.path = path
    self.requestLocation = requestLocation
    self.requestSchema = requestSchema
    self.responseSchema = responseSchema
    self.successStatus = successStatus
    self.errorSchema = errorSchema
    self.errorStatuses = errorStatuses
  }
}

public struct IOSSchemaFieldV0: Equatable, Sendable {
  public let name: String
  public let descriptor: String

  public init(name: String, descriptor: String) {
    self.name = name
    self.descriptor = descriptor
  }
}

public struct IOSSchemaV0: Equatable, Sendable {
  public let name: String
  public let kind: String
  public let required: [String]
  public let fields: [IOSSchemaFieldV0]
  public let contentTypes: [String]

  public init(
    name: String, kind: String, required: [String] = [], fields: [IOSSchemaFieldV0] = [],
    contentTypes: [String] = []
  ) {
    self.name = name
    self.kind = kind
    self.required = required
    self.fields = fields
    self.contentTypes = contentTypes
  }
}

public struct IOSHTTPErrorMappingV0: Equatable, Sendable {
  public let httpStatus: Int
  public let code: String

  public init(httpStatus: Int, code: String) {
    self.httpStatus = httpStatus
    self.code = code
  }
}

public struct IOSTimeoutSignatureV0: Equatable, Sendable {
  public let domain: String
  public let code: Int
  public let retryable: Bool

  public init(domain: String, code: Int, retryable: Bool) {
    self.domain = domain
    self.code = code
    self.retryable = retryable
  }
}

public enum IOSWireContractV0 {
  public static let schemaVersion = 1
  public static let contractVersion = "v0"
  public static let descriptorSHA256 =
    "bfe573e32bcaf07392bb5b6766c795f5df0b44615066d832400a858662bc604e"
  public static let semanticManifest = [
    "descriptor|1|v0",
    "transport|http|127.0.0.1|22087",
    "route|0|runningApp|GET|/runningApp|json_body|RunningAppRequest|RunningAppResponse|200|ErrorResponse",
    "route-error-status|runningApp|0|400",
    "route-error-status|runningApp|1|408",
    "route-error-status|runningApp|2|500",
    "route|1|swipe|POST|/swipe|json_body|SwipeRequest|EmptyResponse|200|ErrorResponse",
    "route-error-status|swipe|0|400",
    "route-error-status|swipe|1|408",
    "route-error-status|swipe|2|500",
    "route|2|swipeV2|POST|/swipeV2|json_body|SwipeV2Request|EmptyResponse|200|ErrorResponse",
    "route-error-status|swipeV2|0|400",
    "route-error-status|swipeV2|1|408",
    "route-error-status|swipeV2|2|500",
    "route|3|inputText|POST|/inputText|json_body|InputTextRequest|EmptyResponse|200|ErrorResponse",
    "route-error-status|inputText|0|400",
    "route-error-status|inputText|1|408",
    "route-error-status|inputText|2|500",
    "route|4|touch|POST|/touch|json_body|TouchRequest|EmptyResponse|200|ErrorResponse",
    "route-error-status|touch|0|400",
    "route-error-status|touch|1|408",
    "route-error-status|touch|2|500",
    "route|5|screenshot|GET|/screenshot|query|ScreenshotQuery|ScreenshotResponse|200|ErrorResponse",
    "route-error-status|screenshot|0|400",
    "route-error-status|screenshot|1|408",
    "route-error-status|screenshot|2|500",
    "route|6|isScreenStatic|GET|/isScreenStatic|none|NoBody|ScreenStaticResponse|200|ErrorResponse",
    "route-error-status|isScreenStatic|0|400",
    "route-error-status|isScreenStatic|1|408",
    "route-error-status|isScreenStatic|2|500",
    "route|7|pressKey|POST|/pressKey|json_body|PressKeyRequest|EmptyResponse|200|ErrorResponse",
    "route-error-status|pressKey|0|400",
    "route-error-status|pressKey|1|408",
    "route-error-status|pressKey|2|500",
    "route|8|pressButton|POST|/pressButton|json_body|PressButtonRequest|EmptyResponse|200|ErrorResponse",
    "route-error-status|pressButton|0|400",
    "route-error-status|pressButton|1|408",
    "route-error-status|pressButton|2|500",
    "route|9|eraseText|POST|/eraseText|json_body|EraseTextRequest|EmptyResponse|200|ErrorResponse",
    "route-error-status|eraseText|0|400",
    "route-error-status|eraseText|1|408",
    "route-error-status|eraseText|2|500",
    "route|10|deviceInfo|GET|/deviceInfo|none|NoBody|DeviceInfoResponse|200|ErrorResponse",
    "route-error-status|deviceInfo|0|400",
    "route-error-status|deviceInfo|1|408",
    "route-error-status|deviceInfo|2|500",
    "route|11|setOrientation|POST|/setOrientation|json_body|SetOrientationRequest|EmptyResponse|200|ErrorResponse",
    "route-error-status|setOrientation|0|400",
    "route-error-status|setOrientation|1|408",
    "route-error-status|setOrientation|2|500",
    "route|12|setPermissions|POST|/setPermissions|json_body|SetPermissionsRequest|EmptyResponse|200|ErrorResponse",
    "route-error-status|setPermissions|0|400",
    "route-error-status|setPermissions|1|408",
    "route-error-status|setPermissions|2|500",
    "route|13|viewHierarchy|POST|/viewHierarchy|json_body|ViewHierarchyRequest|ViewHierarchyResponse|200|ErrorResponse",
    "route-error-status|viewHierarchy|0|400",
    "route-error-status|viewHierarchy|1|408",
    "route-error-status|viewHierarchy|2|500",
    "route|14|status|GET|/status|none|NoBody|StatusResponse|200|ErrorResponse",
    "route-error-status|status|0|400",
    "route-error-status|status|1|408",
    "route-error-status|status|2|500",
    "route|15|keyboard|GET|/keyboard|json_body|KeyboardRequest|KeyboardResponse|200|ErrorResponse",
    "route-error-status|keyboard|0|400",
    "route-error-status|keyboard|1|408",
    "route-error-status|keyboard|2|500",
    "route|16|launchApp|POST|/launchApp|json_body|LaunchAppRequest|EmptyResponse|200|ErrorResponse",
    "route-error-status|launchApp|0|400",
    "route-error-status|launchApp|1|408",
    "route-error-status|launchApp|2|500",
    "route|17|terminateApp|POST|/terminateApp|json_body|TerminateAppRequest|EmptyResponse|200|ErrorResponse",
    "route-error-status|terminateApp|0|400",
    "route-error-status|terminateApp|1|408",
    "route-error-status|terminateApp|2|500",
    "schema|AXElement|object",
    "schema-required|AXElement|0|identifier",
    "schema-required|AXElement|1|frame",
    "schema-required|AXElement|2|label",
    "schema-required|AXElement|3|elementType",
    "schema-required|AXElement|4|enabled",
    "schema-required|AXElement|5|horizontalSizeClass",
    "schema-required|AXElement|6|verticalSizeClass",
    "schema-required|AXElement|7|selected",
    "schema-required|AXElement|8|hasFocus",
    "schema-required|AXElement|9|windowContextID",
    "schema-required|AXElement|10|displayID",
    "schema-field|AXElement|children|array<ref:AXElement>",
    "schema-field|AXElement|displayID|integer",
    "schema-field|AXElement|elementType|integer",
    "schema-field|AXElement|enabled|boolean",
    "schema-field|AXElement|frame|ref:Frame",
    "schema-field|AXElement|hasFocus|boolean",
    "schema-field|AXElement|horizontalSizeClass|integer",
    "schema-field|AXElement|identifier|string",
    "schema-field|AXElement|label|string",
    "schema-field|AXElement|placeholderValue|string",
    "schema-field|AXElement|selected|boolean",
    "schema-field|AXElement|title|string",
    "schema-field|AXElement|value|string",
    "schema-field|AXElement|verticalSizeClass|integer",
    "schema-field|AXElement|windowContextID|number",
    "schema|DeviceInfoResponse|object",
    "schema-required|DeviceInfoResponse|0|widthPoints",
    "schema-required|DeviceInfoResponse|1|heightPoints",
    "schema-required|DeviceInfoResponse|2|widthPixels",
    "schema-required|DeviceInfoResponse|3|heightPixels",
    "schema-field|DeviceInfoResponse|heightPixels|number",
    "schema-field|DeviceInfoResponse|heightPoints|number",
    "schema-field|DeviceInfoResponse|widthPixels|number",
    "schema-field|DeviceInfoResponse|widthPoints|number",
    "schema|EmptyResponse|empty",
    "schema|EraseTextRequest|object",
    "schema-required|EraseTextRequest|0|charactersToErase",
    "schema-required|EraseTextRequest|1|appIds",
    "schema-field|EraseTextRequest|appIds|array<string>",
    "schema-field|EraseTextRequest|charactersToErase|integer(minimum=0)",
    "schema|ErrorResponse|object",
    "schema-required|ErrorResponse|0|code",
    "schema-required|ErrorResponse|1|errorMessage",
    "schema-field|ErrorResponse|code|string{internal,precondition,timeout}",
    "schema-field|ErrorResponse|errorMessage|string",
    "schema|Frame|object",
    "schema-required|Frame|0|X",
    "schema-required|Frame|1|Y",
    "schema-required|Frame|2|Width",
    "schema-required|Frame|3|Height",
    "schema-field|Frame|Height|number",
    "schema-field|Frame|Width|number",
    "schema-field|Frame|X|number",
    "schema-field|Frame|Y|number",
    "schema|InputTextRequest|object",
    "schema-required|InputTextRequest|0|text",
    "schema-required|InputTextRequest|1|appIds",
    "schema-field|InputTextRequest|appIds|array<string>",
    "schema-field|InputTextRequest|text|string",
    "schema|KeyboardRequest|object",
    "schema-required|KeyboardRequest|0|appIds",
    "schema-field|KeyboardRequest|appIds|array<string>",
    "schema|KeyboardResponse|object",
    "schema-required|KeyboardResponse|0|isKeyboardVisible",
    "schema-field|KeyboardResponse|isKeyboardVisible|boolean",
    "schema|LaunchAppRequest|object",
    "schema-required|LaunchAppRequest|0|bundleId",
    "schema-field|LaunchAppRequest|bundleId|string",
    "schema|NoBody|none",
    "schema|PressButtonRequest|object",
    "schema-required|PressButtonRequest|0|button",
    "schema-field|PressButtonRequest|button|string{home,lock}",
    "schema|PressKeyRequest|object",
    "schema-required|PressKeyRequest|0|key",
    "schema-field|PressKeyRequest|key|string{delete,return,enter,tab,space,escape}",
    "schema|RunningAppRequest|object",
    "schema-required|RunningAppRequest|0|appIds",
    "schema-field|RunningAppRequest|appIds|array<string>",
    "schema|RunningAppResponse|object",
    "schema-required|RunningAppResponse|0|runningAppBundleId",
    "schema-field|RunningAppResponse|runningAppBundleId|string",
    "schema|ScreenStaticResponse|object",
    "schema-required|ScreenStaticResponse|0|isScreenStatic",
    "schema-field|ScreenStaticResponse|isScreenStatic|boolean",
    "schema|ScreenshotQuery|object",
    "schema-field|ScreenshotQuery|compressed|boolean",
    "schema|ScreenshotResponse|binary",
    "schema-content-type|ScreenshotResponse|0|image/png",
    "schema-content-type|ScreenshotResponse|1|image/jpeg",
    "schema|SetOrientationRequest|object",
    "schema-required|SetOrientationRequest|0|orientation",
    "schema-field|SetOrientationRequest|orientation|string{portrait,landscapeLeft,landscapeRight,upsideDown}",
    "schema|SetPermissionsRequest|object",
    "schema-required|SetPermissionsRequest|0|permissions",
    "schema-field|SetPermissionsRequest|permissions|object<additional-properties:string>",
    "schema|StatusResponse|object",
    "schema-required|StatusResponse|0|status",
    "schema-field|StatusResponse|status|string{ok}",
    "schema|SwipeRequest|object",
    "schema-required|SwipeRequest|0|startX",
    "schema-required|SwipeRequest|1|startY",
    "schema-required|SwipeRequest|2|endX",
    "schema-required|SwipeRequest|3|endY",
    "schema-required|SwipeRequest|4|duration",
    "schema-field|SwipeRequest|appId|string",
    "schema-field|SwipeRequest|duration|number",
    "schema-field|SwipeRequest|endX|number",
    "schema-field|SwipeRequest|endY|number",
    "schema-field|SwipeRequest|startX|number",
    "schema-field|SwipeRequest|startY|number",
    "schema|SwipeV2Request|object",
    "schema-required|SwipeV2Request|0|startX",
    "schema-required|SwipeV2Request|1|startY",
    "schema-required|SwipeV2Request|2|endX",
    "schema-required|SwipeV2Request|3|endY",
    "schema-required|SwipeV2Request|4|duration",
    "schema-field|SwipeV2Request|appIds|array<string>",
    "schema-field|SwipeV2Request|duration|number",
    "schema-field|SwipeV2Request|endX|number",
    "schema-field|SwipeV2Request|endY|number",
    "schema-field|SwipeV2Request|startX|number",
    "schema-field|SwipeV2Request|startY|number",
    "schema|TerminateAppRequest|object",
    "schema-required|TerminateAppRequest|0|appId",
    "schema-field|TerminateAppRequest|appId|string",
    "schema|TouchRequest|object",
    "schema-required|TouchRequest|0|x",
    "schema-required|TouchRequest|1|y",
    "schema-field|TouchRequest|duration|number",
    "schema-field|TouchRequest|x|number",
    "schema-field|TouchRequest|y|number",
    "schema|ViewHierarchyRequest|object",
    "schema-required|ViewHierarchyRequest|0|appIds",
    "schema-required|ViewHierarchyRequest|1|excludeKeyboardElements",
    "schema-field|ViewHierarchyRequest|appIds|array<string>",
    "schema-field|ViewHierarchyRequest|excludeKeyboardElements|boolean",
    "schema|ViewHierarchyResponse|object",
    "schema-required|ViewHierarchyResponse|0|axElement",
    "schema-required|ViewHierarchyResponse|1|depth",
    "schema-field|ViewHierarchyResponse|axElement|ref:AXElement",
    "schema-field|ViewHierarchyResponse|depth|integer",
    "error-schema|ErrorResponse",
    "error-mapping|0|400|precondition",
    "error-mapping|1|408|timeout",
    "error-mapping|2|500|internal",
    "timeout-signature|0|com.apple.dt.XCTest.XCTFuture|1000|false",
    "timeout-signature|1|com.apple.dt.xctest.automation-support.error|6|false",
  ].joined(separator: "\n")
  public static let scheme = "http"
  public static let bindHost = "127.0.0.1"
  public static let defaultPort = 22_087

  public static let routes: [IOSRouteV0] = [
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
    route("viewHierarchy", "POST", "json_body", "ViewHierarchyRequest", "ViewHierarchyResponse"),
    route("status", "GET", "none", "NoBody", "StatusResponse"),
    route("keyboard", "GET", "json_body", "KeyboardRequest", "KeyboardResponse"),
    route("launchApp", "POST", "json_body", "LaunchAppRequest", "EmptyResponse"),
    route("terminateApp", "POST", "json_body", "TerminateAppRequest", "EmptyResponse"),
  ]

  public static let schemas: [IOSSchemaV0] = [
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
      "PressKeyRequest", "object", required: ["key"],
      fields: [field("key", "string{delete,return,enter,tab,space,escape}")]),
    schema(
      "PressButtonRequest", "object", required: ["button"],
      fields: [field("button", "string{home,lock}")]),
    schema(
      "EraseTextRequest", "object", required: ["charactersToErase", "appIds"],
      fields: [field("charactersToErase", "integer(minimum=0)"), field("appIds", "array<string>")]),
    schema(
      "DeviceInfoResponse", "object",
      required: ["widthPoints", "heightPoints", "widthPixels", "heightPixels"],
      fields: [
        field("widthPoints", "number"), field("heightPoints", "number"),
        field("widthPixels", "number"), field("heightPixels", "number"),
      ]),
    schema(
      "SetOrientationRequest", "object", required: ["orientation"],
      fields: [field("orientation", "string{portrait,landscapeLeft,landscapeRight,upsideDown}")]),
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
      "StatusResponse", "object", required: ["status"], fields: [field("status", "string{ok}")]),
    schema(
      "KeyboardRequest", "object", required: ["appIds"], fields: [field("appIds", "array<string>")]),
    schema(
      "KeyboardResponse", "object", required: ["isKeyboardVisible"],
      fields: [field("isKeyboardVisible", "boolean")]),
    schema(
      "LaunchAppRequest", "object", required: ["bundleId"], fields: [field("bundleId", "string")]),
    schema(
      "TerminateAppRequest", "object", required: ["appId"], fields: [field("appId", "string")]),
    schema(
      "ErrorResponse", "object", required: ["code", "errorMessage"],
      fields: [
        field("code", "string{internal,precondition,timeout}"), field("errorMessage", "string"),
      ]),
  ]

  public static let errorSchema = "ErrorResponse"

  public static let errorMappings: [IOSHTTPErrorMappingV0] = [
    IOSHTTPErrorMappingV0(httpStatus: 400, code: "precondition"),
    IOSHTTPErrorMappingV0(httpStatus: 408, code: "timeout"),
    IOSHTTPErrorMappingV0(httpStatus: 500, code: "internal"),
  ]

  public static let timeoutSignatures: [IOSTimeoutSignatureV0] = [
    IOSTimeoutSignatureV0(domain: "com.apple.dt.XCTest.XCTFuture", code: 1_000, retryable: false),
    IOSTimeoutSignatureV0(
      domain: "com.apple.dt.xctest.automation-support.error", code: 6, retryable: false),
  ]

  private static func route(
    _ name: String, _ method: String, _ requestLocation: String, _ requestSchema: String,
    _ responseSchema: String
  ) -> IOSRouteV0 {
    IOSRouteV0(
      name: name, method: method, path: "/\(name)", requestLocation: requestLocation,
      requestSchema: requestSchema, responseSchema: responseSchema, successStatus: 200,
      errorSchema: "ErrorResponse", errorStatuses: [400, 408, 500])
  }

  private static func field(_ name: String, _ descriptor: String) -> IOSSchemaFieldV0 {
    IOSSchemaFieldV0(name: name, descriptor: descriptor)
  }

  private static func schema(
    _ name: String, _ kind: String, required: [String] = [], fields: [IOSSchemaFieldV0] = [],
    contentTypes: [String] = []
  ) -> IOSSchemaV0 {
    IOSSchemaV0(
      name: name, kind: kind, required: required, fields: fields, contentTypes: contentTypes)
  }
}
