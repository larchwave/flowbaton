package foundation_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestAndroidConnectedRunnerHandlesEmulatorLifecycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Android connected runner is a Bash job on Linux")
	}
	if _, err := exec.LookPath("timeout"); err != nil {
		t.Skip("timeout command is unavailable")
	}

	for _, test := range []struct {
		name       string
		mode       string
		readyLimit string
		wantError  bool
		wantOutput []string
	}{
		{name: "ready", mode: "ready", readyLimit: "5"},
		{name: "emulator exit", mode: "exit", readyLimit: "10", wantError: true, wantOutput: []string{
			"Android emulator exited with status 23", "emulator-exit-marker",
		}},
		{name: "readiness timeout", mode: "timeout", readyLimit: "1", wantError: true, wantOutput: []string{
			"Android emulator did not become ready within 1 seconds", "emulator-timeout-marker",
		}},
		{name: "adb ignores term", mode: "adb-hang", readyLimit: "1", wantError: true, wantOutput: []string{
			"Android emulator did not become ready within 1 seconds", "emulator-adb-hang-marker",
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			bin := filepath.Join(directory, "bin")
			if err := os.Mkdir(bin, 0o700); err != nil {
				t.Fatal(err)
			}
			androidSDK := filepath.Join(directory, "android-sdk")
			if err := os.MkdirAll(filepath.Join(androidSDK, "emulator"), 0o700); err != nil {
				t.Fatal(err)
			}
			writeExecutable(t, filepath.Join(bin, "sdkmanager"), "#!/usr/bin/env bash\nexit 0\n")
			writeExecutable(t, filepath.Join(bin, "avdmanager"), "#!/usr/bin/env bash\nexit 0\n")
			writeExecutable(t, filepath.Join(androidSDK, "emulator", "emulator"), `#!/usr/bin/env bash
if [[ "${1:-}" == "-version" ]]; then
  printf '%s\n' 'Android emulator harness'
  exit 0
fi
case "${HARNESS_EMULATOR_MODE:?}" in
  exit)
    printf '%s\n' 'emulator-exit-marker' >&2
    exit 23
    ;;
  timeout|adb-hang)
    printf '%s\n' "emulator-${HARNESS_EMULATOR_MODE}-marker" >&2
    ;;
esac
exec sleep 3600
`)
			writeExecutable(t, filepath.Join(bin, "adb"), `#!/usr/bin/env bash
if [[ "${HARNESS_EMULATOR_MODE:?}" == "adb-hang" && "$*" == *'getprop sys.boot_completed'* ]]; then
  trap '' TERM
  exec sleep 3600
fi
if [[ "${HARNESS_EMULATOR_MODE:?}" != "ready" ]]; then
  exit 1
fi
case "$*" in
  *'getprop sys.boot_completed'*) printf '%s\n' '1' ;;
  *'/system/bin/pm path android'*) printf '%s\n' 'package:/system/framework/framework-res.apk' ;;
esac
`)
			resultPath := filepath.Join(directory, "gradle-result")
			writeExecutable(t, filepath.Join(directory, "gradlew"), `#!/usr/bin/env bash
printf '%s|%s\n' "${ANDROID_SERIAL:-}" "$*" >"${HARNESS_RESULT:?}"
`)

			command := exec.Command(
				"timeout", "--kill-after=2s", "25s",
				"bash", filepath.Join(repoRoot(t), "scripts", "ci", "android-connected.sh"),
			)
			command.Dir = directory
			command.Env = append(os.Environ(),
				"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
				"ANDROID_SDK_ROOT="+androidSDK,
				"ANDROID_HOME="+androidSDK,
				"RUNNER_TEMP="+directory,
				"ANDROID_EMULATOR_READY_TIMEOUT_SECONDS="+test.readyLimit,
				"HARNESS_EMULATOR_MODE="+test.mode,
				"HARNESS_RESULT="+resultPath,
			)
			started := time.Now()
			output, err := command.CombinedOutput()
			if (err != nil) != test.wantError {
				t.Fatalf("runner error = %v, wantError %t; output:\n%s", err, test.wantError, output)
			}
			if elapsed := time.Since(started); elapsed > 20*time.Second {
				t.Fatalf("runner took %s; emulator startup must fail promptly", elapsed)
			}
			for _, want := range test.wantOutput {
				if !strings.Contains(string(output), want) {
					t.Errorf("output does not contain %q:\n%s", want, output)
				}
			}
			if test.mode == "ready" {
				result, readErr := os.ReadFile(resultPath)
				if readErr != nil {
					t.Fatal(readErr)
				}
				for _, want := range []string{"emulator-5554", "connectedDebugAndroidTest"} {
					if !strings.Contains(string(result), want) {
						t.Errorf("Gradle invocation does not contain %q: %s", want, result)
					}
				}
			}
		})
	}
}

func writeExecutable(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
}
