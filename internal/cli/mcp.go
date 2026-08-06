package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/version"
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
		baseDir, err := os.Getwd()
		if err != nil {
			baseDir = ""
		}
		if err := runner.checker().Check(ctx, Source{Name: "-", BaseDir: baseDir, Data: []byte(in.Yaml)}); err != nil {
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
		tree, err := runner.Hierarchy.fetch()(ctx, in.Platform, in.UDID, appIDFilter(in.AppID))
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

	return server
}

func (runner MCPRunner) Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	options, code := parseMCPArgs(args, stderr)
	if code != ExitOK {
		return code
	}
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
	return parsed, ExitOK
}
