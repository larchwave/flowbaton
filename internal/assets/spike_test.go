package assets

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestFileModePolicyMatchesHostSemantics(t *testing.T) {
	if runtime.GOOS == "windows" {
		if !fileModeMatchesPlatform(0o666, 0o644) {
			t.Fatal("Windows mode policy must not claim POSIX permission enforcement")
		}
		return
	}

	if fileModeMatchesPlatform(0o600, 0o755) {
		t.Fatal("POSIX mode policy accepted a mismatched permission mode")
	}
	if !fileModeMatchesPlatform(0o755, 0o755) {
		t.Fatal("POSIX mode policy rejected an exact permission mode")
	}
}

func TestRepresentativeFixturesAreDeterministicAndSelfDescribing(t *testing.T) {
	t.Parallel()

	first := RepresentativeFixtures()
	second := RepresentativeFixtures()
	if !reflect.DeepEqual(first, second) {
		t.Fatal("RepresentativeFixtures returned different bytes or metadata across calls")
	}
	if len(first) != 2 {
		t.Fatalf("fixture count = %d, want 2", len(first))
	}

	want := map[Platform]struct {
		artifactName     string
		identity         string
		verificationKind VerificationKind
		mode             fs.FileMode
		compressedSHA256 string
		payloadSHA256    string
		compressedSize   int64
		payloadSize      int64
		budgetBytes      int64
	}{
		PlatformAndroid: {
			artifactName:     "android-agent.fixture",
			identity:         "dev.larchwave.flowbaton",
			verificationKind: VerificationPackageIdentity,
			mode:             0o644,
			compressedSHA256: "0b595af42bb4c5d6acfd7684cff88b08ef2b186b3872990d511ac494b258f399",
			payloadSHA256:    "9961579845486c1def0fe9f5ff74d3f6dcb8ee7f01050dc7f8b5f6454d6fd8fb",
			compressedSize:   148,
			payloadSize:      145,
			budgetBytes:      20 * 1024 * 1024,
		},
		PlatformIOSSimulator: {
			artifactName:     "ios-runner.fixture",
			identity:         "dev.larchwave.flowbaton.driver",
			verificationKind: VerificationBundleSignatureIdentity,
			mode:             0o755,
			compressedSHA256: "fe697c8bff6125880535adcdb53df26453e3c3b6687bc1354d32adf26b378d42",
			payloadSHA256:    "cbe46645cad2812ba650fa40510992ed6aa8204e4b095b33c8bd6c90f3e6202a",
			compressedSize:   141,
			payloadSize:      136,
			budgetBytes:      25 * 1024 * 1024,
		},
	}

	seen := make(map[Platform]bool, len(first))
	for _, fixture := range first {
		expected, ok := want[fixture.Manifest.Platform]
		if !ok {
			t.Fatalf("unexpected platform %q", fixture.Manifest.Platform)
		}
		if seen[fixture.Manifest.Platform] {
			t.Fatalf("duplicate fixture for %q", fixture.Manifest.Platform)
		}
		seen[fixture.Manifest.Platform] = true

		manifest := fixture.Manifest
		if manifest.SchemaVersion != FeasibilitySchemaVersion {
			t.Errorf("%s schema version = %q, want %q", manifest.Platform, manifest.SchemaVersion, FeasibilitySchemaVersion)
		}
		if manifest.Scope != FeasibilityOnlyScope {
			t.Errorf("%s scope = %q, want explicit feasibility-only scope", manifest.Platform, manifest.Scope)
		}
		if manifest.ArtifactName != expected.artifactName {
			t.Errorf("%s artifact name = %q, want %q", manifest.Platform, manifest.ArtifactName, expected.artifactName)
		}
		if manifest.Identity != expected.identity {
			t.Errorf("%s identity = %q, want %q", manifest.Platform, manifest.Identity, expected.identity)
		}
		if manifest.VerificationKind != expected.verificationKind {
			t.Errorf("%s verification kind = %q, want %q", manifest.Platform, manifest.VerificationKind, expected.verificationKind)
		}
		if manifest.Mode != expected.mode {
			t.Errorf("%s mode = %#o, want %#o", manifest.Platform, manifest.Mode, expected.mode)
		}
		if manifest.CompressedSHA256 != expected.compressedSHA256 {
			t.Errorf("%s compressed SHA-256 = %q, want %q", manifest.Platform, manifest.CompressedSHA256, expected.compressedSHA256)
		}
		if manifest.PayloadSHA256 != expected.payloadSHA256 {
			t.Errorf("%s payload SHA-256 = %q, want %q", manifest.Platform, manifest.PayloadSHA256, expected.payloadSHA256)
		}
		if manifest.CompressedSize != expected.compressedSize {
			t.Errorf("%s compressed size = %d, want %d", manifest.Platform, manifest.CompressedSize, expected.compressedSize)
		}
		if manifest.PayloadSize != expected.payloadSize {
			t.Errorf("%s payload size = %d, want %d", manifest.Platform, manifest.PayloadSize, expected.payloadSize)
		}
		if manifest.BudgetBytes != expected.budgetBytes {
			t.Errorf("%s budget = %d, want %d", manifest.Platform, manifest.BudgetBytes, expected.budgetBytes)
		}
		if manifest.CompressedSize > manifest.BudgetBytes {
			t.Errorf("%s fixture is over its feasibility budget", manifest.Platform)
		}
		if got := hashHex(fixture.Compressed); got != manifest.CompressedSHA256 {
			t.Errorf("%s embedded compressed bytes hash = %q, want manifest %q", manifest.Platform, got, manifest.CompressedSHA256)
		}

		payload := gunzipForTest(t, fixture.Compressed)
		if got := hashHex(payload); got != manifest.PayloadSHA256 {
			t.Errorf("%s uncompressed payload hash = %q, want manifest %q", manifest.Platform, got, manifest.PayloadSHA256)
		}
		if got := int64(len(payload)); got != manifest.PayloadSize {
			t.Errorf("%s uncompressed payload size = %d, want %d", manifest.Platform, got, manifest.PayloadSize)
		}
		if !strings.Contains(string(payload), "representative-not-") {
			t.Errorf("%s fixture does not label itself as non-release representative data", manifest.Platform)
		}
	}
}

