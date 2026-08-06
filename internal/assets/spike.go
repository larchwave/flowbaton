package assets

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

var (
	ErrVerifierRequired       = errors.New("identity verifier is required")
	ErrInvalidManifest        = errors.New("invalid feasibility manifest")
	ErrCompressedSizeMismatch = errors.New("compressed size mismatch")
	ErrCompressedHashMismatch = errors.New("compressed hash mismatch")
	ErrPayloadSizeMismatch    = errors.New("payload size mismatch")
	ErrPayloadHashMismatch    = errors.New("payload hash mismatch")
	ErrModeMismatch           = errors.New("payload mode mismatch")
	ErrAlreadyPublished       = errors.New("fixture destination already exists")
)

// NonRegularCandidateError reports that the extracted asset was replaced by a
// symlink, directory, device, or other non-regular filesystem entry. Path and
// Mode come from Lstat, so inspecting the error never follows the entry.
type NonRegularCandidateError struct {
	Path string
	Mode fs.FileMode
}

func (e *NonRegularCandidateError) Error() string {
	return fmt.Sprintf("asset candidate %q is not a regular file (mode %s)", e.Path, e.Mode)
}

// VerificationCandidate is passed to an injected platform-aware verifier after
// the payload has passed internal hash and mode checks but before publication.
type VerificationCandidate struct {
	Path     string
	Platform Platform
	Identity string
	Kind     VerificationKind
}

// IdentityVerifier represents the Android package-identity or iOS
// bundle-signature check required before publication.
type IdentityVerifier interface {
	Verify(context.Context, VerificationCandidate) error
}

// PublishedFixture describes a successfully and atomically published fixture.
type PublishedFixture struct {
	Directory string
	AssetPath string
}

// FeasibilityPublisher performs verified extraction followed by atomic
// publication in a caller-provided temporary cache. Locking, cache recovery,
// release selection, and retention policy are outside this bounded component.
type FeasibilityPublisher struct {
	Verifier IdentityVerifier
}

// Publish validates and expands one representative gzip payload into a sibling
// temporary directory, invokes the identity verifier, rechecks bytes and mode,
// and publishes the complete directory with one atomic rename.
func (p FeasibilityPublisher) Publish(ctx context.Context, cacheRoot string, fixture Fixture) (PublishedFixture, error) {
	if p.Verifier == nil {
		return PublishedFixture{}, ErrVerifierRequired
	}
	if err := ctx.Err(); err != nil {
		return PublishedFixture{}, err
	}
	if err := validateManifest(fixture.Manifest); err != nil {
		return PublishedFixture{}, err
	}
	if got := int64(len(fixture.Compressed)); got != fixture.Manifest.CompressedSize {
		return PublishedFixture{}, fmt.Errorf("%w: got %d, want %d", ErrCompressedSizeMismatch, got, fixture.Manifest.CompressedSize)
	}
	if got := sha256Hex(fixture.Compressed); got != fixture.Manifest.CompressedSHA256 {
		return PublishedFixture{}, fmt.Errorf("%w: got %s, want %s", ErrCompressedHashMismatch, got, fixture.Manifest.CompressedSHA256)
	}

	payload, err := expandFixture(fixture.Compressed)
	if err != nil {
		return PublishedFixture{}, fmt.Errorf("expand compressed fixture: %w", err)
	}
	if got := int64(len(payload)); got != fixture.Manifest.PayloadSize {
		return PublishedFixture{}, fmt.Errorf("%w: got %d, want %d", ErrPayloadSizeMismatch, got, fixture.Manifest.PayloadSize)
	}
	if got := sha256Hex(payload); got != fixture.Manifest.PayloadSHA256 {
		return PublishedFixture{}, fmt.Errorf("%w: got %s, want %s", ErrPayloadHashMismatch, got, fixture.Manifest.PayloadSHA256)
	}

	if err := os.MkdirAll(cacheRoot, 0o700); err != nil {
		return PublishedFixture{}, fmt.Errorf("create feasibility cache root: %w", err)
	}
	finalDirectory := filepath.Join(cacheRoot, fixture.Manifest.PayloadSHA256)
	if _, err := os.Lstat(finalDirectory); err == nil {
		return PublishedFixture{}, fmt.Errorf("%w: %s", ErrAlreadyPublished, finalDirectory)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return PublishedFixture{}, fmt.Errorf("inspect fixture destination: %w", err)
	}

	temporaryDirectory, err := os.MkdirTemp(cacheRoot, ".flowbaton-g001-")
	if err != nil {
		return PublishedFixture{}, fmt.Errorf("create sibling temporary directory: %w", err)
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(temporaryDirectory)
		}
	}()

	temporaryAssetPath := filepath.Join(temporaryDirectory, fixture.Manifest.ArtifactName)
	if err := os.WriteFile(temporaryAssetPath, payload, 0o600); err != nil {
		return PublishedFixture{}, fmt.Errorf("write temporary fixture: %w", err)
	}
	if err := os.Chmod(temporaryAssetPath, fixture.Manifest.Mode.Perm()); err != nil {
		return PublishedFixture{}, fmt.Errorf("restore fixture mode: %w", err)
	}
	if err := verifyCandidateFile(temporaryAssetPath, fixture.Manifest); err != nil {
		return PublishedFixture{}, err
	}

	candidate := VerificationCandidate{
		Path:     temporaryAssetPath,
		Platform: fixture.Manifest.Platform,
		Identity: fixture.Manifest.Identity,
		Kind:     fixture.Manifest.VerificationKind,
	}
	if err := p.Verifier.Verify(ctx, candidate); err != nil {
		return PublishedFixture{}, fmt.Errorf("verify %s identity: %w", fixture.Manifest.Platform, err)
	}
	if err := ctx.Err(); err != nil {
		return PublishedFixture{}, err
	}

	// Recheck after the injected verifier so a verifier cannot accidentally
	// mutate a candidate that is then published as if it were still verified.
	if err := verifyCandidateFile(temporaryAssetPath, fixture.Manifest); err != nil {
		return PublishedFixture{}, err
	}
	if err := os.Rename(temporaryDirectory, finalDirectory); err != nil {
		return PublishedFixture{}, fmt.Errorf("atomically publish fixture: %w", err)
	}
	published = true

	return PublishedFixture{
		Directory: finalDirectory,
		AssetPath: filepath.Join(finalDirectory, fixture.Manifest.ArtifactName),
	}, nil
}

