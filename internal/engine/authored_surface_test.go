package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/nohavewho/flowbaton/internal/capability"
	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/enginetest"
	"github.com/nohavewho/flowbaton/internal/model"
)

// testdata/flows/command-manifest.json declares both
// authored forms of every keyword, so it is the input: each minimal and
// maximal form is written to disk, taken through the real Prepare, and
// compiled with the production registry. Compilation is effect-free, so
// this needs no driver.

func TestEveryManifestAuthoredFormCompiles(t *testing.T) {
	t.Parallel()

	registry, err := productionHandlerRegistry()
	if err != nil {
		t.Fatalf("productionHandlerRegistry() error = %v", err)
	}
	entries := loadCommandManifest(t)
	covered := 0
	for _, entry := range entries {
		if entry.RuntimeStatus != "planned-v1" {
			continue
		}
		if _, registered := registry.lookup(model.CommandKeyword(entry.Keyword)); !registered {
			continue
		}
		covered++
		for _, form := range []struct {
			name string
			yaml string
		}{
			{name: "minimal", yaml: entry.Minimal},
			{name: "maximal", yaml: entry.Maximal},
		} {
			t.Run(entry.Keyword+"/"+form.name, func(t *testing.T) {
				gap, known := authoredSurfaceGaps[entry.Keyword+"/"+form.name]
				program, prepareErr := tryPrepareAuthoredForm(t, form.yaml)
				var err error
				if prepareErr != nil {
					err = prepareErr
				} else {
					_, err = compileProgram(context.Background(), program, registry)
				}
				switch {
				case err == nil && known:
					t.Fatalf("%s now compiles; delete its authoredSurfaceGaps entry (%s)",
						entry.Keyword, gap)
				case err != nil && !known:
					t.Fatalf("compileProgram(%s) error = %T %v\nauthored: %s",
						entry.Keyword, err, err, form.yaml)
				}
			})
		}
	}
	// Every registered handler must be reached by a manifest entry.
	if covered != len(registry.byKeyword) {
		t.Fatalf("manifest covered %d registered keywords, want all %d", covered, len(registry.byKeyword))
	}
}

// authoredSurfaceGaps lists manifest forms intentionally excluded from the
// authored contract. The matrix fails when an unlisted form is refused or a
// listed form becomes supported.
var authoredSurfaceGaps = map[string]string{
	// Deliberate: the maximal form is a field catalogue, and it catalogues the
	// selector's `start`/`end` drag anchors, which belong to swipe and have no
	// meaning for a tap. The tap compiler rejects them by name.
	"tapOn/maximal": "the maximal form authors the swipe-only start/end anchors, which tapOn does not implement",

	// Deliberate: the manifest's maximal forms are field catalogues, and
	// retry's `file` and `commands` are mutually exclusive, so authoring both
	// at once is not a runnable command. Each field on its own compiles, which
	// the retry handler's own tests cover.
	"retry/maximal": "the maximal form authors both file and commands, which are mutually exclusive",
}

// TestEveryManifestAuthoredFormEvaluates is the second half of the gate.
// Compilation is structural; evaluation is where interpolation, appId
// resolution and every payload type assertion happen, and a form can pass the
// first and fail the second. Nothing here touches a device, so a handler whose
// evaluator is fine but whose device work would fail is still a pass.
func TestEveryManifestAuthoredFormEvaluates(t *testing.T) {
	t.Parallel()

	registry, err := productionHandlerRegistry()
	if err != nil {
		t.Fatalf("productionHandlerRegistry() error = %v", err)
	}
	evaluation := manifestEvaluationContext(t)
	for _, entry := range loadCommandManifest(t) {
		if entry.RuntimeStatus != "planned-v1" {
			continue
		}
		if _, registered := registry.lookup(model.CommandKeyword(entry.Keyword)); !registered {
			continue
		}
		for _, form := range []struct {
			name string
			yaml string
		}{
			{name: "minimal", yaml: entry.Minimal},
			{name: "maximal", yaml: entry.Maximal},
		} {
			if _, known := authoredSurfaceGaps[entry.Keyword+"/"+form.name]; known {
				continue
			}
			t.Run(entry.Keyword+"/"+form.name, func(t *testing.T) {
				program, prepareErr := tryPrepareAuthoredForm(t, form.yaml)
				if prepareErr != nil {
					t.Fatalf("Prepare(%s) error = %v", form.yaml, prepareErr)
				}
				compiled, compileErr := compileProgram(context.Background(), program, registry)
				if compileErr != nil {
					t.Fatalf("compileProgram(%s) error = %v", form.yaml, compileErr)
				}
				flow, ok := compiled.Flow(compiled.Roots()[0])
				if !ok {
					t.Fatalf("compiled program has no root flow")
				}
				for _, dispatch := range flow.body {
					if dispatch.spec.evaluate == nil {
						continue
					}
					_, err := dispatch.spec.evaluate(
						context.Background(), evaluation, dispatch.command, dispatch.value)
					// ConfigurationError indicates inconsistent authored-shape
					// validation. Other errors represent unavailable runtime services.
					if isConfigurationError(err) {
						t.Fatalf("evaluate(%s) error = %v\nauthored: %s",
							entry.Keyword, err, form.yaml)
					}
				}
			})
		}
	}
}

