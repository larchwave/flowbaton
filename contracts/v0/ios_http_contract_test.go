package v0

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/nohavewho/flowbaton/internal/device"
)

type iosHTTPDocument struct {
	SchemaVersion    int                     `json:"schema_version"`
	ContractVersion  string                  `json:"contract_version"`
	DeclarationBasis iosHTTPDeclarationBasis `json:"declaration_basis"`
	Transport        iosHTTPTransport        `json:"transport"`
	Routes           []device.IOSRouteV0     `json:"routes"`
	Schemas          map[string]wireSchema   `json:"schemas"`
	ErrorContract    iosErrorContract        `json:"error_contract"`
}

type iosHTTPDeclarationBasis struct {
	ContractKind     string                `json:"contract_kind"`
	Specifications   []string              `json:"specifications"`
	ProductDecisions []iosHTTPMethodChoice `json:"product_decisions"`
}

type iosHTTPMethodChoice struct {
	ID        string `json:"id"`
	Statement string `json:"statement"`
}

type iosHTTPTransport struct {
	Scheme      string `json:"scheme"`
	BindHost    string `json:"bind_host"`
	DefaultPort int    `json:"default_port"`
}

type iosErrorContract struct {
	Schema            string                `json:"schema"`
	Mappings          []iosErrorMapping     `json:"mappings"`
	TimeoutSignatures []iosTimeoutSignature `json:"timeout_signatures"`
}

type iosErrorMapping struct {
	HTTPStatus int    `json:"http_status"`
	Code       string `json:"code"`
}

type iosTimeoutSignature struct {
	Domain    string `json:"domain"`
	Code      int    `json:"code"`
	Retryable bool   `json:"retryable"`
}

type wireSchema struct {
	Kind         string               `json:"kind"`
	Required     []string             `json:"required,omitempty"`
	Fields       map[string]wireField `json:"fields,omitempty"`
	ContentTypes []string             `json:"content_types,omitempty"`
}

type wireField struct {
	Type                 string   `json:"type,omitempty"`
	Ref                  string   `json:"ref,omitempty"`
	Items                string   `json:"items,omitempty"`
	Enum                 []string `json:"enum,omitempty"`
	Minimum              *float64 `json:"minimum,omitempty"`
	AdditionalProperties string   `json:"additional_properties,omitempty"`
}

type expectedShape struct {
	Required []string
	Fields   map[string]string
}

func TestIOSHTTPV0RoutesMatchGoAndMachineReadableContracts(t *testing.T) {
	document := loadIOSHTTPDocument(t)
	if document.SchemaVersion != 1 || document.ContractVersion != device.ContractVersionV0 {
		t.Fatalf("iOS contract version = schema %d contract %q, want 1/%s", document.SchemaVersion, document.ContractVersion, device.ContractVersionV0)
	}
	if document.Transport != (iosHTTPTransport{Scheme: "http", BindHost: "127.0.0.1", DefaultPort: 22087}) {
		t.Fatalf("transport = %#v, want frozen loopback HTTP transport", document.Transport)
	}
	assertIOSMethodChoiceIsExplicit(t, document.DeclarationBasis)

	want := expectedIOSRoutes()
	if !reflect.DeepEqual(document.Routes, want) {
		gotJSON, _ := json.MarshalIndent(document.Routes, "", "  ")
		wantJSON, _ := json.MarshalIndent(want, "", "  ")
		t.Fatalf("iOS route snapshot drifted\n--- got ---\n%s\n--- want ---\n%s", gotJSON, wantJSON)
	}
	if got := device.IOSRoutesContractV0(); !reflect.DeepEqual(got, want) {
		gotJSON, _ := json.MarshalIndent(got, "", "  ")
		wantJSON, _ := json.MarshalIndent(want, "", "  ")
		t.Fatalf("Go iOS route contract drifted\n--- got ---\n%s\n--- want ---\n%s", gotJSON, wantJSON)
	}
	if len(document.Routes) != 18 {
		t.Fatalf("route count = %d, want exactly 18", len(document.Routes))
	}

	seenNames := make(map[string]bool, len(document.Routes))
	seenEndpoints := make(map[string]bool, len(document.Routes))
	for _, route := range document.Routes {
		if seenNames[route.Name] {
			t.Fatalf("duplicate route name %q", route.Name)
		}
		seenNames[route.Name] = true
		endpoint := route.Method + " " + route.Path
		if seenEndpoints[endpoint] {
			t.Fatalf("duplicate endpoint %q", endpoint)
		}
		seenEndpoints[endpoint] = true
		for _, schemaName := range []string{route.RequestSchema, route.ResponseSchema, route.ErrorSchema} {
			if _, exists := document.Schemas[schemaName]; !exists {
				t.Fatalf("route %s uses missing schema %q", route.Name, schemaName)
			}
		}
		if route.SuccessStatus != 200 {
			t.Fatalf("route %s success status = %d, want 200", route.Name, route.SuccessStatus)
		}
		if !reflect.DeepEqual(route.ErrorStatuses, []int{400, 408, 500}) {
			t.Fatalf("route %s error statuses = %v, want [400 408 500]", route.Name, route.ErrorStatuses)
		}
	}
}

