package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"reflect"
	"testing"

	"github.com/larchwave/flowbaton/internal/capability"
	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/imagecheck"
	"github.com/larchwave/flowbaton/internal/model"
)

// These tests cover assertScreenshot. The expected image is
// resolved through ResourceReader rather than the filesystem, and the authored
// threshold is a percentage (specs/01-core-engine.md names the field
// thresholdPercentage) whose default is SCREENSHOT_DIFF_THRESHOLD expressed as
// 0.5% rather than the 0.005 ratio the checker consumes.

func TestAssertScreenshotHandlerSpecShape(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(assertScreenshotHandlerSpec())
	if err != nil {
		t.Fatalf("newHandlerRegistry(assertScreenshot) error = %v", err)
	}
	if got := sortedHandlerKeywords(registry); !reflect.DeepEqual(got, []string{"assertScreenshot"}) {
		t.Fatalf("registry = %#v, want exactly assertScreenshot", got)
	}
	spec, _ := registry.lookup(model.CommandAssertScreenshot)
	if spec.effectClass != EffectObserved {
		t.Fatalf("effect class = %v, want %v", spec.effectClass, EffectObserved)
	}
	if spec.postAction != postActionUnspecified || spec.settleRequest != nil {
		t.Fatalf("assertScreenshot must not declare a post-action policy: %#v", spec)
	}
}

func TestAssertScreenshotChecksResolvedExpectedAgainstFreshCapture(t *testing.T) {
	t.Parallel()

	reader := &stubResourceReader{data: []byte("expected-png")}
	checker := &stubImageChecker{ratio: 0}
	result, err := runAssertScreenshot(t, assertScreenshotServices{reader: reader, checker: checker},
		assertScreenshotCommand("expectations/${'home'}", nil, nil), nil)
	if err != nil {
		t.Fatalf("execute(assertScreenshot) error = %T %v", err, err)
	}
	if result.Outcome() != Completed {
		t.Fatalf("outcome = %s, want %s", result.Outcome(), Completed)
	}
	// .png because that is what the contract reads: an authored
	// `assertScreenshot: expected/home` with no expected reports
	// "Screenshot file not found: expected/home.png".
	if len(reader.requests) != 1 || reader.requests[0].Path != "expectations/home.png" {
		t.Fatalf("resource reads = %#v, want one interpolated expected path", reader.requests)
	}
	if len(checker.requests) != 1 {
		t.Fatalf("checks = %#v, want exactly one", checker.requests)
	}
	check := checker.requests[0]
	if string(check.Expected) != "expected-png" {
		t.Fatalf("expected = %q, want the resolved resource bytes", check.Expected)
	}
	if string(check.Actual) != string(assertScreenshotCaptureBytes) {
		t.Fatalf("actual = %q, want the freshly captured screenshot", check.Actual)
	}
	if check.Crop != nil {
		t.Fatalf("crop = %#v, want nil without cropOn", check.Crop)
	}
}