func validateManifest(manifest FeasibilityManifest) error {
	if manifest.SchemaVersion != FeasibilitySchemaVersion || manifest.Scope != FeasibilityOnlyScope {
		return fmt.Errorf("%w: unexpected schema or scope", ErrInvalidManifest)
	}
	if manifest.Platform == "" || manifest.Identity == "" || manifest.VerificationKind == "" {
		return fmt.Errorf("%w: platform and verifier metadata are required", ErrInvalidManifest)
	}
	if manifest.ArtifactName == "" || manifest.ArtifactName == "." || filepath.Base(manifest.ArtifactName) != manifest.ArtifactName {
		return fmt.Errorf("%w: unsafe artifact name %q", ErrInvalidManifest, manifest.ArtifactName)
	}
	if manifest.Mode != manifest.Mode.Perm() || manifest.Mode.Perm() == 0 {
		return fmt.Errorf("%w: invalid mode %#o", ErrInvalidManifest, manifest.Mode)
	}
	if manifest.CompressedSize <= 0 || manifest.PayloadSize <= 0 || manifest.BudgetBytes <= 0 {
		return fmt.Errorf("%w: sizes and budget must be positive", ErrInvalidManifest)
	}
	if manifest.CompressedSize > manifest.BudgetBytes {
		return fmt.Errorf("%w: compressed fixture exceeds budget", ErrInvalidManifest)
	}
	return nil
}

func expandFixture(compressed []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, err
	}
	payload, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return payload, nil
}

func verifyCandidateFile(path string, manifest FeasibilityManifest) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("lstat extracted fixture: %w", err)
	}
	if !info.Mode().IsRegular() {
		return &NonRegularCandidateError{Path: path, Mode: info.Mode()}
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read extracted fixture: %w", err)
	}
	if got := int64(len(contents)); got != manifest.PayloadSize {
		return fmt.Errorf("%w: got %d, want %d", ErrPayloadSizeMismatch, got, manifest.PayloadSize)
	}
	if got := sha256Hex(contents); got != manifest.PayloadSHA256 {
		return fmt.Errorf("%w: got %s, want %s", ErrPayloadHashMismatch, got, manifest.PayloadSHA256)
	}
	if got := info.Mode().Perm(); !fileModeMatchesPlatform(got, manifest.Mode.Perm()) {
		return fmt.Errorf("%w: got %#o, want %#o", ErrModeMismatch, got, manifest.Mode.Perm())
	}
	return nil
}

// Windows file APIs do not preserve Unix permission bits: a successfully
// written regular file commonly reports 0666 even after Chmod. The feasibility
// publisher still enforces regular-file type, bytes, hashes, and identity on
// every host; exact POSIX permissions remain an enforceable invariant only on
// hosts whose filesystem API represents them.
func fileModeMatchesPlatform(got, want fs.FileMode) bool {
	return runtime.GOOS == "windows" || got == want
}

func sha256Hex(contents []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(contents))
}