func TestIOSHTTPV0RequestAndResponseSchemasAreExact(t *testing.T) {
	document := loadIOSHTTPDocument(t)
	expected := expectedIOSShapes()
	if len(document.Schemas) != len(expected) {
		t.Fatalf("schema count = %d, want %d", len(document.Schemas), len(expected))
	}
	for name, want := range expected {
		got, exists := document.Schemas[name]
		if !exists {
			t.Errorf("missing schema %s", name)
			continue
		}
		if got.Kind != want.Kind {
			t.Errorf("schema %s kind = %q, want %q", name, got.Kind, want.Kind)
		}
		if !reflect.DeepEqual(got.Required, want.Required) {
			t.Errorf("schema %s required = %v, want %v", name, got.Required, want.Required)
		}
		gotFields := make(map[string]string, len(got.Fields))
		for fieldName, field := range got.Fields {
			gotFields[fieldName] = wireFieldSignature(field)
		}
		if !reflect.DeepEqual(gotFields, want.Fields) {
			t.Errorf("schema %s fields = %#v, want %#v", name, gotFields, want.Fields)
		}
		if !reflect.DeepEqual(got.ContentTypes, want.ContentTypes) {
			t.Errorf("schema %s content types = %v, want %v", name, got.ContentTypes, want.ContentTypes)
		}
	}
}

func TestIOSHTTPV0ErrorMappingAndTimeoutSignaturesAreExact(t *testing.T) {
	document := loadIOSHTTPDocument(t)
	want := iosErrorContract{
		Schema: "ErrorResponse",
		Mappings: []iosErrorMapping{
			{HTTPStatus: 400, Code: "precondition"},
			{HTTPStatus: 408, Code: "timeout"},
			{HTTPStatus: 500, Code: "internal"},
		},
		TimeoutSignatures: []iosTimeoutSignature{
			{Domain: "com.apple.dt.XCTest.XCTFuture", Code: 1000, Retryable: false},
			{Domain: "com.apple.dt.xctest.automation-support.error", Code: 6, Retryable: false},
		},
	}
	if !reflect.DeepEqual(document.ErrorContract, want) {
		t.Fatalf("iOS error contract = %#v, want %#v", document.ErrorContract, want)
	}
}

func TestIOSHTTPV0RouteCopiesCannotMutateFrozenContract(t *testing.T) {
	first := device.IOSRoutesContractV0()
	first[0].Name = "mutated"
	first[0].ErrorStatuses[0] = 999
	second := device.IOSRoutesContractV0()
	if second[0].Name == "mutated" || second[0].ErrorStatuses[0] == 999 {
		t.Fatal("IOSRoutesContractV0 exposed mutable contract backing storage")
	}
}

func loadIOSHTTPDocument(t *testing.T) iosHTTPDocument {
	t.Helper()
	data, err := os.ReadFile("ios-http.json")
	if err != nil {
		t.Fatalf("read iOS HTTP descriptor: %v", err)
	}
	var document iosHTTPDocument
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		t.Fatalf("decode iOS HTTP descriptor: %v", err)
	}
	return document
}

