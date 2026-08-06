package assets

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"
)

const ManifestSchemaVersion = "flowbaton.assets.v0"

const (
	androidCompressedBudget = int64(20 * 1024 * 1024)
	iosCompressedBudget     = int64(25 * 1024 * 1024)
)

var (
	safeTokenPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	hashPattern      = regexp.MustCompile(`^[0-9a-f]{64}$`)
	modePattern      = regexp.MustCompile(`^0[0-7]{3}$`)
)

type AssetStatus string

const (
	AssetStatusRepresentative AssetStatus = "representative-not-release-artifact"
	AssetStatusRelease        AssetStatus = "release"
)

type ArchiveFormat string

const (
	ArchiveFormatGZIP    ArchiveFormat = "gzip"
	ArchiveFormatTarGZIP ArchiveFormat = "tar+gzip"
)

var (
	ErrInvalidAssetManifest    = errors.New("invalid asset manifest")
	ErrNoCompatibleAsset       = errors.New("no compatible asset")
	ErrAssetNotReleaseEligible = errors.New("asset is not release eligible")
)

type Manifest struct {
	SchemaVersion   string  `json:"schema_version"`
	ManifestVersion string  `json:"manifest_version"`
	Assets          []Asset `json:"assets"`
}

type Asset struct {
	ID            string        `json:"id"`
	Status        AssetStatus   `json:"status"`
	HostVersion   string        `json:"host_version"`
	AssetVersion  string        `json:"asset_version"`
	HostOS        string        `json:"host_os"`
	HostArch      string        `json:"host_arch"`
	Platform      Platform      `json:"platform"`
	AssetHash     string        `json:"asset_hash"`
	Archive       AssetArchive  `json:"archive"`
	Files         []AssetFile   `json:"files"`
	Identity      AssetIdentity `json:"identity"`
	Compatibility Compatibility `json:"compatibility"`
}

type AssetArchive struct {
	Format             ArchiveFormat `json:"format"`
	SHA256             string        `json:"sha256"`
	Size               int64         `json:"size"`
	UncompressedSHA256 string        `json:"uncompressed_sha256"`
	UncompressedSize   int64         `json:"uncompressed_size"`
}

type AssetFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Mode   string `json:"mode"`
}

type AssetIdentity struct {
	Kind  VerificationKind `json:"kind"`
	Value string           `json:"value"`
	Path  string           `json:"path"`
}

type Compatibility struct {
	AndroidAPI IntegerRange `json:"android_api"`
	Xcode      VersionRange `json:"xcode"`
	IOSRuntime VersionRange `json:"ios_runtime"`
}

type IntegerRange struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

type VersionRange struct {
	Min string `json:"min"`
	Max string `json:"max"`
}

type Runtime struct {
	HostVersion       string
	HostOS            string
	HostArch          string
	AndroidAPI        int
	XcodeVersion      string
	IOSRuntimeVersion string
}

type Request struct {
	ID           string
	AssetVersion string
	Platform     Platform
}

type ResolvedAsset struct {
	Asset           Asset
	ManifestVersion string
	Runtime         Runtime
	resolved        bool
}

func ParseManifest(contents []byte) (Manifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("%w: decode: %v", ErrInvalidAssetManifest, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Manifest{}, err
	}
	if manifest.SchemaVersion != ManifestSchemaVersion || manifest.ManifestVersion == "" {
		return Manifest{}, fmt.Errorf("%w: unsupported schema or empty manifest version", ErrInvalidAssetManifest)
	}
	if err := validateAssetManifest(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("%w: multiple JSON values", ErrInvalidAssetManifest)
		}
		return fmt.Errorf("%w: trailing data: %v", ErrInvalidAssetManifest, err)
	}
	return nil
}

func Resolve(manifest Manifest, runtime Runtime, request Request) (ResolvedAsset, error) {
	if err := validateAssetManifest(manifest); err != nil {
		return ResolvedAsset{}, err
	}
	for _, asset := range manifest.Assets {
		if asset.ID != request.ID || asset.AssetVersion != request.AssetVersion || asset.Platform != request.Platform {
			continue
		}
		if asset.HostVersion != runtime.HostVersion || asset.HostOS != runtime.HostOS || asset.HostArch != runtime.HostArch {
			continue
		}
		if asset.Status != AssetStatusRelease {
			return ResolvedAsset{}, fmt.Errorf("%w: %s@%s", ErrAssetNotReleaseEligible, asset.ID, asset.AssetVersion)
		}
		if !runtimeCompatible(asset, runtime) {
			continue
		}
		return ResolvedAsset{Asset: asset, ManifestVersion: manifest.ManifestVersion, Runtime: runtime, resolved: true}, nil
	}
	return ResolvedAsset{}, fmt.Errorf("%w: %s@%s for %s/%s", ErrNoCompatibleAsset, request.ID, request.AssetVersion, runtime.HostOS, runtime.HostArch)
}

