package assets

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/larchwave/flowbaton/internal/strictjson"
)

func extractArchive(root string, asset Asset, archive []byte) error {
	switch asset.Archive.Format {
	case ArchiveFormatGZIP:
		return extractGZIP(root, asset, archive)
	case ArchiveFormatTarGZIP:
		return extractTarGZIP(root, asset, archive)
	default:
		return fmt.Errorf("%w: unsupported archive format %q", ErrInvalidAssetManifest, asset.Archive.Format)
	}
}

func extractGZIP(root string, asset Asset, archive []byte) error {
	reader, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return fmt.Errorf("open gzip asset archive: %w", err)
	}
	payload, readErr := io.ReadAll(io.LimitReader(reader, asset.Archive.UncompressedSize+1))
	closeErr := reader.Close()
	if readErr != nil {
		return fmt.Errorf("read gzip asset archive: %w", readErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close gzip asset archive: %w", closeErr)
	}
	if got := int64(len(payload)); got != asset.Archive.UncompressedSize {
		return fmt.Errorf("%w: got %d, want %d", ErrPayloadSizeMismatch, got, asset.Archive.UncompressedSize)
	}
	if got := sha256Hex(payload); got != asset.Archive.UncompressedSHA256 {
		return fmt.Errorf("%w: got %s, want %s", ErrPayloadHashMismatch, got, asset.Archive.UncompressedSHA256)
	}
	return writeExtractedFile(root, asset.Files[0], bytes.NewReader(payload))
}

func extractTarGZIP(root string, asset Asset, archive []byte) error {
	gzipReader, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return fmt.Errorf("open tar+gzip asset archive: %w", err)
	}
	digest := sha256.New()
	counter := &countingWriter{writer: digest}
	payloadReader := io.TeeReader(
		io.LimitReader(gzipReader, asset.Archive.UncompressedSize+1),
		counter,
	)
	tarReader := tar.NewReader(payloadReader)
	declared := make(map[string]AssetFile, len(asset.Files))
	allowedDirectories := assetDirectories(asset.Files)
	for _, file := range asset.Files {
		declared[file.Path] = file
	}
	seen := make(map[string]struct{}, len(asset.Files))
	for {
		header, nextErr := tarReader.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			_ = gzipReader.Close()
			return fmt.Errorf("read tar asset entry: %w", nextErr)
		}
		name := strings.TrimSuffix(header.Name, "/")
		if !validAssetPath(name) {
			_ = gzipReader.Close()
			return fmt.Errorf("%w: unsafe tar path %q", ErrInvalidAssetManifest, header.Name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if _, allowed := allowedDirectories[name]; !allowed {
				_ = gzipReader.Close()
				return fmt.Errorf("%w: undeclared tar directory %q", ErrInvalidAssetManifest, name)
			}
			if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(name)), 0o700); err != nil {
				_ = gzipReader.Close()
				return fmt.Errorf("create tar directory %q: %w", name, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			file, ok := declared[name]
			if !ok {
				_ = gzipReader.Close()
				return fmt.Errorf("%w: undeclared tar file %q", ErrInvalidAssetManifest, name)
			}
			if _, duplicate := seen[name]; duplicate {
				_ = gzipReader.Close()
				return fmt.Errorf("%w: duplicate tar file %q", ErrInvalidAssetManifest, name)
			}
			if header.Size != file.Size {
				_ = gzipReader.Close()
				return fmt.Errorf("%w: tar file %q size %d, want %d", ErrPayloadSizeMismatch, name, header.Size, file.Size)
			}
			if err := writeExtractedFile(root, file, io.LimitReader(tarReader, header.Size)); err != nil {
				_ = gzipReader.Close()
				return err
			}
			seen[name] = struct{}{}
		default:
			_ = gzipReader.Close()
			return fmt.Errorf("%w: tar entry %q has unsupported type %d", ErrInvalidAssetManifest, name, header.Typeflag)
		}
	}
	if _, err := io.Copy(io.Discard, payloadReader); err != nil {
		_ = gzipReader.Close()
		return fmt.Errorf("read tar+gzip asset tail: %w", err)
	}
	if err := gzipReader.Close(); err != nil {
		return fmt.Errorf("close tar+gzip asset archive: %w", err)
	}
	if counter.count != asset.Archive.UncompressedSize {
		return fmt.Errorf("%w: got %d, want %d", ErrPayloadSizeMismatch, counter.count, asset.Archive.UncompressedSize)
	}
	if got := fmt.Sprintf("%x", digest.Sum(nil)); got != asset.Archive.UncompressedSHA256 {
		return fmt.Errorf("%w: got %s, want %s", ErrPayloadHashMismatch, got, asset.Archive.UncompressedSHA256)
	}
	if len(seen) != len(declared) {
		return fmt.Errorf("%w: archive contains %d of %d declared files", ErrPayloadSizeMismatch, len(seen), len(declared))
	}
	return nil
}

type countingWriter struct {
	writer hash.Hash
	count  int64
}

func (w *countingWriter) Write(contents []byte) (int, error) {
	w.count += int64(len(contents))
	return w.writer.Write(contents)
}

