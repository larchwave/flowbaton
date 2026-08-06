package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Starting the runner needs the .xctestrun `driver-setup` built. The two
// subcommands are separate processes with no state between them, so they have
// to agree on WHERE it lands — that agreement is this file. Without it a
// self-starting runner would need the operator to name the path every run,
// which is the manual step this whole batch removes.

func TestTheRunnerBundleComesFromTheOverrideWhenSet(t *testing.T) {
	t.Setenv(iosXCTestRunVariable, "/somewhere/Custom.xctestrun")

	bundle, err := iosRunnerBundle()
	if err != nil {
		t.Fatalf("iosRunnerBundle() error = %v", err)
	}
	if bundle == nil || bundle.XCTestRun != "/somewhere/Custom.xctestrun" {
		t.Fatalf("bundle = %+v, want the override", bundle)
	}
}

// With no built runner, the operator may manage the runner externally.
func TestNoBuiltRunnerLeavesTheOperatorInCharge(t *testing.T) {
	t.Setenv(iosXCTestRunVariable, "")
	t.Setenv("HOME", t.TempDir())

	bundle, err := iosRunnerBundle()
	if err != nil {
		t.Fatalf("iosRunnerBundle() error = %v", err)
	}
	if bundle != nil {
		t.Fatalf("bundle = %+v, want nil with nothing built", bundle)
	}
}

func TestTheRunnerBundleIsFoundWhereDriverSetupPutIt(t *testing.T) {
	home := t.TempDir()
	t.Setenv(iosXCTestRunVariable, "")
	t.Setenv("HOME", home)
	built := writeXCTestRun(t, home, "FlowBatonIOSRunnerUITests_iphonesimulator26.2-arm64.xctestrun")

	bundle, err := iosRunnerBundle()
	if err != nil {
		t.Fatalf("iosRunnerBundle() error = %v", err)
	}
	if bundle == nil || bundle.XCTestRun != built {
		t.Fatalf("bundle = %+v, want %s", bundle, built)
	}
}

// Two builds for different destinations both leave a file behind. Picking one
// at random would silently drive the wrong runner, so it asks instead.
func TestTwoBuiltRunnersAskWhichOne(t *testing.T) {
	home := t.TempDir()
	t.Setenv(iosXCTestRunVariable, "")
	t.Setenv("HOME", home)
	writeXCTestRun(t, home, "one_iphonesimulator26.2-arm64.xctestrun")
	writeXCTestRun(t, home, "two_iphoneos26.2-arm64.xctestrun")

	_, err := iosRunnerBundle()
	if err == nil {
		t.Fatal("two built runners were accepted without a word")
	}
	if !strings.Contains(err.Error(), iosXCTestRunVariable) {
		t.Fatalf("error = %q, want it to name the variable that picks one", err)
	}
}

func writeXCTestRun(t *testing.T, home, name string) string {
	t.Helper()
	products := filepath.Join(home, ".flowbaton", "ios-driver", "Build", "Products")
	if err := os.MkdirAll(products, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(products, name)
	if err := os.WriteFile(path, []byte("<plist/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