// TestEveryManifestAuthoredFormExecutes runs each bounded form through public
// Execute with a permissive FakeDriver and complete service stubs.
//
// ConfigurationError indicates inconsistent authored-shape validation. Element
// lookup and assertion failures confirm that execution reached the device layer.
func TestEveryManifestAuthoredFormExecutes(t *testing.T) {
	t.Parallel()

	for _, entry := range loadCommandManifest(t) {
		if entry.RuntimeStatus != "planned-v1" {
			continue
		}
		for _, form := range []struct {
			name string
			yaml string
		}{
			{name: "minimal", yaml: entry.Minimal},
			{name: "maximal", yaml: entry.Maximal},
		} {
			key := entry.Keyword + "/" + form.name
			if _, known := authoredSurfaceGaps[key]; known {
				continue
			}
			if _, unbounded := manifestUnboundedForms[key]; unbounded {
				continue
			}
			t.Run(key, func(t *testing.T) {
				program, prepareErr := tryPrepareAuthoredForm(t, form.yaml)
				if prepareErr != nil {
					t.Fatalf("Prepare(%s) error = %v", form.yaml, prepareErr)
				}
				controller := &recordingControllerStub{}
				_, err := Execute(context.Background(), program, Dependencies{
					ExecutionID: "manifest-execute", Driver: manifestDriver(),
					Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{},
					ArtifactSink: &recordingArtifactSink{}, RecordingController: controller,
					ResourceReader: &batch714ResourceReader{}, ImageChecker: &stubImageChecker{},
					InputGenerator: manifestInputGenerator{},
				})
				if isConfigurationError(err) {
					t.Fatalf("Execute(%s) error = %v\nauthored: %s", entry.Keyword, err, form.yaml)
				}
			})
		}
	}
}

// manifestUnboundedForms are the forms this layer cannot run to completion
// because their semantics have no end, not because anything is wrong with
// them. They still pass the compile and evaluate matrices.
var manifestUnboundedForms = map[string]string{
	// repeat with neither `times` nor `while` repeats indefinitely — the
	// compiler sets times to MaxInt32 deliberately. Against a fake clock and a
	// driver that never tires, that is an infinite loop.
	"repeat/minimal": "repeat without times or while has no termination condition",
}

// manifestDriver answers anything, generously. Counts are budgets rather than
// expectations: this matrix asserts that no command is structurally refused,
// not how many times each one touches the device.
func manifestDriver() *enginetest.FakeDriver {
	driver := enginetest.NewFakeDriver()
	settled := &device.ViewHierarchy{Root: manifestTree()}
	trees := make([]enginetest.Result[device.TreeNode], manifestCallBudget)
	settles := make([]enginetest.Result[*device.ViewHierarchy], manifestCallBudget)
	infos := make([]enginetest.Result[device.DeviceInfo], manifestCallBudget)
	for index := range trees {
		trees[index] = enginetest.Result[device.TreeNode]{Value: manifestTree()}
		settles[index] = enginetest.Result[*device.ViewHierarchy]{Value: settled}
		infos[index] = enginetest.Result[device.DeviceInfo]{Value: device.DeviceInfo{
			Platform: device.Platform("android"), WidthGrid: 300, HeightGrid: 600,
			WidthPixels: 300, HeightPixels: 600,
		}}
	}
	void := make([]enginetest.Result[struct{}], manifestCallBudget)
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo: infos, ContentDescriptor: trees, WaitForAppToSettle: settles,
		LaunchApp: void, StopApp: void, KillApp: void, ClearAppState: void, ClearKeychain: void,
		Tap: void, LongPress: void, PressKey: void, ScrollVertical: void, Swipe: void,
		BackPress: void, InputText: void, OpenLink: void, HideKeyboard: void, EraseText: void,
		SetLocation: void, SetOrientation: void, SetPermissions: void, AddMedia: void,
		SetAirplaneMode:         void,
		IsKeyboardVisible:       manifestValues(true),
		IsAirplaneModeEnabled:   manifestValues(false),
		WaitUntilScreenIsStatic: manifestValues(true),
		TakeScreenshot:          manifestValues(checkStripPNG(0)),
		StartScreenRecording:    manifestValues(device.CaptureID("capture")),
	})
	return driver
}

