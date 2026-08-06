package engine

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/enginetest"
	"github.com/nohavewho/flowbaton/internal/model"
)

// Travel expectations follow specs/05-command-semantics-addendum.md §2.3.

func TestTravelHandlerSpecShape(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(travelHandlerSpec())
	if err != nil {
		t.Fatalf("newHandlerRegistry(travel) error = %v", err)
	}
	if got := sortedHandlerKeywords(registry); !reflect.DeepEqual(got, []string{"travel"}) {
		t.Fatalf("registry = %#v, want exactly travel", got)
	}
	spec, _ := registry.lookup(model.CommandTravel)
	if spec.effectClass != EffectDeviceMutation {
		t.Fatalf("effect class = %v, want %v", spec.effectClass, EffectDeviceMutation)
	}
	if spec.postAction != postActionNoSettle {
		t.Fatalf("post action = %v, want no-settle", spec.postAction)
	}
}

func TestTravelWithASinglePointEmitsItOnceAndWaitsForNothing(t *testing.T) {
	t.Parallel()

	clock := &travelRecordingClock{now: time.Unix(1_700_000_000, 0).UTC()}
	driver := travelDriver(nil)
	command := travelCommand([]any{"48.0, 2.0"}, nil)
	if _, err := runSingleCommandFlowWithClock(t, driver, travelRegistry(t), command, "single", clock); err != nil {
		t.Fatalf("execute(travel) error = %T %v", err, err)
	}
	want := []device.Location{{Latitude: 48.0, Longitude: 2.0}}
	if got := travelLocations(driver); !reflect.DeepEqual(got, want) {
		t.Fatalf("emissions = %#v, want %#v", got, want)
	}
	if len(clock.waits) != 0 {
		t.Fatalf("waits = %v, want none: there is no segment to walk", clock.waits)
	}
}

func TestTravelEmitsOneStartPlusFiftyStepsPerSegment(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		points []any
		want   int
	}{
		{name: "two points", points: []any{"48.0, 2.0", "48.1, 2.0"}, want: 1 + travelStepsPerSegment},
		{name: "three points", points: []any{"48.0, 2.0", "48.1, 2.0", "48.1, 2.1"}, want: 1 + 2*travelStepsPerSegment},
	} {
		t.Run(test.name, func(t *testing.T) {
			driver := travelDriver(nil)
			command := travelCommand(test.points, nil)
			if _, err := runSingleCommandFlow(t, driver, travelRegistry(t), command, test.name); err != nil {
				t.Fatalf("execute(travel) error = %T %v", err, err)
			}
			if got := len(travelLocations(driver)); got != test.want {
				t.Fatalf("setLocation calls = %d, want %d", got, test.want)
			}
		})
	}
}

func TestTravelInterpolatesEachAxisLinearlyAndLandsOnTheExactEndpoint(t *testing.T) {
	t.Parallel()

	// Deltas differ by 2.5x between axes, so a per-axis fixed step would show up.
	driver := travelDriver(nil)
	command := travelCommand([]any{"48.0, 2.0", "48.001, 2.0004"}, nil)
	if _, err := runSingleCommandFlow(t, driver, travelRegistry(t), command, "interpolation"); err != nil {
		t.Fatalf("execute(travel) error = %T %v", err, err)
	}
	locations := travelLocations(driver)
	first, last := locations[0], locations[len(locations)-1]
	if first != (device.Location{Latitude: 48.0, Longitude: 2.0}) {
		t.Fatalf("first emission = %#v, want the authored start point", first)
	}
	// The endpoint must be exact, not the accumulation of fifty additions.
	if last != (device.Location{Latitude: 48.001, Longitude: 2.0004}) {
		t.Fatalf("last emission = %#v, want the authored end point exactly", last)
	}
	wantLat, wantLon := 0.001/travelStepsPerSegment, 0.0004/travelStepsPerSegment
	for index := 1; index < len(locations); index++ {
		gotLat := locations[index].Latitude - locations[index-1].Latitude
		gotLon := locations[index].Longitude - locations[index-1].Longitude
		if math.Abs(gotLat-wantLat) > 1e-9 || math.Abs(gotLon-wantLon) > 1e-9 {
			t.Fatalf("step %d = (%g, %g), want (%g, %g)", index, gotLat, gotLon, wantLat, wantLon)
		}
	}
}