func TestFeasibilityPublisherVerifiesBeforeAtomicPublication(t *testing.T) {
	t.Parallel()

	for _, fixture := range RepresentativeFixtures() {
		fixture := fixture
		t.Run(string(fixture.Manifest.Platform), func(t *testing.T) {
			t.Parallel()

			cacheRoot := t.TempDir()
			finalDirectory := filepath.Join(cacheRoot, fixture.Manifest.PayloadSHA256)
			verifier := &recordingVerifier{
				verify: func(candidate VerificationCandidate) error {
					if _, err := os.Stat(finalDirectory); !errors.Is(err, fs.ErrNotExist) {
						return fmt.Errorf("final path visible before verification: %v", err)
					}
					if candidate.Platform != fixture.Manifest.Platform || candidate.Identity != fixture.Manifest.Identity || candidate.Kind != fixture.Manifest.VerificationKind {
						return fmt.Errorf("candidate metadata = %#v; want platform=%q identity=%q kind=%q", candidate, fixture.Manifest.Platform, fixture.Manifest.Identity, fixture.Manifest.VerificationKind)
					}
					return verifyExtractedFile(candidate.Path, fixture.Manifest)
				},
			}

			published, err := (FeasibilityPublisher{Verifier: verifier}).Publish(context.Background(), cacheRoot, fixture)
			if err != nil {
				t.Fatalf("Publish() error = %v", err)
			}
			if verifier.calls != 1 {
				t.Fatalf("verifier calls = %d, want 1", verifier.calls)
			}
			if published.Directory != finalDirectory {
				t.Errorf("published directory = %q, want %q", published.Directory, finalDirectory)
			}
			if published.AssetPath != filepath.Join(finalDirectory, fixture.Manifest.ArtifactName) {
				t.Errorf("published asset path = %q", published.AssetPath)
			}
			if err := verifyExtractedFile(published.AssetPath, fixture.Manifest); err != nil {
				t.Errorf("published file verification failed: %v", err)
			}
			matches, err := filepath.Glob(filepath.Join(cacheRoot, ".flowbaton-g001-*"))
			if err != nil {
				t.Fatalf("glob temporary paths: %v", err)
			}
			if len(matches) != 0 {
				t.Errorf("temporary paths remain after publication: %v", matches)
			}
		})
	}
}

