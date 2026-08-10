package capability

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/android"
	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/drivercontract"
	"github.com/larchwave/flowbaton/internal/ios"
	"github.com/larchwave/flowbaton/internal/model"
	"github.com/larchwave/flowbaton/internal/web"
)

func TestDefaultRegistryIsExhaustiveAndValid(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	if registry.Version() != RegistryVersionV0 {
		t.Fatalf("registry version = %q, want %q", registry.Version(), RegistryVersionV0)
	}
	if err := registry.Validate(); err != nil {
		t.Fatalf("default registry validation: %v", err)
	}

	wantCounts := map[FeatureKind]int{
		FeatureCommand:         53,
		FeatureSelector:        29,
		FeatureConfigExtension: 4,
		FeatureCLISubcommand:   21,
		FeatureCLIFlag:         119,
		FeatureHostTarget:      6,
		FeatureDevicePlatform:  5,
	}
	gotCounts := make(map[FeatureKind]int)
	for _, entry := range registry.Entries() {
		gotCounts[entry.Kind]++
	}
	if !reflect.DeepEqual(gotCounts, wantCounts) {
		t.Fatalf("registry counts = %#v, want %#v", gotCounts, wantCounts)
	}

	for _, keyword := range model.CommandKeywords() {
		entry, found := registry.Lookup(FeatureCommand, string(keyword))
		if !found {
			t.Fatalf("command %q is unclassified", keyword)
		}
		if entry.ParseStatus != ParseStatusParseable {
			t.Fatalf("command %q parse status = %q", keyword, entry.ParseStatus)
		}
	}
}

func TestRegistryCommandPlatformsComeFromDriverCapabilities(t *testing.T) {
	t.Parallel()

	documents := []struct {
		platform     string
		capabilities device.Capabilities
	}{
		{platform: drivercontract.PlatformAndroid, capabilities: android.DeclaredCapabilities()},
		{platform: drivercontract.PlatformIOSSimulator, capabilities: ios.DeclaredCapabilities()},
		{platform: drivercontract.PlatformWeb, capabilities: web.DeclaredCapabilities()},
	}
	registry := DefaultRegistry()
	for _, keyword := range model.CommandKeywords() {
		entry, found := registry.Lookup(FeatureCommand, string(keyword))
		if !found {
			t.Fatalf("command %q is absent", keyword)
		}
		want := make([]string, 0, len(documents))
		for _, document := range documents {
			if document.capabilities.Features[drivercontract.CommandFeature(string(keyword))] {
				want = append(want, document.platform)
			}
		}
		if !reflect.DeepEqual(entry.Platforms, want) {
			t.Errorf("command %q platforms = %v, want driver capability platforms %v", keyword, entry.Platforms, want)
		}
		wantStatus := RuntimeStatusPlannedV1
		if len(want) != 3 {
			wantStatus = RuntimeStatusPlatformLimited
		}
		if entry.RuntimeStatus != wantStatus {
			t.Errorf("command %q status = %q, want %q", keyword, entry.RuntimeStatus, wantStatus)
		}
	}
}

func TestRegistryDeclaresDocumentedCLIFlagAliases(t *testing.T) {
	t.Parallel()

	want := []string{
		"test.-c",
		"test.-e",
		"test.-p",
		"test.--no-reinstall-driver",
	}
	registry := DefaultRegistry()
	for _, name := range want {
		entry, found := registry.Lookup(FeatureCLIFlag, name)
		if !found {
			t.Errorf("documented CLI alias %q is unclassified", name)
			continue
		}
		if entry.ParseStatus != ParseStatusParseable || entry.RuntimeStatus != RuntimeStatusPlannedV1 {
			t.Errorf("documented CLI alias %q = %#v", name, entry)
		}
	}
}

