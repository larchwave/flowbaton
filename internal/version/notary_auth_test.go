package version

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDarwinNotaryCredentialValidation(t *testing.T) {
	base := map[string]string{
		"APPLE_DEVELOPER_ID_APPLICATION":          "Developer ID Application: Example",
		"APPLE_DEVELOPER_ID_CERTIFICATE_BASE64":   "Y2VydA==",
		"APPLE_DEVELOPER_ID_CERTIFICATE_PASSWORD": "certificate-password",
	}
	tests := []struct {
		name      string
		env       map[string]string
		wantOK    bool
		wantError string
	}{
		{name: "missing notarization mode", wantError: "notarization credentials"},
		{
			name: "partial API key mode",
			env: map[string]string{
				"APPLE_NOTARY_KEY_ID": "KEY123",
			},
			wantError: "incomplete App Store Connect API key",
		},
		{
			name: "partial app-specific password mode",
			env: map[string]string{
				"APPLE_NOTARY_APPLE_ID": "developer@example.invalid",
				"APPLE_NOTARY_TEAM_ID":  "TEAM123",
			},
			wantError: "incomplete app-specific password",
		},
		{
			name: "mixed modes",
			env: map[string]string{
				"APPLE_NOTARY_KEY_ID":             "KEY123",
				"APPLE_NOTARY_ISSUER_ID":          "ISSUER123",
				"APPLE_NOTARY_PRIVATE_KEY_BASE64": "a2V5",
				"APPLE_NOTARY_APPLE_ID":           "developer@example.invalid",
			},
			wantError: "must not be mixed",
		},
		{
			name: "complete API key mode",
			env: map[string]string{
				"APPLE_NOTARY_KEY_ID":             "KEY123",
				"APPLE_NOTARY_ISSUER_ID":          "ISSUER123",
				"APPLE_NOTARY_PRIVATE_KEY_BASE64": "a2V5",
			},
			wantOK: true,
		},
		{
			name: "complete app-specific password mode",
			env: map[string]string{
				"APPLE_NOTARY_APPLE_ID": "developer@example.invalid",
				"APPLE_NOTARY_TEAM_ID":  "TEAM123",
				"APPLE_NOTARY_PASSWORD": "app-specific-password",
			},
			wantOK: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := cloneEnv(base)
			for name, value := range tt.env {
				env[name] = value
			}
			command := notaryScriptCommand(t, "--check-credentials")
			command.Env = releaseCredentialEnv(env)
			output, err := command.CombinedOutput()
			if tt.wantOK {
				if err != nil {
					t.Fatalf("credential check failed: %v\n%s", err, output)
				}
				return
			}
			if err == nil {
				t.Fatalf("credential check succeeded; want error containing %q", tt.wantError)
			}
			if !strings.Contains(string(output), tt.wantError) {
				t.Fatalf("credential check error = %q, want substring %q", output, tt.wantError)
			}
			for _, value := range env {
				if value != "" && strings.Contains(string(output), value) {
					t.Fatalf("credential check exposed a secret value in diagnostics: %q", output)
				}
			}
		})
	}
}

func TestReleaseWorkflowProvidesAppPasswordCredentialsToPreflightAndSigning(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release-publish.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(contents)
	for _, secret := range []string{
		"APPLE_NOTARY_APPLE_ID: ${{ secrets.APPLE_NOTARY_APPLE_ID }}",
		"APPLE_NOTARY_TEAM_ID: ${{ secrets.APPLE_NOTARY_TEAM_ID }}",
		"APPLE_NOTARY_PASSWORD: ${{ secrets.APPLE_NOTARY_PASSWORD }}",
	} {
		if count := strings.Count(workflow, secret); count != 2 {
			t.Errorf("release workflow provides %q %d times, want preflight and signing steps", secret, count)
		}
	}
	if !strings.Contains(workflow, "run: scripts/release/sign-notarize-darwin.sh --check-credentials") {
		t.Error("release workflow does not use the signing script's credential preflight")
	}
}