const manifestCallBudget = 512

func manifestValues[T any](value T) []enginetest.Result[T] {
	results := make([]enginetest.Result[T], manifestCallBudget)
	for index := range results {
		results[index].Value = value
	}
	return results
}

// manifestTree carries every selector target the manifest authors, so a
// lookup-based command reaches its device work instead of stopping at a miss.
func manifestTree() device.TreeNode {
	return device.TreeNode{
		Attributes: map[string]string{"bounds": "[0,0][300,600]"},
		Children: []device.TreeNode{
			{Attributes: map[string]string{
				"text": "Continue", "resource-id": "com.example:id/continue", "bounds": "[10,10][70,70]",
			}},
			{Attributes: map[string]string{
				"text": "Ready", "resource-id": "com.example:id/total", "bounds": "[10,80][70,140]",
			}},
			{Attributes: map[string]string{"text": "Content", "bounds": "[10,150][70,210]"}},
			{Attributes: map[string]string{"text": "Child", "bounds": "[10,220][70,280]"}},
			{Attributes: map[string]string{"text": "Error", "bounds": "[10,290][70,350]"}},
		},
	}
}

type manifestInputGenerator struct{}

func (manifestInputGenerator) Generate(_ context.Context, _ InputRequest) (string, error) {
	return "manifest-input", nil
}

// manifestEvaluationContext supplies the two things evaluation needs and a
// device does not: a real JavaScript runtime for interpolation, and the flow
// config that appId resolution falls back to.
func manifestEvaluationContext(t testing.TB) evaluationContext {
	t.Helper()
	runtime, err := tapJSFactory(t).NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	return evaluationContext{
		evaluateFn:      runtime.Evaluate,
		interpolateFn:   runtime.Interpolate,
		activeConfig:    model.Config{AppID: "com.example.manifest"},
		hasActiveConfig: true,
	}
}

type commandManifestEntry struct {
	Keyword       string `json:"keyword"`
	RuntimeStatus string `json:"runtimeStatus"`
	Minimal       string `json:"minimal"`
	Maximal       string `json:"maximal"`
}

func loadCommandManifest(t testing.TB) []commandManifestEntry {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "testdata", "flows", "command-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read command manifest: %v", err)
	}
	var manifest struct {
		Entries []commandManifestEntry `json:"entries"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode command manifest: %v", err)
	}
	if len(manifest.Entries) == 0 {
		t.Fatal("command manifest declares no entries")
	}
	return manifest.Entries
}

// prepareAuthoredForm writes one authored command as a complete flow, next to
// the contracted files the manifest names, and prepares it for real. The
// support files have to exist because the capability preflight canonicalizes
// every prepared file before execution.
func tryPrepareAuthoredForm(t testing.TB, command string) (*Program, error) {
	t.Helper()
	root := t.TempDir()
	write := func(relative, contents string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("child.yaml", "appId: com.example.child\n---\n- back\n")
	write("fallback.yaml", "appId: com.example.fallback\n---\n- back\n")
	write("scripts/setup.js", "output.ready = true\n")
	write("media/a.png", "png")
	write("media/b.mp4", "mp4")
	write("flow.yaml", "appId: com.example.manifest\n---\n"+command+"\n")

	return Prepare(context.Background(), model.ExecutionPlan{
		SelectedRoots: []string{filepath.Join(root, "flow.yaml")},
	}, capability.FileLoader{})
}
