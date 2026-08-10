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
	for _, want := range []string{"check_syntax", "list_devices", "hierarchy", "query"} {
		if !seen[want] {
			t.Fatalf("tool %q not advertised; saw %v", want, seen)
		}
	}
}

func TestMCPHierarchyToolReturnsTheTree(t *testing.T) {
	t.Parallel()

	runner := MCPRunner{
		Checker: stubChecker{},
		Hierarchy: HierarchyRunner{
			Fetch: func(_ context.Context, platform, udid string, _ []string) (device.TreeNode, error) {
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
			Fetch: func(_ context.Context, _, _ string, _ []string) (device.TreeNode, error) {
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
