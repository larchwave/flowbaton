package version

import (
	"os"
	"os/exec"
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

func TestHomebrewCaskRendererSelectsStableAndBetaChannels(t *testing.T) {
	tests := []struct {
		name        string
		version     string
		wantCask    string
		dontWant    string
		wantArchive string
	}{
		{
			name:        "stable",
			version:     "1.2.3",
			wantCask:    `cask "flowbaton" do`,
			dontWant:    `cask "flowbaton-beta" do`,
			wantArchive: "flowbaton_1.2.3_darwin_arm64.tar.gz",
		},
		{
			name:        "beta",
			version:     "1.2.3-beta.2",
			wantCask:    `cask "flowbaton-beta" do`,
			dontWant:    `cask "flowbaton" do`,
			wantArchive: "flowbaton_1.2.3-beta.2_darwin_arm64.tar.gz",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cask := renderHomebrewCask(t, tt.version)
			if !strings.Contains(cask, tt.wantCask) {
				t.Fatalf("rendered cask is missing %q:\n%s", tt.wantCask, cask)
			}
			if strings.Contains(cask, tt.dontWant) {
				t.Fatalf("rendered cask contains wrong channel %q:\n%s", tt.dontWant, cask)
			}
			if !strings.Contains(cask, tt.wantArchive) {
				t.Fatalf("rendered cask is missing archive %q:\n%s", tt.wantArchive, cask)
			}
		})
	}
}

func TestHomebrewCaskRendererRejectsMalformedVersions(t *testing.T) {
	python := releasePython(t)
	for _, version := range []string{
		"1.2",
		"01.2.3",
		"1.2.3-",
		"1.2.3-01",
		"1.2.3+build",
		"not-a-version",
	} {
		t.Run(version, func(t *testing.T) {
			root := filepath.Join("..", "..")
			candidate := t.TempDir()
			for _, arch := range []string{"arm64", "amd64"} {
				name := "flowbaton_" + version + "_darwin_" + arch + ".tar.gz"
				if err := os.WriteFile(filepath.Join(candidate, name), []byte("candidate "+arch), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			output := filepath.Join(t.TempDir(), "flowbaton.rb")
			command := exec.Command(
				python, filepath.Join(root, "scripts", "release", "render-homebrew-cask.py"),
				"--version", version,
				"--candidate", candidate,
				"--output", output,
			)
			if err := command.Run(); err == nil {
				t.Fatalf("renderer accepted malformed version %q", version)
			}
		})
	}
}

func TestReleaseWorkflowPublishesStableAndBetaChannelsSeparately(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release-publish.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(contents)
	for _, want := range []string{
		`cask=flowbaton-beta`,
		`cask_path=Casks/flowbaton-beta.rb`,
		`prerelease=true`,
		`latest=false`,
		`cask=flowbaton`,
		`cask_path=Casks/flowbaton.rb`,
		`prerelease=false`,
		`latest=true`,
		`--prerelease="$prerelease"`,
		`--latest="$latest"`,
		`--output "$tap/$cask_path"`,
		`git -C "$tap" add "$cask_path"`,
	} {
		if !strings.Contains(workflow, want) {
			t.Errorf("release workflow is missing channel behavior %q", want)
		}
	}
	// Execute the actual workflow branch: tokens alone would still pass if
	// the beta and stable assignments were accidentally inverted.
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is required to execute the release workflow branch")
	}
	start := strings.Index(workflow, `          TAG="$GITHUB_REF_NAME"`)
	if start < 0 {
		t.Fatal("release workflow tag assignment not found")
	}
	end := strings.Index(workflow[start:], `          if gh release view`)
	if end < 0 {
		t.Fatal("release workflow channel branch not found")
	}
	branch := workflow[start : start+end]
	for _, test := range []struct{ tag, want string }{
		{"v1.2.3", "flowbaton Casks/flowbaton.rb false true"},
		{"v1.2.3-beta.2", "flowbaton-beta Casks/flowbaton-beta.rb true false"},
		{"v1.2.3-rc.1", "flowbaton-beta Casks/flowbaton-beta.rb true false"},
	} {
		command := exec.Command(bash, "-c", branch+`printf '%s %s %s %s' "$cask" "$cask_path" "$prerelease" "$latest"`)
		command.Env = append(os.Environ(), "GITHUB_REF_NAME="+test.tag)
		output, err := command.CombinedOutput()
		if err != nil || string(output) != test.want {
			t.Fatalf("channel for %s = %q, %v; want %q", test.tag, output, err, test.want)
		}
	}
}

func renderHomebrewCask(t *testing.T, version string) string {
	t.Helper()
	python := releasePython(t)
	root := filepath.Join("..", "..")
	candidate := t.TempDir()
	for _, arch := range []string{"arm64", "amd64"} {
		name := "flowbaton_" + version + "_darwin_" + arch + ".tar.gz"
		if err := os.WriteFile(filepath.Join(candidate, name), []byte("candidate "+arch), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	output := filepath.Join(t.TempDir(), "flowbaton.rb")
	command := exec.Command(
		python, filepath.Join(root, "scripts", "release", "render-homebrew-cask.py"),
		"--version", version,
		"--candidate", candidate,
		"--base-url", "https://example.invalid/releases/v{version}",
		"--output", output,
	)
	if outputBytes, err := command.CombinedOutput(); err != nil {
		t.Fatalf("render cask for %s: %v\n%s", version, err, outputBytes)
	}
	contents, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func releasePython(t *testing.T) string {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python 3 is required for release renderer checks")
	}
	return python
}
