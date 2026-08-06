package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/enginetest"
	"github.com/nohavewho/flowbaton/internal/js"
	"github.com/nohavewho/flowbaton/internal/model"
)

// Reserved variables are injected by the host.
//
// specs/01-core-engine.md:101 reserves FLOWBATON_FILENAME,
// FLOWBATON_DEVICE_UDID, FLOWBATON_SHARD_ID, and FLOWBATON_SHARD_INDEX for host
// injection. External input cannot override them.

func reservedEnvironmentRun(
	t *testing.T,
	flow model.Flow,
	external map[string]string,
	reserved map[string]string,
) (*sessionRuntime, error) {
	t.Helper()
	runtime := &sessionRuntime{}
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{DeviceInfo: []enginetest.Result[device.DeviceInfo]{{
		Value: device.DeviceInfo{Platform: device.Platform("ios"), WidthGrid: 390, HeightGrid: 844},
	}}})
	_, err := Execute(context.Background(), singleCompileProgram(flow), Dependencies{
		ExecutionID:         "reserved-environment",
		ExternalEnvironment: external,
		ReservedEnvironment: reserved,
		Driver:              driver,
		Clock:               newAdvancingClock(),
		JSFactory:           &sessionRuntimeFactory{runtime: runtime},
		Controller:          NoopController{},
	})
	return runtime, err
}

