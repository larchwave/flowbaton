package iosdevice

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	goios "github.com/danielpaulus/go-ios/ios"
	"github.com/danielpaulus/go-ios/ios/testmanagerd"
	"howett.net/plist"

	"github.com/larchwave/flowbaton/internal/ios"
)

// runnerServeTest is the serving test in go-ios's {MODULE.}CLASS/METHOD
// form. The xcodebuild spelling of the same test lives in internal/ios;
// testmanagerd ignores the target segment, so the class/method pair is what
// selects it.
const runnerServeTest = "RunnerHostTests/testServeTheWireUntilTheHostIsDone"

const (
	runnerStartupPoll    = 100 * time.Millisecond
	runnerStartupTimeout = 180 * time.Second
	startupTimeoutEnv    = "FLOWBATON_DRIVER_STARTUP_TIMEOUT"
)

// runnerPlan is what the driver needs from an iphoneos .xctestrun: which
// app to install and which test bundle to attach.
type runnerPlan struct {
	// TestHostPath is the runner app on the host filesystem, __TESTROOT__
	// already resolved.
	TestHostPath string
	// TestHostBundleID identifies the installed runner app on the device.
	TestHostBundleID string
	// XctestConfigName is the .xctest bundle name testmanagerd attaches.
	XctestConfigName string
}

// deviceRunner installs the signed runner app and serves the wire through
// one long-running XCUITest session started by testmanagerd.
type deviceRunner struct {
	bundle ios.RunnerBundle
	udid   string

	cancel context.CancelFunc
	// done carries the serving goroutine's single result; finished closes
	// when it ends. Waiters watch finished (safe to share), and only the
	// exit-reason reader consumes done.
	done     chan error
	finished chan struct{}

	// Seams; nil means the real device.
	parse      func(path string) (runnerPlan, error)
	installApp func(entry goios.DeviceEntry, appPath string) error
	run        func(ctx context.Context, config testmanagerd.TestConfig) error
}

func newDeviceRunner(udid string, bundle ios.RunnerBundle) *deviceRunner {
	return &deviceRunner{bundle: bundle, udid: udid}
}