func expectedIOSRoutes() []device.IOSRouteV0 {
	return []device.IOSRouteV0{
		iosRoute("runningApp", "GET", "json_body", "RunningAppRequest", "RunningAppResponse"),
		iosRoute("swipe", "POST", "json_body", "SwipeRequest", "EmptyResponse"),
		iosRoute("swipeV2", "POST", "json_body", "SwipeV2Request", "EmptyResponse"),
		iosRoute("inputText", "POST", "json_body", "InputTextRequest", "EmptyResponse"),
		iosRoute("touch", "POST", "json_body", "TouchRequest", "EmptyResponse"),
		iosRoute("screenshot", "GET", "query", "ScreenshotQuery", "ScreenshotResponse"),
		iosRoute("isScreenStatic", "GET", "none", "NoBody", "ScreenStaticResponse"),
		iosRoute("pressKey", "POST", "json_body", "PressKeyRequest", "EmptyResponse"),
		iosRoute("pressButton", "POST", "json_body", "PressButtonRequest", "EmptyResponse"),
		iosRoute("eraseText", "POST", "json_body", "EraseTextRequest", "EmptyResponse"),
		iosRoute("deviceInfo", "GET", "none", "NoBody", "DeviceInfoResponse"),
		iosRoute("setOrientation", "POST", "json_body", "SetOrientationRequest", "EmptyResponse"),
		iosRoute("setPermissions", "POST", "json_body", "SetPermissionsRequest", "EmptyResponse"),
		iosRoute("viewHierarchy", "POST", "json_body", "ViewHierarchyRequest", "ViewHierarchyResponse"),
		iosRoute("status", "GET", "none", "NoBody", "StatusResponse"),
		iosRoute("keyboard", "GET", "json_body", "KeyboardRequest", "KeyboardResponse"),
		iosRoute("launchApp", "POST", "json_body", "LaunchAppRequest", "EmptyResponse"),
		iosRoute("terminateApp", "POST", "json_body", "TerminateAppRequest", "EmptyResponse"),
	}
}

func iosRoute(name, method, location, request, response string) device.IOSRouteV0 {
	return device.IOSRouteV0{
		Name:            name,
		Method:          method,
		Path:            "/" + name,
		RequestLocation: location,
		RequestSchema:   request,
		ResponseSchema:  response,
		SuccessStatus:   200,
		ErrorSchema:     "ErrorResponse",
		ErrorStatuses:   []int{400, 408, 500},
	}
}

type completeExpectedShape struct {
	expectedShape
	Kind         string
	ContentTypes []string
}

