package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/larchwave/flowbaton/internal/android"
	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/ios"
)

// The mcp subcommand exposes flowbaton capabilities to an MCP client. These
// tests drive the real server through the SDK's in-memory transport — a client
// connected to the same server the CLI runs over stdio — so the tools are
// exercised end to end without a subprocess.

type stubChecker struct {
	err     error
	checked chan Source
}

func (c stubChecker) Check(_ context.Context, source Source) error {
	if c.checked != nil {
		c.checked <- source
	}
	return c.err
}

func connectMCP(t *testing.T, runner MCPRunner) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	server := runner.server()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func callMCPText(t *testing.T, session *mcp.ClientSession, name string, args any) (string, bool) {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	var text strings.Builder
	for _, content := range result.Content {
		if textContent, ok := content.(*mcp.TextContent); ok {
			text.WriteString(textContent.Text)
		}
	}
	return text.String(), result.IsError
}

func TestMCPServerAdvertisesTheFlowbatonTools(t *testing.T) {
	t.Parallel()

	session := connectMCP(t, MCPRunner{Checker: stubChecker{}})
	seen := map[string]bool{}
	for tool, err := range session.Tools(context.Background(), nil) {
		if err != nil {
			t.Fatalf("list tools: %v", err)
		}
		seen[tool.Name] = true
	}
	for _, want := range []string{"check_syntax", "list_devices", "hierarchy", "query", "run_flow", "screenshot", "start_device", "explore"} {
		if !seen[want] {
			t.Fatalf("tool %q not advertised; saw %v", want, seen)
		}
	}
}

func callMCPRaw(t *testing.T, session *mcp.ClientSession, name string, args any) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	return result
}

func TestMCPScreenshotToolReturnsThePNG(t *testing.T) {
	t.Parallel()

	png := []byte("\x89PNG\r\n\x1a\nfake-pixels")
	var seenPlatform, seenUDID string
	runner := MCPRunner{
		Checker: stubChecker{},
		Screenshot: ScreenshotRunner{
			Fetch: func(_ context.Context, platform, udid string) ([]byte, error) {
				seenPlatform, seenUDID = platform, udid
				return png, nil
			},
		},
	}
	session := connectMCP(t, runner)
	result := callMCPRaw(t, session, "screenshot",
		map[string]string{"platform": "android", "udid": "emulator-5554"})
	if result.IsError {
		t.Fatalf("screenshot reported an error: %+v", result.Content)
	}
	if seenPlatform != "android" || seenUDID != "emulator-5554" {
		t.Fatalf("fetch saw platform %q udid %q", seenPlatform, seenUDID)
	}
	var image *mcp.ImageContent
	for _, content := range result.Content {
		if imageContent, ok := content.(*mcp.ImageContent); ok {
			image = imageContent
		}
	}
	if image == nil {
		t.Fatalf("no image content returned: %+v", result.Content)
	}
	if image.MIMEType != "image/png" {
		t.Fatalf("mime type = %q, want image/png", image.MIMEType)
	}
	if !strings.HasPrefix(string(image.Data), "\x89PNG") {
		t.Fatalf("image bytes were not returned intact")
	}
}

func TestMCPScreenshotToolSurfacesAFetchError(t *testing.T) {
	t.Parallel()

	runner := MCPRunner{
		Checker: stubChecker{},
		Screenshot: ScreenshotRunner{
			Fetch: func(context.Context, string, string) ([]byte, error) {
				return nil, errors.New("runner not reachable")
			},
		},
	}
	session := connectMCP(t, runner)
	text, isError := callMCPText(t, session, "screenshot",
		map[string]string{"platform": "ios", "udid": "AAAA"})
	if !isError {
		t.Fatalf("a fetch failure was not marked as an error: %q", text)
	}
	if !strings.Contains(text, "runner not reachable") {
		t.Fatalf("the driver error was not surfaced: %q", text)
	}
}

