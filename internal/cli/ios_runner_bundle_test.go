package cli

import (
	"context"
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

	bundle, err := iosRunnerBundle(context.Background(), iosRunnerFlavorSimulator)
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

	bundle, err := iosRunnerBundle(context.Background(), iosRunnerFlavorSimulator)
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

	bundle, err := iosRunnerBundle(context.Background(), iosRunnerFlavorSimulator)
	if err != nil {
		t.Fatalf("iosRunnerBundle() error = %v", err)
	}
	if bundle == nil || bundle.XCTestRun != built {
		t.Fatalf("bundle = %+v, want %s", bundle, built)
	}
}

// One build per destination is the normal state after building for both a
// simulator and a device: each flavor picks its own file, no question asked.
func TestEachFlavorPicksItsOwnBuiltRunner(t *testing.T) {
	home := t.TempDir()
	t.Setenv(iosXCTestRunVariable, "")
	t.Setenv("HOME", home)
	simulator := writeXCTestRun(t, home, "one_iphonesimulator26.2-arm64.xctestrun")
	device := writeXCTestRun(t, home, "two_iphoneos26.2-arm64.xctestrun")

	bundle, err := iosRunnerBundle(context.Background(), iosRunnerFlavorSimulator)
	if err != nil {
		t.Fatalf("simulator flavor: %v", err)
	}
	if bundle == nil || bundle.XCTestRun != simulator {
		t.Fatalf("simulator bundle = %+v, want %s", bundle, simulator)
	}

	bundle, err = iosRunnerBundle(context.Background(), iosRunnerFlavorDevice)
	if err != nil {
		t.Fatalf("device flavor: %v", err)
	}
	if bundle == nil || bundle.XCTestRun != device {
		t.Fatalf("device bundle = %+v, want %s", bundle, device)
	}
}

// Two builds for the SAME destination are genuinely ambiguous. Picking one
// at random would silently drive the wrong runner, so it asks instead.
func TestTwoBuiltRunnersOfOneFlavorAskWhichOne(t *testing.T) {
	home := t.TempDir()
	t.Setenv(iosXCTestRunVariable, "")
	t.Setenv("HOME", home)
	writeXCTestRun(t, home, "one_iphonesimulator26.2-arm64.xctestrun")
	writeXCTestRun(t, home, "two_iphonesimulator26.2-arm64.xctestrun")

	_, err := iosRunnerBundle(context.Background(), iosRunnerFlavorSimulator)
	if err == nil {
		t.Fatal("two built runners were accepted without a word")
	}
	if !strings.Contains(err.Error(), iosXCTestRunVariable) {
		t.Fatalf("error = %q, want it to name the variable that picks one", err)
	}
}

func TestSignedRunnerBundleRequiresExactlyOneRootXCTestRun(t *testing.T) {
	directory := t.TempDir()
	want := filepath.Join(directory, "FlowBatonIOSRunnerUITests.xctestrun")
	if err := os.WriteFile(want, []byte("<plist/>"), 0o600); err != nil {
		t.Fatal(err)
	}
	bundle, err := iosRunnerBundleAt(directory, iosRunnerFlavorSimulator)
	if err != nil {
		t.Fatalf("iosRunnerBundleAt() error = %v", err)
	}
	if bundle == nil || bundle.XCTestRun != want {
		t.Fatalf("bundle = %#v, want %s", bundle, want)
	}

	second := filepath.Join(directory, "another.xctestrun")
	if err := os.WriteFile(second, []byte("<plist/>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := iosRunnerBundleAt(directory, iosRunnerFlavorSimulator); err == nil {
		t.Fatal("iosRunnerBundleAt() accepted multiple descriptors")
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