func expectedIOSShapes() map[string]completeExpectedShape {
	object := func(required []string, fields map[string]string) completeExpectedShape {
		return completeExpectedShape{Kind: "object", expectedShape: expectedShape{Required: required, Fields: fields}}
	}
	return map[string]completeExpectedShape{
		"NoBody":             {Kind: "none", expectedShape: expectedShape{Fields: map[string]string{}}},
		"EmptyResponse":      {Kind: "empty", expectedShape: expectedShape{Fields: map[string]string{}}},
		"ScreenshotResponse": {Kind: "binary", ContentTypes: []string{"image/png", "image/jpeg"}, expectedShape: expectedShape{Fields: map[string]string{}}},
		"RunningAppRequest":  object([]string{"appIds"}, map[string]string{"appIds": "array:string"}),
		"RunningAppResponse": object([]string{"runningAppBundleId"}, map[string]string{"runningAppBundleId": "string"}),
		"SwipeRequest": object([]string{"startX", "startY", "endX", "endY", "duration"}, map[string]string{
			"appId": "string", "startX": "number", "startY": "number", "endX": "number", "endY": "number", "duration": "number",
		}),
		"SwipeV2Request": object([]string{"startX", "startY", "endX", "endY", "duration"}, map[string]string{
			"startX": "number", "startY": "number", "endX": "number", "endY": "number", "duration": "number", "appIds": "array:string",
		}),
		"InputTextRequest":     object([]string{"text", "appIds"}, map[string]string{"text": "string", "appIds": "array:string"}),
		"TouchRequest":         object([]string{"x", "y"}, map[string]string{"x": "number", "y": "number", "duration": "number"}),
		"ScreenshotQuery":      object(nil, map[string]string{"compressed": "boolean"}),
		"ScreenStaticResponse": object([]string{"isScreenStatic"}, map[string]string{"isScreenStatic": "boolean"}),
		"PressKeyRequest":      object([]string{"key"}, map[string]string{"key": "string:enum=delete|return|enter|tab|space|escape"}),
		"PressButtonRequest":   object([]string{"button"}, map[string]string{"button": "string:enum=home|lock"}),
		"EraseTextRequest":     object([]string{"charactersToErase", "appIds"}, map[string]string{"charactersToErase": "integer:min=0", "appIds": "array:string"}),
		"DeviceInfoResponse": object([]string{"widthPoints", "heightPoints", "widthPixels", "heightPixels"}, map[string]string{
			"widthPoints": "number", "heightPoints": "number", "widthPixels": "number", "heightPixels": "number",
		}),
		"SetOrientationRequest": object([]string{"orientation"}, map[string]string{"orientation": "string:enum=portrait|landscapeLeft|landscapeRight|upsideDown"}),
		"SetPermissionsRequest": object([]string{"permissions"}, map[string]string{"permissions": "object:additional=string"}),
		"ViewHierarchyRequest":  object([]string{"appIds", "excludeKeyboardElements"}, map[string]string{"appIds": "array:string", "excludeKeyboardElements": "boolean"}),
		"Frame":                 object([]string{"X", "Y", "Width", "Height"}, map[string]string{"X": "number", "Y": "number", "Width": "number", "Height": "number"}),
		"AXElement": object([]string{"identifier", "frame", "label", "elementType", "enabled", "horizontalSizeClass", "verticalSizeClass", "selected", "hasFocus", "windowContextID", "displayID"}, map[string]string{
			"identifier": "string", "frame": "ref:Frame", "value": "string", "title": "string", "label": "string", "elementType": "integer", "enabled": "boolean",
			"horizontalSizeClass": "integer", "verticalSizeClass": "integer", "placeholderValue": "string", "selected": "boolean", "hasFocus": "boolean",
			"children": "array:ref:AXElement", "windowContextID": "number", "displayID": "integer",
		}),
		"ViewHierarchyResponse": object([]string{"axElement", "depth"}, map[string]string{"axElement": "ref:AXElement", "depth": "integer"}),
		"StatusResponse":        object([]string{"status"}, map[string]string{"status": "string:enum=ok"}),
		"KeyboardRequest":       object([]string{"appIds"}, map[string]string{"appIds": "array:string"}),
		"KeyboardResponse":      object([]string{"isKeyboardVisible"}, map[string]string{"isKeyboardVisible": "boolean"}),
		"LaunchAppRequest":      object([]string{"bundleId"}, map[string]string{"bundleId": "string"}),
		"TerminateAppRequest":   object([]string{"appId"}, map[string]string{"appId": "string"}),
		"ErrorResponse": object([]string{"code", "errorMessage"}, map[string]string{
			"code": "string:enum=internal|precondition|timeout", "errorMessage": "string",
		}),
	}
}

func wireFieldSignature(field wireField) string {
	parts := make([]string, 0, 4)
	switch {
	case field.Ref != "":
		parts = append(parts, "ref:"+field.Ref)
	case field.Type == "array":
		parts = append(parts, "array:"+field.Items)
	default:
		parts = append(parts, field.Type)
	}
	if len(field.Enum) > 0 {
		parts = append(parts, "enum="+strings.Join(field.Enum, "|"))
	}
	if field.Minimum != nil {
		parts = append(parts, "min=0")
	}
	if field.AdditionalProperties != "" {
		parts = append(parts, "additional="+field.AdditionalProperties)
	}
	return strings.Join(parts, ":")
}

func assertIOSMethodChoiceIsExplicit(t *testing.T, basis iosHTTPDeclarationBasis) {
	t.Helper()
	if basis.ContractKind != "flowbaton-wire-contract" || !reflect.DeepEqual(basis.Specifications, []string{"specs/04-wire-protocols.md sections 3 and 4"}) {
		t.Fatalf("iOS declaration basis = %#v, want FlowBaton contract specifications", basis)
	}
	wantFragments := map[string][]string{
		"ios-http-methods":         {"GET runningApp", "GET keyboard", "POST viewHierarchy"},
		"permissions-value-schema": {"string values"},
	}
	seen := make(map[string]bool, len(wantFragments))
	for _, choice := range basis.ProductDecisions {
		fragments, required := wantFragments[choice.ID]
		if !required {
			t.Fatalf("unexpected iOS product decision %q", choice.ID)
		}
		for _, fragment := range fragments {
			if !strings.Contains(choice.Statement, fragment) {
				t.Fatalf("iOS choice %s statement %q omits %q", choice.ID, choice.Statement, fragment)
			}
		}
		seen[choice.ID] = true
	}
	for choiceID := range wantFragments {
		if !seen[choiceID] {
			t.Fatalf("missing explicit iOS contract choice %q", choiceID)
		}
	}
}
