package v0

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type contractSetManifest struct {
	SchemaVersion        int                  `json:"schema_version"`
	ContractVersion      string               `json:"contract_version"`
	ContractUpdatePolicy contractUpdatePolicy `json:"contract_update_policy"`
	Artifacts            []contractArtifact   `json:"artifacts"`
}

type contractUpdatePolicy struct {
	ChangeMode                        string              `json:"change_mode"`
	BreakingChangeRequiresVersionBump bool                `json:"breaking_change_requires_version_bump"`
	CompatibleChangeRefreshesAll      bool                `json:"compatible_change_refreshes_all"`
	UnlistedArtifactPolicy            string              `json:"unlisted_artifact_policy"`
	RequiredLanguages                 map[string][]string `json:"required_languages"`
}

type contractArtifact struct {
	ID        string `json:"id"`
	Component string `json:"component"`
	Language  string `json:"language"`
	Path      string `json:"path"`
	Coverage  string `json:"coverage"`
	SHA256    string `json:"sha256"`
}

func TestV0ContractSetRejectsPartialArtifactDrift(t *testing.T) {
	data, err := os.ReadFile("contract-set.json")
	if err != nil {
		t.Fatalf("read contract-set manifest: %v", err)
	}
	var manifest contractSetManifest
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		t.Fatalf("decode contract-set manifest: %v", err)
	}
	if manifest.SchemaVersion != 1 || manifest.ContractVersion != "v0" {
		t.Fatalf("contract set version = schema %d contract %q, want 1/v0", manifest.SchemaVersion, manifest.ContractVersion)
	}
	wantPolicy := contractUpdatePolicy{
		ChangeMode:                        "atomic",
		BreakingChangeRequiresVersionBump: true,
		CompatibleChangeRefreshesAll:      true,
		UnlistedArtifactPolicy:            "fail",
		RequiredLanguages: map[string][]string{
			"ast":             {"machine-readable"},
			"support":         {"machine-readable"},
			"asset-manifest":  {"machine-readable"},
			"parser-commands": {"machine-readable"},
			"driver":          {"go", "machine-readable"},
			"android-grpc":    {"proto", "machine-readable", "jvm"},
			"ios-http":        {"go", "machine-readable", "swift"},
		},
	}
	if !reflect.DeepEqual(manifest.ContractUpdatePolicy, wantPolicy) {
		t.Fatalf("contract update policy = %#v, want %#v", manifest.ContractUpdatePolicy, wantPolicy)
	}

	wantArtifacts := map[string]struct {
		component string
		language  string
		path      string
		coverage  string
	}{
		"driver-go-source":              {"driver", "go", "internal/device/driver.go", "contract-complete"},
		"driver-json-descriptor":        {"driver", "machine-readable", "contracts/v0/driver.json", "contract-complete"},
		"ast-json-snapshot":             {"ast", "machine-readable", "contracts/v0/ast.json", "contract-complete"},
		"support-json-snapshot":         {"support", "machine-readable", "contracts/v0/support-registry.json", "contract-complete"},
		"asset-schema-snapshot":         {"asset-manifest", "machine-readable", "contracts/v0/asset-manifest-schema.json", "contract-complete"},
		"parser-commands-json-snapshot": {"parser-commands", "machine-readable", "contracts/v0/parser-commands.json", "contract-complete"},
		"android-canonical-proto":       {"android-grpc", "proto", "proto/flowbaton_android.proto", "contract-complete"},
		"android-json-descriptor":       {"android-grpc", "machine-readable", "contracts/v0/android-grpc.json", "contract-complete"},
		"android-jvm-declarations":      {"android-grpc", "jvm", "drivers/android/core/src/main/java/dev/larchwave/flowbaton/driver/contract/AndroidWireContractV0.java", "contract-complete"},
		"android-g001-runtime":          {"android-grpc", "jvm-runtime", "drivers/android/core/src/main/java/dev/larchwave/flowbaton/driver/GrpcDeviceServer.java", "partial-g001-deviceInfo-only"},
		"ios-go-routes":                 {"ios-http", "go", "internal/device/ios_http.go", "contract-complete"},
		"ios-json-descriptor":           {"ios-http", "machine-readable", "contracts/v0/ios-http.json", "contract-complete"},
		"ios-swift-declarations":        {"ios-http", "swift", "drivers/ios/Sources/FlowBatonIOSRunner/WireContractV0.swift", "contract-complete"},
		"ios-g001-runtime":              {"ios-http", "swift-runtime", "drivers/ios/Sources/FlowBatonIOSRunner/StatusEndpoint.swift", "partial-g001-status-only"},
	}
	if len(manifest.Artifacts) != len(wantArtifacts) {
		t.Fatalf("artifact count = %d, want %d", len(manifest.Artifacts), len(wantArtifacts))
	}

	root := filepath.Join("..", "..")
	seenIDs := make(map[string]bool, len(manifest.Artifacts))
	seenPaths := make(map[string]bool, len(manifest.Artifacts))
	completeLanguages := make(map[string]map[string]bool)
	for _, artifact := range manifest.Artifacts {
		want, exists := wantArtifacts[artifact.ID]
		if !exists {
			t.Fatalf("unclassified contract artifact %q", artifact.ID)
		}
		if seenIDs[artifact.ID] {
			t.Fatalf("duplicate contract artifact ID %q", artifact.ID)
		}
		seenIDs[artifact.ID] = true
		if seenPaths[artifact.Path] {
			t.Fatalf("duplicate contract artifact path %q", artifact.Path)
		}
		seenPaths[artifact.Path] = true
		if artifact.Component != want.component || artifact.Language != want.language || artifact.Path != want.path || artifact.Coverage != want.coverage {
			t.Fatalf("artifact %s classification = %#v, want component=%q language=%q path=%q coverage=%q", artifact.ID, artifact, want.component, want.language, want.path, want.coverage)
		}
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(artifact.Path)))
		if err != nil {
			t.Fatalf("read contract artifact %s: %v", artifact.ID, err)
		}
		digest := sha256.Sum256(contents)
		gotHash := hex.EncodeToString(digest[:])
		if artifact.SHA256 != gotHash {
			t.Fatalf("contract artifact %s drifted: manifest SHA-256 %s, current %s; refresh the complete contract set", artifact.ID, artifact.SHA256, gotHash)
		}
		if artifact.Coverage == "contract-complete" {
			if completeLanguages[artifact.Component] == nil {
				completeLanguages[artifact.Component] = make(map[string]bool)
			}
			completeLanguages[artifact.Component][artifact.Language] = true
		}
	}
	for component, languages := range manifest.ContractUpdatePolicy.RequiredLanguages {
		for _, language := range languages {
			if !completeLanguages[component][language] {
				t.Fatalf("component %s lacks contract-complete %s artifact", component, language)
			}
		}
	}
}

func TestV0MachineDescriptorsShareOneContractVersion(t *testing.T) {
	for _, name := range []string{"driver.json", "ast.json", "support-registry.json", "asset-manifest-schema.json", "parser-commands.json", "android-grpc.json", "ios-http.json"} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		var header struct {
			SchemaVersion   int    `json:"schema_version"`
			ContractVersion string `json:"contract_version"`
		}
		if err := json.Unmarshal(data, &header); err != nil {
			t.Fatalf("decode %s header: %v", name, err)
		}
		if header.SchemaVersion != 1 || header.ContractVersion != "v0" {
			t.Fatalf("%s version = schema %d contract %q, want 1/v0", name, header.SchemaVersion, header.ContractVersion)
		}
	}
}
