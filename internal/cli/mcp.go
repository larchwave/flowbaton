package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/version"
)

// mcp runs flowbaton as an MCP server (spec 03), exposing its capabilities as
// tools an MCP client can call. FlowBaton uses the SDK's protocol implementation.
// Unless --no-viewer, it also runs the optional Viewer HTTP server (spec 03)
// alongside the stdio server; see mcp_viewer.go.

// MCPRunner builds and serves the flowbaton MCP server over stdio. Its
// dependencies are fields so a test can drive the same server through an
// in-memory transport.
type MCPRunner struct {
	Checker     Checker
	ListDevices ListDevicesRunner
	Hierarchy   HierarchyRunner
	Query       QueryRunner
	// RunFlow executes run_flow calls through the same pipeline as the test
	// subcommand. Its zero value builds real device sessions.
	RunFlow TestRunner
	// BaseDir confines inline flow links exposed through MCP. Run fills it from
	// --base-dir (or the current working directory).
	BaseDir string
}

func (runner MCPRunner) checker() Checker {
	if runner.Checker != nil {
		return runner.Checker
	}
	return NewParserChecker()
}

type checkSyntaxToolInput struct {
	Name string `json:"name"`
	Yaml string `json:"yaml"`
}

type listDevicesToolInput struct {
	Platform string `json:"platform"`
}

type deviceToolInput struct {
	Platform string `json:"platform"`
	UDID     string `json:"udid"`
	// AppID names the app the tree is about. iOS needs it: with no app the
	// runner answers with the springboard's tree, not the app in front.
	AppID string `json:"appId,omitempty"`
}

type queryToolInput struct {
	Platform   string `json:"platform"`
	UDID       string `json:"udid"`
	AppID      string `json:"appId,omitempty"`
	Expression string `json:"expression"`
}

type runFlowToolInput struct {
	Platform string `json:"platform"`
	UDID     string `json:"udid"`
	// Path names a flow file under the base directory. Exactly one of Path and
	// Yaml must be set.
	Path string `json:"path,omitempty"`
	// Yaml is inline flow content. It runs from a private temporary directory,
	// so links inside it resolve against nothing but that directory.
	Yaml string `json:"yaml,omitempty"`
	// OutputDir receives run artifacts and the report, under the base
	// directory. Empty uses the test subcommand's default location.
	OutputDir string `json:"outputDir,omitempty"`
}

// confineToMCPBase resolves a caller-supplied path against the canonical base
// directory and refuses anything that lands outside it. Relative paths join
// the base; absolute paths must already be inside it.
func confineToMCPBase(baseDir, supplied string) (string, error) {
	cleaned := filepath.Clean(strings.TrimSpace(supplied))
	if cleaned == "" || cleaned == "." {
		return "", fmt.Errorf("a path is required")
	}
	joined := cleaned
	if !filepath.IsAbs(joined) {
		joined = filepath.Join(baseDir, joined)
	}
	// The parent is canonicalized rather than the target so a not-yet-created
	// output directory can still be confined; the final element itself must not
	// be a symlink pointing out of the base.
	resolvedParent, err := filepath.EvalSymlinks(filepath.Dir(joined))
	if err != nil {
		return "", err
	}
	resolved := filepath.Join(resolvedParent, filepath.Base(joined))
	if target, err := filepath.EvalSymlinks(resolved); err == nil {
		resolved = target
	}
	if resolved != baseDir && !strings.HasPrefix(resolved, baseDir+string(filepath.Separator)) {
		return "", fmt.Errorf("%s is outside the base directory", supplied)
	}
	return resolved, nil
}

// jsonResult marshals a value to an indented-JSON tool result, or an error
// result if marshaling fails.
func jsonResult(value any) (*mcp.CallToolResult, any, error) {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return errorResult(err), nil, nil
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}}, nil, nil
}

func errorResult(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
		IsError: true,
	}
}