func TestFeasibilityPublisherRejectsCorruptionBeforeVerifier(t *testing.T) {
	t.Parallel()

	fixture := RepresentativeFixtures()[0]
	fixture.Compressed = append([]byte(nil), fixture.Compressed...)
	fixture.Compressed[len(fixture.Compressed)-1] ^= 0xff
	verifier := &recordingVerifier{}
	cacheRoot := t.TempDir()

	_, err := (FeasibilityPublisher{Verifier: verifier}).Publish(context.Background(), cacheRoot, fixture)
	if !errors.Is(err, ErrCompressedHashMismatch) {
		t.Fatalf("Publish() error = %v, want ErrCompressedHashMismatch", err)
	}
	if verifier.calls != 0 {
		t.Fatalf("verifier calls = %d, want 0", verifier.calls)
	}
	assertEmptyDirectory(t, cacheRoot)
}

func TestFeasibilityPublisherRejectsModeMutationByVerifier(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not preserve POSIX permission bits")
	}

	fixture := RepresentativeFixtures()[1]
	cacheRoot := t.TempDir()
	verifier := &recordingVerifier{
		verify: func(candidate VerificationCandidate) error {
			return os.Chmod(candidate.Path, 0o600)
		},
	}

	_, err := (FeasibilityPublisher{Verifier: verifier}).Publish(context.Background(), cacheRoot, fixture)
	if !errors.Is(err, ErrModeMismatch) {
		t.Fatalf("Publish() error = %v, want ErrModeMismatch", err)
	}
	if verifier.calls != 1 {
		t.Fatalf("verifier calls = %d, want 1", verifier.calls)
	}
	assertEmptyDirectory(t, cacheRoot)
}

func TestFeasibilityPublisherDoesNotPublishWhenIdentityVerificationFails(t *testing.T) {
	t.Parallel()

	fixture := RepresentativeFixtures()[1]
	cacheRoot := t.TempDir()
	wantErr := errors.New("signature identity rejected")
	verifier := &recordingVerifier{verify: func(VerificationCandidate) error { return wantErr }}

	_, err := (FeasibilityPublisher{Verifier: verifier}).Publish(context.Background(), cacheRoot, fixture)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Publish() error = %v, want wrapped verifier error", err)
	}
	if verifier.calls != 1 {
		t.Fatalf("verifier calls = %d, want 1", verifier.calls)
	}
	assertEmptyDirectory(t, cacheRoot)
}

func TestFeasibilityPublisherRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	valid := RepresentativeFixtures()[0]
	tests := []struct {
		name    string
		mutate  func(*Fixture)
		wantErr error
	}{
		{
			name:    "compressed size",
			mutate:  func(fixture *Fixture) { fixture.Manifest.CompressedSize++ },
			wantErr: ErrCompressedSizeMismatch,
		},
		{
			name:    "payload size",
			mutate:  func(fixture *Fixture) { fixture.Manifest.PayloadSize++ },
			wantErr: ErrPayloadSizeMismatch,
		},
		{
			name:    "payload hash",
			mutate:  func(fixture *Fixture) { fixture.Manifest.PayloadSHA256 = strings.Repeat("0", 64) },
			wantErr: ErrPayloadHashMismatch,
		},
		{
			name:    "schema",
			mutate:  func(fixture *Fixture) { fixture.Manifest.SchemaVersion = "production.v0" },
			wantErr: ErrInvalidManifest,
		},
		{
			name:    "missing verifier metadata",
			mutate:  func(fixture *Fixture) { fixture.Manifest.Identity = "" },
			wantErr: ErrInvalidManifest,
		},
		{
			name:    "unsafe artifact name",
			mutate:  func(fixture *Fixture) { fixture.Manifest.ArtifactName = "../escape" },
			wantErr: ErrInvalidManifest,
		},
		{
			name:    "empty mode",
			mutate:  func(fixture *Fixture) { fixture.Manifest.Mode = 0 },
			wantErr: ErrInvalidManifest,
		},
		{
			name:    "empty size",
			mutate:  func(fixture *Fixture) { fixture.Manifest.PayloadSize = 0 },
			wantErr: ErrInvalidManifest,
		},
		{
			name: "over budget",
			mutate: func(fixture *Fixture) {
				fixture.Manifest.BudgetBytes = fixture.Manifest.CompressedSize - 1
			},
			wantErr: ErrInvalidManifest,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fixture := valid
			fixture.Compressed = append([]byte(nil), valid.Compressed...)
			test.mutate(&fixture)
			cacheRoot := t.TempDir()
			verifier := &recordingVerifier{}
			_, err := (FeasibilityPublisher{Verifier: verifier}).Publish(context.Background(), cacheRoot, fixture)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Publish() error = %v, want %v", err, test.wantErr)
			}
			if verifier.calls != 0 {
				t.Fatalf("verifier calls = %d, want 0", verifier.calls)
			}
			assertEmptyDirectory(t, cacheRoot)
		})
	}
}