func TestRegistryDeclaresImplementedAndDeferredFeatures(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	deferred := []struct {
		kind FeatureKind
		name string
	}{
		{FeatureDevicePlatform, "ios-physical"},
	}
	for _, test := range deferred {
		entry, found := registry.Lookup(test.kind, test.name)
		if !found {
			t.Fatalf("missing deferred entry %s/%s", test.kind, test.name)
		}
		if entry.ParseStatus != ParseStatusParseable || entry.RuntimeStatus != RuntimeStatusDeferred || entry.Reason == "" {
			t.Fatalf("deferred entry %s/%s = %#v", test.kind, test.name, entry)
		}
	}
	for _, name := range []string{"test.--headless", "test.--screen-size"} {
		entry, found := registry.Lookup(FeatureCLIFlag, name)
		if !found || entry.RuntimeStatus != RuntimeStatusPlatformLimited ||
			!reflect.DeepEqual(entry.Platforms, []string{"web"}) {
			t.Errorf("implemented Web flag %q = %#v", name, entry)
		}
	}
}

// The Web/CDP driver implements these three entries. Their capability status
// must allow preflight to admit the supported surface.
//
// css and url are platform-limited so selected-platform preflight can refuse
// them before Android or iOS driver mutation.
func TestRegistryDeclaresTheWebSurfaceAsImplemented(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	tests := []struct {
		kind FeatureKind
		name string
		want RuntimeStatus
	}{
		{FeatureSelector, "css", RuntimeStatusPlatformLimited},
		{FeatureConfigExtension, "url", RuntimeStatusPlatformLimited},
		{FeatureDevicePlatform, "web", RuntimeStatusPlannedV1},
		// The Chrome DevTools WebView merge is the same story on Android: the
		// engine enables it from the flow config, the driver forwards the
		// abstract CDP socket and merges the page.
		{FeatureConfigExtension, "androidWebViewHierarchy=devtools", RuntimeStatusPlatformLimited},
	}
	for _, test := range tests {
		entry, found := registry.Lookup(test.kind, test.name)
		if !found {
			t.Fatalf("missing entry %s/%s", test.kind, test.name)
		}
		if entry.ParseStatus != ParseStatusParseable || entry.RuntimeStatus != test.want || entry.Reason == "" {
			t.Fatalf("entry %s/%s = %#v, want runtime status %q", test.kind, test.name, entry, test.want)
		}
	}
}