func TestMCPScreenshotToolRequiresPlatformAndDevice(t *testing.T) {
	t.Parallel()

	session := connectMCP(t, MCPRunner{Checker: stubChecker{}, Screenshot: ScreenshotRunner{
		Fetch: func(context.Context, string, string) ([]byte, error) { return []byte("x"), nil },
	}})
	for name, args := range map[string]map[string]string{
		"missing platform": {"udid": "AAAA"},
		"missing udid":     {"platform": "ios"},
	} {
		if text, isError := callMCPText(t, session, "screenshot", args); !isError {
			t.Fatalf("%s was accepted: %q", name, text)
		}
	}
}

// runFlowMCPRunner builds an MCPRunner whose run_flow tool executes through the
// real TestRunner pipeline against a fake driver, in a base directory the test
// owns.
func runFlowMCPRunner(base string) MCPRunner {
	return MCPRunner{
		BaseDir: base,
		Checker: stubChecker{},
		RunFlow: TestRunner{
			Environ: func() []string { return nil },
			NewSession: func(_ context.Context, shard Shard, _ TestOptions) (TestSession, error) {
				return DeviceSession{
					Driver:          permissiveDriver(),
					OutputDirectory: shard.OutputDirectory,
					BaseDirectory:   base,
				}, nil
			},
		},
	}
}

func TestMCPRunFlowToolRunsAFlowFileInsideTheBaseDirectory(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	writeFile(t, filepath.Join(base, "flow.yaml"), "appId: com.example.a\n---\n- launchApp\n")
	session := connectMCP(t, runFlowMCPRunner(base))
	text, isError := callMCPText(t, session, "run_flow", map[string]string{
		"platform": "android", "udid": "emulator-5554",
		"path": "flow.yaml", "outputDir": "out",
	})
	if isError {
		t.Fatalf("run_flow reported an error: %q", text)
	}
	if !strings.Contains(text, "PASS") {
		t.Fatalf("the run outcome was not returned: %q", text)
	}
}

func TestMCPRunFlowToolRunsInlineYaml(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	session := connectMCP(t, runFlowMCPRunner(base))
	text, isError := callMCPText(t, session, "run_flow", map[string]string{
		"platform": "android", "udid": "emulator-5554",
		"yaml": "appId: com.example.a\n---\n- launchApp\n", "outputDir": "out",
	})
	if isError {
		t.Fatalf("run_flow reported an error: %q", text)
	}
	if !strings.Contains(text, "PASS") {
		t.Fatalf("the run outcome was not returned: %q", text)
	}
}

func TestMCPRunFlowToolConfinesThePathToTheBaseDirectory(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	base := filepath.Join(parent, "workspace")
	if err := os.Mkdir(base, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(parent, "outside.yaml"), "appId: com.example.a\n---\n- launchApp\n")
	session := connectMCP(t, runFlowMCPRunner(base))
	text, isError := callMCPText(t, session, "run_flow", map[string]string{
		"platform": "android", "udid": "emulator-5554",
		"path": "../outside.yaml", "outputDir": "out",
	})
	if !isError {
		t.Fatalf("a path outside the base directory was accepted: %q", text)
	}
	if !strings.Contains(text, "outside") {
		t.Fatalf("the confinement failure was not named: %q", text)
	}
}

func TestMCPRunFlowToolRejectsPathTogetherWithYaml(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	session := connectMCP(t, runFlowMCPRunner(base))
	text, isError := callMCPText(t, session, "run_flow", map[string]string{
		"platform": "android", "udid": "emulator-5554",
		"path": "flow.yaml", "yaml": "appId: com.example.a\n---\n- launchApp\n",
	})
	if !isError {
		t.Fatalf("path and yaml together were accepted: %q", text)
	}
}

