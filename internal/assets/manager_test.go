package assets

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestManagerAcquirePublishesVerifiedTarGzipAtomicallyAndReusesCache(t *testing.T) {
	t.Parallel()

	asset, archive := tarGzipAssetForTest(t)
	manifest := Manifest{SchemaVersion: ManifestSchemaVersion, ManifestVersion: "test-v1", Assets: []Asset{asset}}
	resolved, err := Resolve(manifest, runtimeForAsset(asset), requestForAsset(asset))
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	cacheRoot := realTempDir(t)
	finalDirectory := filepath.Join(cacheRoot, asset.HostVersion, asset.AssetHash)
	source := &countingArchiveSource{contents: archive}
	verifier := &concurrentVerifier{verify: func(call int, candidate VerificationCandidate) error {
		if call == 1 {
			if _, err := os.Lstat(finalDirectory); !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("final directory visible before verification: %v", err)
			}
		}
		if candidate.Path != filepath.Join(finalDirectory, asset.Identity.Path) && !strings.Contains(candidate.Path, ".tmp-") {
			return fmt.Errorf("identity candidate path = %q, want temp candidate or cached final bundle", candidate.Path)
		}
		if candidate.Platform != asset.Platform || candidate.Identity != asset.Identity.Value || candidate.Kind != asset.Identity.Kind {
			return fmt.Errorf("identity candidate metadata = %#v", candidate)
		}
		info, err := os.Lstat(candidate.Path)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("identity candidate is not a bundle directory: %s", info.Mode())
		}
		return nil
	}}
	manager := Manager{CacheRoot: cacheRoot, Source: source, Verifier: verifier}

	acquired, err := manager.Acquire(context.Background(), resolved)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if acquired.Directory != finalDirectory {
		t.Fatalf("acquired directory = %q, want %q", acquired.Directory, finalDirectory)
	}
	if acquired.IdentityPath != filepath.Join(finalDirectory, asset.Identity.Path) {
		t.Fatalf("identity path = %q, want final bundle", acquired.IdentityPath)
	}
	assertAssetFiles(t, acquired.Directory, asset.Files)
	assertNoAssetTemps(t, filepath.Dir(finalDirectory), asset.AssetHash)

	reused, err := manager.Acquire(context.Background(), resolved)
	if err != nil {
		t.Fatalf("second Acquire() error = %v", err)
	}
	if reused != acquired {
		t.Fatalf("second Acquire() = %#v, want %#v", reused, acquired)
	}
	if got := source.Calls(); got != 1 {
		t.Fatalf("archive source calls = %d, want one extraction then cache reuse", got)
	}
	if got := verifier.Calls(); got != 2 {
		t.Fatalf("identity verifier calls = %d, want temp and cached verification", got)
	}
}

func TestManagerAcquireConsumesTarRecordPaddingForPayloadVerification(t *testing.T) {
	t.Parallel()

	asset, archive := tarGzipAssetForTest(t)
	asset, archive = padTarGzipAssetForTest(t, asset, archive, 10*1024)

	acquired := acquireTestAsset(t, Manager{
		CacheRoot: realTempDir(t),
		Verifier:  &concurrentVerifier{},
	}, asset, archive)
	assertAssetFiles(t, acquired.Directory, asset.Files)
}

