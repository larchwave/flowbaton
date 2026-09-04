package version

import (
	"os"
	"strings"
	"testing"
)

func TestCandidateDownloadsPreserveCleanCheckout(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/release-publish.yml")
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(data)
	start := strings.Index(workflow, "  build-candidate:")
	end := strings.Index(workflow, "  darwin-sign-notarize:")
	if start < 0 || end <= start {
		t.Fatal("candidate job missing")
	}
	job := workflow[start:end]
	for _, want := range []string{"path: ${{ runner.temp }}/mobile-assets", `cp "$RUNNER_TEMP"/mobile-assets/*.tar.gz candidate/`, `"$RUNNER_TEMP"/mobile-assets/*.asset.json`, "args: release --clean --skip=publish,homebrew"} {
		if !strings.Contains(job, want) {
			t.Errorf("candidate job missing %q", want)
		}
	}
	if strings.Contains(job, "skip-validate") {
		t.Fatal("candidate must retain clean checkout validation")
	}
}
