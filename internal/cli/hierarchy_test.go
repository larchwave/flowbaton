package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
)

// hierarchy is spec 03's view-hierarchy dump: "what is on screen right now",
// the operator's second diagnostic after list-devices. It reads the tree off
// the driver's ContentDescriptor and prints it; nothing is tapped or changed.

func hierarchyRunnerReturning(node device.TreeNode, err error) HierarchyRunner {
	return HierarchyRunner{
		Fetch: func(_ context.Context, _ string, _ string, _ []string, _ string) (device.TreeNode, error) {
			return node, err
		},
	}
}

func runHierarchy(t *testing.T, runner HierarchyRunner, args ...string) (string, string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runner.Run(context.Background(), args, &stdout, &stderr)
	return stdout.String(), stderr.String(), code
}

func sampleTree() device.TreeNode {
	clickable := true
	return device.TreeNode{
		Attributes: map[string]string{"text": "Login", "resource-id": "btn_login"},
		Clickable:  &clickable,
		Children: []device.TreeNode{{
			Attributes: map[string]string{"text": "Welcome"},
		}},
	}
}

func TestHierarchyPrintsTheTreeAsJSON(t *testing.T) {
	t.Parallel()

	stdout, _, code := runHierarchy(t, hierarchyRunnerReturning(sampleTree(), nil), "-p", "ios", "--device", "AAAA")
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	// Default output is the tree as JSON, so a tool downstream can consume it.
	var decoded device.TreeNode
	if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, stdout)
	}
	if decoded.Attributes["text"] != "Login" {
		t.Fatalf("root text = %q, want Login\n%s", decoded.Attributes["text"], stdout)
	}
	if len(decoded.Children) != 1 || decoded.Children[0].Attributes["text"] != "Welcome" {
		t.Fatalf("child not preserved through JSON\n%s", stdout)
	}
}

func TestHierarchyFlattensToCSVOnRequest(t *testing.T) {
	t.Parallel()

	stdout, _, code := runHierarchy(t, hierarchyRunnerReturning(sampleTree(), nil),
		"-p", "ios", "--device", "AAAA", "--csv")
	if code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	// CSV is one row per node with a depth column, so the nesting survives a
	// format that has no nesting. The root is depth 0, its child depth 1.
	if !strings.Contains(stdout, "depth") {
		t.Fatalf("no CSV header\n%s", stdout)
	}
	if !strings.Contains(stdout, "Login") || !strings.Contains(stdout, "Welcome") {
		t.Fatalf("CSV dropped a node\n%s", stdout)
	}
	// A CSV must not carry a JSON object literal — that would mean it never
	// flattened.
	if strings.Contains(stdout, "\"children\"") || strings.Contains(stdout, "{") {
		t.Fatalf("CSV still contains JSON structure\n%s", stdout)
	}
}

func TestHierarchyRequiresAPlatform(t *testing.T) {
	t.Parallel()

	// Unlike list-devices, hierarchy targets one device, so it cannot default to
	// "both". No platform is a usage error, not an empty dump.
	_, stderr, code := runHierarchy(t, hierarchyRunnerReturning(sampleTree(), nil))
	if code != ExitInvalid {
		t.Fatalf("exit = %d, want %d", code, ExitInvalid)
	}
	if !strings.Contains(stderr, "platform") {
		t.Fatalf("the refusal did not mention the missing platform: %q", stderr)
	}
}

func TestHierarchyRefusesAnUnknownPlatform(t *testing.T) {
	t.Parallel()

	_, stderr, code := runHierarchy(t, hierarchyRunnerReturning(sampleTree(), nil), "-p", "web")
	if code != ExitInvalid {
		t.Fatalf("exit = %d, want %d", code, ExitInvalid)
	}
	if !strings.Contains(stderr, "web") {
		t.Fatalf("the refusal did not name the bad platform: %q", stderr)
	}
}

