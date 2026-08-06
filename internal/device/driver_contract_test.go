package device

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

type driverContractSnapshot struct {
	SchemaVersion   int                  `json:"schema_version"`
	ContractVersion string               `json:"contract_version"`
	Interface       interfaceSnapshot    `json:"interface"`
	Types           []structTypeSnapshot `json:"types"`
}

type interfaceSnapshot struct {
	Package string           `json:"package"`
	Name    string           `json:"name"`
	Methods []methodSnapshot `json:"methods"`
}

type methodSnapshot struct {
	Name       string   `json:"name"`
	Parameters []string `json:"parameters"`
	Results    []string `json:"results"`
}

type structTypeSnapshot struct {
	Name   string          `json:"name"`
	Fields []fieldSnapshot `json:"fields"`
}

type fieldSnapshot struct {
	Name      string `json:"name"`
	GoType    string `json:"go_type"`
	JSONName  string `json:"json_name"`
	OmitEmpty bool   `json:"omit_empty"`
}

func TestDriverV0ContractMatchesVersionedSnapshot(t *testing.T) {
	wantData, err := os.ReadFile(filepath.Join("..", "..", "contracts", "v0", "driver.json"))
	if err != nil {
		t.Fatalf("read versioned driver contract: %v", err)
	}

	var want driverContractSnapshot
	decoder := json.NewDecoder(strings.NewReader(string(wantData)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&want); err != nil {
		t.Fatalf("decode versioned driver contract: %v", err)
	}
	if want.SchemaVersion != 1 || want.ContractVersion != ContractVersionV0 {
		t.Fatalf("snapshot version = schema %d contract %q, want schema 1 contract %q", want.SchemaVersion, want.ContractVersion, ContractVersionV0)
	}
	got := reflectDriverV0Contract(t)
	if !reflect.DeepEqual(got.Interface, want.Interface) {
		gotJSON, _ := json.MarshalIndent(got.Interface, "", "  ")
		wantJSON, _ := json.MarshalIndent(want.Interface, "", "  ")
		t.Fatalf("Driver interface drifted\n--- got ---\n%s\n--- want ---\n%s", gotJSON, wantJSON)
	}
	if !reflect.DeepEqual(got.Types, want.Types) {
		gotJSON, _ := json.MarshalIndent(got.Types, "", "  ")
		wantJSON, _ := json.MarshalIndent(want.Types, "", "  ")
		t.Fatalf("Driver DTOs drifted\n--- got ---\n%s\n--- want ---\n%s", gotJSON, wantJSON)
	}
}

func TestDriverV0IsInterfaceAndDTOsContainNoBehaviorFields(t *testing.T) {
	driverType := reflect.TypeOf((*Driver)(nil)).Elem()
	if driverType.Kind() != reflect.Interface {
		t.Fatalf("Driver kind = %v, want interface", driverType.Kind())
	}

	for _, typ := range driverV0DTOs() {
		if typ.Kind() != reflect.Struct {
			t.Fatalf("%s kind = %v, want struct", typ.Name(), typ.Kind())
		}
		for index := 0; index < typ.NumField(); index++ {
			field := typ.Field(index)
			if field.Type.Kind() == reflect.Func || field.Type.Kind() == reflect.Interface {
				t.Fatalf("%s.%s is behavior-bearing %s; v0 DTOs must remain data-only", typ.Name(), field.Name, field.Type)
			}
			if field.PkgPath != "" {
				t.Fatalf("%s.%s is unexported", typ.Name(), field.Name)
			}
			if field.Tag.Get("json") == "" {
				t.Fatalf("%s.%s has no json tag", typ.Name(), field.Name)
			}
		}
	}
}

func TestIOSRoutesContractV0ReturnsDefensiveCopies(t *testing.T) {
	first := IOSRoutesContractV0()
	if got := len(first); got != 18 {
		t.Fatalf("iOS route count = %d, want 18", got)
	}
	first[0].Name = "mutated"
	first[0].ErrorStatuses[0] = 999

	second := IOSRoutesContractV0()
	if second[0].Name == "mutated" || second[0].ErrorStatuses[0] == 999 {
		t.Fatal("IOSRoutesContractV0 exposed mutable backing storage")
	}
}

func reflectDriverV0Contract(t *testing.T) driverContractSnapshot {
	t.Helper()
	driverType := reflect.TypeOf((*Driver)(nil)).Elem()
	methods := make([]methodSnapshot, 0, driverType.NumMethod())
	for index := 0; index < driverType.NumMethod(); index++ {
		method := driverType.Method(index)
		parameters := make([]string, 0, method.Type.NumIn())
		for parameterIndex := 0; parameterIndex < method.Type.NumIn(); parameterIndex++ {
			parameters = append(parameters, contractTypeName(method.Type.In(parameterIndex)))
		}
		results := make([]string, 0, method.Type.NumOut())
		for resultIndex := 0; resultIndex < method.Type.NumOut(); resultIndex++ {
			results = append(results, contractTypeName(method.Type.Out(resultIndex)))
		}
		methods = append(methods, methodSnapshot{Name: method.Name, Parameters: parameters, Results: results})
	}

	types := driverV0DTOs()
	typeSnapshots := make([]structTypeSnapshot, 0, len(types))
	for _, typ := range types {
		fields := make([]fieldSnapshot, 0, typ.NumField())
		for index := 0; index < typ.NumField(); index++ {
			field := typ.Field(index)
			tag := strings.Split(field.Tag.Get("json"), ",")
			jsonName := tag[0]
			omitEmpty := len(tag) > 1 && tag[1] == "omitempty"
			fields = append(fields, fieldSnapshot{
				Name:      field.Name,
				GoType:    contractTypeName(field.Type),
				JSONName:  jsonName,
				OmitEmpty: omitEmpty,
			})
		}
		typeSnapshots = append(typeSnapshots, structTypeSnapshot{Name: typ.Name(), Fields: fields})
	}
	sort.Slice(typeSnapshots, func(i, j int) bool { return typeSnapshots[i].Name < typeSnapshots[j].Name })

	return driverContractSnapshot{
		SchemaVersion:   1,
		ContractVersion: ContractVersionV0,
		Interface: interfaceSnapshot{
			Package: "github.com/larchwave/flowbaton/internal/device",
			Name:    "Driver",
			Methods: methods,
		},
		Types: typeSnapshots,
	}
}

func driverV0DTOs() []reflect.Type {
	return []reflect.Type{
		reflect.TypeOf(AddMediaRequest{}),
		reflect.TypeOf(AirplaneModeRequest{}),
		reflect.TypeOf(AppRequest{}),
		reflect.TypeOf(Artifact{}),
		reflect.TypeOf(ArtifactRequest{}),
		reflect.TypeOf(Bounds{}),
		reflect.TypeOf(Capabilities{}),
		reflect.TypeOf(ChromeDevToolsRequest{}),
		reflect.TypeOf(ContentDescriptorRequest{}),
		reflect.TypeOf(DeviceInfo{}),
		reflect.TypeOf(DeviceLogRequest{}),
		reflect.TypeOf(EraseTextRequest{}),
		reflect.TypeOf(InputTextRequest{}),
		reflect.TypeOf(KeyboardRequest{}),
		reflect.TypeOf(LaunchAppRequest{}),
		reflect.TypeOf(LaunchArgument{}),
		reflect.TypeOf(Location{}),
		reflect.TypeOf(LongPressRequest{}),
		reflect.TypeOf(MediaFile{}),
		reflect.TypeOf(OpenLinkRequest{}),
		reflect.TypeOf(PermissionsRequest{}),
		reflect.TypeOf(Point{}),
		reflect.TypeOf(PressKeyRequest{}),
		reflect.TypeOf(Proxy{}),
		reflect.TypeOf(QueryRequest{}),
		reflect.TypeOf(ScreenRecordingRequest{}),
		reflect.TypeOf(ScreenStaticRequest{}),
		reflect.TypeOf(ScreenshotRequest{}),
		reflect.TypeOf(ScrollVerticalRequest{}),
		reflect.TypeOf(SettleRequest{}),
		reflect.TypeOf(SwipeRequest{}),
		reflect.TypeOf(TapRequest{}),
		reflect.TypeOf(TreeNode{}),
		reflect.TypeOf(UiElement{}),
		reflect.TypeOf(ViewHierarchy{}),
	}
}

func contractTypeName(typ reflect.Type) string {
	switch typ.Kind() {
	case reflect.Pointer:
		return "*" + contractTypeName(typ.Elem())
	case reflect.Slice:
		return "[]" + contractTypeName(typ.Elem())
	case reflect.Map:
		return "map[" + contractTypeName(typ.Key()) + "]" + contractTypeName(typ.Elem())
	}
	if typ.PkgPath() == "context" {
		return "context." + typ.Name()
	}
	if typ.PkgPath() == "github.com/larchwave/flowbaton/internal/device" {
		return typ.Name()
	}
	if typ.Name() != "" {
		return typ.Name()
	}
	return typ.String()
}
