package assets

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductionManifestContractIsVersionedAndComplete(t *testing.T) {
	t.Parallel()

	contents, err := os.ReadFile(filepath.Join("..", "..", "assets", "manifest.json"))
	if err != nil {
		t.Fatalf("read production asset manifest: %v", err)
	}
	manifest, err := ParseManifest(contents)
	if err != nil {
		t.Fatalf("ParseManifest() error = %v", err)
	}
	if manifest.SchemaVersion != ManifestSchemaVersion {
		t.Fatalf("schema version = %q, want %q", manifest.SchemaVersion, ManifestSchemaVersion)
	}
	if manifest.ManifestVersion != "0.0.0-contract.1" {
		t.Fatalf("manifest version = %q, want contract version", manifest.ManifestVersion)
	}
	if len(manifest.Assets) != 2 {
		t.Fatalf("asset count = %d, want two representative contract records", len(manifest.Assets))
	}

	seen := map[Platform]bool{}
	for _, asset := range manifest.Assets {
		seen[asset.Platform] = true
		if asset.Status != AssetStatusRepresentative {
			t.Errorf("asset %q status = %q, want explicitly non-release representative", asset.ID, asset.Status)
		}
		if asset.HostOS == "" || asset.HostArch == "" || asset.HostVersion == "" || asset.AssetVersion == "" {
			t.Errorf("asset %q omits host or asset compatibility identity", asset.ID)
		}
		if asset.AssetHash == "" || asset.Archive.SHA256 == "" || asset.Archive.Size <= 0 || asset.Archive.UncompressedSize <= 0 {
			t.Errorf("asset %q omits archive hash or size metadata", asset.ID)
		}
		if asset.Identity.Value == "" || asset.Identity.Path == "" || asset.Identity.Kind == "" {
			t.Errorf("asset %q omits identity metadata", asset.ID)
		}
		if len(asset.Files) == 0 {
			t.Errorf("asset %q omits file hash/mode metadata", asset.ID)
		}
	}
	if !seen[PlatformAndroid] || !seen[PlatformIOSSimulator] {
		t.Fatalf("manifest platforms = %v, want Android and iOS simulator", seen)
	}

	fixtures := make(map[Platform]Fixture)
	for _, fixture := range RepresentativeFixtures() {
		fixtures[fixture.Manifest.Platform] = fixture
	}
	for _, asset := range manifest.Assets {
		fixture, ok := fixtures[asset.Platform]
		if !ok {
			t.Fatalf("no embedded representative payload for %s", asset.Platform)
		}
		file := asset.Files[0]
		if asset.Archive.SHA256 != fixture.Manifest.CompressedSHA256 || asset.Archive.Size != fixture.Manifest.CompressedSize {
			t.Errorf("%s compressed metadata does not match embedded payload", asset.Platform)
		}
		if asset.Archive.UncompressedSHA256 != fixture.Manifest.PayloadSHA256 || asset.Archive.UncompressedSize != fixture.Manifest.PayloadSize {
			t.Errorf("%s uncompressed metadata does not match embedded payload", asset.Platform)
		}
		if file.Path != fixture.Manifest.ArtifactName || file.SHA256 != fixture.Manifest.PayloadSHA256 || file.Size != fixture.Manifest.PayloadSize {
			t.Errorf("%s file metadata does not match embedded payload", asset.Platform)
		}
		if file.Mode != modeString(fixture.Manifest.Mode) {
			t.Errorf("%s mode = %q, want embedded mode %q", asset.Platform, file.Mode, modeString(fixture.Manifest.Mode))
		}
		if asset.Identity.Value != fixture.Manifest.Identity || asset.Identity.Kind != fixture.Manifest.VerificationKind {
			t.Errorf("%s identity metadata does not match embedded payload", asset.Platform)
		}
		if got := hashHex(fixture.Compressed); got != asset.Archive.SHA256 {
			t.Errorf("%s embedded compressed hash = %s, want manifest %s", asset.Platform, got, asset.Archive.SHA256)
		}
	}
}