func TestAssertScreenshotDefaultThresholdIsNinetyFivePercentSimilar(t *testing.T) {
	t.Parallel()

	// The number is required similarity, so five percent difference is the edge.
	for _, test := range []struct {
		name      string
		ratio     float64
		wantError bool
	}{
		{name: "just inside", ratio: 0.04},
		{name: "at the threshold", ratio: 0.05},
		{name: "just outside", ratio: 0.06, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := runAssertScreenshot(t,
				assertScreenshotServices{reader: &stubResourceReader{}, checker: &stubImageChecker{ratio: test.ratio}},
				assertScreenshotCommand("expectations/home", nil, nil), nil)
			if test.wantError {
				var assertion *AssertionError
				if !errors.As(err, &assertion) {
					t.Fatalf("ratio %v error = %T %v, want AssertionError", test.ratio, err, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ratio %v error = %T %v, want completion", test.ratio, err, err)
			}
		})
	}
}

func TestAssertScreenshotAuthoredThresholdIsAPercentage(t *testing.T) {
	t.Parallel()

	// thresholdPercentage: 98 demands 98% similarity, so a 0.015 difference
	// passes and 0.025 does not. If the authored number were used as a ratio,
	// or as an allowed difference, both would pass.
	threshold := 98.0
	for _, test := range []struct {
		ratio     float64
		wantError bool
	}{
		{ratio: 0.015},
		{ratio: 0.025, wantError: true},
	} {
		t.Run(fmt.Sprintf("ratio %v", test.ratio), func(t *testing.T) {
			_, err := runAssertScreenshot(t,
				assertScreenshotServices{reader: &stubResourceReader{}, checker: &stubImageChecker{ratio: test.ratio}},
				assertScreenshotCommand("expectations/home", &threshold, nil), nil)
			if test.wantError != (err != nil) {
				t.Fatalf("ratio %v error = %T %v, wantError = %v", test.ratio, err, err, test.wantError)
			}
		})
	}
}

func TestAssertScreenshotCropsToScaledElementBounds(t *testing.T) {
	t.Parallel()

	// Grid is points and the capture is pixels; specs/02-device-drivers.md
	// declares both spaces on DeviceInfo, so a 2x device must scale the crop.
	bounds := device.Bounds{X: 30, Y: 40, Width: 20, Height: 30}
	checker := &stubImageChecker{ratio: 0}
	selector := &model.ElementSelector{TextRegex: stringPointerForScreenshot("Continue")}
	_, err := runAssertScreenshot(t,
		assertScreenshotServices{reader: &stubResourceReader{}, checker: checker},
		assertScreenshotCommand("expectations/home", nil, selector),
		&assertScreenshotScreen{widthGrid: 400, heightGrid: 884, widthPixels: 800, heightPixels: 1768, bounds: &bounds})
	if err != nil {
		t.Fatalf("execute(assertScreenshot cropOn) error = %T %v", err, err)
	}
	if len(checker.requests) != 1 || checker.requests[0].Crop == nil {
		t.Fatalf("checks = %#v, want exactly one cropped check", checker.requests)
	}
	want := image.Rect(60, 80, 100, 140)
	if got := *checker.requests[0].Crop; got != want {
		t.Fatalf("crop = %v, want %v", got, want)
	}
}

func TestAssertScreenshotCropIsUnscaledWhenGridEqualsPixels(t *testing.T) {
	t.Parallel()

	bounds := device.Bounds{X: 30, Y: 40, Width: 20, Height: 30}
	checker := &stubImageChecker{ratio: 0}
	selector := &model.ElementSelector{TextRegex: stringPointerForScreenshot("Continue")}
	_, err := runAssertScreenshot(t,
		assertScreenshotServices{reader: &stubResourceReader{}, checker: checker},
		assertScreenshotCommand("expectations/home", nil, selector),
		&assertScreenshotScreen{widthGrid: 400, heightGrid: 884, widthPixels: 400, heightPixels: 884, bounds: &bounds})
	if err != nil {
		t.Fatalf("execute error = %T %v", err, err)
	}
	want := image.Rect(30, 40, 50, 70)
	if got := *checker.requests[0].Crop; got != want {
		t.Fatalf("crop = %v, want %v", got, want)
	}
}

func TestAssertScreenshotFailsWhenCropTargetIsAbsent(t *testing.T) {
	t.Parallel()

	checker := &stubImageChecker{ratio: 0}
	selector := &model.ElementSelector{TextRegex: stringPointerForScreenshot("Missing")}
	_, err := runAssertScreenshot(t,
		assertScreenshotServices{reader: &stubResourceReader{}, checker: checker},
		assertScreenshotCommand("expectations/home", nil, selector),
		&assertScreenshotScreen{widthGrid: 400, heightGrid: 884, widthPixels: 400, heightPixels: 884})
	var assertion *AssertionError
	if !errors.As(err, &assertion) {
		t.Fatalf("absent cropOn error = %T %v, want AssertionError", err, err)
	}
	if len(checker.requests) != 0 {
		t.Fatalf("checks = %#v, want none once the crop target is missing", checker.requests)
	}
}

func TestAssertScreenshotFailsClosedWithoutItsServices(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		services assertScreenshotServices
	}{
		{name: "no resource reader", services: assertScreenshotServices{checker: &stubImageChecker{}}},
		{name: "no image checker", services: assertScreenshotServices{reader: &stubResourceReader{}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := runAssertScreenshot(t, test.services, assertScreenshotCommand("expectations/home", nil, nil), nil)
			if !isConfigurationError(err) {
				t.Fatalf("error = %T %v, want ConfigurationError", err, err)
			}
		})
	}
}