// start installs the runner and begins serving. The serving goroutine gets
// its own context: the runner must outlive Open's deadline and die on stop.
func (runner *deviceRunner) start(ctx context.Context, entry goios.DeviceEntry) error {
	parse := runner.parse
	if parse == nil {
		parse = parseXCTestRun
	}
	plan, err := parse(runner.bundle.XCTestRun)
	if err != nil {
		return fmt.Errorf("read runner plan from %s: %w", runner.bundle.XCTestRun, err)
	}
	install := runner.installApp
	if install == nil {
		install = installOnDevice
	}
	if err := install(entry, plan.TestHostPath); err != nil {
		return fmt.Errorf("install the runner app %s on %s: %w", plan.TestHostPath, runner.udid, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	run := runner.run
	if run == nil {
		run = runServeTest
	}
	serveCtx, cancel := context.WithCancel(context.Background())
	runner.cancel = cancel
	runner.done = make(chan error, 1)
	runner.finished = make(chan struct{})
	config := testmanagerd.TestConfig{
		TestRunnerBundleId: plan.TestHostBundleID,
		XctestConfigName:   plan.XctestConfigName,
		TestsToRun:         []string{runnerServeTest},
		Env: map[string]any{
			"FLOWBATON_RUNNER_SERVE": "1",
			"PORT":                   strconv.Itoa(runnerDevicePort),
		},
		Device: entry,
	}
	go func() {
		runner.done <- run(serveCtx, config)
		close(runner.finished)
	}()
	return nil
}

// exited fires when the serving test ends on its own — a signing rejection
// or a crashed runner explains itself here instead of timing out silently.
// Closed-channel semantics let any number of waiters observe the exit.
func (runner *deviceRunner) exited() <-chan struct{} {
	return runner.finished
}

// exitReason hands back what testmanagerd reported. Call at most once, and
// only after exited() fired.
func (runner *deviceRunner) exitReason() error {
	return <-runner.done
}

// stop cancels the serving session and waits for the goroutine to end, so
// nothing keeps driving the device. Safe after the runner died on its own.
func (runner *deviceRunner) stop() error {
	if runner.cancel == nil {
		return nil
	}
	runner.cancel()
	runner.cancel = nil
	<-runner.finished
	return nil
}

func runServeTest(ctx context.Context, config testmanagerd.TestConfig) error {
	config.Listener = testmanagerd.NewTestListener(os.Stderr, os.Stderr, os.TempDir())
	_, err := testmanagerd.RunTestWithConfig(ctx, config)
	return err
}

// xctestrunTarget is the per-target slice of the .xctestrun schema this
// driver needs. Format 1 stores targets as top-level keys; format 2 nests
// them under TestConfigurations/TestTargets.
type xctestrunTarget struct {
	TestHostPath             string `plist:"TestHostPath"`
	TestHostBundleIdentifier string `plist:"TestHostBundleIdentifier"`
	TestBundlePath           string `plist:"TestBundlePath"`
}

func (target xctestrunTarget) plan(root string) (runnerPlan, error) {
	if target.TestHostPath == "" || target.TestHostBundleIdentifier == "" || target.TestBundlePath == "" {
		return runnerPlan{}, fmt.Errorf(
			"the .xctestrun target lacks TestHostPath, TestHostBundleIdentifier, or TestBundlePath")
	}
	resolve := func(path string) string {
		return strings.ReplaceAll(path, "__TESTROOT__", root)
	}
	return runnerPlan{
		TestHostPath:     resolve(target.TestHostPath),
		TestHostBundleID: target.TestHostBundleIdentifier,
		XctestConfigName: filepath.Base(resolve(target.TestBundlePath)),
	}, nil
}

// parseXCTestRun reads both .xctestrun format versions and expects exactly
// one test target: the runner suite is the only thing FlowBaton builds.
func parseXCTestRun(path string) (runnerPlan, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return runnerPlan{}, err
	}
	root := filepath.Dir(path)

	var versioned struct {
		Metadata struct {
			FormatVersion int `plist:"FormatVersion"`
		} `plist:"__xctestrun_metadata__"`
	}
	if _, err := plist.Unmarshal(content, &versioned); err != nil {
		return runnerPlan{}, fmt.Errorf("decode %s: %w", path, err)
	}

	switch versioned.Metadata.FormatVersion {
	case 0, 1:
		var targets map[string]xctestrunTarget
		if _, err := plist.Unmarshal(content, &targets); err != nil {
			return runnerPlan{}, fmt.Errorf("decode %s: %w", path, err)
		}
		delete(targets, "__xctestrun_metadata__")
		if len(targets) != 1 {
			return runnerPlan{}, fmt.Errorf(
				"%s holds %d test targets, want exactly the runner suite", path, len(targets))
		}
		for _, target := range targets {
			return target.plan(root)
		}
		return runnerPlan{}, nil
	case 2:
		var format2 struct {
			TestConfigurations []struct {
				TestTargets []xctestrunTarget `plist:"TestTargets"`
			} `plist:"TestConfigurations"`
		}
		if _, err := plist.Unmarshal(content, &format2); err != nil {
			return runnerPlan{}, fmt.Errorf("decode %s: %w", path, err)
		}
		var targets []xctestrunTarget
		for _, configuration := range format2.TestConfigurations {
			targets = append(targets, configuration.TestTargets...)
		}
		if len(targets) != 1 {
			return runnerPlan{}, fmt.Errorf(
				"%s holds %d test targets, want exactly the runner suite", path, len(targets))
		}
		return targets[0].plan(root)
	default:
		return runnerPlan{}, fmt.Errorf(
			"%s has unsupported .xctestrun format version %d", path, versioned.Metadata.FormatVersion)
	}
}
