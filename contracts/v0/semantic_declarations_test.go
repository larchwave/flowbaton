package v0

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

type semanticDeclarationBasis struct {
	ContractKind     string                    `json:"contract_kind"`
	Specifications   []string                  `json:"specifications"`
	ProductDecisions []semanticProductDecision `json:"product_decisions"`
}

type semanticProductDecision struct {
	ID        string `json:"id"`
	Statement string `json:"statement"`
}

type androidSemanticDescriptor struct {
	SchemaVersion    int                      `json:"schema_version"`
	ContractVersion  string                   `json:"contract_version"`
	DeclarationBasis semanticDeclarationBasis `json:"declaration_basis"`
	Proto            androidSemanticProto     `json:"proto"`
	RPCs             []androidSemanticRPC     `json:"rpcs"`
	Messages         []androidSemanticMessage `json:"messages"`
	ErrorContract    androidSemanticError     `json:"error_contract"`
}

type androidSemanticProto struct {
	File    string `json:"file"`
	Syntax  string `json:"syntax"`
	Package string `json:"package"`
	Service string `json:"service"`
}

type androidSemanticRPC struct {
	Name            string `json:"name"`
	Request         string `json:"request"`
	Response        string `json:"response"`
	ClientStreaming bool   `json:"client_streaming"`
	ServerStreaming bool   `json:"server_streaming"`
}

type androidSemanticMessage struct {
	Name   string                 `json:"name"`
	Fields []androidSemanticField `json:"fields"`
}

type androidSemanticField struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Number   int    `json:"number"`
	Repeated bool   `json:"repeated"`
}

type androidSemanticError struct {
	HandlerStatus     string   `json:"handler_status"`
	Trailers          []string `json:"trailers"`
	TransportStatuses []string `json:"transport_statuses"`
}

type iosSemanticDescriptor struct {
	SchemaVersion    int                          `json:"schema_version"`
	ContractVersion  string                       `json:"contract_version"`
	DeclarationBasis semanticDeclarationBasis     `json:"declaration_basis"`
	Transport        iosSemanticTransport         `json:"transport"`
	Routes           []iosSemanticRoute           `json:"routes"`
	Schemas          map[string]iosSemanticSchema `json:"schemas"`
	ErrorContract    iosSemanticError             `json:"error_contract"`
}

type iosSemanticTransport struct {
	Scheme      string `json:"scheme"`
	BindHost    string `json:"bind_host"`
	DefaultPort int    `json:"default_port"`
}

type iosSemanticRoute struct {
	Name            string `json:"name"`
	Method          string `json:"method"`
	Path            string `json:"path"`
	RequestLocation string `json:"request_location"`
	RequestSchema   string `json:"request_schema"`
	ResponseSchema  string `json:"response_schema"`
	SuccessStatus   int    `json:"success_status"`
	ErrorSchema     string `json:"error_schema"`
	ErrorStatuses   []int  `json:"error_statuses"`
}

type iosSemanticSchema struct {
	Kind         string                      `json:"kind"`
	Required     []string                    `json:"required"`
	Fields       map[string]iosSemanticField `json:"fields"`
	ContentTypes []string                    `json:"content_types"`
}

type iosSemanticField struct {
	Type                 string   `json:"type"`
	Items                string   `json:"items"`
	Link                 string   `json:"ref"`
	Enum                 []string `json:"enum"`
	Minimum              *int     `json:"minimum"`
	AdditionalProperties string   `json:"additional_properties"`
}

type iosSemanticError struct {
	Schema            string                        `json:"schema"`
	Mappings          []iosSemanticErrorMapping     `json:"mappings"`
	TimeoutSignatures []iosSemanticTimeoutSignature `json:"timeout_signatures"`
}

type iosSemanticErrorMapping struct {
	HTTPStatus int    `json:"http_status"`
	Code       string `json:"code"`
}

type iosSemanticTimeoutSignature struct {
	Domain    string `json:"domain"`
	Code      int    `json:"code"`
	Retryable bool   `json:"retryable"`
}

func TestAndroidJVMDeclarationsSemanticallyMatchCanonicalDescriptor(t *testing.T) {
	descriptor := readSemanticDescriptor[androidSemanticDescriptor](t, "android-grpc.json")
	want := renderAndroidSemanticManifest(descriptor)
	got := readSemanticSourceConstant(
		t,
		filepath.Join("..", "..", "drivers", "android", "core", "src", "main", "java", "dev", "nohavewho", "flowbaton", "driver", "contract", "AndroidWireContractV0.java"),
		regexp.MustCompile(`(?s)SEMANTIC_MANIFEST\s*=\s*String\.join\(\s*"\\n"\s*,(.*?)\);`),
	)
	assertSemanticManifestEqual(t, got, want)
}