func TestMCPRunFlowToolRequiresPlatformAndDevice(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	session := connectMCP(t, runFlowMCPRunner(base))
	for name, args := range map[string]map[string]string{
		"missing platform": {"udid": "emulator-5554", "yaml": "appId: a\n---\n- launchApp\n"},
		"missing udid":     {"platform": "android", "yaml": "appId: a\n---\n- launchApp\n"},
	} {
		if text, isError := callMCPText(t, session, "run_flow", args); !isError {
			t.Fatalf("%s was accepted: %q", name, text)
		}
	}
}

func TestMCPRunFlowToolSurfacesASessionFailure(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	runner := runFlowMCPRunner(base)
	runner.RunFlow.NewSession = func(context.Context, Shard, TestOptions) (TestSession, error) {
		return nil, errors.New("no device behind that udid")
	}
	session := connectMCP(t, runner)
	text, isError := callMCPText(t, session, "run_flow", map[string]string{
		"platform": "android", "udid": "emulator-5554",
		"yaml": "appId: com.example.a\n---\n- launchApp\n", "outputDir": "out",
	})
	if !isError {
		t.Fatalf("a session failure was not marked as an error: %q", text)
	}
	if !strings.Contains(text, "no device behind that udid") {
		t.Fatalf("the session error was not surfaced: %q", text)
	}
}

func TestMCPStartDeviceToolBootsAnIOSSimulator(t *testing.T) {
	t.Parallel()

	var booted []string
	session := connectMCP(t, MCPRunner{StartDevice: StartDeviceRunner{
		Boot: func(_ context.Context, platform, udid string) error {
			booted = append(booted, platform+" "+udid)
			return nil
		},
		WaitReady: func(context.Context, string, string) error { return nil },
	}})
	text, isError := callMCPText(t, session, "start_device",
		map[string]any{"platform": "ios", "udid": "AAAA"})
	if isError {
		t.Fatalf("start_device failed: %q", text)
	}
	if text != "booted AAAA" {
		t.Fatalf("unexpected output: %q", text)
	}
	if len(booted) != 1 || booted[0] != "ios AAAA" {
		t.Fatalf("boot calls: %v", booted)
	}
}

func TestMCPStartDeviceToolCreatesAnAndroidDeviceWithOptions(t *testing.T) {
	t.Parallel()

	var created deviceCreateOptions
	session := connectMCP(t, MCPRunner{StartDevice: StartDeviceRunner{
		CreateAVD: func(_ context.Context, options deviceCreateOptions) (string, error) {
			created = options
			return "fresh-avd", nil
		},
		LaunchAVD: func(context.Context, string, string) error { return nil },
		WaitReady: func(context.Context, string, string) error { return nil },
		ConfigureLocale: func(context.Context, string, string, string) error {
			return nil
		},
	}})
	text, isError := callMCPText(t, session, "start_device", map[string]any{
		"platform": "android", "forceCreate": true,
		"osVersion": "34", "deviceLocale": "de_DE", "deviceModel": "pixel_7",
		"systemImage": "system-images;android-34;google_apis;arm64-v8a",
	})
	if isError {
		t.Fatalf("start_device failed: %q", text)
	}
	if text != "launched fresh-avd" {
		t.Fatalf("unexpected output: %q", text)
	}
	if created.OSVersion != "34" || created.Locale != "de_DE" ||
		created.Model != "pixel_7" ||
		created.SystemImage != "system-images;android-34;google_apis;arm64-v8a" {
		t.Fatalf("creation options were not passed through: %+v", created)
	}
}

func TestMCPStartDeviceToolSurfacesABootFailure(t *testing.T) {
	t.Parallel()

	session := connectMCP(t, MCPRunner{StartDevice: StartDeviceRunner{
		Boot: func(context.Context, string, string) error {
			return errors.New("no simulator behind that udid")
		},
	}})
	text, isError := callMCPText(t, session, "start_device",
		map[string]any{"platform": "ios", "udid": "AAAA"})
	if !isError {
		t.Fatalf("a boot failure was not marked as an error: %q", text)
	}
	if !strings.Contains(text, "no simulator behind that udid") {
		t.Fatalf("the boot error was not surfaced: %q", text)
	}
}

