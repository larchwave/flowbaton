package assets

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestGitHubReleaseSourceVerifiesBeforeReturningArchive(t *testing.T) {
	payload := []byte("attested driver archive")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !strings.HasSuffix(request.URL.Path, ".tar.gz") {
			t.Fatalf("download path = %q", request.URL.Path)
		}
		_, _ = writer.Write(payload)
	}))
	defer server.Close()

	var command string
	var arguments []string
	source := GitHubReleaseSource{
		BaseURL: server.URL,
		TempDir: t.TempDir(),
		Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			command = name
			arguments = append([]string(nil), args...)
			if _, err := os.Stat(args[2]); err != nil {
				t.Fatalf("gh received no downloaded artifact: %v", err)
			}
			return nil, nil
		},
	}
	asset := validReleaseAsset()
	asset.Archive.Size = int64(len(payload))
	reader, err := source.Open(context.Background(), asset)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("archive = %q, want %q", got, payload)
	}
	if command != "gh" {
		t.Fatalf("verification command = %q, want gh", command)
	}
	for _, want := range []string{
		"attestation", "verify", "--repo", GitHubRepository,
		"--signer-workflow", GitHubSignerWorkflow,
		"--source-ref", "refs/tags/v" + asset.HostVersion,
	} {
		if !contains(arguments, want) {
			t.Fatalf("gh arguments %q do not contain %q", arguments, want)
		}
	}
}

func TestGitHubReleaseSourceDownloadsAndVerifiesManifest(t *testing.T) {
	payload := []byte(`{"schema_version":"flowbaton.assets.v0"}`)
	var requested string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requested = request.URL.Path
		_, _ = writer.Write(payload)
	}))
	defer server.Close()

	verified := false
	source := GitHubReleaseSource{
		BaseURL: server.URL,
		TempDir: t.TempDir(),
		Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			verified = name == "gh" && len(args) > 2
			return nil, nil
		},
	}
	got, err := source.DownloadManifest(context.Background(), "1.2.3")
	if err != nil {
		t.Fatalf("DownloadManifest() error = %v", err)
	}
	if requested != "/v1.2.3/driver-manifest.json" {
		t.Fatalf("manifest path = %q", requested)
	}
	if !verified || string(got) != string(payload) {
		t.Fatalf("verified = %v, contents = %q", verified, got)
	}
}

func TestGitHubReleaseSourceFailsClosedOnAttestationFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("archive"))
	}))
	defer server.Close()
	want := errors.New("untrusted signer")
	source := GitHubReleaseSource{
		BaseURL: server.URL,
		TempDir: t.TempDir(),
		Run: func(context.Context, string, ...string) ([]byte, error) {
			return []byte("signer workflow mismatch"), want
		},
	}
	asset := validReleaseAsset()
	asset.Archive.Size = int64(len("archive"))
	if _, err := source.Open(context.Background(), asset); !errors.Is(err, want) {
		t.Fatalf("Open() error = %v, want %v", err, want)
	}
}

func TestGitHubReleaseSourceRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("too large"))
	}))
	defer server.Close()
	source := GitHubReleaseSource{BaseURL: server.URL, TempDir: t.TempDir()}
	asset := validReleaseAsset()
	asset.Archive.Size = 2
	if _, err := source.Open(context.Background(), asset); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("Open() error = %v, want size refusal", err)
	}
}

func TestCommandIdentityVerifierChecksPlatformIdentity(t *testing.T) {
	tests := []struct {
		name      string
		candidate VerificationCandidate
		output    string
		command   string
		args      []string
	}{
		{
			name: "android", candidate: VerificationCandidate{
				Path: "/tmp/agent.apk", Kind: VerificationPackageIdentity,
				Identity: "dev.larchwave.flowbaton",
			},
			output: "dev.larchwave.flowbaton\n", command: "apkanalyzer",
			args: []string{"manifest", "application-id", "/tmp/agent.apk"},
		},
		{
			name: "ios", candidate: VerificationCandidate{
				Path: "/tmp/Runner.app", Kind: VerificationBundleSignatureIdentity,
				Identity: "dev.larchwave.flowbaton.driver",
			},
			output: "Executable=/tmp/Runner.app/Runner\nIdentifier=dev.larchwave.flowbaton.driver\n", command: "codesign",
			args: []string{"-dv", "--verbose=4", "/tmp/Runner.app"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			verifier := CommandIdentityVerifier{Run: func(_ context.Context, command string, args ...string) ([]byte, error) {
				if command != test.command || !reflect.DeepEqual(args, test.args) {
					t.Fatalf("command = %s %q, want %s %q", command, args, test.command, test.args)
				}
				return []byte(test.output), nil
			}}
			if err := verifier.Verify(context.Background(), test.candidate); err != nil {
				t.Fatalf("Verify() error = %v", err)
			}
		})
	}
}

func TestAndroidAnalyzerPathFallsBackToTheConfiguredSDK(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	sdk := t.TempDir()
	t.Setenv("ANDROID_HOME", sdk)
	t.Setenv("ANDROID_SDK_ROOT", "")
	name := "apkanalyzer"
	if runtime.GOOS == "windows" {
		name += ".bat"
	}
	want := filepath.Join(sdk, "cmdline-tools", "latest", "bin", name)
	if err := os.MkdirAll(filepath.Dir(want), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(want, []byte("tool"), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := androidAnalyzerPath()
	if err != nil {
		t.Fatalf("androidAnalyzerPath() error = %v", err)
	}
	if got != want {
		t.Fatalf("androidAnalyzerPath() = %q, want %q", got, want)
	}
}

func contains(values []string, want string) bool {
	return slices.Contains(values, want)
}