func TestFeasibilityPublisherRequiresVerifierAndLiveContext(t *testing.T) {
	t.Parallel()

	fixture := RepresentativeFixtures()[0]
	if _, err := (FeasibilityPublisher{}).Publish(context.Background(), t.TempDir(), fixture); !errors.Is(err, ErrVerifierRequired) {
		t.Fatalf("Publish() without verifier error = %v, want ErrVerifierRequired", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (FeasibilityPublisher{Verifier: &recordingVerifier{}}).Publish(ctx, t.TempDir(), fixture); !errors.Is(err, context.Canceled) {
		t.Fatalf("Publish() with canceled context error = %v, want context.Canceled", err)
	}
}

func TestFeasibilityPublisherRejectsMalformedGzip(t *testing.T) {
	t.Parallel()

	fixture := RepresentativeFixtures()[0]
	fixture.Compressed = append([]byte(nil), fixture.Compressed[:len(fixture.Compressed)-1]...)
	fixture.Manifest.CompressedSize = int64(len(fixture.Compressed))
	fixture.Manifest.CompressedSHA256 = hashHex(fixture.Compressed)
	verifier := &recordingVerifier{}
	cacheRoot := t.TempDir()

	_, err := (FeasibilityPublisher{Verifier: verifier}).Publish(context.Background(), cacheRoot, fixture)
	if err == nil || !strings.Contains(err.Error(), "expand compressed fixture") {
		t.Fatalf("Publish() error = %v, want malformed gzip expansion failure", err)
	}
	if verifier.calls != 0 {
		t.Fatalf("verifier calls = %d, want 0", verifier.calls)
	}
	assertEmptyDirectory(t, cacheRoot)
}

func TestFeasibilityPublisherRechecksPayloadAfterVerifier(t *testing.T) {
	t.Parallel()

	fixture := RepresentativeFixtures()[0]
	cacheRoot := t.TempDir()
	verifier := &recordingVerifier{
		verify: func(candidate VerificationCandidate) error {
			contents, err := os.ReadFile(candidate.Path)
			if err != nil {
				return err
			}
			contents[0] ^= 0xff
			return os.WriteFile(candidate.Path, contents, fixture.Manifest.Mode.Perm())
		},
	}

	_, err := (FeasibilityPublisher{Verifier: verifier}).Publish(context.Background(), cacheRoot, fixture)
	if !errors.Is(err, ErrPayloadHashMismatch) {
		t.Fatalf("Publish() error = %v, want ErrPayloadHashMismatch", err)
	}
	if verifier.calls != 1 {
		t.Fatalf("verifier calls = %d, want 1", verifier.calls)
	}
	assertEmptyDirectory(t, cacheRoot)
}

func TestFeasibilityPublisherRejectsVerifierReplacingCandidateWithSymlink(t *testing.T) {
	t.Parallel()

	fixture := RepresentativeFixtures()[1]
	cacheRoot := t.TempDir()
	externalPath := filepath.Join(t.TempDir(), "external-same-content")
	payload := gunzipForTest(t, fixture.Compressed)
	if err := os.WriteFile(externalPath, payload, fixture.Manifest.Mode.Perm()); err != nil {
		t.Fatalf("write external target: %v", err)
	}
	if err := os.Chmod(externalPath, fixture.Manifest.Mode.Perm()); err != nil {
		t.Fatalf("set external target mode: %v", err)
	}
	externalBefore, err := os.ReadFile(externalPath)
	if err != nil {
		t.Fatalf("read external target before publication: %v", err)
	}
	externalInfoBefore, err := os.Stat(externalPath)
	if err != nil {
		t.Fatalf("stat external target before publication: %v", err)
	}

	verifier := &recordingVerifier{
		verify: func(candidate VerificationCandidate) error {
			if err := os.Remove(candidate.Path); err != nil {
				return err
			}
			return os.Symlink(externalPath, candidate.Path)
		},
	}
	_, err = (FeasibilityPublisher{Verifier: verifier}).Publish(context.Background(), cacheRoot, fixture)
	if err == nil {
		t.Fatal("Publish() succeeded after verifier replaced candidate with a symlink")
	}
	var nonRegular *NonRegularCandidateError
	if !errors.As(err, &nonRegular) {
		t.Fatalf("Publish() error = %T %v, want *NonRegularCandidateError", err, err)
	}
	if nonRegular.Path == "" || nonRegular.Mode&fs.ModeSymlink == 0 {
		t.Fatalf("non-regular candidate details = %#v, want symlink path and mode", nonRegular)
	}
	if verifier.calls != 1 {
		t.Fatalf("verifier calls = %d, want 1", verifier.calls)
	}
	finalDirectory := filepath.Join(cacheRoot, fixture.Manifest.PayloadSHA256)
	if _, statErr := os.Lstat(finalDirectory); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("published destination exists after symlink rejection: %v", statErr)
	}
	assertEmptyDirectory(t, cacheRoot)

	externalAfter, err := os.ReadFile(externalPath)
	if err != nil {
		t.Fatalf("read external target after publication: %v", err)
	}
	if !reflect.DeepEqual(externalAfter, externalBefore) {
		t.Fatal("external symlink target changed")
	}
	externalInfo, err := os.Stat(externalPath)
	if err != nil {
		t.Fatalf("stat external target: %v", err)
	}
	if got := externalInfo.Mode().Perm(); !fileModeMatchesPlatform(got, externalInfoBefore.Mode().Perm()) {
		t.Fatalf("external target mode = %#o, want unchanged %#o", got, externalInfoBefore.Mode().Perm())
	}
}

func TestFeasibilityPublisherHonorsCancellationAfterVerifier(t *testing.T) {
	t.Parallel()

	fixture := RepresentativeFixtures()[0]
	cacheRoot := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	verifier := &recordingVerifier{verify: func(VerificationCandidate) error {
		cancel()
		return nil
	}}

	_, err := (FeasibilityPublisher{Verifier: verifier}).Publish(ctx, cacheRoot, fixture)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Publish() error = %v, want context.Canceled", err)
	}
	assertEmptyDirectory(t, cacheRoot)
}

