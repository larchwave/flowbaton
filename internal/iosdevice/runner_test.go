package iosdevice

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	goios "github.com/danielpaulus/go-ios/ios"
	"github.com/danielpaulus/go-ios/ios/testmanagerd"

	"github.com/larchwave/flowbaton/internal/ios"
)

const xctestrunFormat1 = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>__xctestrun_metadata__</key>
	<dict>
		<key>FormatVersion</key>
		<integer>1</integer>
	</dict>
	<key>FlowBatonIOSRunnerUITests</key>
	<dict>
		<key>TestHostPath</key>
		<string>__TESTROOT__/Debug-iphoneos/FlowBatonIOSRunnerUITests-Runner.app</string>
		<key>TestHostBundleIdentifier</key>
		<string>dev.flowbaton.FlowBatonIOSRunnerUITests.xctrunner</string>
		<key>TestBundlePath</key>
		<string>__TESTHOST__/PlugIns/FlowBatonIOSRunnerUITests.xctest</string>
	</dict>
</dict>
</plist>`

const xctestrunFormat2 = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>__xctestrun_metadata__</key>
	<dict>
		<key>FormatVersion</key>
		<integer>2</integer>
	</dict>
	<key>TestConfigurations</key>
	<array>
		<dict>
			<key>Name</key>
			<string>Test Scheme Action</string>
			<key>TestTargets</key>
			<array>
				<dict>
					<key>TestHostPath</key>
					<string>__TESTROOT__/Debug-iphoneos/FlowBatonIOSRunnerUITests-Runner.app</string>
					<key>TestHostBundleIdentifier</key>
					<string>dev.flowbaton.FlowBatonIOSRunnerUITests.xctrunner</string>
					<key>TestBundlePath</key>
					<string>__TESTHOST__/PlugIns/FlowBatonIOSRunnerUITests.xctest</string>
				</dict>
			</array>
		</dict>
	</array>
</dict>
</plist>`

func writeRunnerXCTestRun(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runner_iphoneos26.0-arm64.xctestrun")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseXCTestRunReadsBothFormats(t *testing.T) {
	for name, content := range map[string]string{"format1": xctestrunFormat1, "format2": xctestrunFormat2} {
		t.Run(name, func(t *testing.T) {
			path := writeRunnerXCTestRun(t, content)
			plan, err := parseXCTestRun(path)
			if err != nil {
				t.Fatalf("parseXCTestRun: %v", err)
			}
			wantHost := filepath.Join(filepath.Dir(path), "Debug-iphoneos", "FlowBatonIOSRunnerUITests-Runner.app")
			if plan.TestHostPath != wantHost {
				t.Fatalf("TestHostPath = %q, want %q", plan.TestHostPath, wantHost)
			}
			if plan.TestHostBundleID != "dev.flowbaton.FlowBatonIOSRunnerUITests.xctrunner" {
				t.Fatalf("TestHostBundleID = %q", plan.TestHostBundleID)
			}
			if plan.XctestConfigName != "FlowBatonIOSRunnerUITests.xctest" {
				t.Fatalf("XctestConfigName = %q", plan.XctestConfigName)
			}
		})
	}
}

func TestParseXCTestRunRefusesMissingKeys(t *testing.T) {
	path := writeRunnerXCTestRun(t, strings.ReplaceAll(
		xctestrunFormat1, "TestHostBundleIdentifier", "SomethingElse"))
	if _, err := parseXCTestRun(path); err == nil {
		t.Fatal("a target without its bundle identifier was accepted")
	}
}

func TestDeviceRunnerServesTheWireTest(t *testing.T) {
	path := writeRunnerXCTestRun(t, xctestrunFormat1)
	runner := newDeviceRunner("00008110-TEST", ios.RunnerBundle{XCTestRun: path})

	installed := ""
	var captured testmanagerd.TestConfig
	runner.installApp = func(_ goios.DeviceEntry, appPath string) error {
		installed = appPath
		return nil
	}
	started := make(chan struct{})
	runner.run = func(ctx context.Context, config testmanagerd.TestConfig) error {
		captured = config
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}

	if err := runner.start(context.Background(), goios.DeviceEntry{}); err != nil {
		t.Fatalf("start: %v", err)
	}
	<-started
	if err := runner.stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}

	if !strings.HasSuffix(installed, "FlowBatonIOSRunnerUITests-Runner.app") {
		t.Fatalf("installed %q, want the runner app", installed)
	}
	if captured.TestRunnerBundleId != "dev.flowbaton.FlowBatonIOSRunnerUITests.xctrunner" {
		t.Fatalf("TestRunnerBundleId = %q", captured.TestRunnerBundleId)
	}
	if captured.XctestConfigName != "FlowBatonIOSRunnerUITests.xctest" {
		t.Fatalf("XctestConfigName = %q", captured.XctestConfigName)
	}
	if len(captured.TestsToRun) != 1 || captured.TestsToRun[0] != runnerServeTest {
		t.Fatalf("TestsToRun = %v, want only the serve test", captured.TestsToRun)
	}
	if captured.Env["FLOWBATON_RUNNER_SERVE"] != "1" || captured.Env["PORT"] != "22087" {
		t.Fatalf("Env = %v: the runner must serve on the device port", captured.Env)
	}
}