func TestManagerAcquireRejectsUnverifiedTarRecordTail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func([]byte) []byte
		wantErr error
	}{
		{
			name: "tampered padding",
			mutate: func(payload []byte) []byte {
				payload[len(payload)-1] = 1
				return payload
			},
			wantErr: ErrPayloadHashMismatch,
		},
		{
			name: "excess trailing byte",
			mutate: func(payload []byte) []byte {
				return append(payload, 1)
			},
			wantErr: ErrPayloadSizeMismatch,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			asset, archive := tarGzipAssetForTestVariant(t, test.name)
			asset, archive = padTarGzipAssetForTest(t, asset, archive, 10*1024)
			payload := gunzipAssetForTest(t, archive)
			archive = gzipAssetPayloadForTest(t, test.mutate(payload))
			asset.Archive.SHA256 = hashHex(archive)
			asset.Archive.Size = int64(len(archive))

			manifest := Manifest{SchemaVersion: ManifestSchemaVersion, ManifestVersion: "test-v1", Assets: []Asset{asset}}
			resolved, err := Resolve(manifest, runtimeForAsset(asset), requestForAsset(asset))
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			_, err = (Manager{
				CacheRoot: realTempDir(t),
				Source:    &countingArchiveSource{contents: archive},
				Verifier:  &concurrentVerifier{},
			}).Acquire(context.Background(), resolved)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Acquire() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestManagerAcquireValidatesGzipTrailerAfterTarEOF(t *testing.T) {
	t.Parallel()

	asset, archive := tarGzipAssetForTestVariant(t, "invalid-gzip-trailer")
	asset, archive = padTarGzipAssetForTest(t, asset, archive, 10*1024)
	archive[len(archive)-8] ^= 0xff
	asset.Archive.SHA256 = hashHex(archive)

	manifest := Manifest{SchemaVersion: ManifestSchemaVersion, ManifestVersion: "test-v1", Assets: []Asset{asset}}
	resolved, err := Resolve(manifest, runtimeForAsset(asset), requestForAsset(asset))
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	_, err = (Manager{
		CacheRoot: realTempDir(t),
		Source:    &countingArchiveSource{contents: archive},
		Verifier:  &concurrentVerifier{},
	}).Acquire(context.Background(), resolved)
	if err == nil || !strings.Contains(err.Error(), "read tar+gzip asset tail: gzip: invalid checksum") {
		t.Fatalf("Acquire() error = %v, want tar tail gzip checksum failure", err)
	}
}

func TestManagerAcquireRequiresResolvedContractAndDependenciesBeforeMutation(t *testing.T) {
	t.Parallel()

	asset, archive := tarGzipAssetForTest(t)
	cacheRoot := realTempDir(t)
	source := &countingArchiveSource{contents: archive}
	manager := Manager{CacheRoot: cacheRoot, Source: source, Verifier: &concurrentVerifier{}}

	_, err := manager.Acquire(context.Background(), ResolvedAsset{Asset: asset})
	if !errors.Is(err, ErrUnresolvedAsset) {
		t.Fatalf("Acquire() unresolved error = %v, want ErrUnresolvedAsset", err)
	}
	if got := source.Calls(); got != 0 {
		t.Fatalf("source calls = %d before resolved contract, want zero", got)
	}
	assertEmptyDirectory(t, cacheRoot)

	manifest := Manifest{SchemaVersion: ManifestSchemaVersion, ManifestVersion: "test-v1", Assets: []Asset{asset}}
	resolved, err := Resolve(manifest, runtimeForAsset(asset), requestForAsset(asset))
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	withoutSource := Manager{CacheRoot: cacheRoot, Verifier: &concurrentVerifier{}}
	if _, err := withoutSource.Acquire(context.Background(), resolved); !errors.Is(err, ErrArchiveSourceRequired) {
		t.Fatalf("Acquire() missing-source error = %v, want ErrArchiveSourceRequired", err)
	}
	withoutVerifier := Manager{CacheRoot: cacheRoot, Source: source}
	if _, err := withoutVerifier.Acquire(context.Background(), resolved); !errors.Is(err, ErrVerifierRequired) {
		t.Fatalf("Acquire() missing-verifier error = %v, want ErrVerifierRequired", err)
	}
	assertEmptyDirectory(t, cacheRoot)
}

func TestManagerAcquireRejectsCorruptArchiveBeforeIdentityOrPublication(t *testing.T) {
	t.Parallel()

	asset, archive := tarGzipAssetForTest(t)
	archive = append([]byte(nil), archive...)
	archive[len(archive)-1] ^= 0xff
	manifest := Manifest{SchemaVersion: ManifestSchemaVersion, ManifestVersion: "test-v1", Assets: []Asset{asset}}
	resolved, err := Resolve(manifest, runtimeForAsset(asset), requestForAsset(asset))
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	cacheRoot := realTempDir(t)
	verifier := &concurrentVerifier{}
	_, err = (Manager{
		CacheRoot: cacheRoot,
		Source:    &countingArchiveSource{contents: archive},
		Verifier:  verifier,
	}).Acquire(context.Background(), resolved)
	if !errors.Is(err, ErrCompressedHashMismatch) {
		t.Fatalf("Acquire() error = %v, want ErrCompressedHashMismatch", err)
	}
	if got := verifier.Calls(); got != 0 {
		t.Fatalf("identity verifier calls = %d, want zero", got)
	}
	if _, statErr := os.Lstat(filepath.Join(cacheRoot, asset.HostVersion, asset.AssetHash)); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("final directory exists after corrupt archive: %v", statErr)
	}
}

func TestManagerAcquirePropagatesArchiveSourceFailuresWithoutPublication(t *testing.T) {
	t.Parallel()

	asset, archive := tarGzipAssetForTestVariant(t, "source-errors")
	manifest := Manifest{SchemaVersion: ManifestSchemaVersion, ManifestVersion: "test-v1", Assets: []Asset{asset}}
	resolved, err := Resolve(manifest, runtimeForAsset(asset), requestForAsset(asset))
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	openFailure := errors.New("open failed")
	readFailure := errors.New("read failed")
	closeFailure := errors.New("close failed")
	tests := []struct {
		name    string
		source  ArchiveSource
		wantErr error
	}{
		{
			name: "open",
			source: archiveSourceFunc(func(context.Context, Asset) (io.ReadCloser, error) {
				return nil, openFailure
			}),
			wantErr: openFailure,
		},
		{
			name: "read",
			source: archiveSourceFunc(func(context.Context, Asset) (io.ReadCloser, error) {
				return &faultReadCloser{readErr: readFailure}, nil
			}),
			wantErr: readFailure,
		},
		{
			name: "close",
			source: archiveSourceFunc(func(context.Context, Asset) (io.ReadCloser, error) {
				return &faultReadCloser{Reader: bytes.NewReader(archive), closeErr: closeFailure}, nil
			}),
			wantErr: closeFailure,
		},
		{
			name: "size",
			source: archiveSourceFunc(func(context.Context, Asset) (io.ReadCloser, error) {
				return io.NopCloser(bytes.NewReader(archive[:len(archive)-1])), nil
			}),
			wantErr: ErrCompressedSizeMismatch,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cacheRoot := realTempDir(t)
			_, err := (Manager{CacheRoot: cacheRoot, Source: test.source, Verifier: &concurrentVerifier{}}).Acquire(context.Background(), resolved)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Acquire() error = %v, want %v", err, test.wantErr)
			}
			assertPathAbsent(t, filepath.Join(cacheRoot, asset.HostVersion, asset.AssetHash))
		})
	}
}

func TestManagerAcquireSupportsVerifiedGZIPArtifact(t *testing.T) {
	t.Parallel()

	asset, archive, payload := gzipAssetForTest(t)
	manifest := Manifest{SchemaVersion: ManifestSchemaVersion, ManifestVersion: "test-v1", Assets: []Asset{asset}}
	resolved, err := Resolve(manifest, runtimeForAsset(asset), requestForAsset(asset))
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	verifier := &concurrentVerifier{verify: func(_ int, candidate VerificationCandidate) error {
		contents, err := os.ReadFile(candidate.Path)
		if err != nil {
			return err
		}
		if !bytes.Equal(contents, payload) {
			return fmt.Errorf("identity payload = %q, want %q", contents, payload)
		}
		return nil
	}}
	cacheRoot := realTempDir(t)
	acquired, err := (Manager{
		CacheRoot: cacheRoot,
		Source:    &countingArchiveSource{contents: archive},
		Verifier:  verifier,
	}).Acquire(context.Background(), resolved)
	if err != nil {
		t.Fatalf("Acquire() gzip error = %v", err)
	}
	assertAssetFiles(t, acquired.Directory, asset.Files)
	if acquired.IdentityPath != filepath.Join(acquired.Directory, asset.Files[0].Path) {
		t.Fatalf("identity path = %q, want gzip artifact path", acquired.IdentityPath)
	}
}