// server assembles the MCP server with flowbaton's tools registered. Kept
// separate from Run so a test can connect a client to it directly.
func (runner MCPRunner) server() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "flowbaton", Version: version.Version}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "check_syntax",
		Description: "Check a FlowBaton flow's syntax and command support. Returns ok, or the diagnostic.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in checkSyntaxToolInput) (*mcp.CallToolResult, any, error) {
		// The content is inline, so this mirrors the CLI's stdin path exactly:
		// Name "-" tells the preflight the flow has no file of its own to stat
		// (passing a fake path made it fail to resolve a nonexistent file), and
		// relative links resolve against the working directory. in.Name is
		// intentionally not used as a path — it would be treated as a real file.
		baseDir, err := resolveMCPBaseDir(runner.BaseDir)
		if err != nil {
			return errorResult(fmt.Errorf("mcp: base directory: %w", err)), nil, nil
		}
		if err := runner.checker().Check(ctx, Source{
			Name: "-", BaseDir: baseDir, ConfineTo: baseDir, Data: []byte(in.Yaml),
		}); err != nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
				IsError: true,
			}, nil, nil
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_devices",
		Description: "List targetable devices. Optional platform filter: ios or android.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listDevicesToolInput) (*mcp.CallToolResult, any, error) {
		var out bytes.Buffer
		var args []string
		if in.Platform != "" {
			args = []string{"-p", in.Platform}
		}
		code := runner.ListDevices.Run(ctx, args, &out, &out)
		result := &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: strings.TrimRight(out.String(), "\n")}},
		}
		if code != ExitOK {
			result.IsError = true
		}
		return result, nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "hierarchy",
		Description: "Dump the current view hierarchy of a device as JSON. Requires platform (ios|android) and a device udid; on iOS pass appId for the app in front, or the springboard's tree comes back instead.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in deviceToolInput) (*mcp.CallToolResult, any, error) {
		tree, err := runner.Hierarchy.fetch()(
			ctx, in.Platform, in.UDID, appIDFilter(in.AppID), "")
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(tree)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "query",
		Description: "Find on-device elements matching an expression. Requires platform (ios|android), a device udid, and expression.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in queryToolInput) (*mcp.CallToolResult, any, error) {
		matches, err := runner.Query.fetch()(ctx, in.Platform, in.UDID, in.AppID, in.Expression)
		if err != nil {
			return errorResult(err), nil, nil
		}
		if matches == nil {
			matches = []device.TreeNode{}
		}
		return jsonResult(matches)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "run_flow",
		Description: "Run one FlowBaton flow on a device and return the PASS/FAIL report. " +
			"Requires platform (ios|android|web) and a device udid. Pass either path " +
			"(a flow file under the base directory) or yaml (inline flow content). " +
			"This executes the flow for real: it launches apps and drives the device.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in runFlowToolInput) (*mcp.CallToolResult, any, error) {
		if strings.TrimSpace(in.Platform) == "" || strings.TrimSpace(in.UDID) == "" {
			return errorResult(fmt.Errorf("run_flow: platform and udid are required")), nil, nil
		}
		baseDir, err := resolveMCPBaseDir(runner.BaseDir)
		if err != nil {
			return errorResult(fmt.Errorf("mcp: base directory: %w", err)), nil, nil
		}

		var target string
		switch {
		case in.Path != "" && in.Yaml != "":
			return errorResult(fmt.Errorf("run_flow: pass path or yaml, not both")), nil, nil
		case in.Path != "":
			target, err = confineToMCPBase(baseDir, in.Path)
			if err != nil {
				return errorResult(fmt.Errorf("run_flow: %w", err)), nil, nil
			}
		case in.Yaml != "":
			directory, tempErr := os.MkdirTemp("", "flowbaton-mcp-flow")
			if tempErr != nil {
				return errorResult(fmt.Errorf("run_flow: %w", tempErr)), nil, nil
			}
			defer func() { _ = os.RemoveAll(directory) }()
			target = filepath.Join(directory, "flow.yaml")
			if writeErr := os.WriteFile(target, []byte(in.Yaml), 0o600); writeErr != nil {
				return errorResult(fmt.Errorf("run_flow: %w", writeErr)), nil, nil
			}
		default:
			return errorResult(fmt.Errorf("run_flow: pass path or yaml")), nil, nil
		}

		args := []string{"-p", in.Platform, "--device", in.UDID}
		if in.OutputDir != "" {
			outputDirectory, outErr := confineToMCPBase(baseDir, in.OutputDir)
			if outErr != nil {
				return errorResult(fmt.Errorf("run_flow: %w", outErr)), nil, nil
			}
			args = append(args, "--test-output-dir", outputDirectory)
		}
		args = append(args, target)

		var out bytes.Buffer
		code := runner.RunFlow.Run(ctx, args, &out, &out)
		result := &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: strings.TrimRight(out.String(), "\n")}},
		}
		if code != ExitOK {
			result.IsError = true
		}
		return result, nil, nil
	})

	return server
}