func TestDeviceRunnerStopWaitsForTheServingGoroutine(t *testing.T) {
	path := writeRunnerXCTestRun(t, xctestrunFormat1)
	runner := newDeviceRunner("00008110-TEST", ios.RunnerBundle{XCTestRun: path})
	runner.installApp = func(goios.DeviceEntry, string) error { return nil }
	finished := false
	runner.run = func(ctx context.Context, _ testmanagerd.TestConfig) error {
		<-ctx.Done()
		finished = true
		return ctx.Err()
	}
	if err := runner.start(context.Background(), goios.DeviceEntry{}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := runner.stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if !finished {
		t.Fatal("stop returned before the serving goroutine ended")
	}
	if err := runner.stop(); err != nil {
		t.Fatalf("second stop must be a no-op, got %v", err)
	}
}

func TestManagedOpenDeliversRunnerAndAnswers(t *testing.T) {
	_, client := runnerStub(t)
	path := writeRunnerXCTestRun(t, xctestrunFormat1)
	driver := NewDriver("00008110-TEST", 30001, client, &ios.RunnerBundle{XCTestRun: path})
	session, _, _ := fakeSession("00008110-TEST", 30001, 17)
	driver.session = session
	driver.runner.installApp = func(goios.DeviceEntry, string) error { return nil }
	driver.runner.run = func(ctx context.Context, _ testmanagerd.TestConfig) error {
		<-ctx.Done()
		return ctx.Err()
	}

	if err := driver.Open(context.Background()); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := driver.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestManagedOpenReportsARunnerThatDiedEarly(t *testing.T) {
	// No status server: the poll cannot succeed, the exit must explain.
	path := writeRunnerXCTestRun(t, xctestrunFormat1)
	driver := NewDriver("00008110-TEST", 30001, ios.NewClient("http://127.0.0.1:1"),
		&ios.RunnerBundle{XCTestRun: path})
	driver.startupTimeout = 5 * time.Second
	session, tunnelHandle, forwardHandle := fakeSession("00008110-TEST", 30001, 17)
	driver.session = session
	driver.runner.installApp = func(goios.DeviceEntry, string) error { return nil }
	driver.runner.run = func(context.Context, testmanagerd.TestConfig) error {
		return errors.New("code signature invalid")
	}

	err := driver.Open(context.Background())
	if err == nil {
		t.Fatal("expected Open to fail with the runner's exit reason")
	}
	if !strings.Contains(err.Error(), "stopped before it answered") ||
		!strings.Contains(err.Error(), "code signature invalid") {
		t.Fatalf("error %q must carry the runner's own explanation", err)
	}
	if forwardHandle.closed != 1 || tunnelHandle.closed != 1 {
		t.Fatalf("failed managed Open must roll the session back: forward %d / tunnel %d",
			forwardHandle.closed, tunnelHandle.closed)
	}
}

func TestManagedOpenHonorsStartupBudgetOverride(t *testing.T) {
	t.Setenv(startupTimeoutEnv, "50")
	path := writeRunnerXCTestRun(t, xctestrunFormat1)
	driver := NewDriver("00008110-TEST", 30001, ios.NewClient("http://127.0.0.1:1"),
		&ios.RunnerBundle{XCTestRun: path})
	session, _, _ := fakeSession("00008110-TEST", 30001, 17)
	driver.session = session
	driver.runner.installApp = func(goios.DeviceEntry, string) error { return nil }
	driver.runner.run = func(ctx context.Context, _ testmanagerd.TestConfig) error {
		<-ctx.Done()
		return ctx.Err()
	}

	begun := time.Now()
	err := driver.Open(context.Background())
	if err == nil {
		t.Fatal("expected the budget to expire")
	}
	if elapsed := time.Since(begun); elapsed > 3*time.Second {
		t.Fatalf("50ms budget took %v", elapsed)
	}
	if !strings.Contains(err.Error(), startupTimeoutEnv) {
		t.Fatalf("timeout error %q must name the override variable", err)
	}
}