func TestTravelEmitsAJunctionPointExactlyOnce(t *testing.T) {
	t.Parallel()

	driver := travelDriver(nil)
	command := travelCommand([]any{"48.0, 2.0", "48.1, 2.0", "48.1, 2.1"}, nil)
	if _, err := runSingleCommandFlow(t, driver, travelRegistry(t), command, "junction"); err != nil {
		t.Fatalf("execute(travel) error = %T %v", err, err)
	}
	junction := device.Location{Latitude: 48.1, Longitude: 2.0}
	seen := 0
	for _, location := range travelLocations(driver) {
		if location == junction {
			seen++
		}
	}
	if seen != 1 {
		t.Fatalf("junction emitted %d times, want exactly once", seen)
	}
}

func TestTravelDelayUsesSegmentDistanceAndSpeed(t *testing.T) {
	t.Parallel()

	// spec 05 §2.3: delay = stepDistanceMetres * (pi/180) / speed, and the start
	// point is emitted with no preceding delay.
	clock := &travelRecordingClock{now: time.Unix(1_700_000_000, 0).UTC()}
	driver := travelDriver(nil)
	speed := 0.37
	command := travelCommand([]any{"48.0, 2.0", "48.1, 2.0"}, &speed)
	if _, err := runSingleCommandFlowWithClock(t, driver, travelRegistry(t), command, "delay", clock); err != nil {
		t.Fatalf("execute(travel) error = %T %v", err, err)
	}
	if len(clock.waits) != travelStepsPerSegment {
		t.Fatalf("waits = %d, want %d (one after each interpolated emission, none before the start point)",
			len(clock.waits), travelStepsPerSegment)
	}
	stepMetres := haversineMetres(
		device.Location{Latitude: 48.0, Longitude: 2.0},
		device.Location{Latitude: 48.1, Longitude: 2.0},
	) / travelStepsPerSegment
	want := time.Duration(stepMetres * math.Pi / 180 / speed * float64(time.Second))
	for index, wait := range clock.waits {
		if wait != want {
			t.Fatalf("wait[%d] = %v, want %v", index, wait, want)
		}
	}
}

func TestTravelDefaultSpeedIsFourMetresPerSecond(t *testing.T) {
	t.Parallel()

	// Omitted speed uses the four-metres-per-second default.
	authored := travelDefaultSpeed
	explicit := &travelRecordingClock{now: time.Unix(1_700_000_000, 0).UTC()}
	implicit := &travelRecordingClock{now: time.Unix(1_700_000_000, 0).UTC()}
	points := []any{"48.0, 2.0", "48.1, 2.0"}
	if _, err := runSingleCommandFlowWithClock(t, travelDriver(nil), travelRegistry(t),
		travelCommand(points, &authored), "explicit", explicit); err != nil {
		t.Fatalf("execute(travel explicit) error = %v", err)
	}
	if _, err := runSingleCommandFlowWithClock(t, travelDriver(nil), travelRegistry(t),
		travelCommand(points, nil), "implicit", implicit); err != nil {
		t.Fatalf("execute(travel implicit) error = %v", err)
	}
	if !reflect.DeepEqual(explicit.waits, implicit.waits) {
		t.Fatalf("implicit speed waits differ from speed: %v", travelDefaultSpeed)
	}
}

func TestTravelInterpolatesPointStrings(t *testing.T) {
	t.Parallel()

	driver := travelDriver(nil)
	command := travelCommand([]any{"${'48.0'}, 2.0", "48.1, 2.0"}, nil)
	if _, err := runSingleCommandFlow(t, driver, travelRegistry(t), command, "interpolated-points"); err != nil {
		t.Fatalf("execute(travel) error = %T %v", err, err)
	}
	if got := travelLocations(driver)[0]; got != (device.Location{Latitude: 48.0, Longitude: 2.0}) {
		t.Fatalf("first emission = %#v, want the interpolated start point", got)
	}
}

