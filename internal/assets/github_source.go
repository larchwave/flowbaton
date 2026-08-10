package assets

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	GitHubRepository     = "larchwave/flowbaton"
	GitHubSignerWorkflow = "larchwave/flowbaton/.github/workflows/release-publish.yml"
	manifestSizeLimit    = int64(4 * 1024 * 1024)
)

// CommandRunner runs one external verification command. The injected seam keeps
// provenance verification testable without trusting a fake gh executable.
type CommandRunner func(context.Context, string, ...string) ([]byte, error)

// GitHubReleaseSource downloads driver archives from the release matching the
// host version and verifies their GitHub build attestation before Manager reads
// or publishes any bytes.
type GitHubReleaseSource struct {
	Client  *http.Client
	Run     CommandRunner
	TempDir string
	BaseURL string
}

func (source GitHubReleaseSource) Open(ctx context.Context, asset Asset) (io.ReadCloser, error) {
	url := source.assetURL(asset)
	path, err := source.download(ctx, url, asset.Archive.Size)
	if err != nil {
		return nil, err
	}
	if err := source.verify(ctx, path, asset.HostVersion); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("open verified release asset: %w", err)
	}
	return &removingReadCloser{File: file, path: path}, nil
}

// DownloadManifest obtains and verifies the release's driver manifest. The
// manifest is itself an attested release artifact, so it cannot redirect asset
// selection before provenance has been established.
func (source GitHubReleaseSource) DownloadManifest(ctx context.Context, hostVersion string) ([]byte, error) {
	if !safeToken(hostVersion) {
		return nil, fmt.Errorf("%w: unsafe host version", ErrInvalidAssetManifest)
	}
	url := strings.TrimRight(source.baseURL(), "/") + "/v" + hostVersion + "/driver-manifest.json"
	path, err := source.download(ctx, url, manifestSizeLimit)
	if err != nil {
		return nil, err
	}
	defer os.Remove(path)
	if err := source.verify(ctx, path, hostVersion); err != nil {
		return nil, err
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read verified driver manifest: %w", err)
	}
	return contents, nil
}

func (source GitHubReleaseSource) assetURL(asset Asset) string {
	name := strings.Join([]string{
		"flowbaton", asset.HostVersion, asset.ID, asset.AssetVersion,
		asset.HostOS, asset.HostArch,
	}, "_") + ".tar.gz"
	return strings.TrimRight(source.baseURL(), "/") + "/v" + asset.HostVersion + "/" + name
}

func (source GitHubReleaseSource) baseURL() string {
	if strings.TrimSpace(source.BaseURL) != "" {
		return source.BaseURL
	}
	return "https://github.com/" + GitHubRepository + "/releases/download"
}

func (source GitHubReleaseSource) download(ctx context.Context, url string, maximum int64) (string, error) {
	client := source.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("create release download request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("download release artifact: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return "", fmt.Errorf("download release artifact: %s returned %s", url, response.Status)
	}
	if response.ContentLength > maximum {
		return "", fmt.Errorf("download release artifact: content length %d exceeds %d", response.ContentLength, maximum)
	}
	file, err := os.CreateTemp(source.TempDir, "flowbaton-release-*")
	if err != nil {
		return "", fmt.Errorf("create release artifact temp file: %w", err)
	}
	path := file.Name()
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = os.Remove(path)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return "", fmt.Errorf("secure release artifact temp file: %w", err)
	}
	written, err := io.Copy(file, io.LimitReader(response.Body, maximum+1))
	if err != nil {
		return "", fmt.Errorf("write release artifact temp file: %w", err)
	}
	if written > maximum {
		return "", fmt.Errorf("download release artifact: payload exceeds %d", maximum)
	}
	if err := file.Sync(); err != nil {
		return "", fmt.Errorf("sync release artifact temp file: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close release artifact temp file: %w", err)
	}
	remove = false
	return path, nil
}

func (source GitHubReleaseSource) verify(ctx context.Context, path, hostVersion string) error {
	run := source.Run
	if run == nil {
		run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).CombinedOutput()
		}
	}
	args := []string{
		"attestation", "verify", path,
		"--repo", GitHubRepository,
		"--signer-workflow", GitHubSignerWorkflow,
		"--source-ref", "refs/tags/v" + hostVersion,
		"--deny-self-hosted-runners",
	}
	output, err := run(ctx, "gh", args...)
	if err != nil {
		return fmt.Errorf("verify GitHub release attestation: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

type removingReadCloser struct {
	*os.File
	path string
}

func (reader *removingReadCloser) Close() error {
	closeErr := reader.File.Close()
	removeErr := os.Remove(reader.path)
	if closeErr != nil {
		return closeErr
	}
	if removeErr != nil && !os.IsNotExist(removeErr) {
		return fmt.Errorf("remove verified release temp file %s: %w", filepath.Base(reader.path), removeErr)
	}
	return nil
}
