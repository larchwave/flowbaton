package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
)

// Authored screenshots receive an extension derived from their bytes. Failure
// captures follow the same rule because compressed iOS screenshots are JPEG
// while Android screenshots are PNG.

// screenshotFlowRun runs a one-screenshot flow and returns the directory the
// authored capture is supposed to land in — the working directory, not the
// run's artifact directory. An author who writes a name is naming a file next
// to themselves; only the run's own captures belong under --test-output-dir.
//
// Chdir means no t.Parallel in the callers: a process has one working
// directory, so a test that owns it cannot share it.
func screenshotFlowRun(t *testing.T, body string, screenshot []byte) (string, int) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "shot.yaml"), "appId: com.example\n---\n"+body)
	output := t.TempDir()
	working := t.TempDir()
	t.Chdir(working)

	driver := fakeDriverShowingWithScreenshot(device.TreeNode{
		Attributes: map[string]string{"text": "OK", "bounds": "[0,0][100,50]"},
	}, screenshot)
	runner := fakeRunner(driver, t.TempDir())
	code := runner.Run(context.Background(), []string{"--test-output-dir", output, filepath.Join(dir, "shot.yaml")},
		discard{}, discard{})
	return working, code
}

func artifactNames(t *testing.T, root string) []string {
	t.Helper()
	var names []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && filepath.Ext(entry.Name()) != ".html" &&
			filepath.Ext(entry.Name()) != ".xml" {
			names = append(names, entry.Name())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return names
}

func TestAnAuthoredScreenshotNameGetsTheExtensionItsBytesDeserve(t *testing.T) {
	working, code := screenshotFlowRun(t, "- takeScreenshot: after-swipe\n", fakeScreenshotPNG)
	if code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	if names := artifactNames(t, working); len(names) != 1 || names[0] != "after-swipe.png" {
		t.Fatalf("artifacts = %v, want [after-swipe.png]", names)
	}
}

func TestAnExtensionTheAuthorAlreadyWroteIsNotDoubled(t *testing.T) {
	// `shot.png.png` would be worse than the bug it fixes.
	working, code := screenshotFlowRun(t, "- takeScreenshot: shot.png\n", fakeScreenshotPNG)
	if code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	if names := artifactNames(t, working); len(names) != 1 || names[0] != "shot.png" {
		t.Fatalf("artifacts = %v, want [shot.png]", names)
	}
}

func TestAJpegScreenshotIsNotCalledPng(t *testing.T) {
	// The exact live case: compressed bytes under a .png name.
	jpeg := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F'}
	working, code := screenshotFlowRun(t, "- takeScreenshot: compressed\n", jpeg)
	if code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	if names := artifactNames(t, working); len(names) != 1 || names[0] != "compressed.jpg" {
		t.Fatalf("artifacts = %v, want [compressed.jpg]", names)
	}
}

func TestBytesInNoFormatAnyoneKnowsAreNotClaimedToBeAnImage(t *testing.T) {
	// A driver answering something else is not this layer's to guess at. `.bin`
	// says so; `.png` would send whoever opens it looking for a corrupt image.
	working, code := screenshotFlowRun(t, "- takeScreenshot: mystery\n", []byte("not an image"))
	if code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	if names := artifactNames(t, working); len(names) != 1 || names[0] != "mystery.bin" {
		t.Fatalf("artifacts = %v, want [mystery.bin]", names)
	}
}

func TestAFailureScreenshotIsNamedAfterWhatItActuallyIs(t *testing.T) {
	t.Parallel()

	// The failure path asks for a COMPRESSED capture on purpose, so on iOS its
	// bytes are JPEG. The name has to follow the bytes.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "fails.yaml"), "appId: com.example\n---\n- assertVisible: Nope\n")
	output := t.TempDir()

	jpeg := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F'}
	driver := fakeDriverShowingWithScreenshot(device.TreeNode{
		Attributes: map[string]string{"bounds": "[0,0][300,600]"},
	}, jpeg)
	runner := fakeRunner(driver, t.TempDir())
	if code := runner.Run(context.Background(), []string{"--test-output-dir", output,
		filepath.Join(dir, "fails.yaml")}, discard{}, discard{}); code == ExitOK {
		t.Fatal("the flow passed; it was supposed to fail and leave a screenshot")
	}

	// Only the capture is this test's business: since debug artifacts were
	// wired up, a run also leaves commands.json and the manifest beside it.
	var captures []string
	for _, name := range artifactNames(t, output) {
		if name != "commands.json" && name != "artifacts.json" {
			captures = append(captures, name)
		}
	}
	if len(captures) != 1 {
		t.Fatalf("captures = %v, want exactly the failure screenshot", captures)
	}
	if filepath.Ext(captures[0]) != ".jpg" {
		t.Fatalf("failure artifact = %q, want a .jpg for jpeg bytes", captures[0])
	}
}
