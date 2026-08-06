package version

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoReleaserInjectsSnapshotOrTagVersion(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("..", "..", ".goreleaser.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	const want = "-X github.com/larchwave/flowbaton/internal/version.Version={{ .Version }}"
	if !strings.Contains(string(contents), want) {
		t.Fatalf(".goreleaser.yaml must contain %q", want)
	}
}

func TestIOSProjectSpecPinsTheGeneratorAndTestTargets(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("..", "..", "drivers", "ios", "project.yml"))
	if err != nil {
		t.Fatal(err)
	}
	spec := string(contents)
	for _, want := range []string{
		"minimumXcodeGenVersion: 2.44.1",
		"iOS: \"17.0\"",
		"SWIFT_VERSION: \"6.0\"",
		"type: framework",
		"type: bundle.unit-test",
		"type: bundle.ui-testing",
		"path: Sources/FlowBatonIOSRunner",
		"path: Tests/FlowBatonIOSRunnerTests",
		"path: UITests/FlowBatonIOSRunnerUITests",
		"FlowBatonIOSRunnerUITests:",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("drivers/ios/project.yml is missing %q", want)
		}
	}
}

func TestCIRegeneratesAndBuildsTheIOSRunnerOnMacOS(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	ci := string(contents)
	for _, want := range []string{
		"ios:",
		"runs-on: macos-15",
		"https://github.com/yonaskolb/XcodeGen/releases/download/2.44.1/xcodegen.zip",
		"a2e905fb68446e9bb4008cdfe2e13e3f176d0cbcca828b71770f8e53fca91b73",
		"xcodegen generate --spec drivers/ios/project.yml --project drivers/ios",
		"swift test --package-path drivers/ios",
		"swift-format lint --strict --recursive",
		"drivers/ios/Sources drivers/ios/Tests drivers/ios/UITests",
		"-scheme FlowBatonIOSRunnerUITests",
		"generic/platform=iOS Simulator",
		"build-for-testing",
	} {
		if !strings.Contains(ci, want) {
			t.Errorf("iOS CI is missing %q", want)
		}
	}
}