func TestResolveRejectsRepresentativeRecords(t *testing.T) {
	t.Parallel()

	contents, err := os.ReadFile(filepath.Join("..", "..", "assets", "manifest.json"))
	if err != nil {
		t.Fatalf("read production asset manifest: %v", err)
	}
	manifest, err := ParseManifest(contents)
	if err != nil {
		t.Fatalf("ParseManifest() error = %v", err)
	}
	asset := manifest.Assets[0]
	_, err = Resolve(manifest, Runtime{
		HostVersion: asset.HostVersion,
		HostOS:      asset.HostOS,
		HostArch:    asset.HostArch,
		AndroidAPI:  asset.Compatibility.AndroidAPI.Min,
	}, Request{ID: asset.ID, AssetVersion: asset.AssetVersion, Platform: asset.Platform})
	if !errors.Is(err, ErrAssetNotReleaseEligible) {
		t.Fatalf("Resolve() error = %v, want ErrAssetNotReleaseEligible", err)
	}
}

func TestResolveEnforcesHostAndRuntimeCompatibilityWithoutFilesystemMutation(t *testing.T) {
	t.Parallel()

	asset := validReleaseAsset()
	manifest := Manifest{
		SchemaVersion:   ManifestSchemaVersion,
		ManifestVersion: "test-v1",
		Assets:          []Asset{asset},
	}
	runtime := Runtime{
		HostVersion: asset.HostVersion,
		HostOS:      asset.HostOS,
		HostArch:    asset.HostArch,
		AndroidAPI:  asset.Compatibility.AndroidAPI.Min,
	}
	request := Request{ID: asset.ID, AssetVersion: asset.AssetVersion, Platform: asset.Platform}

	resolved, err := Resolve(manifest, runtime, request)
	if err != nil {
		t.Fatalf("Resolve() compatible asset error = %v", err)
	}
	if resolved.Asset.ID != asset.ID || !resolved.resolved {
		t.Fatalf("resolved asset = %#v, want validated %q", resolved, asset.ID)
	}

	for _, test := range []struct {
		name    string
		runtime Runtime
	}{
		{name: "android API below minimum", runtime: withAndroidAPI(runtime, asset.Compatibility.AndroidAPI.Min-1)},
		{name: "android API above maximum", runtime: withAndroidAPI(runtime, asset.Compatibility.AndroidAPI.Max+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Resolve(manifest, test.runtime, request)
			if !errors.Is(err, ErrNoCompatibleAsset) {
				t.Fatalf("Resolve() error = %v, want ErrNoCompatibleAsset", err)
			}
		})
	}
}

func TestResolveEnforcesIOSVersionRangesNumerically(t *testing.T) {
	t.Parallel()

	asset := validReleaseAsset()
	asset.ID = "ios-runner"
	asset.HostOS = "darwin"
	asset.HostArch = "arm64"
	asset.Platform = PlatformIOSSimulator
	asset.Identity.Kind = VerificationBundleSignatureIdentity
	asset.Identity.Value = "dev.nohavewho.flowbaton.driver"
	asset.Compatibility.AndroidAPI = IntegerRange{}
	asset.Compatibility.Xcode = VersionRange{Min: "16.0", Max: "26.10"}
	asset.Compatibility.IOSRuntime = VersionRange{Min: "17.0", Max: "26.2"}
	manifest := Manifest{SchemaVersion: ManifestSchemaVersion, ManifestVersion: "test-v1", Assets: []Asset{asset}}
	request := Request{ID: asset.ID, AssetVersion: asset.AssetVersion, Platform: asset.Platform}
	base := Runtime{
		HostVersion:       asset.HostVersion,
		HostOS:            asset.HostOS,
		HostArch:          asset.HostArch,
		XcodeVersion:      "26.2",
		IOSRuntimeVersion: "18.5",
	}

	if _, err := Resolve(manifest, base, request); err != nil {
		t.Fatalf("Resolve() compatible iOS runtime error = %v", err)
	}
	tooNew := base
	tooNew.IOSRuntimeVersion = "26.10"
	if _, err := Resolve(manifest, tooNew, request); !errors.Is(err, ErrNoCompatibleAsset) {
		t.Fatalf("Resolve() out-of-range iOS runtime error = %v, want ErrNoCompatibleAsset", err)
	}
	tooOld := base
	tooOld.XcodeVersion = "15.4"
	if _, err := Resolve(manifest, tooOld, request); !errors.Is(err, ErrNoCompatibleAsset) {
		t.Fatalf("Resolve() out-of-range Xcode error = %v, want ErrNoCompatibleAsset", err)
	}
}