func TestTravelCompileRejectsMalformedCommands(t *testing.T) {
	t.Parallel()

	negative := -1.0
	zero := 0.0
	for _, test := range []struct {
		name    string
		command model.Command
	}{
		{name: "wrong keyword", command: model.Command{Kind: model.CommandTapOn, Form: model.CommandFormScalar}},
		{name: "bare scalar", command: model.Command{Kind: model.CommandTravel, Form: model.CommandFormScalar}},
		{name: "missing points", command: model.Command{
			Kind: model.CommandTravel, Form: model.CommandFormObject, Arguments: map[string]any{},
		}},
		{name: "unknown key", command: model.Command{
			Kind: model.CommandTravel, Form: model.CommandFormObject,
			Arguments: map[string]any{"points": []any{"48.0, 2.0"}, "speedMPS": 1.0},
		}},
		{name: "non-string point", command: travelCommand([]any{int64(1)}, nil)},
		// Travel requires at least one point; a single point is the minimal form.
		{name: "empty points", command: travelCommand([]any{}, nil)},
		{name: "negative speed", command: travelCommand([]any{"48.0, 2.0", "48.1, 2.0"}, &negative)},
		{name: "zero speed", command: travelCommand([]any{"48.0, 2.0", "48.1, 2.0"}, &zero)},
	} {
		t.Run(test.name, func(t *testing.T) {
			compiled, err := compileTravel(test.command)
			if compiled != nil || !isConfigurationError(err) {
				t.Fatalf("compileTravel() = %#v, %T %v; want nil and ConfigurationError", compiled, err, err)
			}
		})
	}
}

func TestTravelRejectsUnparseablePointsAfterInterpolation(t *testing.T) {
	t.Parallel()

	// Point text receives numeric validation at runtime before device movement.
	for _, point := range []string{"not-a-point", "48.0", "48.0, 2.0, 3.0", "48.0, east"} {
		t.Run(point, func(t *testing.T) {
			command := travelCommand([]any{point, "48.1, 2.0"}, nil)
			driver := travelDriver(nil)
			_, err := runSingleCommandFlow(t, driver, travelRegistry(t), command, "bad-point")
			if !isConfigurationError(err) {
				t.Fatalf("error = %T %v, want ConfigurationError", err, err)
			}
			if calls := travelLocations(driver); len(calls) != 0 {
				t.Fatalf("driver was called %d times before the route was validated", len(calls))
			}
		})
	}
}

func TestTravelPropagatesDriverFailure(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("set location boundary refused")
	command := travelCommand([]any{"48.0, 2.0", "48.1, 2.0"}, nil)
	_, err := runSingleCommandFlow(t, travelDriver(sentinel), travelRegistry(t), command, "failure")
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %T %v, want the exact driver cause", err, err)
	}
}

func travelRegistry(t testing.TB) handlerRegistry {
	t.Helper()
	registry, err := newHandlerRegistry(travelHandlerSpec())
	if err != nil {
		t.Fatalf("newHandlerRegistry(travel) error = %v", err)
	}
	return registry
}

func travelCommand(points []any, speed *float64) model.Command {
	arguments := map[string]any{"points": points}
	if speed != nil {
		arguments["speed"] = *speed
	}
	return model.Command{Kind: model.CommandTravel, Form: model.CommandFormObject, Arguments: arguments}
}

func travelDriver(failure error) *enginetest.FakeDriver {
	driver := enginetest.NewFakeDriver()
	results := make([]enginetest.Result[struct{}], 1+2*travelStepsPerSegment)
	for index := range results {
		results[index].Err = failure
	}
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{
			Platform: device.Platform("android"), WidthGrid: 400, HeightGrid: 884,
		}}},
		SetLocation: results,
	})
	return driver
}

func travelLocations(driver *enginetest.FakeDriver) []device.Location {
	var locations []device.Location
	for _, action := range driver.Actions() {
		if location, ok := action.Request.(device.Location); ok {
			locations = append(locations, location)
		}
	}
	return locations
}

// travelRecordingClock records every wait so the schedule can be asserted
// without spending real time.
type travelRecordingClock struct {
	now   time.Time
	waits []time.Duration
}

func (clock *travelRecordingClock) Now() time.Time { return clock.now }

func (clock *travelRecordingClock) Wait(_ context.Context, duration time.Duration) error {
	clock.waits = append(clock.waits, duration)
	clock.now = clock.now.Add(duration)
	return nil
}