func TestAssertScreenshotPropagatesBoundaryFailure(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("screenshot boundary refused")
	for _, test := range []struct {
		name     string
		services assertScreenshotServices
	}{
		{
			name:     "expected read",
			services: assertScreenshotServices{reader: &stubResourceReader{err: sentinel}, checker: &stubImageChecker{}},
		},
		{
			name:     "check",
			services: assertScreenshotServices{reader: &stubResourceReader{}, checker: &stubImageChecker{err: sentinel}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := runAssertScreenshot(t, test.services, assertScreenshotCommand("expectations/home", nil, nil), nil)
			if !errors.Is(err, sentinel) {
				t.Fatalf("error = %T %v, want the exact boundary cause", err, err)
			}
		})
	}
}

func TestAssertScreenshotCompileRejectsMalformedCommands(t *testing.T) {
	t.Parallel()

	blank := " "
	negative := -1.0
	overOneHundred := 100.5
	for _, test := range []struct {
		name    string
		command model.Command
	}{
		{name: "wrong keyword", command: model.Command{Kind: model.CommandTapOn, Form: model.CommandFormScalar}},
		{name: "bare scalar", command: model.Command{Kind: model.CommandAssertScreenshot, Form: model.CommandFormScalar}},
		{name: "missing path", command: model.Command{
			Kind: model.CommandAssertScreenshot, Form: model.CommandFormObject, Arguments: map[string]any{},
		}},
		{name: "blank path", command: assertScreenshotCommand(blank, nil, nil)},
		{name: "negative threshold", command: assertScreenshotCommand("expectations/home", &negative, nil)},
		{name: "threshold above one hundred", command: assertScreenshotCommand("expectations/home", &overOneHundred, nil)},
		{name: "unknown key", command: model.Command{
			Kind: model.CommandAssertScreenshot, Form: model.CommandFormObject,
			Arguments: map[string]any{"path": "expectations/home", "tolerance": 1.0},
		}},
		{name: "child commands", command: model.Command{
			Kind: model.CommandAssertScreenshot, Form: model.CommandFormObject,
			Arguments: map[string]any{"path": "expectations/home"},
			Children:  []model.Command{{Kind: model.CommandBack, Form: model.CommandFormScalar}},
		}},
		{name: "cropOn without a normalized selector", command: model.Command{
			Kind: model.CommandAssertScreenshot, Form: model.CommandFormObject,
			Arguments: map[string]any{"path": "expectations/home", "cropOn": map[string]any{"text": "Continue"}},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			compiled, err := compileAssertScreenshot(test.command)
			if compiled != nil || !isConfigurationError(err) {
				t.Fatalf("compileAssertScreenshot() = %#v, %T %v; want nil and ConfigurationError", compiled, err, err)
			}
		})
	}
}

var assertScreenshotCaptureBytes = []byte("fresh-capture-png")

func stringPointerForScreenshot(value string) *string { return &value }

type assertScreenshotServices struct {
	reader  ResourceReader
	checker ImageChecker
}

// assertScreenshotScreen describes the device geometry and the single
// cropOn-matchable element the fake hierarchy exposes.
type assertScreenshotScreen struct {
	widthGrid, heightGrid     int
	widthPixels, heightPixels int
	bounds                    *device.Bounds
}

type stubResourceReader struct {
	requests []ResourceReadRequest
	data     []byte
	err      error
}

func (reader *stubResourceReader) Read(_ context.Context, request ResourceReadRequest) (ResourceReadResult, error) {
	reader.requests = append(reader.requests, request)
	if reader.err != nil {
		return ResourceReadResult{}, reader.err
	}
	return ResourceReadResult{Data: reader.data}, nil
}

// stubImageChecker records the request the handler built and answers with a
// real imagecheck.Result. The ratio is produced by checking two synthetic
// 1000-pixel images so the reported difference ratio is exact rather than a
// hand-built value the production Result type cannot express.
type stubImageChecker struct {
	requests []ImageCheckRequest
	ratio    float64
	err      error
}

func (checker *stubImageChecker) Check(
	_ context.Context,
	request ImageCheckRequest,
) (imagecheck.Result, error) {
	checker.requests = append(checker.requests, request)
	if checker.err != nil {
		return imagecheck.Result{}, checker.err
	}
	different := int(math.Round(checker.ratio * float64(checkPixelCount)))
	return imagecheck.Check(checkStripPNG(0), checkStripPNG(different), nil)
}

// checkPixelCount makes every ratio this test needs land on a whole pixel.
const checkPixelCount = 1000

