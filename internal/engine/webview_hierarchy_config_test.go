package engine

import (
	"context"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/flow"
)

// specs/01-core-engine.md:62 enables the Android Chrome DevTools hierarchy when
// `ext["androidWebViewHierarchy"]` is "devtools".
func TestFlowConfigEnablesTheDevToolsHierarchy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config string
		want   bool
	}{
		{name: "devtools", config: "androidWebViewHierarchy: devtools\n", want: true},
		// Other values keep the accessibility hierarchy.
		{name: "accessibility", config: "androidWebViewHierarchy: accessibility\n"},
		{name: "absent", config: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			contents := "appId: com.example.web\n" + test.config + "---\n- back\n"
			parsed, err := flow.ParseBytes("/workspace/webview.yaml", []byte(contents))
			if err != nil {
				t.Fatalf("flow.ParseBytes() error = %v", err)
			}
			driver := enginetest.NewFakeDriver()
			driver.Enqueue(enginetest.DriverScript{
				Capabilities: []device.Capabilities{{Platform: device.Platform("android")}},
				DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{
					Platform: device.Platform("android"), WidthGrid: 400, HeightGrid: 884,
				}}},
				BackPress:                       []enginetest.Result[struct{}]{{}},
				SetAndroidChromeDevToolsEnabled: []enginetest.Result[struct{}]{{}},
			})
			_, err = Execute(context.Background(), singleCompileProgram(parsed), Dependencies{
				ExecutionID: "webview-hierarchy",
				Driver:      driver, Clock: newAdvancingClock(),
				JSFactory: tapJSFactory(t), Controller: NoopController{},
			})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			enabled := false
			for _, action := range driver.Actions() {
				if action.Method != enginetest.MethodSetAndroidChromeDevToolsEnabled {
					continue
				}
				request, ok := action.Request.(device.ChromeDevToolsRequest)
				if !ok {
					t.Fatalf("devtools action carried %T", action.Request)
				}
				enabled = enabled || request.Enabled
			}
			if enabled != test.want {
				t.Fatalf("devtools enabled = %v, want %v (actions %#v)",
					enabled, test.want, driver.Actions())
			}
		})
	}
}