func TestFeasibilityPublisherDoesNotReplacePublishedFixture(t *testing.T) {
	t.Parallel()

	fixture := RepresentativeFixtures()[0]
	cacheRoot := t.TempDir()
	verifier := &recordingVerifier{}
	publisher := FeasibilityPublisher{Verifier: verifier}
	first, err := publisher.Publish(context.Background(), cacheRoot, fixture)
	if err != nil {
		t.Fatalf("first Publish() error = %v", err)
	}
	firstContents, err := os.ReadFile(first.AssetPath)
	if err != nil {
		t.Fatalf("read first published fixture: %v", err)
	}

	_, err = publisher.Publish(context.Background(), cacheRoot, fixture)
	if !errors.Is(err, ErrAlreadyPublished) {
		t.Fatalf("second Publish() error = %v, want ErrAlreadyPublished", err)
	}
	secondContents, err := os.ReadFile(first.AssetPath)
	if err != nil {
		t.Fatalf("read fixture after rejected replacement: %v", err)
	}
	if !reflect.DeepEqual(firstContents, secondContents) {
		t.Fatal("existing published fixture changed after rejected replacement")
	}
}

func TestMustDecodeHexPanicsForInvalidEmbeddedConstant(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Fatal("mustDecodeHex did not panic for invalid hex")
		}
	}()
	_ = mustDecodeHex("not-hex")
}

type recordingVerifier struct {
	calls  int
	verify func(VerificationCandidate) error
}

func (v *recordingVerifier) Verify(_ context.Context, candidate VerificationCandidate) error {
	v.calls++
	if v.verify == nil {
		return nil
	}
	return v.verify(candidate)
}

func verifyExtractedFile(path string, manifest FeasibilityManifest) error {
	contents, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read candidate: %w", err)
	}
	if got := hashHex(contents); got != manifest.PayloadSHA256 {
		return fmt.Errorf("candidate SHA-256 = %s, want %s", got, manifest.PayloadSHA256)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat candidate: %w", err)
	}
	if got := info.Mode().Perm(); !fileModeMatchesPlatform(got, manifest.Mode.Perm()) {
		return fmt.Errorf("candidate mode = %#o, want %#o", got, manifest.Mode.Perm())
	}
	return nil
}

func gunzipForTest(t *testing.T, compressed []byte) []byte {
	t.Helper()

	reader, err := gzip.NewReader(strings.NewReader(string(compressed)))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	payload, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read compressed fixture: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close gzip reader: %v", err)
	}
	return payload
}

func hashHex(contents []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(contents))
}

func assertEmptyDirectory(t *testing.T, path string) {
	t.Helper()

	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatalf("read cache root: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("cache root contains %v, want empty", entries)
	}
}