func validateAssetManifest(manifest Manifest) error {
	if manifest.SchemaVersion != ManifestSchemaVersion || !safeToken(manifest.ManifestVersion) {
		return fmt.Errorf("%w: unsupported schema or unsafe manifest version", ErrInvalidAssetManifest)
	}
	if len(manifest.Assets) == 0 {
		return fmt.Errorf("%w: assets must not be empty", ErrInvalidAssetManifest)
	}
	coordinates := make(map[string]struct{}, len(manifest.Assets))
	for index, asset := range manifest.Assets {
		if err := validateAsset(asset); err != nil {
			return fmt.Errorf("%w: asset[%d]: %v", ErrInvalidAssetManifest, index, err)
		}
		coordinate := strings.Join([]string{
			asset.ID,
			asset.AssetVersion,
			string(asset.Platform),
			asset.HostVersion,
			asset.HostOS,
			asset.HostArch,
		}, "\x00")
		if _, duplicate := coordinates[coordinate]; duplicate {
			return fmt.Errorf("%w: duplicate asset coordinate %s@%s", ErrInvalidAssetManifest, asset.ID, asset.AssetVersion)
		}
		coordinates[coordinate] = struct{}{}
	}
	return nil
}

func validateAsset(asset Asset) error {
	for label, value := range map[string]string{
		"id":            asset.ID,
		"host_version":  asset.HostVersion,
		"asset_version": asset.AssetVersion,
		"host_os":       asset.HostOS,
		"host_arch":     asset.HostArch,
	} {
		if !safeToken(value) {
			return fmt.Errorf("unsafe or empty %s %q", label, value)
		}
	}
	if asset.Status != AssetStatusRepresentative && asset.Status != AssetStatusRelease {
		return fmt.Errorf("unknown status %q", asset.Status)
	}
	if asset.HostOS != "darwin" && asset.HostOS != "linux" && asset.HostOS != "windows" {
		return fmt.Errorf("unsupported host OS %q", asset.HostOS)
	}
	if asset.HostArch != "amd64" && asset.HostArch != "arm64" {
		return fmt.Errorf("unsupported host architecture %q", asset.HostArch)
	}
	if asset.Platform != PlatformAndroid && asset.Platform != PlatformIOSSimulator {
		return fmt.Errorf("unsupported platform %q", asset.Platform)
	}
	if !canonicalHash(asset.AssetHash) || !canonicalHash(asset.Archive.SHA256) || !canonicalHash(asset.Archive.UncompressedSHA256) {
		return errors.New("asset and archive hashes must be lowercase SHA-256")
	}
	if asset.AssetHash != asset.Archive.UncompressedSHA256 {
		return errors.New("asset hash must equal the uncompressed archive hash")
	}
	if asset.Archive.Size <= 0 || asset.Archive.UncompressedSize <= 0 {
		return errors.New("archive sizes must be positive")
	}
	if asset.Archive.Format != ArchiveFormatGZIP && asset.Archive.Format != ArchiveFormatTarGZIP {
		return fmt.Errorf("unsupported archive format %q", asset.Archive.Format)
	}
	budget := androidCompressedBudget
	if asset.Platform == PlatformIOSSimulator {
		budget = iosCompressedBudget
	}
	if asset.Archive.Size > budget {
		return fmt.Errorf("compressed archive size %d exceeds %d-byte platform budget", asset.Archive.Size, budget)
	}
	if len(asset.Files) == 0 {
		return errors.New("files must not be empty")
	}
	if asset.Archive.Format == ArchiveFormatGZIP && len(asset.Files) != 1 {
		return errors.New("gzip archives must describe exactly one file")
	}
	declaredFiles := make(map[string]struct{}, len(asset.Files))
	for _, file := range asset.Files {
		if !validAssetPath(file.Path) {
			return fmt.Errorf("unsafe file path %q", file.Path)
		}
		if _, duplicate := declaredFiles[file.Path]; duplicate {
			return fmt.Errorf("duplicate file path %q", file.Path)
		}
		declaredFiles[file.Path] = struct{}{}
		if !canonicalHash(file.SHA256) {
			return fmt.Errorf("file %q hash must be lowercase SHA-256", file.Path)
		}
		if file.Size < 0 {
			return fmt.Errorf("file %q size must not be negative", file.Path)
		}
		if !modePattern.MatchString(file.Mode) || file.Mode == "0000" {
			return fmt.Errorf("file %q mode %q is not canonical", file.Path, file.Mode)
		}
	}
	if asset.Archive.Format == ArchiveFormatGZIP {
		file := asset.Files[0]
		if file.SHA256 != asset.Archive.UncompressedSHA256 || file.Size != asset.Archive.UncompressedSize {
			return errors.New("gzip file hash and size must equal uncompressed archive metadata")
		}
	}
	if asset.Identity.Value == "" || !validAssetPath(asset.Identity.Path) {
		return errors.New("identity value and safe path are required")
	}
	if _, declared := declaredFiles[asset.Identity.Path]; !declared {
		identityDirectory := asset.Identity.Path + "/"
		containsDeclaredFile := false
		for filePath := range declaredFiles {
			if strings.HasPrefix(filePath, identityDirectory) {
				containsDeclaredFile = true
				break
			}
		}
		if !containsDeclaredFile {
			return fmt.Errorf("identity path %q is neither a declared file nor bundle directory", asset.Identity.Path)
		}
	}
	switch asset.Platform {
	case PlatformAndroid:
		if asset.Identity.Kind != VerificationPackageIdentity {
			return fmt.Errorf("Android identity kind = %q, want %q", asset.Identity.Kind, VerificationPackageIdentity)
		}
		if asset.Compatibility.AndroidAPI.Min <= 0 || asset.Compatibility.AndroidAPI.Max < asset.Compatibility.AndroidAPI.Min {
			return errors.New("Android API bounds are missing or reversed")
		}
		if asset.Compatibility.Xcode != (VersionRange{}) || asset.Compatibility.IOSRuntime != (VersionRange{}) {
			return errors.New("Android asset must not declare iOS compatibility")
		}
	case PlatformIOSSimulator:
		if asset.Identity.Kind != VerificationBundleSignatureIdentity {
			return fmt.Errorf("iOS identity kind = %q, want %q", asset.Identity.Kind, VerificationBundleSignatureIdentity)
		}
		if asset.Compatibility.AndroidAPI != (IntegerRange{}) {
			return errors.New("iOS asset must not declare Android compatibility")
		}
		if !validVersionRange(asset.Compatibility.Xcode) || !validVersionRange(asset.Compatibility.IOSRuntime) {
			return errors.New("Xcode and iOS runtime bounds are missing, malformed, or reversed")
		}
	}
	return nil
}