func writeExtractedFile(root string, manifest AssetFile, source io.Reader) error {
	path := filepath.Join(root, filepath.FromSlash(manifest.Path))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create parent for asset file %q: %w", manifest.Path, err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create asset file %q: %w", manifest.Path, err)
	}
	digest := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, digest), source)
	syncErr := file.Sync()
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("extract asset file %q: %w", manifest.Path, copyErr)
	}
	if syncErr != nil {
		return fmt.Errorf("sync asset file %q: %w", manifest.Path, syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close asset file %q: %w", manifest.Path, closeErr)
	}
	if written != manifest.Size {
		return fmt.Errorf("%w: file %q got %d, want %d", ErrPayloadSizeMismatch, manifest.Path, written, manifest.Size)
	}
	if got := fmt.Sprintf("%x", digest.Sum(nil)); got != manifest.SHA256 {
		return fmt.Errorf("%w: file %q got %s, want %s", ErrPayloadHashMismatch, manifest.Path, got, manifest.SHA256)
	}
	mode, err := parseManifestMode(manifest.Mode)
	if err != nil {
		return err
	}
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("restore asset file mode %q: %w", manifest.Path, err)
	}
	return nil
}

func verifyAssetDirectory(ctx context.Context, root string, resolved ResolvedAsset, verifier IdentityVerifier) error {
	if err := verifyAssetTree(root, resolved); err != nil {
		return err
	}
	candidate := VerificationCandidate{
		Path:     filepath.Join(root, filepath.FromSlash(resolved.Asset.Identity.Path)),
		Platform: resolved.Asset.Platform,
		Identity: resolved.Asset.Identity.Value,
		Kind:     resolved.Asset.Identity.Kind,
	}
	if err := verifier.Verify(ctx, candidate); err != nil {
		return fmt.Errorf("verify asset identity: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Verification is followed by a full recheck so a verifier cannot mutate or
	// replace the candidate and still cause it to be published as verified.
	return verifyAssetTree(root, resolved)
}

func verifyAssetTree(root string, resolved ResolvedAsset) error {
	if err := requireRealDirectory(root); err != nil {
		return err
	}
	marker, err := readCacheMarker(root)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(marker, expectedCacheMarker(resolved)) {
		return fmt.Errorf("%w: cache marker does not match resolved descriptor", ErrInvalidAssetCache)
	}
	declared := make(map[string]AssetFile, len(resolved.Asset.Files))
	for _, file := range resolved.Asset.Files {
		declared[file.Path] = file
	}
	allowedDirectories := assetDirectories(resolved.Asset.Files)
	seen := make(map[string]struct{}, len(declared))
	err = filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if current == root {
			return nil
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%w: cache entry %q is a symlink", ErrInvalidAssetCache, relative)
		}
		if entry.IsDir() {
			if _, allowed := allowedDirectories[relative]; !allowed {
				return fmt.Errorf("%w: undeclared cache directory %q", ErrInvalidAssetCache, relative)
			}
			return nil
		}
		if relative == ".flowbaton-asset.json" {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("%w: cache marker is not regular", ErrInvalidAssetCache)
			}
			return nil
		}
		manifest, declaredFile := declared[relative]
		if !declaredFile || !info.Mode().IsRegular() {
			return fmt.Errorf("%w: undeclared or non-regular cache file %q", ErrInvalidAssetCache, relative)
		}
		contents, err := os.ReadFile(current)
		if err != nil {
			return err
		}
		if int64(len(contents)) != manifest.Size || sha256Hex(contents) != manifest.SHA256 {
			return fmt.Errorf("%w: file %q hash or size mismatch", ErrInvalidAssetCache, relative)
		}
		mode, err := parseManifestMode(manifest.Mode)
		if err != nil {
			return err
		}
		if !fileModeMatchesPlatform(info.Mode().Perm(), mode) {
			return fmt.Errorf("%w: file %q mode %#o, want %#o", ErrInvalidAssetCache, relative, info.Mode().Perm(), mode)
		}
		seen[relative] = struct{}{}
		return nil
	})
	if err != nil {
		return err
	}
	if len(seen) != len(declared) {
		return fmt.Errorf("%w: cache contains %d of %d declared files", ErrInvalidAssetCache, len(seen), len(declared))
	}
	identityPath := filepath.Join(root, filepath.FromSlash(resolved.Asset.Identity.Path))
	identityInfo, err := os.Lstat(identityPath)
	if err != nil {
		return fmt.Errorf("%w: lstat identity candidate: %v", ErrInvalidAssetCache, err)
	}
	if identityInfo.Mode()&fs.ModeSymlink != 0 || (!identityInfo.IsDir() && !identityInfo.Mode().IsRegular()) {
		return fmt.Errorf("%w: identity candidate is not a real file or directory", ErrInvalidAssetCache)
	}
	return nil
}

func readCacheMarker(root string) (cacheMarker, error) {
	path := filepath.Join(root, ".flowbaton-asset.json")
	info, err := os.Lstat(path)
	if err != nil {
		return cacheMarker{}, fmt.Errorf("%w: lstat cache marker: %v", ErrInvalidAssetCache, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&fs.ModeSymlink != 0 {
		return cacheMarker{}, fmt.Errorf("%w: cache marker is not a regular file", ErrInvalidAssetCache)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return cacheMarker{}, fmt.Errorf("%w: read cache marker: %v", ErrInvalidAssetCache, err)
	}
	var marker cacheMarker
	if err := strictjson.Decode(contents, &marker); err != nil {
		return cacheMarker{}, fmt.Errorf("%w: decode cache marker: %v", ErrInvalidAssetCache, err)
	}
	return marker, nil
}

func assetDirectories(files []AssetFile) map[string]struct{} {
	directories := make(map[string]struct{})
	for _, file := range files {
		current := path.Dir(file.Path)
		for current != "." && current != "/" {
			directories[current] = struct{}{}
			next := path.Dir(current)
			if next == current {
				break
			}
			current = next
		}
	}
	return directories
}