func TestParseManifestRejectsUnsafeOrContradictoryMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{name: "empty assets", mutate: func(m *Manifest) { m.Assets = nil }},
		{name: "unsafe manifest version", mutate: func(m *Manifest) { m.ManifestVersion = "../escape" }},
		{name: "unknown status", mutate: func(m *Manifest) { m.Assets[0].Status = "draft" }},
		{name: "unsafe host version", mutate: func(m *Manifest) { m.Assets[0].HostVersion = "../escape" }},
		{name: "unsupported host OS", mutate: func(m *Manifest) { m.Assets[0].HostOS = "plan9" }},
		{name: "unsupported host architecture", mutate: func(m *Manifest) { m.Assets[0].HostArch = "386" }},
		{name: "unsupported platform", mutate: func(m *Manifest) { m.Assets[0].Platform = "web" }},
		{name: "non-canonical hash", mutate: func(m *Manifest) { m.Assets[0].Archive.SHA256 = strings.Repeat("A", 64) }},
		{name: "asset hash mismatch", mutate: func(m *Manifest) { m.Assets[0].AssetHash = strings.Repeat("b", 64) }},
		{name: "empty archive size", mutate: func(m *Manifest) { m.Assets[0].Archive.Size = 0 }},
		{name: "unsafe file path", mutate: func(m *Manifest) { m.Assets[0].Files[0].Path = "../escape" }},
		{name: "non-canonical file hash", mutate: func(m *Manifest) { m.Assets[0].Files[0].SHA256 = strings.Repeat("A", 64) }},
		{name: "negative file size", mutate: func(m *Manifest) { m.Assets[0].Files[0].Size = -1 }},
		{name: "non-canonical mode", mutate: func(m *Manifest) { m.Assets[0].Files[0].Mode = "755" }},
		{name: "gzip metadata mismatch", mutate: func(m *Manifest) { m.Assets[0].Files[0].Size++ }},
		{name: "identity path undeclared", mutate: func(m *Manifest) { m.Assets[0].Identity.Path = "missing.apk" }},
		{name: "wrong Android identity kind", mutate: func(m *Manifest) { m.Assets[0].Identity.Kind = VerificationBundleSignatureIdentity }},
		{name: "missing Android bounds", mutate: func(m *Manifest) { m.Assets[0].Compatibility.AndroidAPI = IntegerRange{} }},
		{name: "reversed Android bounds", mutate: func(m *Manifest) { m.Assets[0].Compatibility.AndroidAPI = IntegerRange{Min: 30, Max: 21} }},
		{name: "Android declares iOS compatibility", mutate: func(m *Manifest) { m.Assets[0].Compatibility.Xcode = VersionRange{Min: "16.0", Max: "26.2"} }},
		{name: "iOS declares Android compatibility", mutate: func(m *Manifest) {
			convertAssetToIOS(&m.Assets[0])
			m.Assets[0].Compatibility.AndroidAPI = IntegerRange{Min: 21, Max: 36}
		}},
		{name: "iOS missing runtime range", mutate: func(m *Manifest) {
			convertAssetToIOS(&m.Assets[0])
			m.Assets[0].Compatibility.IOSRuntime = VersionRange{}
		}},
		{name: "iOS malformed version range", mutate: func(m *Manifest) {
			convertAssetToIOS(&m.Assets[0])
			m.Assets[0].Compatibility.Xcode = VersionRange{Min: "016.0", Max: "26.2"}
		}},
		{name: "unknown archive format", mutate: func(m *Manifest) { m.Assets[0].Archive.Format = "zip" }},
		{name: "gzip has multiple files", mutate: func(m *Manifest) { m.Assets[0].Files = append(m.Assets[0].Files, m.Assets[0].Files[0]) }},
		{name: "compressed Android budget exceeded", mutate: func(m *Manifest) { m.Assets[0].Archive.Size = 20*1024*1024 + 1 }},
		{name: "duplicate coordinate", mutate: func(m *Manifest) { m.Assets = append(m.Assets, m.Assets[0]) }},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			manifest := validReleaseManifest()
			test.mutate(&manifest)
			contents, err := json.Marshal(manifest)
			if err != nil {
				t.Fatalf("json.Marshal: %v", err)
			}
			if _, err := ParseManifest(contents); !errors.Is(err, ErrInvalidAssetManifest) {
				t.Fatalf("ParseManifest() error = %v, want ErrInvalidAssetManifest", err)
			}
		})
	}
}

