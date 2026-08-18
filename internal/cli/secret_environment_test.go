package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/enginetest"
)

// Exported explore flows carry ${FLOWBATON_EXPLORE_SECRET_n} placeholders for
// text that was typed into a secure field. These tests pin the two promises
// that make that export safe: an unset secret fails the flow instead of
// typing the literal "undefined", and a supplied secret reaches the device
// without reaching the written artifacts.

func secretFlowDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.yaml"),
		"appId: com.example.a\n---\n- inputText: ${FLOWBATON_EXPLORE_SECRET_1}\n")
	return dir
}

func TestAnUnsetSecretVariableFailsTheFlow(t *testing.T) {
	t.Parallel()

	dir := secretFlowDir(t)
	driver := permissiveDriver()
	runner := fakeRunner(driver, dir)
	runner.Environ = func() []string { return []string{"HOME=/home/operator"} }
	var stdout, stderr strings.Builder
	if code := runner.Run(context.Background(), []string{
		"--device", "solo-udid", "--test-output-dir", t.TempDir(),
		filepath.Join(dir, "a.yaml"),
	}, &stdout, &stderr); code == ExitOK {
		t.Fatalf("an unset secret passed\nstdout: %s", stdout.String())
	}
	if got := typedText(driver); got != "" {
		t.Fatalf("the device was typed into anyway: %q", got)
	}
	combined := stdout.String() + stderr.String()
	if !strings.Contains(combined, "FLOWBATON_EXPLORE_SECRET_1") {
		t.Fatalf("the failure does not name the variable:\n%s", combined)
	}
}

func TestAFailedSecretInputStaysOutOfArtifacts(t *testing.T) {
	t.Parallel()

	dir := secretFlowDir(t)
	outputDir := t.TempDir()
	// Android's no-focus failure quotes what was typed; the report pipeline
	// must not persist that quote. The error must be first in the queue —
	// Enqueue appends, and permissiveDriver prefills InputText successes.
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{InputText: []enginetest.Result[struct{}]{
		{Err: errors.New(`no focused field accepts "hunter2"`)},
	}})
	root := device.TreeNode{Attributes: map[string]string{"text": "OK", "bounds": "[0,0][100,50]"}}
	void := make([]enginetest.Result[struct{}], callBudget)
	trees := make([]enginetest.Result[device.TreeNode], callBudget)
	settles := make([]enginetest.Result[*device.ViewHierarchy], callBudget)
	infos := make([]enginetest.Result[device.DeviceInfo], callBudget)
	for index := range trees {
		trees[index] = enginetest.Result[device.TreeNode]{Value: root}
		settles[index] = enginetest.Result[*device.ViewHierarchy]{Value: &device.ViewHierarchy{Root: root}}
		infos[index] = enginetest.Result[device.DeviceInfo]{Value: device.DeviceInfo{
			Platform: device.Platform("android"), WidthGrid: 300, HeightGrid: 600,
			WidthPixels: 300, HeightPixels: 600,
		}}
	}
	driver.Enqueue(enginetest.DriverScript{
		Open: void, Close: void, DeviceInfo: infos,
		ContentDescriptor: trees, WaitForAppToSettle: settles,
		LaunchApp: void, StopApp: void, KillApp: void, ClearAppState: void,
	})
	runner := fakeRunner(driver, dir)
	runner.Environ = func() []string {
		return []string{"FLOWBATON_EXPLORE_SECRET_1=hunter2"}
	}
	var stdout, stderr strings.Builder
	if code := runner.Run(context.Background(), []string{
		"--device", "solo-udid", "--test-output-dir", outputDir,
		filepath.Join(dir, "a.yaml"),
	}, &stdout, &stderr); code == ExitOK {
		t.Fatalf("a failing input reported success\nstdout: %s\nstderr: %s\nactions: %v", stdout.String(), stderr.String(), driver.Actions())
	}
	if strings.Contains(stdout.String()+stderr.String(), "hunter2") {
		t.Error("secret leaked into command output")
	}
	err := filepath.Walk(outputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(data), "hunter2") {
			t.Errorf("secret leaked into %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestASuppliedSecretStaysOutOfArtifacts(t *testing.T) {
	t.Parallel()

	dir := secretFlowDir(t)
	outputDir := t.TempDir()
	driver := permissiveDriver()
	runner := fakeRunner(driver, dir)
	runner.Environ = func() []string {
		return []string{"FLOWBATON_EXPLORE_SECRET_1=hunter2"}
	}
	var stdout, stderr strings.Builder
	if code := runner.Run(context.Background(), []string{
		"--device", "solo-udid", "--test-output-dir", outputDir,
		filepath.Join(dir, "a.yaml"),
	}, &stdout, &stderr); code != ExitOK {
		t.Fatalf("exit = %d\nstderr: %s", code, stderr.String())
	}
	if got := typedText(driver); got != "hunter2" {
		t.Fatalf("typed %q, want the supplied secret", got)
	}
	checked := 0
	err := filepath.Walk(outputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(data), "hunter2") {
			t.Errorf("secret leaked into %s", path)
		}
		if filepath.Base(path) == "commands.json" {
			checked++
			if !strings.Contains(string(data), "FLOWBATON_EXPLORE_SECRET_1") {
				t.Errorf("commands.json lost the placeholder:\n%s", data)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("no commands.json written; the leak assertion never ran")
	}
}