func TestHierarchyReportsAFetchFailure(t *testing.T) {
	t.Parallel()

	_, stderr, code := runHierarchy(t,
		hierarchyRunnerReturning(device.TreeNode{}, errors.New("runner not reachable on port 22087")),
		"-p", "ios", "--device", "AAAA")
	if code != ExitFailure {
		t.Fatalf("exit = %d, want %d", code, ExitFailure)
	}
	if !strings.Contains(stderr, "runner not reachable") {
		t.Fatalf("the failure did not carry the driver error: %q", stderr)
	}
}

func TestHierarchyPassesTheDevToolsTargetToAndroidFetch(t *testing.T) {
	t.Parallel()

	var gotTarget string
	runner := HierarchyRunner{Fetch: func(
		_ context.Context, _, _ string, _ []string, target string,
	) (device.TreeNode, error) {
		gotTarget = target
		return sampleTree(), nil
	}}
	_, stderr, code := runHierarchy(t, runner,
		"-p", "android", "--target", "devtools")
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d; stderr: %s", code, ExitOK, stderr)
	}
	if gotTarget != "devtools" {
		t.Fatalf("target = %q, want devtools", gotTarget)
	}

	_, stderr, code = runHierarchy(t, runner, "-p", "ios", "--target=devtools")
	if code != ExitInvalid || !strings.Contains(stderr, "Android-only") {
		t.Fatalf("iOS devtools exit/stderr = %d/%q, want Android-only usage error", code, stderr)
	}
}

// `flowbaton hierarchy` exposes CSV output through `--compact` and keeps
// `--csv` as a compatibility alias.
func TestHierarchyAcceptsTheContractCompactSpelling(t *testing.T) {
	t.Parallel()

	for _, flag := range []string{"--csv", "--compact"} {
		options, code := parseHierarchyArgs([]string{"-p", "android", flag}, io.Discard)
		if code != ExitOK {
			t.Fatalf("%s was rejected", flag)
		}
		if !options.csv {
			t.Fatalf("%s did not select the CSV output", flag)
		}
	}
	// The control: without either flag the output stays JSON.
	options, code := parseHierarchyArgs([]string{"-p", "android"}, io.Discard)
	if code != ExitOK || options.csv {
		t.Fatalf("plain hierarchy = %#v / %d, want JSON output", options, code)
	}
}

// XCUITest requires an app filter for an application hierarchy; an empty filter
// resolves to SpringBoard. The CLI therefore lets the operator name the app.
func TestHierarchyCanBeAskedAboutOneApp(t *testing.T) {
	t.Parallel()

	var gotAppIDs []string
	runner := HierarchyRunner{
		Fetch: func(_ context.Context, _, _ string, appIDs []string, _ string) (device.TreeNode, error) {
			gotAppIDs = appIDs
			return device.TreeNode{Attributes: map[string]string{"text": "root"}}, nil
		},
	}
	var stdout, stderr bytes.Buffer
	if code := runner.Run(context.Background(),
		[]string{"-p", "ios", "--app-id", "com.example.a"}, &stdout, &stderr); code != ExitOK {
		t.Fatalf("exit = %d\nstderr: %s", code, stderr.String())
	}
	if len(gotAppIDs) != 1 || gotAppIDs[0] != "com.example.a" {
		t.Fatalf("appIDs = %v, want [com.example.a]", gotAppIDs)
	}
}

// Without one the operator is told what they are about to be shown, rather than
// handed SpringBoard's tree as if it were the app's.
func TestHierarchyOnIOSSaysWhoseTreeItIsShowing(t *testing.T) {
	t.Parallel()

	runner := HierarchyRunner{
		Fetch: func(context.Context, string, string, []string, string) (device.TreeNode, error) {
			return device.TreeNode{}, nil
		},
	}
	var stdout, stderr bytes.Buffer
	if code := runner.Run(
		context.Background(), []string{"-p", "ios"}, &stdout, &stderr); code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stderr.String(), "--app-id") {
		t.Fatalf("stderr = %q, want it to point at --app-id", stderr.String())
	}
	// Android has no such caveat: its dump is the window's.
	var androidOut, androidErr bytes.Buffer
	if code := runner.Run(
		context.Background(), []string{"-p", "android"}, &androidOut, &androidErr); code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	if androidErr.Len() != 0 {
		t.Fatalf("android stderr = %q, want nothing", androidErr.String())
	}
}
