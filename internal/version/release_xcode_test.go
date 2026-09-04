package version

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestReleaseIOSJobsSelectXcode262(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release-publish.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(contents)
	for _, job := range []string{"ios-simulator", "clean-host-smoke"} {
		section := releaseJobSection(t, workflow, job)
		for _, want := range []string{
			"/Applications/Xcode_26.2.app/Contents/Developer",
			`sudo xcode-select --switch "$developer_dir"`,
			`test "$xcode_version" = "Xcode 26.2"`,
			`test "$xcode_build" = "Build version 17C52"`,
		} {
			if !strings.Contains(section, want) {
				t.Errorf("release job %s does not verify %q", job, want)
			}
		}
	}

	cleanHost := releaseJobSection(t, workflow, "clean-host-smoke")
	if !strings.Contains(cleanHost, "if: runner.os == 'macOS'") {
		t.Error("clean-host Xcode selection must only run on macOS")
	}
}

func releaseJobSection(t *testing.T, workflow, job string) string {
	t.Helper()
	startMarker := "  " + job + ":\n"
	start := strings.Index(workflow, startMarker)
	if start < 0 {
		t.Fatalf("release job %s not found", job)
	}
	remainder := workflow[start+len(startMarker):]
	end := len(remainder)
	if next := regexp.MustCompile(`(?m)^  [^ \n][^\n]*:\s*$`).FindStringIndex(remainder); next != nil {
		end = next[0]
	}
	return remainder[:end]
}