func TestManagerAcquireRejectsTarTraversalLinksAndUndeclaredFiles(t *testing.T) {
	t.Parallel()

	externalPath := filepath.Join(t.TempDir(), "escape")
	tests := []struct {
		name  string
		extra tar.Header
		body  []byte
	}{
		{
			name:  "parent traversal",
			extra: tar.Header{Name: "../escape", Mode: 0o600, Typeflag: tar.TypeReg, Size: 6},
			body:  []byte("escape"),
		},
		{
			name:  "symlink",
			extra: tar.Header{Name: "Runner.app/link", Mode: 0o777, Typeflag: tar.TypeSymlink, Linkname: externalPath},
		},
		{
			name:  "undeclared regular file",
			extra: tar.Header{Name: "undeclared", Mode: 0o600, Typeflag: tar.TypeReg, Size: 1},
			body:  []byte("x"),
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			asset, archive := tarGzipAssetForTestWithExtra(t, test.extra, test.body)
			manifest := Manifest{SchemaVersion: ManifestSchemaVersion, ManifestVersion: "test-v1", Assets: []Asset{asset}}
			resolved, err := Resolve(manifest, runtimeForAsset(asset), requestForAsset(asset))
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			cacheRoot := realTempDir(t)
			_, err = (Manager{
				CacheRoot: cacheRoot,
				Source:    &countingArchiveSource{contents: archive},
				Verifier:  &concurrentVerifier{},
			}).Acquire(context.Background(), resolved)
			if !errors.Is(err, ErrInvalidAssetManifest) {
				t.Fatalf("Acquire() unsafe tar error = %v, want ErrInvalidAssetManifest", err)
			}
			assertPathAbsent(t, filepath.Join(cacheRoot, asset.HostVersion, asset.AssetHash))
		})
	}
	assertPathAbsent(t, externalPath)
}

