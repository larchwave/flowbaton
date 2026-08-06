// The fixtures in this package are representative, non-installable payloads.
// They are not a versioned release manifest or cache manager.
package assets

import (
	"encoding/hex"
	"io/fs"
)

// Platform identifies the device platform represented by a feasibility fixture.
type Platform string

const (
	PlatformAndroid      Platform = "android"
	PlatformIOSSimulator Platform = "ios-simulator"
)

// VerificationKind identifies the external identity check an artifact
// requires. The publisher injects this check; it does not implement
// Android package parsing or Apple code-signature verification itself.
type VerificationKind string

const (
	VerificationPackageIdentity         VerificationKind = "package-identity"
	VerificationBundleSignatureIdentity VerificationKind = "bundle-signature-identity"
)

const (
	// FeasibilitySchemaVersion identifies representative fixture metadata.
	FeasibilitySchemaVersion = "flowbaton.g001.feasibility.v1"
	FeasibilityOnlyScope     = "representative-fixture-not-release-artifact"
)

// FeasibilityManifest is the minimum metadata needed to test the
// compressed-payload pipeline.
type FeasibilityManifest struct {
	SchemaVersion    string           `json:"schema_version"`
	Scope            string           `json:"scope"`
	Platform         Platform         `json:"platform"`
	ArtifactName     string           `json:"artifact_name"`
	Identity         string           `json:"identity"`
	VerificationKind VerificationKind `json:"verification_kind"`
	Mode             fs.FileMode      `json:"mode"`
	CompressedSHA256 string           `json:"compressed_sha256"`
	PayloadSHA256    string           `json:"payload_sha256"`
	CompressedSize   int64            `json:"compressed_size"`
	PayloadSize      int64            `json:"payload_size"`
	BudgetBytes      int64            `json:"budget_bytes"`
}

// Fixture pairs deterministic gzip bytes with their feasibility metadata.
type Fixture struct {
	Manifest   FeasibilityManifest
	Compressed []byte
}

// These gzip streams were produced with gzip -n -9. Their payload text states
// explicitly that they are representative rather than installable artifacts.
const androidFixtureGZIPHex = "1f8b080000000000020325cacb0a83301040d17dfe25a21f9085c507425110fbd885d18e554c27328e91fe7d5bbabde716e7e6764abba6b6651c2736adb3b6a9329b9679ddd9a2ba779736b7d744ad302cf044f3c010919f20e031f96874fee8413c2960994718c430ae8c1b9280cc013579d1336d02ce41ef50c3baa82ff2dbfc2aefafffe949f34e84ac3efb89089f91000000"

const iosFixtureGZIPHex = "1f8b080000000000020315cb410a83301000c07bfe12d107e4d08216a14448b5ed2daccd8a42d8844d8cf4f7b5e761bafbf0ba5ec641db5b5d37b61f1ed64c5ab7c676fd7b9c4c6b9f8d9877721e95c3525158a1e0b1866af1e1982107aa1c6f055900e76d814f568c91312165c827480a59f24e04b34709318a53f8abd2c97b9209f99f7f16ad195f88000000"

var (
	androidFixtureGZIP = mustDecodeHex(androidFixtureGZIPHex)
	iosFixtureGZIP     = mustDecodeHex(iosFixtureGZIPHex)
)

// RepresentativeFixtures returns fresh copies so a corruption test or caller
// cannot alter the package's embedded fixture bytes.
func RepresentativeFixtures() []Fixture {
	return []Fixture{
		{
			Manifest: FeasibilityManifest{
				SchemaVersion:    FeasibilitySchemaVersion,
				Scope:            FeasibilityOnlyScope,
				Platform:         PlatformAndroid,
				ArtifactName:     "android-agent.fixture",
				Identity:         "dev.larchwave.flowbaton",
				VerificationKind: VerificationPackageIdentity,
				Mode:             0o644,
				CompressedSHA256: "0b595af42bb4c5d6acfd7684cff88b08ef2b186b3872990d511ac494b258f399",
				PayloadSHA256:    "9961579845486c1def0fe9f5ff74d3f6dcb8ee7f01050dc7f8b5f6454d6fd8fb",
				CompressedSize:   148,
				PayloadSize:      145,
				BudgetBytes:      20 * 1024 * 1024,
			},
			Compressed: append([]byte(nil), androidFixtureGZIP...),
		},
		{
			Manifest: FeasibilityManifest{
				SchemaVersion:    FeasibilitySchemaVersion,
				Scope:            FeasibilityOnlyScope,
				Platform:         PlatformIOSSimulator,
				ArtifactName:     "ios-runner.fixture",
				Identity:         "dev.larchwave.flowbaton.driver",
				VerificationKind: VerificationBundleSignatureIdentity,
				Mode:             0o755,
				CompressedSHA256: "fe697c8bff6125880535adcdb53df26453e3c3b6687bc1354d32adf26b378d42",
				PayloadSHA256:    "cbe46645cad2812ba650fa40510992ed6aa8204e4b095b33c8bd6c90f3e6202a",
				CompressedSize:   141,
				PayloadSize:      136,
				BudgetBytes:      25 * 1024 * 1024,
			},
			Compressed: append([]byte(nil), iosFixtureGZIP...),
		},
	}
}

func mustDecodeHex(encoded string) []byte {
	decoded, err := hex.DecodeString(encoded)
	if err != nil {
		panic(err)
	}
	return decoded
}