func TestRegistryCommandStatusesMatchIndependentManifest(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("../../testdata/flows/command-manifest.json")
	if err != nil {
		t.Fatalf("read command manifest: %v", err)
	}
	var manifest struct {
		Entries []struct {
			Keyword       string `json:"keyword"`
			RuntimeStatus string `json:"runtimeStatus"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode command manifest: %v", err)
	}

	registry := DefaultRegistry()
	for _, expected := range manifest.Entries {
		entry, found := registry.Lookup(FeatureCommand, expected.Keyword)
		if !found {
			t.Fatalf("manifest command %q missing from registry", expected.Keyword)
		}
		if string(entry.RuntimeStatus) != expected.RuntimeStatus {
			t.Fatalf("command %q registry status = %q, manifest = %q", expected.Keyword, entry.RuntimeStatus, expected.RuntimeStatus)
		}
	}
}

func TestRegistryMatchesFrozenSupportContract(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("../../contracts/v0/support-registry.json")
	if err != nil {
		t.Fatalf("read support registry contract: %v", err)
	}
	var contract struct {
		SchemaVersion   int     `json:"schema_version"`
		ContractVersion string  `json:"contract_version"`
		RegistryVersion string  `json:"registry_version"`
		Entries         []Entry `json:"entries"`
	}
	if err := json.Unmarshal(data, &contract); err != nil {
		t.Fatalf("decode support registry contract: %v", err)
	}
	if contract.SchemaVersion != 1 || contract.ContractVersion != "v0" || contract.RegistryVersion != RegistryVersionV0 {
		t.Fatalf("support registry contract header = schema %d / contract %q / registry %q", contract.SchemaVersion, contract.ContractVersion, contract.RegistryVersion)
	}

	expected := make(map[string]Entry, len(contract.Entries))
	for _, entry := range contract.Entries {
		key := entryKey(entry.Kind, entry.Name)
		if _, duplicate := expected[key]; duplicate {
			t.Fatalf("duplicate support contract entry %s", key)
		}
		expected[key] = entry
	}
	actualEntries := DefaultRegistry().Entries()
	if len(actualEntries) != len(expected) {
		t.Fatalf("registry/contract entry counts = %d/%d", len(actualEntries), len(expected))
	}
	for _, actual := range actualEntries {
		key := entryKey(actual.Kind, actual.Name)
		want, found := expected[key]
		if !found {
			t.Errorf("registry entry %s is absent from frozen support contract", key)
			continue
		}
		if !reflect.DeepEqual(actual, want) {
			t.Errorf("registry entry %s = %#v, contract = %#v", key, actual, want)
		}
		delete(expected, key)
	}
	for key := range expected {
		t.Errorf("frozen support contract entry %s is absent from registry", key)
	}
}

func TestRegistryValidationFailsClosed(t *testing.T) {
	t.Parallel()

	base := DefaultRegistry().Entries()
	tests := []struct {
		name    string
		mutate  func([]Entry) []Entry
		wantErr string
	}{
		{
			name: "missing required entry",
			mutate: func(entries []Entry) []Entry {
				return entries[1:]
			},
			wantErr: "missing registry entry",
		},
		{
			name: "duplicate entry",
			mutate: func(entries []Entry) []Entry {
				return append(entries, entries[0])
			},
			wantErr: "duplicate registry entry",
		},
		{
			name: "omitted but planned",
			mutate: func(entries []Entry) []Entry {
				entries[0].ParseStatus = ParseStatusOmitted
				entries[0].RuntimeStatus = RuntimeStatusPlannedV1
				return entries
			},
			wantErr: "contradictory registry entry",
		},
		{
			name: "deferred without reason",
			mutate: func(entries []Entry) []Entry {
				for index := range entries {
					if entries[index].RuntimeStatus == RuntimeStatusDeferred {
						entries[index].Reason = ""
						break
					}
				}
				return entries
			},
			wantErr: "requires a reason",
		},
		{
			name: "platform limited without platforms",
			mutate: func(entries []Entry) []Entry {
				for index := range entries {
					if entries[index].RuntimeStatus == RuntimeStatusPlatformLimited {
						entries[index].Platforms = nil
						break
					}
				}
				return entries
			},
			wantErr: "requires platforms",
		},
		{
			name: "unknown platform",
			mutate: func(entries []Entry) []Entry {
				entries[0].Platforms = []string{"satellite"}
				return entries
			},
			wantErr: "unknown platform",
		},
		{
			name: "unclassified extra",
			mutate: func(entries []Entry) []Entry {
				return append(entries, Entry{
					Kind:          FeatureCommand,
					Name:          "futureMagic",
					ParseStatus:   ParseStatusParseable,
					RuntimeStatus: RuntimeStatusDeferred,
					Reason:        "not in v0",
				})
			},
			wantErr: "undeclared registry entry",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			entries := append([]Entry(nil), base...)
			registry := NewRegistry(RegistryVersionV0, test.mutate(entries))
			err := registry.Validate()
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Validate() error = %v, want substring %q", err, test.wantErr)
			}
		})
	}
}

func TestRegistryEntriesAndPlatformsAreDefensiveCopies(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	entries := registry.Entries()
	originalName := entries[0].Name
	entries[0].Name = "mutated"
	if len(entries[0].Platforms) > 0 {
		entries[0].Platforms[0] = "mutated"
	}

	entriesAgain := registry.Entries()
	if entriesAgain[0].Name != originalName {
		t.Fatalf("registry entry mutated through returned slice: %#v", entriesAgain[0])
	}
	for _, platform := range entriesAgain[0].Platforms {
		if platform == "mutated" {
			t.Fatalf("registry platform mutated through returned slice: %#v", entriesAgain[0])
		}
	}
}

func TestEntryJSONSchemaUsesStableFieldNames(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(Entry{
		Kind:          FeatureCommand,
		Name:          "tapOn",
		ParseStatus:   ParseStatusParseable,
		RuntimeStatus: RuntimeStatusPlannedV1,
		Platforms:     []string{"android", "ios-simulator"},
		Reason:        "v1 command",
	})
	if err != nil {
		t.Fatalf("marshal entry: %v", err)
	}
	want := `{"kind":"command","name":"tapOn","parseStatus":"parseable","runtimeStatus":"planned-v1","platforms":["android","ios-simulator"],"reason":"v1 command"}`
	if string(encoded) != want {
		t.Fatalf("entry JSON = %s, want %s", encoded, want)
	}
}