func (runner MCPRunner) Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	options, code := parseMCPArgs(args, stderr)
	if code != ExitOK {
		return code
	}
	baseDir, err := resolveMCPBaseDir(options.baseDir)
	if err != nil {
		fmt.Fprintf(stderr, "mcp: base directory: %v\n", err)
		return ExitInvalid
	}
	runner.BaseDir = baseDir
	if !options.noViewer {
		addr, stop, err := startViewer(options.viewerPort, runner.Hierarchy)
		if err != nil {
			fmt.Fprintf(stderr, "mcp: viewer: %v\n", err)
			return ExitFailure
		}
		// Run blocks on the stdio server, which returns when ctx is cancelled or
		// stdin closes; the deferred stop then shuts the viewer down cleanly.
		defer stop()
		fmt.Fprintf(stderr, "mcp: viewer serving on http://%s\n", addr)
	}
	server := runner.server()
	transport := &mcp.StdioTransport{}
	if err := server.Run(ctx, transport); err != nil {
		fmt.Fprintf(stderr, "mcp: %v\n", err)
		return ExitFailure
	}
	return ExitOK
}

// resolveMCPBaseDir returns an existing canonical directory. Canonicalizing
// once prevents a symlinked --base-dir from weakening later confinement checks.
func resolveMCPBaseDir(authored string) (string, error) {
	path := strings.TrimSpace(authored)
	if path == "" {
		var err error
		path, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("current working directory: %w", err)
		}
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", authored)
	}
	return resolved, nil
}

type mcpArgs struct {
	noViewer   bool
	viewerPort int
	baseDir    string
}

// parseMCPArgs reads the viewer flags. --viewer-port 0 (the default) means an
// OS-assigned free port, matching the CLI contract. --base-dir is accepted for
// flag behavior; the viewer is device-scoped and does not read the working tree.
func parseMCPArgs(args []string, stderr io.Writer) (mcpArgs, int) {
	var parsed mcpArgs
	for i := 0; i < len(args); i++ {
		arg := args[i]
		needsValue := func() (string, bool) {
			if i+1 >= len(args) {
				fmt.Fprintf(stderr, "mcp: %s needs a value\n", arg)
				return "", false
			}
			i++
			return args[i], true
		}
		setPort := func(raw string) bool {
			port, err := strconv.Atoi(raw)
			if err != nil || port < 0 || port > 65535 {
				fmt.Fprintf(stderr, "mcp: --viewer-port %q is not a valid port\n", raw)
				return false
			}
			parsed.viewerPort = port
			return true
		}
		switch {
		case arg == "--no-viewer":
			parsed.noViewer = true
		case arg == "--base-dir":
			value, ok := needsValue()
			if !ok {
				return parsed, ExitInvalid
			}
			parsed.baseDir = value
		case strings.HasPrefix(arg, "--base-dir="):
			parsed.baseDir = strings.TrimPrefix(arg, "--base-dir=")
		case arg == "--viewer-port":
			value, ok := needsValue()
			if !ok || !setPort(value) {
				return parsed, ExitInvalid
			}
		case strings.HasPrefix(arg, "--viewer-port="):
			if !setPort(strings.TrimPrefix(arg, "--viewer-port=")) {
				return parsed, ExitInvalid
			}
		default:
			fmt.Fprintf(stderr, "mcp: unexpected argument %q\n", arg)
			return parsed, ExitInvalid
		}
	}
	if strings.TrimSpace(parsed.baseDir) == "" {
		for _, arg := range args {
			if arg == "--base-dir" || strings.HasPrefix(arg, "--base-dir=") {
				fmt.Fprintln(stderr, "mcp: --base-dir must not be empty")
				return parsed, ExitInvalid
			}
		}
	}
	return parsed, ExitOK
}