func TestMCPStartDeviceToolRequiresAPlatform(t *testing.T) {
	t.Parallel()

	session := connectMCP(t, MCPRunner{})
	text, isError := callMCPText(t, session, "start_device", map[string]any{})
	if !isError {
		t.Fatalf("a missing platform was not marked as an error: %q", text)
	}
	if !strings.Contains(text, "platform") {
		t.Fatalf("the platform requirement was not surfaced: %q", text)
	}
}

// fakeExploreInvoker records what the explore tool handed it and answers a
// scripted result.
type fakeExploreInvoker struct {
	seen   ExploreToolOptions
	result ExploreToolResult
	err    error
}

func (invoker *fakeExploreInvoker) Explore(
	_ context.Context, options ExploreToolOptions,
) (ExploreToolResult, error) {
	invoker.seen = options
	return invoker.result, invoker.err
}

func TestMCPExploreToolReturnsTheReportAndFlowPaths(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	canonicalBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	invoker := &fakeExploreInvoker{result: ExploreToolResult{
		Report: "# session report",
		Flows:  []string{filepath.Join(canonicalBase, "out", "flows", "flow-01.yaml")},
	}}
	session := connectMCP(t, MCPRunner{Checker: stubChecker{}, BaseDir: base, Explore: invoker})
	text, isError := callMCPText(t, session, "explore", map[string]any{
		"app_id": "com.example.app", "platform": "android",
		"device": "emulator-5554", "max_tests": 2, "output_dir": "out",
	})
	if isError {
		t.Fatalf("explore reported an error: %q", text)
	}
	if !strings.Contains(text, "# session report") || !strings.Contains(text, "flow-01.yaml") {
		t.Fatalf("report or flow paths missing from the result: %q", text)
	}
	want := ExploreToolOptions{
		AppID: "com.example.app", Platform: "android", Device: "emulator-5554",
		MaxTests: 2, OutputDir: filepath.Join(canonicalBase, "out"),
	}
	if invoker.seen != want {
		t.Fatalf("invoker saw %+v, want %+v", invoker.seen, want)
	}
}

func TestMCPExploreToolConfinesTheOutputDir(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	base := filepath.Join(parent, "workspace")
	if err := os.Mkdir(base, 0o755); err != nil {
		t.Fatal(err)
	}
	invoker := &fakeExploreInvoker{}
	session := connectMCP(t, MCPRunner{Checker: stubChecker{}, BaseDir: base, Explore: invoker})
	text, isError := callMCPText(t, session, "explore", map[string]any{
		"app_id": "com.example.app", "platform": "android", "output_dir": "../outside",
	})
	if !isError {
		t.Fatalf("an output_dir outside the base directory was accepted: %q", text)
	}
	if !strings.Contains(text, "outside") {
		t.Fatalf("the confinement failure was not named: %q", text)
	}
	if invoker.seen.AppID != "" {
		t.Fatalf("the invoker ran despite the confinement failure: %+v", invoker.seen)
	}
}

func TestMCPExploreToolRequiresAppAndPlatform(t *testing.T) {
	t.Parallel()

	session := connectMCP(t, MCPRunner{Checker: stubChecker{}, Explore: &fakeExploreInvoker{}})
	for name, args := range map[string]map[string]any{
		"missing app_id":   {"platform": "android"},
		"missing platform": {"app_id": "com.example.app"},
	} {
		if text, isError := callMCPText(t, session, "explore", args); !isError {
			t.Fatalf("%s was accepted: %q", name, text)
		}
	}
}

func TestMCPExploreToolSurfacesAnInvokerFailure(t *testing.T) {
	t.Parallel()

	invoker := &fakeExploreInvoker{err: errors.New("no device behind that udid")}
	session := connectMCP(t, MCPRunner{Checker: stubChecker{}, Explore: invoker})
	text, isError := callMCPText(t, session, "explore", map[string]any{
		"app_id": "com.example.app", "platform": "android",
	})
	if !isError {
		t.Fatalf("an invoker failure was not marked as an error: %q", text)
	}
	if !strings.Contains(text, "no device behind that udid") {
		t.Fatalf("the failure was not surfaced: %q", text)
	}
}

