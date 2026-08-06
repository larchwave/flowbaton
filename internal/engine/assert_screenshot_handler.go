package engine

import (
	"context"
	"fmt"
	"image"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/model"
)

// specs/01-core-engine.md declares AssertScreenshotCommand(path,
// thresholdPercentage, cropOn). thresholdPercentage is the minimum similarity
// the capture must reach. imagecheck reports a difference ratio, which is
// converted at this boundary. The contract default is 95%.
const screenshotSimilarityPercentDefault = 95

// screenshotExpectedExtension is what the contract appends to an authored path
// that has none: `assertScreenshot: expected/home` reads expected/home.png.
const screenshotExpectedExtension = ".png"

type assertScreenshotCompiled struct {
	path              string
	similarityPercent float64
	cropSelector      *model.ElementSelector
}

type assertScreenshotEvaluated struct {
	path              string
	similarityPercent float64
	cropSelector      *model.ElementSelector
}

func assertScreenshotHandlerSpec() handlerSpec {
	return handlerSpec{
		keyword: model.CommandAssertScreenshot, effectClass: EffectObserved,
		compile:  pureCompiler(compileAssertScreenshot),
		evaluate: evaluateAssertScreenshot, execute: executeAssertScreenshot,
	}
}

func compileAssertScreenshot(command model.Command) (any, error) {
	if command.Kind != model.CommandAssertScreenshot {
		return nil, NewConfigurationError("assertScreenshot keyword is invalid", nil)
	}
	if len(command.Children) != 0 {
		return nil, commandDecodeError(command.Kind, "must not contain child commands")
	}
	// The parser accepts both `assertScreenshot: expected/home` and the object
	// form, so the scalar shorthand has to compile to the same payload with
	// the default threshold and no crop.
	decoded, err := decodeStringOrObject(command)
	if err != nil {
		return nil, err
	}
	if decoded.stringValue != nil {
		if strings.TrimSpace(*decoded.stringValue) == "" {
			return nil, commandDecodeError(command.Kind, "path must not be blank")
		}
		return assertScreenshotCompiled{
			path: *decoded.stringValue, similarityPercent: screenshotSimilarityPercentDefault,
		}, nil
	}
	object := *decoded.objectValue
	if err := object.rejectUnknown("path", "thresholdPercentage", "cropOn"); err != nil {
		return nil, err
	}
	path, err := object.requireString("path")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(path) == "" {
		return nil, commandDecodeError(command.Kind, "path must not be blank")
	}
	payload := assertScreenshotCompiled{path: path, similarityPercent: screenshotSimilarityPercentDefault}
	threshold, exists, err := object.optionalNumber("thresholdPercentage")
	if err != nil {
		return nil, err
	}
	if exists {
		if threshold < 0 || threshold > 100 {
			return nil, commandDecodeError(command.Kind, "thresholdPercentage is a percentage in [0,100]")
		}
		payload.similarityPercent = threshold
	}
	if _, authored := object.raw("cropOn"); authored {
		// The parser copies cropOn onto the normalized selector used for resolution.
		if command.Selector == nil {
			return nil, commandDecodeError(command.Kind, "cropOn requires a normalized selector")
		}
		if err := validateImplementedSelectorTargets(command.Kind, command.Selector, "cropOn"); err != nil {
			return nil, err
		}
		payload.cropSelector = cloneSelector(command.Selector)
	}
	return payload, nil
}

func evaluateAssertScreenshot(
	ctx context.Context,
	evaluation evaluationContext,
	command model.Command,
	compiled any,
) (evaluatedDispatch, error) {
	payload, ok := compiled.(assertScreenshotCompiled)
	if !ok {
		return evaluatedDispatch{}, NewConfigurationError("assertScreenshot received an invalid compiled payload", nil)
	}
	evaluated := evaluatedDispatch{command: cloneCommand(command), value: assertScreenshotEvaluated{}}
	path, err := evaluation.Interpolate(ctx, payload.path, nil)
	if err != nil {
		return evaluated, err
	}
	if strings.TrimSpace(path) == "" {
		return evaluated, NewConfigurationError("assertScreenshot path must not be blank after interpolation", nil)
	}
	value := assertScreenshotEvaluated{
		path: expectedPath(path), similarityPercent: payload.similarityPercent,
	}
	if payload.cropSelector != nil {
		selector := cloneSelector(payload.cropSelector)
		if err := interpolateSelector(ctx, evaluation, selector); err != nil {
			return evaluated, err
		}
		value.cropSelector = selector
	}
	evaluated.value = value
	return evaluated, nil
}