func TestIOSSwiftDeclarationsSemanticallyMatchCanonicalDescriptor(t *testing.T) {
	descriptor := readSemanticDescriptor[iosSemanticDescriptor](t, "ios-http.json")
	want := renderIOSSemanticManifest(descriptor)
	got := readSemanticSourceConstant(
		t,
		filepath.Join("..", "..", "drivers", "ios", "Sources", "FlowBatonIOSRunner", "WireContractV0.swift"),
		regexp.MustCompile(`(?s)semanticManifest\s*=\s*\[(.*?)\]\.joined\(separator:\s*"\\n"\s*\)`),
	)
	assertSemanticManifestEqual(t, got, want)
}

func TestSemanticManifestsDetectContractDatumMutations(t *testing.T) {
	androidGolden := renderAndroidSemanticManifest(readSemanticDescriptor[androidSemanticDescriptor](t, "android-grpc.json"))
	androidMutations := map[string]func(*androidSemanticDescriptor){
		"proto":         func(value *androidSemanticDescriptor) { value.Proto.Service += "Changed" },
		"rpc":           func(value *androidSemanticDescriptor) { value.RPCs[0].ServerStreaming = true },
		"message field": func(value *androidSemanticDescriptor) { value.Messages[0].Fields[0].Repeated = true },
		"error":         func(value *androidSemanticDescriptor) { value.ErrorContract.Trailers[0] += "-changed" },
	}
	for name, mutate := range androidMutations {
		t.Run("android "+name, func(t *testing.T) {
			descriptor := readSemanticDescriptor[androidSemanticDescriptor](t, "android-grpc.json")
			mutate(&descriptor)
			if got := renderAndroidSemanticManifest(descriptor); got == androidGolden {
				t.Fatalf("%s mutation did not change the semantic manifest", name)
			}
		})
	}

	iosGolden := renderIOSSemanticManifest(readSemanticDescriptor[iosSemanticDescriptor](t, "ios-http.json"))
	iosMutations := map[string]func(*iosSemanticDescriptor){
		"transport": func(value *iosSemanticDescriptor) { value.Transport.DefaultPort++ },
		"route":     func(value *iosSemanticDescriptor) { value.Routes[0].SuccessStatus++ },
		"schema field": func(value *iosSemanticDescriptor) {
			field := value.Schemas["StatusResponse"].Fields["status"]
			field.Enum[0] += "-changed"
		},
		"error mapping": func(value *iosSemanticDescriptor) {
			value.ErrorContract.Mappings[0].Code += "-changed"
		},
		"timeout": func(value *iosSemanticDescriptor) { value.ErrorContract.TimeoutSignatures[0].Retryable = true },
	}
	for name, mutate := range iosMutations {
		t.Run("ios "+name, func(t *testing.T) {
			descriptor := readSemanticDescriptor[iosSemanticDescriptor](t, "ios-http.json")
			mutate(&descriptor)
			if got := renderIOSSemanticManifest(descriptor); got == iosGolden {
				t.Fatalf("%s mutation did not change the semantic manifest", name)
			}
		})
	}
}

func renderAndroidSemanticManifest(descriptor androidSemanticDescriptor) string {
	lines := []string{
		semanticLine("descriptor", descriptor.SchemaVersion, descriptor.ContractVersion),
		semanticLine("proto", descriptor.Proto.File, descriptor.Proto.Syntax, descriptor.Proto.Package, descriptor.Proto.Service),
	}
	for index, rpc := range descriptor.RPCs {
		lines = append(lines, semanticLine("rpc", index, rpc.Name, rpc.Request, rpc.Response, rpc.ClientStreaming, rpc.ServerStreaming))
	}
	for messageIndex, message := range descriptor.Messages {
		lines = append(lines, semanticLine("message", messageIndex, message.Name))
		for fieldIndex, field := range message.Fields {
			lines = append(lines, semanticLine("field", message.Name, fieldIndex, field.Name, field.Type, field.Number, field.Repeated))
		}
	}
	lines = append(lines, semanticLine("error-handler-status", descriptor.ErrorContract.HandlerStatus))
	for index, trailer := range descriptor.ErrorContract.Trailers {
		lines = append(lines, semanticLine("error-trailer", index, trailer))
	}
	for index, status := range descriptor.ErrorContract.TransportStatuses {
		lines = append(lines, semanticLine("error-transport-status", index, status))
	}
	return strings.Join(lines, "\n")
}