func TestParseManifestRejectsUnknownAndTrailingJSON(t *testing.T) {
	t.Parallel()

	valid, err := json.Marshal(validReleaseManifest())
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	withUnknown := strings.Replace(string(valid), `"manifest_version":`, `"unknown":true,"manifest_version":`, 1)
	if _, err := ParseManifest([]byte(withUnknown)); !errors.Is(err, ErrInvalidAssetManifest) {
		t.Fatalf("ParseManifest() unknown-field error = %v, want ErrInvalidAssetManifest", err)
	}
	if _, err := ParseManifest(append(valid, []byte(` {}`)...)); !errors.Is(err, ErrInvalidAssetManifest) {
		t.Fatalf("ParseManifest() trailing-value error = %v, want ErrInvalidAssetManifest", err)
	}
}

func validReleaseManifest() Manifest {
	return Manifest{
		SchemaVersion:   ManifestSchemaVersion,
		ManifestVersion: "test-v1",
		Assets:          []Asset{validReleaseAsset()},
	}
}

func validReleaseAsset() Asset {
	payloadHash := strings.Repeat("a", 64)
	return Asset{
		ID:           "android-agent",
		Status:       AssetStatusRelease,
		HostVersion:  "0.1.0",
		AssetVersion: "1.0.0",
		HostOS:       "linux",
		HostArch:     "amd64",
		Platform:     PlatformAndroid,
		AssetHash:    payloadHash,
		Archive: AssetArchive{
			Format:             ArchiveFormatGZIP,
			SHA256:             strings.Repeat("c", 64),
			Size:               128,
			UncompressedSHA256: payloadHash,
			UncompressedSize:   256,
		},
		Files: []AssetFile{{Path: "agent.apk", SHA256: payloadHash, Size: 256, Mode: "0644"}},
		Identity: AssetIdentity{
			Kind:  VerificationPackageIdentity,
			Value: "dev.nohavewho.flowbaton",
			Path:  "agent.apk",
		},
		Compatibility: Compatibility{AndroidAPI: IntegerRange{Min: 21, Max: 36}},
	}
}

func withAndroidAPI(runtime Runtime, api int) Runtime {
	runtime.AndroidAPI = api
	return runtime
}

func convertAssetToIOS(asset *Asset) {
	asset.HostOS = "darwin"
	asset.HostArch = "arm64"
	asset.Platform = PlatformIOSSimulator
	asset.Identity.Kind = VerificationBundleSignatureIdentity
	asset.Identity.Value = "dev.nohavewho.flowbaton.driver"
	asset.Compatibility = Compatibility{
		Xcode:      VersionRange{Min: "16.0", Max: "26.2"},
		IOSRuntime: VersionRange{Min: "17.0", Max: "26.0"},
	}
}

func modeString(mode os.FileMode) string {
	return fmt.Sprintf("%04o", mode.Perm())
}