func TestReservedEnvironmentOutranksBothTheFlowAndTheOperator(t *testing.T) {
	t.Parallel()

	// The whole point of a reserved name is that only the host sets it. If a
	// flow's own env or the operator's -e could win, a suite could lie to
	// itself about which shard and which device it is running on.
	flow := model.Flow{
		SchemaVersion: model.ASTVersionV0,
		Path:          "/workspace/reserved.yaml",
		Config: model.Config{Env: map[string]string{
			"FLOWBATON_DEVICE_UDID": "from-the-flow",
		}},
	}
	runtime, err := reservedEnvironmentRun(t, flow,
		map[string]string{"FLOWBATON_DEVICE_UDID": "from-the-operator"},
		map[string]string{"FLOWBATON_DEVICE_UDID": "UDID-REAL"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	calls := runtime.EnvCalls()
	if got := lastPut(calls, "FLOWBATON_DEVICE_UDID"); got != "UDID-REAL" {
		t.Fatalf("FLOWBATON_DEVICE_UDID = %q, want the host's value; calls = %#v", got, calls)
	}
}

func TestTheShardVariablesReachTheFlow(t *testing.T) {
	t.Parallel()

	flow := model.Flow{SchemaVersion: model.ASTVersionV0, Path: "/workspace/reserved.yaml"}
	runtime, err := reservedEnvironmentRun(t, flow, nil, map[string]string{
		"FLOWBATON_SHARD_ID":    "2",
		"FLOWBATON_SHARD_INDEX": "1",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	calls := runtime.EnvCalls()
	for key, want := range map[string]string{"FLOWBATON_SHARD_ID": "2", "FLOWBATON_SHARD_INDEX": "1"} {
		if got := lastPut(calls, key); got != want {
			t.Fatalf("%s = %q, want %q; calls = %#v", key, got, want, calls)
		}
	}
}

func TestFlowBatonFilenameIsTheFlowsOwnFileName(t *testing.T) {
	t.Parallel()

	// The host cannot supply this one: it differs per flow, and only the engine
	// knows which flow is running.
	flow := model.Flow{SchemaVersion: model.ASTVersionV0, Path: "/workspace/deep/checkout.yaml"}
	runtime, err := reservedEnvironmentRun(t, flow, nil, nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	calls := runtime.EnvCalls()
	if got := lastPut(calls, "FLOWBATON_FILENAME"); got != "checkout.yaml" {
		t.Fatalf("FLOWBATON_FILENAME = %q, want the base name; calls = %#v", got, calls)
	}
}

func TestFlowBatonFilenameCannotBeOverriddenByTheOperator(t *testing.T) {
	t.Parallel()

	// The operator cannot replace the host-owned filename.
	flow := model.Flow{SchemaVersion: model.ASTVersionV0, Path: "/workspace/checkout.yaml"}
	runtime, err := reservedEnvironmentRun(t, flow,
		map[string]string{"FLOWBATON_FILENAME": "not-this-one.yaml"}, nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	calls := runtime.EnvCalls()
	if got := lastPut(calls, "FLOWBATON_FILENAME"); got != "checkout.yaml" {
		t.Fatalf("FLOWBATON_FILENAME = %q, want the flow's own name; calls = %#v", got, calls)
	}
}

func TestAReservedEnvironmentKeyMustBeAReservedName(t *testing.T) {
	t.Parallel()

	// The field exists for names only the host may set. A key without the
	// prefix would quietly outrank a flow's own env from a channel the flow
	// author cannot see, which is a worse failure than a refusal.
	flow := model.Flow{SchemaVersion: model.ASTVersionV0, Path: "/workspace/reserved.yaml"}
	_, err := reservedEnvironmentRun(t, flow, nil, map[string]string{"API_TOKEN": "sneaky"})
	if err == nil {
		t.Fatal("Execute() accepted a non-reserved key in ReservedEnvironment")
	}
	if !strings.Contains(err.Error(), "FLOWBATON_") {
		t.Fatalf("error = %q, want it to name the required prefix", err)
	}
}

func TestAnEmptyReservedEnvironmentStillInjectsTheFileName(t *testing.T) {
	t.Parallel()

	// Ordinary runs inject the filename even without other reserved values.
	flow := model.Flow{SchemaVersion: model.ASTVersionV0, Path: "/workspace/plain.yaml"}
	runtime, err := reservedEnvironmentRun(t, flow, nil, nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := lastPut(runtime.EnvCalls(), "FLOWBATON_FILENAME"); got != "plain.yaml" {
		t.Fatalf("FLOWBATON_FILENAME = %q, want it injected anyway", got)
	}
}

func TestTheReservedEnvironmentIsSnapshottedOnce(t *testing.T) {
	t.Parallel()

	// Two things at once, and the second is what makes the snapshot observable:
	// Execute must not mutate the caller's map, AND a caller that mutates its
	// own map mid-run must not change what a later root sees. Asserting only
	// the first left the clone unfalsifiable — nothing mutates the map, so
	// deleting the clone changed no test at all.
	flow := model.Flow{SchemaVersion: model.ASTVersionV0, Path: "/workspace/snapshot.yaml"}
	program := singleCompileProgram(flow)
	program.roots = []string{flow.Path, flow.Path}
	program.graph.Roots = append([]string(nil), program.roots...)

	reserved := map[string]string{"FLOWBATON_SHARD_ID": "1"}
	want := cloneStringMap(reserved)
	runtimes := []*sessionRuntime{{}, {}}
	factory := &queuedRuntimeFactory{runtimes: []js.Runtime{runtimes[0], runtimes[1]}}
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{DeviceInfo: []enginetest.Result[device.DeviceInfo]{
		{Value: device.DeviceInfo{Platform: device.Platform("ios"), WidthGrid: 390, HeightGrid: 844}},
		{Value: device.DeviceInfo{Platform: device.Platform("ios"), WidthGrid: 390, HeightGrid: 844}},
	}})

	starts := 0
	results, err := Execute(context.Background(), program, Dependencies{
		ExecutionID:         "reserved-snapshot",
		ReservedEnvironment: reserved,
		Driver:              driver,
		Clock:               newAdvancingClock(),
		JSFactory:           factory,
		Controller:          NoopController{},
		Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
			if event.Kind() == EventFlowStarted {
				starts++
				if starts == 1 {
					reserved["FLOWBATON_SHARD_ID"] = "caller-mutated"
					reserved["FLOWBATON_LATE"] = "must-not-appear"
				}
			}
			return nil
		})},
	})
	if err != nil || len(results) != 2 {
		t.Fatalf("Execute() = results %d error %v", len(results), err)
	}
	for index, runtime := range runtimes {
		calls := runtime.EnvCalls()
		if got := lastPut(calls, "FLOWBATON_SHARD_ID"); got != "1" {
			t.Fatalf("root %d FLOWBATON_SHARD_ID = %q, want the snapshot's 1", index+1, got)
		}
		if got := lastPut(calls, "FLOWBATON_LATE"); got != "" {
			t.Fatalf("root %d saw a late addition: %q", index+1, got)
		}
	}
	if reserved["FLOWBATON_SHARD_ID"] != "caller-mutated" || reserved["FLOWBATON_LATE"] != "must-not-appear" {
		t.Fatalf("the caller's own map was changed by the run: %#v (started as %#v)", reserved, want)
	}
}

func TestAnInlineSubflowKeepsItsContainingFileName(t *testing.T) {
	t.Parallel()

	// Inline runFlow paths include a source position and are not filenames.
	inline := "/workspace/root.yaml" + inlineFlowPathMarker + "12:3:140"
	if _, ok := flowFileName(inline); ok {
		t.Fatalf("flowFileName(%q) claimed a file name", inline)
	}
	// A filesystem path yields its base name.
	if base, ok := flowFileName("/workspace/root.yaml"); !ok || base != "root.yaml" {
		t.Fatalf("flowFileName() = %q %v, want root.yaml true", base, ok)
	}
}

// flowEnvCalls spells one flow scope's environment calls: the push, the puts a
// test set up, and the FLOWBATON_FILENAME the engine injects for that flow.
//
// The helper centralizes the expected injected filename call.
func flowEnvCalls(flowPath string, puts ...string) []string {
	calls := append([]string{"push"}, puts...)
	if base, ok := flowFileName(flowPath); ok {
		calls = append(calls, "put:FLOWBATON_FILENAME="+base)
	}
	return calls
}

// lastPut returns the value of the final put for a key, which is the value the
// flow actually sees.
func lastPut(calls []string, key string) string {
	value := ""
	for _, call := range calls {
		if rest, ok := strings.CutPrefix(call, "put:"+key+"="); ok {
			value = rest
		}
	}
	return value
}

// concatEnvCalls joins per-scope call sequences into the run's full sequence.
func concatEnvCalls(sequences ...[]string) []string {
	calls := []string{}
	for _, sequence := range sequences {
		calls = append(calls, sequence...)
	}
	return calls
}

// withFileName adds the FLOWBATON_FILENAME a flow scope sees to an expected
// environment map, for the same reason flowEnvCalls exists.
func withFileName(flowPath string, values map[string]string) map[string]string {
	merged := cloneStringMap(values)
	if merged == nil {
		merged = map[string]string{}
	}
	if base, ok := flowFileName(flowPath); ok {
		merged["FLOWBATON_FILENAME"] = base
	}
	return merged
}