func checkStripPNG(differentPixels int) []byte {
	strip := image.NewRGBA(image.Rect(0, 0, checkPixelCount, 1))
	for x := 0; x < checkPixelCount; x++ {
		shade := uint8(0)
		if x < differentPixels {
			shade = 255
		}
		strip.SetRGBA(x, 0, color.RGBA{R: shade, G: shade, B: shade, A: 255})
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, strip); err != nil {
		panic(err)
	}
	return encoded.Bytes()
}

func assertScreenshotCommand(path string, threshold *float64, cropOn *model.ElementSelector) model.Command {
	arguments := map[string]any{"path": path}
	command := model.Command{Kind: model.CommandAssertScreenshot, Form: model.CommandFormObject}
	if threshold != nil {
		arguments["thresholdPercentage"] = *threshold
	}
	if cropOn != nil {
		arguments["cropOn"] = map[string]any{"text": *cropOn.TextRegex}
		// The parser copies cropOn onto the normalized selector used by execution.
		command.Selector = cropOn
	}
	command.Arguments = arguments
	return command
}

func runAssertScreenshot(
	t testing.TB,
	services assertScreenshotServices,
	command model.Command,
	screen *assertScreenshotScreen,
) (FlowResult, error) {
	t.Helper()
	registry, err := newHandlerRegistry(assertScreenshotHandlerSpec())
	if err != nil {
		t.Fatalf("newHandlerRegistry(assertScreenshot) error = %v", err)
	}
	if screen == nil {
		screen = &assertScreenshotScreen{widthGrid: 400, heightGrid: 884, widthPixels: 400, heightPixels: 884}
	}
	driver := enginetest.NewFakeDriver()
	info := device.DeviceInfo{
		Platform:  device.Platform("android"),
		WidthGrid: screen.widthGrid, HeightGrid: screen.heightGrid,
		WidthPixels: screen.widthPixels, HeightPixels: screen.heightPixels,
	}
	descriptors := make([]enginetest.Result[device.TreeNode], 4)
	for index := range descriptors {
		descriptors[index].Value = assertScreenshotTree(screen)
	}
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo:        []enginetest.Result[device.DeviceInfo]{{Value: info}, {Value: info}, {Value: info}, {Value: info}},
		ContentDescriptor: descriptors,
		TakeScreenshot:    []enginetest.Result[[]byte]{{Value: assertScreenshotCaptureBytes}},
	})
	path := "/workspace/assert-screenshot.yaml"
	flow := model.Flow{
		SchemaVersion: model.ASTVersionV0, Path: path,
		Config:   model.Config{AppID: "com.example.media"},
		Commands: []model.Command{command},
	}
	program := &Program{
		roots: []string{path}, paths: []string{path},
		flows:   map[string]model.Flow{path: flow},
		aliases: map[string]string{path: path},
		graph: capability.Report{
			Roots: []string{path},
			Nodes: []capability.GraphNode{{Path: path}},
		},
	}
	compiled, compileErr := compileProgram(context.Background(), program, registry)
	if compileErr != nil {
		t.Fatalf("compileProgram(assertScreenshot) error = %v", compileErr)
	}
	root, ok := compiled.Flow(compiled.Roots()[0])
	if !ok {
		t.Fatal("compiled root missing")
	}
	dependencies := Dependencies{
		ExecutionID: "assert-screenshot", Driver: driver, Clock: newAdvancingClock(),
		JSFactory: tapJSFactory(t), Controller: NoopController{},
	}
	if services.reader != nil {
		dependencies.ResourceReader = services.reader
	}
	if services.checker != nil {
		dependencies.ImageChecker = services.checker
	}
	return executeCompiledRootForRun(context.Background(), dependencies, root, "assert-screenshot/root-run-000001")
}

func assertScreenshotTree(screen *assertScreenshotScreen) device.TreeNode {
	root := device.TreeNode{Attributes: map[string]string{
		"bounds": fmt.Sprintf("[0,0][%d,%d]", screen.widthGrid, screen.heightGrid),
	}}
	if screen.bounds != nil {
		root.Children = []device.TreeNode{{Attributes: map[string]string{
			"text": "Continue",
			"bounds": fmt.Sprintf("[%d,%d][%d,%d]",
				screen.bounds.X, screen.bounds.Y,
				screen.bounds.X+screen.bounds.Width, screen.bounds.Y+screen.bounds.Height),
		}}}
	}
	return root
}
