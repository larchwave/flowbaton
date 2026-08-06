package cli

import (
	"path/filepath"
	"testing"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/enginetest"
)

// XCUITest requires a bundle identifier to snapshot an app. The flow declares
// the application under test, so hierarchy requests carry that identifier.

func hierarchyAppIDs(driver *enginetest.FakeDriver) [][]string {
	var seen [][]string
	for _, action := range driver.Actions() {
		if action.Method != enginetest.MethodContentDescriptor {
			continue
		}
		if request, ok := action.Request.(device.ContentDescriptorRequest); ok {
			seen = append(seen, request.AppIDs)
		}
	}
	return seen
}

func TestTheHierarchyIsFetchedForTheFlowsOwnApp(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "declared.yaml"),
		"appId: com.example.declared\n---\n- assertVisible: OK\n")

	driver := permissiveDriver()
	_, stderr, code := runSessionWithArgs(t, driver, []string{filepath.Join(dir, "declared.yaml")})
	if code != ExitOK {
		t.Fatalf("exit = %d\nstderr: %s", code, stderr)
	}

	requests := hierarchyAppIDs(driver)
	if len(requests) == 0 {
		t.Fatal("no hierarchy was fetched at all")
	}
	for index, appIDs := range requests {
		if len(appIDs) != 1 || appIDs[0] != "com.example.declared" {
			t.Fatalf("request %d appIds = %#v, want the flow's declared app", index, appIDs)
		}
	}
}

func TestATapRetryReadsTheHierarchyForTheFlowsApp(t *testing.T) {
	t.Parallel()

	// The second call site. `captureTapRetryHierarchy` does not go through
	// ElementLookup — it asks the driver itself — so it needs the same answer to
	// "which app is this about". A negative control proved nothing covered it:
	// reverting that one line to a bare request left every test green.
	//
	// retryTapIfNoChange is what reaches it: the snapshot hierarchy is captured
	// before the tap so the retry can tell whether the screen changed. Checkd
	// against the wrong app's tree, "nothing changed" is the only possible
	// answer, and the tap repeats forever on a screen that already moved.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "retry.yaml"),
		"appId: com.example.retry\n---\n"+
			"- tapOn:\n    point: '50%,50%'\n    retryTapIfNoChange: true\n")

	driver := permissiveDriver()
	_, stderr, code := runSessionWithArgs(t, driver, []string{filepath.Join(dir, "retry.yaml")})
	if code != ExitOK {
		t.Fatalf("exit = %d\nstderr: %s", code, stderr)
	}

	requests := hierarchyAppIDs(driver)
	if len(requests) == 0 {
		t.Fatal("no hierarchy was fetched at all")
	}
	for index, appIDs := range requests {
		if len(appIDs) != 1 || appIDs[0] != "com.example.retry" {
			t.Fatalf("request %d appIds = %#v, want the flow's declared app", index, appIDs)
		}
	}
}

func TestANestedFlowsAppIsRestoredWhenItReturns(t *testing.T) {
	t.Parallel()

	// The control that makes this scoped rather than sticky. A runFlow with its
	// own appId must not leave the parent's lookups pointed at the child's app —
	// the parent would then be asserting against another app's screen, which is
	// the same defect one level up.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "child.yaml"),
		"appId: com.example.child\n---\n- assertVisible: OK\n")
	writeFile(t, filepath.Join(dir, "parent.yaml"),
		"appId: com.example.parent\n---\n- runFlow: child.yaml\n- assertVisible: OK\n")

	driver := permissiveDriver()
	_, stderr, code := runSessionWithArgs(t, driver, []string{filepath.Join(dir, "parent.yaml")})
	if code != ExitOK {
		t.Fatalf("exit = %d\nstderr: %s", code, stderr)
	}

	requests := hierarchyAppIDs(driver)
	if len(requests) < 2 {
		t.Fatalf("requests = %#v, want one per assertVisible", requests)
	}
	first, last := requests[0], requests[len(requests)-1]
	if len(first) != 1 || first[0] != "com.example.child" {
		t.Fatalf("the child's lookup used %#v, want its own app", first)
	}
	if len(last) != 1 || last[0] != "com.example.parent" {
		t.Fatalf("the parent's lookup after the child used %#v, want the parent's app back", last)
	}
}