func TestMCPExploreToolDefaultInvokerRefusesUnassembled(t *testing.T) {
	t.Parallel()

	// No Explore field: the zero-value invoker adapts the real ExploreRunner,
	// whose seams are unwired in this process, so the tool must answer with
	// the typed refusal before touching any device.
	session := connectMCP(t, MCPRunner{Checker: stubChecker{}})
	text, isError := callMCPText(t, session, "explore", map[string]any{
		"app_id": "com.example.app", "platform": "android",
	})
	if !isError {
		t.Fatalf("the unassembled runner did not refuse: %q", text)
	}
	if !strings.Contains(text, "not assembled") {
		t.Fatalf("the refusal was not typed: %q", text)
	}
}

func TestMCPHierarchyToolReturnsTheTree(t *testing.T) {
	t.Parallel()

	runner := MCPRunner{
		Checker: stubChecker{},
		Hierarchy: HierarchyRunner{
			Fetch: func(_ context.Context, platform, udid string, _ []string, _ string) (device.TreeNode, error) {
				return device.TreeNode{Attributes: map[string]string{"text": "Login"}}, nil
			},
		},
	}
	session := connectMCP(t, runner)
	text, isError := callMCPText(t, session, "hierarchy", map[string]string{"platform": "ios", "udid": "AAAA"})
	if isError {
		t.Fatalf("hierarchy reported an error: %q", text)
	}
	if !strings.Contains(text, "Login") {
		t.Fatalf("the tree was not returned: %q", text)
	}
}

func TestMCPHierarchyToolSurfacesAFetchError(t *testing.T) {
	t.Parallel()

	runner := MCPRunner{
		Checker: stubChecker{},
		Hierarchy: HierarchyRunner{
			Fetch: func(_ context.Context, _, _ string, _ []string, _ string) (device.TreeNode, error) {
				return device.TreeNode{}, errors.New("runner not reachable")
			},
		},
	}
	session := connectMCP(t, runner)
	text, isError := callMCPText(t, session, "hierarchy", map[string]string{"platform": "ios", "udid": "AAAA"})
	if !isError {
		t.Fatalf("a fetch failure was not marked as an error: %q", text)
	}
	if !strings.Contains(text, "runner not reachable") {
		t.Fatalf("the driver error was not surfaced: %q", text)
	}
}

func TestMCPQueryToolReturnsMatches(t *testing.T) {
	t.Parallel()

	runner := MCPRunner{
		Checker: stubChecker{},
		Query: QueryRunner{
			Fetch: func(_ context.Context, _, _, _, expression string) ([]device.TreeNode, error) {
				return []device.TreeNode{{Attributes: map[string]string{"text": "Login"}}}, nil
			},
		},
	}
	session := connectMCP(t, runner)
	text, isError := callMCPText(t, session, "query",
		map[string]string{"platform": "android", "udid": "emulator-5554", "expression": "Log"})
	if isError {
		t.Fatalf("query reported an error: %q", text)
	}
	if !strings.Contains(text, "Login") {
		t.Fatalf("the matches were not returned: %q", text)
	}
}

func TestMCPCheckSyntaxToolPassesValidFlow(t *testing.T) {
	t.Parallel()

	session := connectMCP(t, MCPRunner{Checker: stubChecker{err: nil}})
	text, isError := callMCPText(t, session, "check_syntax",
		map[string]string{"name": "flow.yaml", "yaml": "appId: com.example\n---\n- launchApp\n"})
	if isError {
		t.Fatalf("valid flow reported as error: %q", text)
	}
	if !strings.Contains(text, "ok") {
		t.Fatalf("valid flow did not report ok: %q", text)
	}
}

