package v0

import (
	"encoding/json"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/assets"
	"github.com/larchwave/flowbaton/internal/capability"
	"github.com/larchwave/flowbaton/internal/model"
)

type astContractSnapshot struct {
	SchemaVersion   int                      `json:"schema_version"`
	ContractVersion string                   `json:"contract_version"`
	AST             model.ContractDescriptor `json:"ast"`
}

type supportRegistrySnapshot struct {
	SchemaVersion   int                `json:"schema_version"`
	ContractVersion string             `json:"contract_version"`
	RegistryVersion string             `json:"registry_version"`
	Entries         []capability.Entry `json:"entries"`
}

type assetManifestSchemaSnapshot struct {
	SchemaVersion         int                   `json:"schema_version"`
	ContractVersion       string                `json:"contract_version"`
	ManifestSchemaVersion string                `json:"manifest_schema_version"`
	Types                 []assetTypeDescriptor `json:"types"`
	Enums                 []assetEnumDescriptor `json:"enums"`
}

type assetTypeDescriptor struct {
	Name   string                 `json:"name"`
	Fields []assetFieldDescriptor `json:"fields"`
}

type assetFieldDescriptor struct {
	Name     string `json:"name"`
	JSONName string `json:"json_name"`
	GoType   string `json:"go_type"`
}

type assetEnumDescriptor struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

func TestASTSnapshotMatchesLiveGoContract(t *testing.T) {
	want := astContractSnapshot{
		SchemaVersion:   1,
		ContractVersion: "v0",
		AST:             model.ContractV0(),
	}
	var got astContractSnapshot
	readStrictJSON(t, "ast.json", &got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ast.json does not match model.ContractV0()\n got: %#v\nwant: %#v", got, want)
	}
}

func TestSupportRegistrySnapshotMatchesLiveGoContract(t *testing.T) {
	registry := capability.DefaultRegistry()
	if err := registry.Validate(); err != nil {
		t.Fatalf("live support registry is invalid: %v", err)
	}
	want := supportRegistrySnapshot{
		SchemaVersion:   1,
		ContractVersion: "v0",
		RegistryVersion: registry.Version(),
		Entries:         registry.Entries(),
	}
	var got supportRegistrySnapshot
	readStrictJSON(t, "support-registry.json", &got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("support-registry.json does not match capability.DefaultRegistry()\n got: %#v\nwant: %#v", got, want)
	}
}

func TestAssetManifestSchemaSnapshotMatchesLiveGoContract(t *testing.T) {
	want := assetManifestSchemaSnapshot{
		SchemaVersion:         1,
		ContractVersion:       "v0",
		ManifestSchemaVersion: assets.ManifestSchemaVersion,
		Types: []assetTypeDescriptor{
			describeAssetType[assets.Manifest](),
			describeAssetType[assets.Asset](),
			describeAssetType[assets.AssetArchive](),
			describeAssetType[assets.AssetFile](),
			describeAssetType[assets.AssetIdentity](),
			describeAssetType[assets.Compatibility](),
			describeAssetType[assets.IntegerRange](),
			describeAssetType[assets.VersionRange](),
		},
		Enums: []assetEnumDescriptor{
			{Name: "AssetStatus", Values: []string{string(assets.AssetStatusRepresentative), string(assets.AssetStatusRelease)}},
			{Name: "ArchiveFormat", Values: []string{string(assets.ArchiveFormatGZIP), string(assets.ArchiveFormatTarGZIP)}},
			{Name: "Platform", Values: []string{string(assets.PlatformAndroid), string(assets.PlatformIOSSimulator)}},
			{Name: "VerificationKind", Values: []string{string(assets.VerificationPackageIdentity), string(assets.VerificationBundleSignatureIdentity)}},
		},
	}
	var got assetManifestSchemaSnapshot
	readStrictJSON(t, "asset-manifest-schema.json", &got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("asset-manifest-schema.json does not match the exported assets manifest schema\n got: %#v\nwant: %#v", got, want)
	}
}

func describeAssetType[T any]() assetTypeDescriptor {
	typeOf := reflect.TypeOf((*T)(nil)).Elem()
	fields := make([]assetFieldDescriptor, 0, typeOf.NumField())
	for index := 0; index < typeOf.NumField(); index++ {
		field := typeOf.Field(index)
		fields = append(fields, assetFieldDescriptor{
			Name:     field.Name,
			JSONName: field.Tag.Get("json"),
			GoType:   field.Type.String(),
		})
	}
	return assetTypeDescriptor{Name: typeOf.Name(), Fields: fields}
}

func readStrictJSON(t *testing.T, path string, destination any) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			t.Fatalf("decode %s: trailing JSON value", path)
		}
		t.Fatalf("decode %s trailing data: %v", path, err)
	}
}