func renderIOSSemanticManifest(descriptor iosSemanticDescriptor) string {
	lines := []string{
		semanticLine("descriptor", descriptor.SchemaVersion, descriptor.ContractVersion),
		semanticLine("transport", descriptor.Transport.Scheme, descriptor.Transport.BindHost, descriptor.Transport.DefaultPort),
	}
	for index, route := range descriptor.Routes {
		lines = append(lines, semanticLine(
			"route", index, route.Name, route.Method, route.Path, route.RequestLocation,
			route.RequestSchema, route.ResponseSchema, route.SuccessStatus, route.ErrorSchema,
		))
		for statusIndex, status := range route.ErrorStatuses {
			lines = append(lines, semanticLine("route-error-status", route.Name, statusIndex, status))
		}
	}

	schemaNames := make([]string, 0, len(descriptor.Schemas))
	for name := range descriptor.Schemas {
		schemaNames = append(schemaNames, name)
	}
	sort.Strings(schemaNames)
	for _, name := range schemaNames {
		schema := descriptor.Schemas[name]
		lines = append(lines, semanticLine("schema", name, schema.Kind))
		for index, required := range schema.Required {
			lines = append(lines, semanticLine("schema-required", name, index, required))
		}
		fieldNames := make([]string, 0, len(schema.Fields))
		for fieldName := range schema.Fields {
			fieldNames = append(fieldNames, fieldName)
		}
		sort.Strings(fieldNames)
		for _, fieldName := range fieldNames {
			lines = append(lines, semanticLine("schema-field", name, fieldName, describeIOSSemanticField(schema.Fields[fieldName])))
		}
		for index, contentType := range schema.ContentTypes {
			lines = append(lines, semanticLine("schema-content-type", name, index, contentType))
		}
	}

	lines = append(lines, semanticLine("error-schema", descriptor.ErrorContract.Schema))
	for index, mapping := range descriptor.ErrorContract.Mappings {
		lines = append(lines, semanticLine("error-mapping", index, mapping.HTTPStatus, mapping.Code))
	}
	for index, signature := range descriptor.ErrorContract.TimeoutSignatures {
		lines = append(lines, semanticLine("timeout-signature", index, signature.Domain, signature.Code, signature.Retryable))
	}
	return strings.Join(lines, "\n")
}

func describeIOSSemanticField(field iosSemanticField) string {
	switch {
	case field.Link != "":
		return "ref:" + field.Link
	case field.Type == "array":
		return "array<" + field.Items + ">"
	case field.Type == "object" && field.AdditionalProperties != "":
		return "object<additional-properties:" + field.AdditionalProperties + ">"
	case len(field.Enum) > 0:
		return field.Type + "{" + strings.Join(field.Enum, ",") + "}"
	case field.Minimum != nil:
		return fmt.Sprintf("%s(minimum=%d)", field.Type, *field.Minimum)
	default:
		return field.Type
	}
}

func semanticLine(values ...any) string {
	parts := make([]string, len(values))
	for index, value := range values {
		switch typed := value.(type) {
		case string:
			parts[index] = escapeSemanticToken(typed)
		case int:
			parts[index] = strconv.Itoa(typed)
		case bool:
			parts[index] = strconv.FormatBool(typed)
		default:
			panic(fmt.Sprintf("unsupported semantic manifest value %T", value))
		}
	}
	return strings.Join(parts, "|")
}

func escapeSemanticToken(value string) string {
	replacer := strings.NewReplacer("%", "%25", "|", "%7C", "\r", "%0D", "\n", "%0A")
	return replacer.Replace(value)
}

func readSemanticDescriptor[T any](t *testing.T, path string) T {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var descriptor T
	if err := decoder.Decode(&descriptor); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			t.Fatalf("decode %s: trailing JSON value", path)
		}
		t.Fatalf("decode %s trailing data: %v", path, err)
	}
	return descriptor
}

func readSemanticSourceConstant(t *testing.T, path string, pattern *regexp.Regexp) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	match := pattern.FindSubmatch(contents)
	if len(match) != 2 {
		t.Fatalf("%s does not declare the canonical semantic manifest constant", path)
	}
	literalPattern := regexp.MustCompile(`"(?:\\.|[^"\\])*"`)
	literals := literalPattern.FindAll(match[1], -1)
	if len(literals) == 0 {
		t.Fatalf("%s semantic manifest constant has no lines", path)
	}
	lines := make([]string, len(literals))
	for index, literal := range literals {
		decoded, err := strconv.Unquote(string(literal))
		if err != nil {
			t.Fatalf("decode semantic manifest line %q in %s: %v", literal, path, err)
		}
		lines[index] = decoded
	}
	return strings.Join(lines, "\n")
}

func assertSemanticManifestEqual(t *testing.T, got, want string) {
	t.Helper()
	if got == want {
		return
	}
	gotLines := strings.Split(got, "\n")
	wantLines := strings.Split(want, "\n")
	limit := len(gotLines)
	if len(wantLines) < limit {
		limit = len(wantLines)
	}
	for index := 0; index < limit; index++ {
		if gotLines[index] != wantLines[index] {
			t.Fatalf("semantic manifest line %d differs:\n got: %s\nwant: %s", index+1, gotLines[index], wantLines[index])
		}
	}
	t.Fatalf("semantic manifest line count = %d, want %d", len(gotLines), len(wantLines))
}