func TestMCPCheckSyntaxUsesAndConfinesTheConfiguredBaseDirectory(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	canonicalBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	checked := make(chan Source, 1)
	session := connectMCP(t, MCPRunner{BaseDir: base, Checker: stubChecker{checked: checked}})
	_, isError := callMCPText(t, session, "check_syntax",
		map[string]string{"name": "flow.yaml", "yaml": "appId: com.example\n---\n- launchApp\n"})
	if isError {
		t.Fatal("valid flow was rejected")
	}
	source := <-checked
	if source.BaseDir != canonicalBase || source.ConfineTo != canonicalBase {
		t.Fatalf("source = %+v, want base and confinement %q", source, canonicalBase)
	}
}

func TestResolveMCPBaseDirCanonicalizesASymlink(t *testing.T) {
	t.Parallel()

	real := t.TempDir()
	canonicalReal, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	link := filepath.Join(parent, "workspace")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	got, err := resolveMCPBaseDir(link)
	if err != nil {
		t.Fatalf("resolveMCPBaseDir: %v", err)
	}
	if got != canonicalReal {
		t.Fatalf("resolved = %q, want %q", got, canonicalReal)
	}
}

func TestResolveMCPBaseDirRejectsAFileAndMissingDirectory(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "flow.yaml")
	if err := os.WriteFile(file, []byte("appId: example"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{file, filepath.Join(t.TempDir(), "missing")} {
		if _, err := resolveMCPBaseDir(path); err == nil {
			t.Fatalf("resolveMCPBaseDir(%q) succeeded", path)
		}
	}
}

func TestMCPCheckSyntaxToolAcceptsAValidFlowThroughTheRealChecker(t *testing.T) {
	t.Parallel()

	// Integration guard with the real NewParserChecker (Checker left nil). The
	// stub-based tests cannot catch it: the real preflight resolves links
	// against the filesystem, and passing a fake flow-file path made a valid
	// flow fail on a nonexistent-file stat. Inline content must check clean.
	session := connectMCP(t, MCPRunner{})
	text, isError := callMCPText(t, session, "check_syntax",
		map[string]string{"name": "flow.yaml", "yaml": "appId: com.example\n---\n- launchApp\n"})
	if isError {
		t.Fatalf("a valid inline flow was rejected by the real checker via MCP: %q", text)
	}
	if !strings.Contains(text, "ok") {
		t.Fatalf("valid flow did not report ok: %q", text)
	}
}

func TestMCPCheckSyntaxToolSurfacesADiagnostic(t *testing.T) {
	t.Parallel()

	session := connectMCP(t, MCPRunner{Checker: stubChecker{err: errors.New("flow.yaml:2: unknown command frobnicate")}})
	text, isError := callMCPText(t, session, "check_syntax",
		map[string]string{"name": "flow.yaml", "yaml": "appId: com.example\n---\n- frobnicate\n"})
	if !isError {
		t.Fatalf("invalid flow not marked as error: %q", text)
	}
	if !strings.Contains(text, "unknown command frobnicate") {
		t.Fatalf("the diagnostic was not surfaced: %q", text)
	}
}

func TestMCPListDevicesToolReturnsTheInventory(t *testing.T) {
	t.Parallel()

	runner := MCPRunner{
		Checker: stubChecker{},
		ListDevices: ListDevicesRunner{
			IOS: func(context.Context) ([]ios.Device, error) {
				return []ios.Device{{UDID: "AAAA", Name: "iPhone", State: "Booted"}}, nil
			},
			Android: func(context.Context) ([]android.Device, error) { return nil, nil },
		},
	}
	session := connectMCP(t, runner)
	text, isError := callMCPText(t, session, "list_devices", map[string]string{"platform": "ios"})
	if isError {
		t.Fatalf("list_devices reported an error: %q", text)
	}
	if !strings.Contains(text, "AAAA") {
		t.Fatalf("the device inventory was not returned: %q", text)
	}
}