func runtimeCompatible(asset Asset, runtime Runtime) bool {
	switch asset.Platform {
	case PlatformAndroid:
		return runtime.AndroidAPI >= asset.Compatibility.AndroidAPI.Min && runtime.AndroidAPI <= asset.Compatibility.AndroidAPI.Max
	case PlatformIOSSimulator:
		return versionInRange(runtime.XcodeVersion, asset.Compatibility.Xcode) && versionInRange(runtime.IOSRuntimeVersion, asset.Compatibility.IOSRuntime)
	default:
		return false
	}
}

func safeToken(value string) bool {
	return len(value) <= 128 && safeTokenPattern.MatchString(value)
}

func canonicalHash(value string) bool {
	return hashPattern.MatchString(value)
}

func validAssetPath(value string) bool {
	return value != "" && !strings.ContainsAny(value, "\\\x00") && !strings.HasPrefix(value, "/") && path.Clean(value) == value && value != "." && value != ".." && !strings.HasPrefix(value, "../")
}

func validVersionRange(versionRange VersionRange) bool {
	minimum, ok := parseNumericVersion(versionRange.Min)
	if !ok {
		return false
	}
	maximum, ok := parseNumericVersion(versionRange.Max)
	return ok && orderNumericVersions(minimum, maximum) <= 0
}

func versionInRange(version string, versionRange VersionRange) bool {
	candidate, ok := parseNumericVersion(version)
	if !ok {
		return false
	}
	minimum, minimumOK := parseNumericVersion(versionRange.Min)
	maximum, maximumOK := parseNumericVersion(versionRange.Max)
	return minimumOK && maximumOK && orderNumericVersions(candidate, minimum) >= 0 && orderNumericVersions(candidate, maximum) <= 0
}

func parseNumericVersion(value string) ([]int, bool) {
	if value == "" {
		return nil, false
	}
	parts := strings.Split(value, ".")
	if len(parts) > 4 {
		return nil, false
	}
	parsed := make([]int, len(parts))
	for index, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return nil, false
		}
		component, err := strconv.Atoi(part)
		if err != nil || component < 0 || component > 9999 {
			return nil, false
		}
		parsed[index] = component
	}
	return parsed, true
}

func orderNumericVersions(left, right []int) int {
	length := len(left)
	if len(right) > length {
		length = len(right)
	}
	for index := 0; index < length; index++ {
		var leftPart, rightPart int
		if index < len(left) {
			leftPart = left[index]
		}
		if index < len(right) {
			rightPart = right[index]
		}
		if leftPart < rightPart {
			return -1
		}
		if leftPart > rightPart {
			return 1
		}
	}
	return 0
}