func executeAssertScreenshot(
	ctx context.Context,
	state *executionState,
	evaluated evaluatedDispatch,
) (commandEffect, error) {
	effect := commandEffect{effectClass: EffectObserved}
	payload, ok := evaluated.value.(assertScreenshotEvaluated)
	if !ok {
		return effect, NewConfigurationError("assertScreenshot received an invalid evaluated payload", nil)
	}
	if err := ctx.Err(); err != nil {
		return effect, err
	}
	// The crop is resolved first so a missing target fails before any capture,
	// expected read, or check is spent on it.
	crop, err := resolveScreenshotCrop(ctx, state, payload.cropSelector)
	if err != nil {
		return effect, err
	}
	expected, err := state.readResource(ctx, ResourceReadRequest{Path: payload.path})
	if err != nil {
		return effect, err
	}
	actual, err := state.dependencies.Driver.TakeScreenshot(ctx, device.ScreenshotRequest{})
	if err != nil {
		return effect, err
	}
	check, err := state.checkImages(ctx, ImageCheckRequest{
		Expected: expected.Data, Actual: actual, Crop: crop,
	})
	if err != nil {
		return effect, err
	}
	// The authored number is a required similarity and the checker speaks in
	// difference, so the demand is inverted once, here.
	withinThreshold, thresholdErr := check.WithinThreshold(1 - payload.similarityPercent/100)
	if thresholdErr != nil {
		return effect, NewConfigurationError("assertScreenshot thresholdPercentage is invalid", thresholdErr)
	}
	if !withinThreshold {
		return effect, NewAssertionError(fmt.Sprintf(
			"assertScreenshot similarity %s%% is below the required thresholdPercentage %s%%",
			formatPercent(100-check.DifferenceRatio()*100),
			formatPercent(payload.similarityPercent)), nil)
	}
	return effect, nil
}

// expectedPath is the contract's resolution rule: an authored path with no
// extension gets .png, one that already has an extension is used as it stands.
// Applied after interpolation so `expected/${name}` resolves first.
func expectedPath(path string) string {
	if filepath.Ext(path) != "" {
		return path
	}
	return path + screenshotExpectedExtension
}

// resolveScreenshotCrop turns the cropOn target into a rectangle in the
// capture's pixel space. specs/02-device-drivers.md defines element bounds in
// grid units — pixels on Android, points on iOS — so the grid-to-pixel ratio
// DeviceInfo already carries is applied here. The rectangle encloses the
// element so scaling never trims its edges.
func resolveScreenshotCrop(
	ctx context.Context,
	state *executionState,
	selector *model.ElementSelector,
) (*image.Rectangle, error) {
	if selector == nil {
		return nil, nil
	}
	lookup, err := state.elementLookup()
	if err != nil {
		return nil, err
	}
	element, err := lookup.findUntil(ctx, *selector, LookupOptions{Optional: true}, lookup.adjustedDeadline(LookupOptions{}))
	if err != nil {
		return nil, err
	}
	if element == nil {
		return nil, NewAssertionError("assertScreenshot cropOn target not found", nil)
	}
	if !element.HasBounds {
		return nil, NewConfigurationError("assertScreenshot cropOn target has no bounds", nil)
	}
	info, err := lookup.cachedDeviceInfo(ctx)
	if err != nil {
		return nil, err
	}
	if info.WidthGrid <= 0 || info.HeightGrid <= 0 {
		return nil, NewConfigurationError("assertScreenshot cropOn requires a positive device grid", nil)
	}
	bounds := element.Bounds
	crop := image.Rect(
		scaleGridToPixels(bounds.X, info.WidthPixels, info.WidthGrid, false),
		scaleGridToPixels(bounds.Y, info.HeightPixels, info.HeightGrid, false),
		scaleGridToPixels(bounds.X+bounds.Width, info.WidthPixels, info.WidthGrid, true),
		scaleGridToPixels(bounds.Y+bounds.Height, info.HeightPixels, info.HeightGrid, true),
	)
	return &crop, nil
}

// scaleGridToPixels converts one grid coordinate into capture pixels. A zero or
// absent pixel dimension means the capture is already in grid units, which is
// the Android case where grid equals pixels. roundUp encloses the far edge.
func scaleGridToPixels(value, pixels, grid int, roundUp bool) int {
	if pixels <= 0 || pixels == grid {
		return value
	}
	scaled := value * pixels
	if roundUp && scaled%grid != 0 {
		return scaled/grid + 1
	}
	return scaled / grid
}

// formatPercent prints a percentage without trailing zeroes, so a whole number
// reads as "95" rather than "95.000000".
func formatPercent(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}