func TestManagerAcquireRejectsSymlinkCacheBoundaryBeforeReadingSource(t *testing.T) {
	t.Parallel()

	asset, archive := tarGzipAssetForTestVariant(t, "cache-boundary")
	manifest := Manifest{SchemaVersion: ManifestSchemaVersion, ManifestVersion: "test-v1", Assets: []Asset{asset}}
	resolved, err := Resolve(manifest, runtimeForAsset(asset), requestForAsset(asset))
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	cacheRoot := realTempDir(t)
	external := t.TempDir()
	if err := os.Symlink(external, filepath.Join(cacheRoot, asset.HostVersion)); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	source := &countingArchiveSource{contents: archive}
	_, err = (Manager{CacheRoot: cacheRoot, Source: source, Verifier: &concurrentVerifier{}}).Acquire(context.Background(), resolved)
	if !errors.Is(err, ErrInvalidAssetCache) {
		t.Fatalf("Acquire() symlink-boundary error = %v, want ErrInvalidAssetCache", err)
	}
	if got := source.Calls(); got != 0 {
		t.Fatalf("source calls across symlink boundary = %d, want zero", got)
	}
	entries, err := os.ReadDir(external)
	if err != nil {
		t.Fatalf("read external directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("external directory mutated: %v", entries)
	}
}

func TestManagerRejectsSymlinkedCacheAncestorBeforeFollowingOrMutation(t *testing.T) {
	t.Parallel()

	asset, archive := tarGzipAssetForTestVariant(t, "ancestor-boundary")
	manifest := Manifest{SchemaVersion: ManifestSchemaVersion, ManifestVersion: "test-v1", Assets: []Asset{asset}}
	resolved, err := Resolve(manifest, runtimeForAsset(asset), requestForAsset(asset))
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	t.Run("acquire", func(t *testing.T) {
		ownedParent := realTempDir(t)
		external := t.TempDir()
		redirect := filepath.Join(ownedParent, "redirect")
		if err := os.Symlink(external, redirect); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		cacheRoot := filepath.Join(redirect, "drivers")
		source := &countingArchiveSource{contents: archive}
		_, err := (Manager{CacheRoot: cacheRoot, Source: source, Verifier: &concurrentVerifier{}}).Acquire(context.Background(), resolved)
		if !errors.Is(err, ErrInvalidAssetCache) {
			t.Fatalf("Acquire() symlinked-ancestor error = %v, want ErrInvalidAssetCache", err)
		}
		if got := source.Calls(); got != 0 {
			t.Fatalf("source calls across symlinked ancestor = %d, want zero", got)
		}
		entries, err := os.ReadDir(external)
		if err != nil {
			t.Fatalf("read external directory: %v", err)
		}
		if len(entries) != 0 {
			t.Fatalf("Acquire() followed ancestor and mutated external directory: %v", entries)
		}
	})

	t.Run("cleanup", func(t *testing.T) {
		ownedParent := realTempDir(t)
		external := t.TempDir()
		externalDrivers := filepath.Join(external, "drivers")
		if err := os.Mkdir(externalDrivers, 0o700); err != nil {
			t.Fatalf("create external drivers directory: %v", err)
		}
		redirect := filepath.Join(ownedParent, "redirect")
		if err := os.Symlink(external, redirect); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		err := (Manager{CacheRoot: filepath.Join(redirect, "drivers")}).Cleanup(context.Background(), RetentionPolicy{
			ActiveHostVersion: "0.1.0",
		})
		if !errors.Is(err, ErrInvalidAssetCache) {
			t.Fatalf("Cleanup() symlinked-ancestor error = %v, want ErrInvalidAssetCache", err)
		}
		entries, err := os.ReadDir(externalDrivers)
		if err != nil {
			t.Fatalf("read external drivers directory: %v", err)
		}
		if len(entries) != 0 {
			t.Fatalf("Cleanup() mutated external directory: %v", entries)
		}
	})
}

func TestManagerAcquireRecoversCorruptCacheAndInterruptedTempWithoutFollowingLinks(t *testing.T) {
	t.Parallel()

	asset, archive := tarGzipAssetForTestVariant(t, "recovery")
	manifest := Manifest{SchemaVersion: ManifestSchemaVersion, ManifestVersion: "test-v1", Assets: []Asset{asset}}
	resolved, err := Resolve(manifest, runtimeForAsset(asset), requestForAsset(asset))
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	cacheRoot := realTempDir(t)
	source := &countingArchiveSource{contents: archive}
	manager := Manager{CacheRoot: cacheRoot, Source: source, Verifier: &concurrentVerifier{}}
	first, err := manager.Acquire(context.Background(), resolved)
	if err != nil {
		t.Fatalf("first Acquire() error = %v", err)
	}
	runnerPath := filepath.Join(first.Directory, "Runner.app", "runner")
	if err := os.WriteFile(runnerPath, []byte("corrupt-cache"), 0o600); err != nil {
		t.Fatalf("corrupt cached runner: %v", err)
	}

	externalRoot := t.TempDir()
	externalSentinel := filepath.Join(externalRoot, "must-survive")
	if err := os.WriteFile(externalSentinel, []byte("outside"), 0o600); err != nil {
		t.Fatalf("write external sentinel: %v", err)
	}
	staleTemp := filepath.Join(filepath.Dir(first.Directory), "."+asset.AssetHash+".tmp-interrupted")
	if err := os.Mkdir(staleTemp, 0o700); err != nil {
		t.Fatalf("create interrupted temp: %v", err)
	}
	if err := os.Symlink(externalRoot, filepath.Join(staleTemp, "external-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	recovered, err := manager.Acquire(context.Background(), resolved)
	if err != nil {
		t.Fatalf("recovery Acquire() error = %v", err)
	}
	if recovered != first {
		t.Fatalf("recovered asset = %#v, want stable location %#v", recovered, first)
	}
	if got := source.Calls(); got != 2 {
		t.Fatalf("archive source calls = %d, want initial plus corruption recovery", got)
	}
	assertAssetFiles(t, recovered.Directory, asset.Files)
	assertPathAbsent(t, staleTemp)
	contents, err := os.ReadFile(externalSentinel)
	if err != nil {
		t.Fatalf("read external sentinel: %v", err)
	}
	if string(contents) != "outside" {
		t.Fatalf("external sentinel = %q, want unchanged", contents)
	}
}

func TestManagerAcquireRechecksVerifierMutationBeforePublication(t *testing.T) {
	t.Parallel()

	asset, archive := tarGzipAssetForTestVariant(t, "mutation")
	manifest := Manifest{SchemaVersion: ManifestSchemaVersion, ManifestVersion: "test-v1", Assets: []Asset{asset}}
	resolved, err := Resolve(manifest, runtimeForAsset(asset), requestForAsset(asset))
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	cacheRoot := realTempDir(t)
	verifier := &concurrentVerifier{verify: func(_ int, candidate VerificationCandidate) error {
		runnerPath := filepath.Join(candidate.Path, "runner")
		return os.WriteFile(runnerPath, []byte("mutated-after-verification"), 0o600)
	}}
	_, err = (Manager{
		CacheRoot: cacheRoot,
		Source:    &countingArchiveSource{contents: archive},
		Verifier:  verifier,
	}).Acquire(context.Background(), resolved)
	if !errors.Is(err, ErrInvalidAssetCache) {
		t.Fatalf("Acquire() verifier-mutation error = %v, want ErrInvalidAssetCache", err)
	}
	assertPathAbsent(t, filepath.Join(cacheRoot, asset.HostVersion, asset.AssetHash))
	assertNoAssetTemps(t, filepath.Join(cacheRoot, asset.HostVersion), asset.AssetHash)
}

func TestManagerAcquireRejectsInvalidIdentityAndCancellationBeforePublication(t *testing.T) {
	t.Parallel()

	asset, archive := tarGzipAssetForTestVariant(t, "identity-failure")
	manifest := Manifest{SchemaVersion: ManifestSchemaVersion, ManifestVersion: "test-v1", Assets: []Asset{asset}}
	resolved, err := Resolve(manifest, runtimeForAsset(asset), requestForAsset(asset))
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	t.Run("invalid signature", func(t *testing.T) {
		cacheRoot := realTempDir(t)
		wantErr := errors.New("codesign identity rejected")
		_, err := (Manager{
			CacheRoot: cacheRoot,
			Source:    &countingArchiveSource{contents: archive},
			Verifier: &concurrentVerifier{verify: func(_ int, _ VerificationCandidate) error {
				return wantErr
			}},
		}).Acquire(context.Background(), resolved)
		if !errors.Is(err, wantErr) {
			t.Fatalf("Acquire() identity error = %v, want wrapped verifier error", err)
		}
		assertPathAbsent(t, filepath.Join(cacheRoot, asset.HostVersion, asset.AssetHash))
	})

	t.Run("canceled after signature", func(t *testing.T) {
		cacheRoot := realTempDir(t)
		ctx, cancel := context.WithCancel(context.Background())
		_, err := (Manager{
			CacheRoot: cacheRoot,
			Source:    &countingArchiveSource{contents: archive},
			Verifier: &concurrentVerifier{verify: func(_ int, _ VerificationCandidate) error {
				cancel()
				return nil
			}},
		}).Acquire(ctx, resolved)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Acquire() cancellation error = %v, want context.Canceled", err)
		}
		assertPathAbsent(t, filepath.Join(cacheRoot, asset.HostVersion, asset.AssetHash))
	})
}

func TestManagerAcquireWaitForInterprocessLockHonorsContext(t *testing.T) {
	t.Parallel()

	asset, archive := tarGzipAssetForTestVariant(t, "lock-cancel")
	manifest := Manifest{SchemaVersion: ManifestSchemaVersion, ManifestVersion: "test-v1", Assets: []Asset{asset}}
	resolved, err := Resolve(manifest, runtimeForAsset(asset), requestForAsset(asset))
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	cacheRoot := realTempDir(t)
	_, lockDirectory, err := prepareCacheDirectories(cacheRoot, asset.HostVersion)
	if err != nil {
		t.Fatalf("prepare cache: %v", err)
	}
	locker := FileLocker{}
	held, err := locker.Lock(context.Background(), filepath.Join(lockDirectory, asset.AssetHash+".lock"))
	if err != nil {
		t.Fatalf("hold asset lock: %v", err)
	}
	defer func() {
		if err := held.Unlock(); err != nil {
			t.Errorf("unlock held asset lock: %v", err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	source := &countingArchiveSource{contents: archive}
	_, err = (Manager{CacheRoot: cacheRoot, Source: source, Verifier: &concurrentVerifier{}}).Acquire(ctx, resolved)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Acquire() lock wait error = %v, want context deadline", err)
	}
	if got := source.Calls(); got != 0 {
		t.Fatalf("source calls while waiting for lock = %d, want zero", got)
	}
}

func TestManagerAcquireRejectsSymlinkedLockFileBeforeSourceOrExternalMutation(t *testing.T) {
	t.Parallel()

	asset, archive := tarGzipAssetForTestVariant(t, "lock-link")
	manifest := Manifest{SchemaVersion: ManifestSchemaVersion, ManifestVersion: "test-v1", Assets: []Asset{asset}}
	resolved, err := Resolve(manifest, runtimeForAsset(asset), requestForAsset(asset))
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	cacheRoot := realTempDir(t)
	_, lockDirectory, err := prepareCacheDirectories(cacheRoot, asset.HostVersion)
	if err != nil {
		t.Fatalf("prepare cache directories: %v", err)
	}
	external := filepath.Join(t.TempDir(), "external-lock-target")
	if err := os.WriteFile(external, []byte("unchanged"), 0o600); err != nil {
		t.Fatalf("write external lock target: %v", err)
	}
	if err := os.Symlink(external, filepath.Join(lockDirectory, asset.AssetHash+".lock")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	source := &countingArchiveSource{contents: archive}
	_, err = (Manager{CacheRoot: cacheRoot, Source: source, Verifier: &concurrentVerifier{}}).Acquire(context.Background(), resolved)
	if !errors.Is(err, ErrInvalidAssetCache) {
		t.Fatalf("Acquire() symlinked-lock error = %v, want ErrInvalidAssetCache", err)
	}
	if got := source.Calls(); got != 0 {
		t.Fatalf("source calls through symlinked lock = %d, want zero", got)
	}
	contents, err := os.ReadFile(external)
	if err != nil {
		t.Fatalf("read external lock target: %v", err)
	}
	if string(contents) != "unchanged" {
		t.Fatalf("external lock target = %q, want unchanged", contents)
	}
}

func TestManagerCleanupRetainsActiveAndPreviousAndNeverFollowsLinks(t *testing.T) {
	t.Parallel()

	cacheRoot := realTempDir(t)
	manager := Manager{CacheRoot: cacheRoot, Verifier: &concurrentVerifier{}}

	activeAsset, activeArchive := tarGzipAssetForTestVariant(t, "active")
	activeAsset.HostVersion = "0.3.0"
	acquireTestAsset(t, manager, activeAsset, activeArchive)

	activeStaleAsset, activeStaleArchive := tarGzipAssetForTestVariant(t, "active-stale")
	activeStaleAsset.HostVersion = "0.3.0"
	acquireTestAsset(t, manager, activeStaleAsset, activeStaleArchive)

	previousAsset, previousArchive := tarGzipAssetForTestVariant(t, "previous")
	previousAsset.HostVersion = "0.2.0"
	acquireTestAsset(t, manager, previousAsset, previousArchive)

	oldAsset, oldArchive := tarGzipAssetForTestVariant(t, "old")
	oldAsset.HostVersion = "0.1.0"
	acquireTestAsset(t, manager, oldAsset, oldArchive)

	unownedDirectory := filepath.Join(cacheRoot, "0.1.0", strings.Repeat("d", 64))
	if err := os.MkdirAll(unownedDirectory, 0o700); err != nil {
		t.Fatalf("create unowned directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(unownedDirectory, "sentinel"), []byte("unowned"), 0o600); err != nil {
		t.Fatalf("write unowned sentinel: %v", err)
	}

	externalRoot := t.TempDir()
	externalSentinel := filepath.Join(externalRoot, "must-survive")
	if err := os.WriteFile(externalSentinel, []byte("outside"), 0o600); err != nil {
		t.Fatalf("write external sentinel: %v", err)
	}
	assetLink := filepath.Join(cacheRoot, "0.1.0", strings.Repeat("e", 64))
	if err := os.Symlink(externalRoot, assetLink); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	versionLink := filepath.Join(cacheRoot, "0.0.1")
	if err := os.Symlink(externalRoot, versionLink); err != nil {
		t.Fatalf("create version symlink: %v", err)
	}

	err := manager.Cleanup(context.Background(), RetentionPolicy{
		ActiveHostVersion:   activeAsset.HostVersion,
		ActiveAssetHashes:   []string{activeAsset.AssetHash},
		PreviousHostVersion: previousAsset.HostVersion,
	})
	if err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}

	assertPathExists(t, filepath.Join(cacheRoot, activeAsset.HostVersion, activeAsset.AssetHash))
	assertPathAbsent(t, filepath.Join(cacheRoot, activeStaleAsset.HostVersion, activeStaleAsset.AssetHash))
	assertPathExists(t, filepath.Join(cacheRoot, previousAsset.HostVersion, previousAsset.AssetHash))
	assertPathAbsent(t, filepath.Join(cacheRoot, oldAsset.HostVersion, oldAsset.AssetHash))
	assertPathExists(t, unownedDirectory)
	assertPathExists(t, assetLink)
	assertPathExists(t, versionLink)
	contents, err := os.ReadFile(externalSentinel)
	if err != nil {
		t.Fatalf("read external sentinel after cleanup: %v", err)
	}
	if string(contents) != "outside" {
		t.Fatalf("external sentinel = %q, want unchanged", contents)
	}
}

func TestManagerCleanupValidatesRetentionBeforeFilesystemAccess(t *testing.T) {
	t.Parallel()

	validHash := strings.Repeat("a", 64)
	tests := []struct {
		name    string
		manager Manager
		policy  RetentionPolicy
		wantErr error
	}{
		{
			name:    "missing cache root",
			manager: Manager{},
			policy:  RetentionPolicy{ActiveHostVersion: "1.0.0"},
			wantErr: ErrCacheRootRequired,
		},
		{
			name:    "unsafe active version",
			manager: Manager{CacheRoot: filepath.Join(realTempDir(t), "missing")},
			policy:  RetentionPolicy{ActiveHostVersion: "../escape"},
			wantErr: ErrInvalidAssetCache,
		},
		{
			name:    "unsafe previous version",
			manager: Manager{CacheRoot: filepath.Join(realTempDir(t), "missing")},
			policy:  RetentionPolicy{ActiveHostVersion: "1.0.0", PreviousHostVersion: "../escape"},
			wantErr: ErrInvalidAssetCache,
		},
		{
			name:    "same active and previous",
			manager: Manager{CacheRoot: filepath.Join(realTempDir(t), "missing")},
			policy:  RetentionPolicy{ActiveHostVersion: "1.0.0", PreviousHostVersion: "1.0.0"},
			wantErr: ErrInvalidAssetCache,
		},
		{
			name:    "invalid active hash",
			manager: Manager{CacheRoot: filepath.Join(realTempDir(t), "missing")},
			policy:  RetentionPolicy{ActiveHostVersion: "1.0.0", ActiveAssetHashes: []string{"bad"}},
			wantErr: ErrInvalidAssetCache,
		},
		{
			name:    "duplicate active hash",
			manager: Manager{CacheRoot: filepath.Join(realTempDir(t), "missing")},
			policy:  RetentionPolicy{ActiveHostVersion: "1.0.0", ActiveAssetHashes: []string{validHash, validHash}},
			wantErr: ErrInvalidAssetCache,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			err := test.manager.Cleanup(context.Background(), test.policy)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Cleanup() error = %v, want %v", err, test.wantErr)
			}
		})
	}

	missingRoot := filepath.Join(realTempDir(t), "not-created", "drivers")
	if err := (Manager{CacheRoot: missingRoot}).Cleanup(context.Background(), RetentionPolicy{ActiveHostVersion: "1.0.0"}); err != nil {
		t.Fatalf("Cleanup() missing-root error = %v, want nil", err)
	}
	assertPathAbsent(t, missingRoot)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (Manager{CacheRoot: missingRoot}).Cleanup(canceled, RetentionPolicy{ActiveHostVersion: "1.0.0"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Cleanup() canceled error = %v, want context.Canceled", err)
	}
}

func TestSecureDirectoryPathCreatesMissingComponentsAndRejectsFiles(t *testing.T) {
	t.Parallel()

	base := realTempDir(t)
	nested := filepath.Join(base, "one", "two", "three")
	exists, err := ensureSecureDirectoryPath(nested, false)
	if err != nil || exists {
		t.Fatalf("ensureSecureDirectoryPath(nonexistent, false) = (%v, %v), want (false, nil)", exists, err)
	}
	exists, err = ensureSecureDirectoryPath(nested, true)
	if err != nil || !exists {
		t.Fatalf("ensureSecureDirectoryPath(create) = (%v, %v), want (true, nil)", exists, err)
	}
	assertPathExists(t, nested)

	regular := filepath.Join(base, "regular")
	if err := os.WriteFile(regular, []byte("not-directory"), 0o600); err != nil {
		t.Fatalf("write regular path component: %v", err)
	}
	if _, err := ensureSecureDirectoryPath(filepath.Join(regular, "child"), true); !errors.Is(err, ErrInvalidAssetCache) {
		t.Fatalf("ensureSecureDirectoryPath(file ancestor) error = %v, want ErrInvalidAssetCache", err)
	}
}

func TestManifestModeParserAndNonRegularErrorAreDiagnostic(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"", "0000", "1000", "not-octal"} {
		if _, err := parseManifestMode(mode); err == nil {
			t.Errorf("parseManifestMode(%q) succeeded, want error", mode)
		}
	}
	if mode, err := parseManifestMode("0755"); err != nil || mode != 0o755 {
		t.Fatalf("parseManifestMode(0755) = (%#o, %v), want 0755", mode, err)
	}
	diagnostic := (&NonRegularCandidateError{Path: "candidate", Mode: fs.ModeSymlink}).Error()
	if !strings.Contains(diagnostic, "candidate") || !strings.Contains(diagnostic, "not a regular file") {
		t.Fatalf("NonRegularCandidateError.Error() = %q, want path and reason", diagnostic)
	}
}

func TestManagerTwentyConcurrentProcessesConvergeOnOneVerifiedDirectory(t *testing.T) {
	asset, archive := tarGzipAssetForTestVariant(t, "twenty-processes")
	manifest := Manifest{SchemaVersion: ManifestSchemaVersion, ManifestVersion: "test-v1", Assets: []Asset{asset}}
	manifestContents, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("json.Marshal manifest: %v", err)
	}
	work := realTempDir(t)
	cacheRoot := filepath.Join(work, "drivers")
	archivePath := filepath.Join(work, "asset.tar.gz")
	manifestPath := filepath.Join(work, "manifest.json")
	callLogPath := filepath.Join(work, "source-calls.log")
	if err := os.WriteFile(archivePath, archive, 0o600); err != nil {
		t.Fatalf("write process archive: %v", err)
	}
	if err := os.WriteFile(manifestPath, manifestContents, 0o600); err != nil {
		t.Fatalf("write process manifest: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	type result struct {
		index  int
		output []byte
		err    error
	}
	results := make(chan result, 20)
	for index := 0; index < 20; index++ {
		index := index
		go func() {
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestAssetManagerHelperProcess$", "-test.count=1")
			command.Env = append(os.Environ(),
				"FLOWBATON_ASSET_HELPER=1",
				"FLOWBATON_ASSET_CACHE_ROOT="+cacheRoot,
				"FLOWBATON_ASSET_ARCHIVE="+archivePath,
				"FLOWBATON_ASSET_MANIFEST="+manifestPath,
				"FLOWBATON_ASSET_CALL_LOG="+callLogPath,
			)
			output, err := command.CombinedOutput()
			results <- result{index: index, output: output, err: err}
		}()
	}
	for range 20 {
		result := <-results
		if result.err != nil {
			t.Errorf("asset helper %d failed: %v\n%s", result.index, result.err, result.output)
		}
	}
	if err := ctx.Err(); err != nil {
		t.Fatalf("concurrent helpers did not finish: %v", err)
	}

	callLog, err := os.ReadFile(callLogPath)
	if err != nil {
		t.Fatalf("read source call log: %v", err)
	}
	if calls := len(strings.Fields(string(callLog))); calls != 1 {
		t.Fatalf("archive extraction source calls = %d, want exactly one under interprocess lock; log=%q", calls, callLog)
	}
	finalDirectory := filepath.Join(cacheRoot, asset.HostVersion, asset.AssetHash)
	assertAssetFiles(t, finalDirectory, asset.Files)
	assertNoAssetTemps(t, filepath.Dir(finalDirectory), asset.AssetHash)
	entries, err := os.ReadDir(filepath.Dir(finalDirectory))
	if err != nil {
		t.Fatalf("read version directory: %v", err)
	}
	visibleAssetDirectories := 0
	for _, entry := range entries {
		if canonicalHash(entry.Name()) {
			visibleAssetDirectories++
		}
	}
	if visibleAssetDirectories != 1 {
		t.Fatalf("visible verified asset directories = %d, want one", visibleAssetDirectories)
	}
}

func TestAssetManagerHelperProcess(t *testing.T) {
	if os.Getenv("FLOWBATON_ASSET_HELPER") != "1" {
		return
	}
	manifestContents, err := os.ReadFile(os.Getenv("FLOWBATON_ASSET_MANIFEST"))
	if err != nil {
		t.Fatalf("read helper manifest: %v", err)
	}
	manifest, err := ParseManifest(manifestContents)
	if err != nil {
		t.Fatalf("parse helper manifest: %v", err)
	}
	asset := manifest.Assets[0]
	resolved, err := Resolve(manifest, runtimeForAsset(asset), requestForAsset(asset))
	if err != nil {
		t.Fatalf("resolve helper asset: %v", err)
	}
	manager := Manager{
		CacheRoot: os.Getenv("FLOWBATON_ASSET_CACHE_ROOT"),
		Source: processArchiveSource{
			archivePath: os.Getenv("FLOWBATON_ASSET_ARCHIVE"),
			callLogPath: os.Getenv("FLOWBATON_ASSET_CALL_LOG"),
		},
		Verifier: processIdentityVerifier{},
	}
	acquired, err := manager.Acquire(context.Background(), resolved)
	if err != nil {
		t.Fatalf("helper Acquire(): %v", err)
	}
	if acquired.Directory != filepath.Join(manager.CacheRoot, asset.HostVersion, asset.AssetHash) {
		t.Fatalf("helper acquired directory = %q", acquired.Directory)
	}
}

type processArchiveSource struct {
	archivePath string
	callLogPath string
}

func (s processArchiveSource) Open(ctx context.Context, _ Asset) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	log, err := os.OpenFile(s.callLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err := fmt.Fprintf(log, "%d\n", os.Getpid()); err != nil {
		_ = log.Close()
		return nil, err
	}
	if err := log.Close(); err != nil {
		return nil, err
	}
	timer := time.NewTimer(40 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
	}
	return os.Open(s.archivePath)
}

type processIdentityVerifier struct{}

func (processIdentityVerifier) Verify(ctx context.Context, candidate VerificationCandidate) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Lstat(candidate.Path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("identity candidate is not a real bundle directory: %s", info.Mode())
	}
	return nil
}

type countingArchiveSource struct {
	mu       sync.Mutex
	contents []byte
	calls    int
	delay    time.Duration
}

type archiveSourceFunc func(context.Context, Asset) (io.ReadCloser, error)

func (function archiveSourceFunc) Open(ctx context.Context, asset Asset) (io.ReadCloser, error) {
	return function(ctx, asset)
}

type faultReadCloser struct {
	*bytes.Reader
	readErr  error
	closeErr error
}

func (reader *faultReadCloser) Read(contents []byte) (int, error) {
	if reader.readErr != nil {
		return 0, reader.readErr
	}
	return reader.Reader.Read(contents)
}

func (reader *faultReadCloser) Close() error {
	return reader.closeErr
}

func (s *countingArchiveSource) Open(ctx context.Context, _ Asset) (io.ReadCloser, error) {
	s.mu.Lock()
	s.calls++
	contents := append([]byte(nil), s.contents...)
	delay := s.delay
	s.mu.Unlock()
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return io.NopCloser(bytes.NewReader(contents)), nil
}

func (s *countingArchiveSource) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type concurrentVerifier struct {
	mu     sync.Mutex
	calls  int
	verify func(int, VerificationCandidate) error
}

func (v *concurrentVerifier) Verify(_ context.Context, candidate VerificationCandidate) error {
	v.mu.Lock()
	v.calls++
	call := v.calls
	verify := v.verify
	v.mu.Unlock()
	if verify == nil {
		return nil
	}
	return verify(call, candidate)
}

func (v *concurrentVerifier) Calls() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.calls
}

func tarGzipAssetForTest(t *testing.T) (Asset, []byte) {
	return tarGzipAssetForTestVariant(t, "")
}

func tarGzipAssetForTestVariant(t *testing.T, variant string) (Asset, []byte) {
	return tarGzipAssetForTestArchive(t, variant, nil, nil)
}

func tarGzipAssetForTestWithExtra(t *testing.T, extra tar.Header, body []byte) (Asset, []byte) {
	return tarGzipAssetForTestArchive(t, "unsafe-extra", &extra, body)
}

func tarGzipAssetForTestArchive(t *testing.T, variant string, extra *tar.Header, extraBody []byte) (Asset, []byte) {
	t.Helper()

	files := []struct {
		path string
		mode int64
		body []byte
	}{
		{path: "Runner.app/Info.plist", mode: 0o644, body: []byte("bundle=dev.larchwave.flowbaton.driver\n")},
		{path: "Runner.app/runner", mode: 0o755, body: []byte("#!/bin/sh\necho flowbaton-runner " + variant + "\n")},
	}
	var tarBuffer bytes.Buffer
	tarWriter := tar.NewWriter(&tarBuffer)
	manifestFiles := make([]AssetFile, 0, len(files))
	for _, file := range files {
		header := &tar.Header{
			Name:     file.path,
			Mode:     file.mode,
			Size:     int64(len(file.body)),
			Typeflag: tar.TypeReg,
			ModTime:  time.Unix(0, 0),
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatalf("write tar header: %v", err)
		}
		if _, err := tarWriter.Write(file.body); err != nil {
			t.Fatalf("write tar body: %v", err)
		}
		manifestFiles = append(manifestFiles, AssetFile{
			Path:   file.path,
			SHA256: hashHex(file.body),
			Size:   int64(len(file.body)),
			Mode:   fmt.Sprintf("0%o", file.mode),
		})
	}
	if extra != nil {
		if err := tarWriter.WriteHeader(extra); err != nil {
			t.Fatalf("write extra tar header: %v", err)
		}
		if len(extraBody) > 0 {
			if _, err := tarWriter.Write(extraBody); err != nil {
				t.Fatalf("write extra tar body: %v", err)
			}
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}

	var gzipBuffer bytes.Buffer
	gzipWriter, err := gzip.NewWriterLevel(&gzipBuffer, gzip.BestCompression)
	if err != nil {
		t.Fatalf("gzip.NewWriterLevel: %v", err)
	}
	gzipWriter.Header.ModTime = time.Unix(0, 0)
	gzipWriter.Header.OS = 255
	if _, err := gzipWriter.Write(tarBuffer.Bytes()); err != nil {
		t.Fatalf("write gzip: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}

	assetHash := hashHex(tarBuffer.Bytes())
	asset := Asset{
		ID:           "ios-runner",
		Status:       AssetStatusRelease,
		HostVersion:  "0.1.0",
		AssetVersion: "1.0.0",
		HostOS:       "darwin",
		HostArch:     "arm64",
		Platform:     PlatformIOSSimulator,
		AssetHash:    assetHash,
		Archive: AssetArchive{
			Format:             ArchiveFormatTarGZIP,
			SHA256:             hashHex(gzipBuffer.Bytes()),
			Size:               int64(gzipBuffer.Len()),
			UncompressedSHA256: assetHash,
			UncompressedSize:   int64(tarBuffer.Len()),
		},
		Files: manifestFiles,
		Identity: AssetIdentity{
			Kind:  VerificationBundleSignatureIdentity,
			Value: "dev.larchwave.flowbaton.driver",
			Path:  "Runner.app",
		},
		Compatibility: Compatibility{
			Xcode:      VersionRange{Min: "16.0", Max: "26.2"},
			IOSRuntime: VersionRange{Min: "17.0", Max: "26.0"},
		},
	}
	return asset, append([]byte(nil), gzipBuffer.Bytes()...)
}

func padTarGzipAssetForTest(t *testing.T, asset Asset, archive []byte, recordSize int) (Asset, []byte) {
	t.Helper()
	const tarBlockSize = 512
	payload := gunzipAssetForTest(t, archive)
	padding := recordSize - len(payload)%recordSize
	if padding == 0 {
		padding = recordSize
	}
	if padding <= 2*tarBlockSize {
		t.Fatalf("test tar padding = %d, want bytes beyond the two EOF blocks", padding)
	}
	payload = append(payload, make([]byte, padding)...)
	archive = gzipAssetPayloadForTest(t, payload)
	asset.AssetHash = hashHex(payload)
	asset.Archive.SHA256 = hashHex(archive)
	asset.Archive.Size = int64(len(archive))
	asset.Archive.UncompressedSHA256 = asset.AssetHash
	asset.Archive.UncompressedSize = int64(len(payload))
	return asset, archive
}

func gunzipAssetForTest(t *testing.T, archive []byte) []byte {
	t.Helper()
	reader, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("open test gzip: %v", err)
	}
	payload, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil {
		t.Fatalf("read test gzip: %v", readErr)
	}
	if closeErr != nil {
		t.Fatalf("close test gzip: %v", closeErr)
	}
	return payload
}

func gzipAssetPayloadForTest(t *testing.T, payload []byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	writer, err := gzip.NewWriterLevel(&compressed, gzip.BestCompression)
	if err != nil {
		t.Fatalf("gzip.NewWriterLevel: %v", err)
	}
	writer.Header.ModTime = time.Unix(0, 0)
	writer.Header.OS = 255
	if _, err := writer.Write(payload); err != nil {
		t.Fatalf("write gzip payload: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close gzip payload: %v", err)
	}
	return append([]byte(nil), compressed.Bytes()...)
}

func gzipAssetForTest(t *testing.T) (Asset, []byte, []byte) {
	t.Helper()
	payload := []byte("representative verified android package payload\n")
	var compressed bytes.Buffer
	writer, err := gzip.NewWriterLevel(&compressed, gzip.BestCompression)
	if err != nil {
		t.Fatalf("gzip.NewWriterLevel: %v", err)
	}
	writer.Header.ModTime = time.Unix(0, 0)
	writer.Header.OS = 255
	if _, err := writer.Write(payload); err != nil {
		t.Fatalf("write gzip payload: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close gzip payload: %v", err)
	}
	payloadHash := hashHex(payload)
	asset := Asset{
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
			SHA256:             hashHex(compressed.Bytes()),
			Size:               int64(compressed.Len()),
			UncompressedSHA256: payloadHash,
			UncompressedSize:   int64(len(payload)),
		},
		Files: []AssetFile{{Path: "agent.apk", SHA256: payloadHash, Size: int64(len(payload)), Mode: "0644"}},
		Identity: AssetIdentity{
			Kind:  VerificationPackageIdentity,
			Value: "dev.larchwave.flowbaton",
			Path:  "agent.apk",
		},
		Compatibility: Compatibility{AndroidAPI: IntegerRange{Min: 21, Max: 36}},
	}
	return asset, append([]byte(nil), compressed.Bytes()...), append([]byte(nil), payload...)
}

func acquireTestAsset(t *testing.T, manager Manager, asset Asset, archive []byte) AcquiredAsset {
	t.Helper()
	manifest := Manifest{SchemaVersion: ManifestSchemaVersion, ManifestVersion: "test-v1", Assets: []Asset{asset}}
	resolved, err := Resolve(manifest, runtimeForAsset(asset), requestForAsset(asset))
	if err != nil {
		t.Fatalf("Resolve(%s@%s): %v", asset.ID, asset.AssetVersion, err)
	}
	manager.Source = &countingArchiveSource{contents: archive}
	acquired, err := manager.Acquire(context.Background(), resolved)
	if err != nil {
		t.Fatalf("Acquire(%s@%s): %v", asset.ID, asset.AssetVersion, err)
	}
	return acquired
}

func runtimeForAsset(asset Asset) Runtime {
	runtime := Runtime{HostVersion: asset.HostVersion, HostOS: asset.HostOS, HostArch: asset.HostArch}
	if asset.Platform == PlatformAndroid {
		runtime.AndroidAPI = asset.Compatibility.AndroidAPI.Min
	} else {
		runtime.XcodeVersion = asset.Compatibility.Xcode.Min
		runtime.IOSRuntimeVersion = asset.Compatibility.IOSRuntime.Min
	}
	return runtime
}

func requestForAsset(asset Asset) Request {
	return Request{ID: asset.ID, AssetVersion: asset.AssetVersion, Platform: asset.Platform}
}

func assertAssetFiles(t *testing.T, root string, files []AssetFile) {
	t.Helper()
	for _, file := range files {
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file.Path)))
		if err != nil {
			t.Fatalf("read extracted %s: %v", file.Path, err)
		}
		if got := hashHex(contents); got != file.SHA256 {
			t.Errorf("%s hash = %s, want %s", file.Path, got, file.SHA256)
		}
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(file.Path)))
		if err != nil {
			t.Fatalf("lstat extracted %s: %v", file.Path, err)
		}
		if runtime.GOOS != "windows" {
			wantMode, err := parseManifestMode(file.Mode)
			if err != nil {
				t.Fatalf("parse mode: %v", err)
			}
			if got := info.Mode().Perm(); got != wantMode {
				t.Errorf("%s mode = %#o, want %#o", file.Path, got, wantMode)
			}
		}
	}
}

func assertNoAssetTemps(t *testing.T, versionDirectory, assetHash string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(versionDirectory, "."+assetHash+".tmp-*"))
	if err != nil {
		t.Fatalf("glob asset temp directories: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("asset temp directories remain: %v", matches)
	}
}

func assertPathExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("expected path %q to exist: %v", path, err)
	}
}

func assertPathAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected path %q to be absent: %v", path, err)
	}
}

func realTempDir(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	realDirectory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatalf("resolve temporary directory symlinks: %v", err)
	}
	return realDirectory
}