func TestDarwinNotaryAppPasswordUsesIdenticalAuthForSubmitAndLog(t *testing.T) {
	temp := t.TempDir()
	candidate := filepath.Join(temp, "candidate")
	root := filepath.Join(temp, "flowbaton_1.2.3_darwin_arm64")
	if err := os.MkdirAll(candidate, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "flowbaton"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(candidate, "flowbaton_1.2.3_darwin_arm64.tar.gz")
	tarCommand := exec.Command("tar", "-czf", archive, "-C", temp, filepath.Base(root))
	if output, err := tarCommand.CombinedOutput(); err != nil {
		t.Fatalf("create candidate archive: %v\n%s", err, output)
	}

	fakeBin := filepath.Join(temp, "bin")
	if err := os.Mkdir(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, fakeBin, "security", "#!/bin/sh\nexit 0\n")
	writeExecutable(t, fakeBin, "spctl", "#!/bin/sh\nexit 0\n")
	writeExecutable(t, fakeBin, "ditto", "#!/bin/bash\nset -eu\ntouch \"${!#}\"\n")
	writeExecutable(t, fakeBin, "codesign", `#!/bin/bash
set -eu
for argument in "$@"; do
  if [[ "$argument" == "--display" ]]; then
    echo 'Authority=Developer ID Application: Example' >&2
  fi
done
`)
	capture := filepath.Join(temp, "notarytool-arguments.txt")
	writeExecutable(t, fakeBin, "xcrun", `#!/bin/bash
set -eu
if [[ "$1" == "lipo" ]]; then
  echo arm64
  exit 0
fi
printf '%s\n' "$*" >> "$CAPTURE_FILE"
if [[ "$1 $2" == "notarytool submit" ]]; then
  printf '%s\n' '{"status":"Accepted","id":"submission-id"}'
else
  printf '%s\n' '{"jobId":"submission-id","status":"Accepted"}'
fi
`)

	command := notaryScriptCommand(t, candidate, "1.2.3", "arm64", filepath.Join(temp, "receipts"))
	command.Env = releaseCredentialEnv(map[string]string{
		"APPLE_DEVELOPER_ID_APPLICATION":          "Developer ID Application: Example",
		"APPLE_DEVELOPER_ID_CERTIFICATE_BASE64":   "Y2VydA==",
		"APPLE_DEVELOPER_ID_CERTIFICATE_PASSWORD": "certificate-password",
		"APPLE_NOTARY_APPLE_ID":                   "developer@example.invalid",
		"APPLE_NOTARY_TEAM_ID":                    "TEAM123",
		"APPLE_NOTARY_PASSWORD":                   "app-specific-password",
		"CAPTURE_FILE":                            capture,
		"PATH":                                    fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"SOURCE_DATE_EPOCH":                       "1700000000",
	})
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("notarization script failed: %v\n%s", err, output)
	}

	contents, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(contents)), "\n")
	if len(lines) != 2 {
		t.Fatalf("notarytool calls = %q, want submit and log", lines)
	}
	submitAuth := notaryAuthFields(t, lines[0])
	logAuth := notaryAuthFields(t, lines[1])
	want := []string{
		"--apple-id", "developer@example.invalid",
		"--password", "app-specific-password",
		"--team-id", "TEAM123",
	}
	if !reflect.DeepEqual(submitAuth, want) {
		t.Fatalf("submit auth options = %q, want %q", submitAuth, want)
	}
	if !reflect.DeepEqual(logAuth, want) {
		t.Fatalf("log auth options = %q, want %q", logAuth, want)
	}
}

func writeExecutable(t *testing.T, directory, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
}

func notaryAuthFields(t *testing.T, call string) []string {
	t.Helper()
	fields := strings.Fields(call)
	start := -1
	for index, field := range fields {
		if field == "--apple-id" || field == "--key" {
			start = index
			break
		}
	}
	if start < 0 {
		t.Fatalf("notarytool call has no authentication options: %q", call)
	}
	end := len(fields)
	for index := start; index < len(fields); index++ {
		if fields[index] == "--wait" {
			end = index
			break
		}
	}
	return fields[start:end]
}

func cloneEnv(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for name, value := range source {
		result[name] = value
	}
	return result
}

func notaryScriptCommand(t *testing.T, args ...string) *exec.Cmd {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is required to exercise the Darwin notarization script")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(bash, append([]string{filepath.Join(root, "scripts", "release", "sign-notarize-darwin.sh")}, args...)...)
	command.Dir = root
	return command
}

func releaseCredentialEnv(values map[string]string) []string {
	env := make([]string, 0, len(os.Environ())+len(values))
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "APPLE_DEVELOPER_ID_") && !strings.HasPrefix(entry, "APPLE_NOTARY_") {
			env = append(env, entry)
		}
	}
	for name, value := range values {
		env = append(env, name+"="+value)
	}
	return env
}
