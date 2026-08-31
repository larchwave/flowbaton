package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// An MCP client is usually a model, and every tool schema is
// additionalProperties:false, so a parameter name it guesses wrong is a hard
// error rather than something the server can shrug off. The tools are called
// in sequence -- start_device hands back a udid that hierarchy, screenshot,
// query, run_flow and explore all need -- so a name that changes between two
// adjacent calls is exactly where a caller trips. Seven tools spelled these in
// camelCase and named the device `udid`; explore alone spelled app_id,
// driver_port, max_tests, max_steps, output_dir and named it `device`.
func TestMCPToolParametersUseOneSpelling(t *testing.T) {
	t.Parallel()
	session := connectMCP(t, MCPRunner{Checker: stubChecker{}})
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools.Tools) == 0 {
		t.Fatal("no tools advertised")
	}
	for _, tool := range tools.Tools {
		encoded, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("%s: marshal schema: %v", tool.Name, err)
		}
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(encoded, &schema); err != nil {
			t.Fatalf("%s: decode schema: %v", tool.Name, err)
		}
		for property := range schema.Properties {
			if strings.Contains(property, "_") {
				t.Errorf("%s takes %q; the other tools spell these in camelCase", tool.Name, property)
			}
			if property == "device" {
				t.Errorf("%s calls the device %q; every other tool calls it \"udid\"", tool.Name, property)
			}
		}
	}
}
