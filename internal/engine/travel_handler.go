package engine

import (
	"context"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/model"
)

// specs/05-command-semantics-addendum.md §2 defines the contract. §2.1 fixes
// the authored shape ("latitude, longitude" strings plus a metres-per-second
// speed), and §2.3 fixes the emission schedule.
const (
	// travelStepsPerSegment is the number of emissions per segment, excluding
	// the path's first point. spec 05 §2.3.
	travelStepsPerSegment = 50
	// travelDefaultSpeed is what omitting `speed` behaves like, in m/s.
	travelDefaultSpeed = 4.0
	// travelSpeedFactor converts authored speed to the delay defined by spec 05 §2.3.
	travelSpeedFactor = math.Pi / 180
	// earthRadiusMetres is the mean radius used for the great-circle distance
	// that feeds the per-step delay.
	earthRadiusMetres = 6_371_000.0
)

type travelCompiled struct {
	// points stays authored (uninterpolated) so evaluation is the only phase
	// that resolves them, and re-evaluation always starts from the source text.
	points []string
	speed  float64
}

type travelEvaluated struct {
	route []device.Location
	speed float64
}

func travelHandlerSpec() handlerSpec {
	return handlerSpec{
		keyword: model.CommandTravel, effectClass: EffectDeviceMutation,
		postAction: postActionNoSettle,
		compile:    pureCompiler(compileTravel), evaluate: evaluateTravel, execute: executeTravel,
	}
}

func compileTravel(command model.Command) (any, error) {
	if command.Kind != model.CommandTravel {
		return nil, NewConfigurationError("travel keyword is invalid", nil)
	}
	if err := rejectCommandEnvelope(command); err != nil {
		return nil, err
	}
	object, err := decodeObject(command)
	if err != nil {
		return nil, err
	}
	if err := object.rejectUnknown("points", "speed"); err != nil {
		return nil, err
	}
	authored, exists := object.raw("points")
	if !exists {
		return nil, commandDecodeError(command.Kind, "requires points")
	}
	list, ok := authored.([]any)
	if !ok {
		return nil, object.fieldError("points", "must be an array")
	}
	// The point strings may interpolate, so compilation only pins their type
	// and the route's shape; their contents are resolved in evaluation.
	points := make([]string, len(list))
	for index, item := range list {
		if points[index], ok = item.(string); !ok {
			return nil, object.fieldError("points", "must contain only \"latitude, longitude\" strings")
		}
	}
	// One point is a route: the manifest declares it as travel's minimal
	// authored form, and the schedule of spec 05 section 2.3 handles it as
	// 1 + 50*(P-1) = one emission with no segment to walk.
	if len(points) == 0 {
		return nil, commandDecodeError(command.Kind, "requires at least one point")
	}
	speed := travelDefaultSpeed
	authoredSpeed, exists, err := object.optionalNumber("speed")
	if err != nil {
		return nil, err
	}
	if exists {
		if authoredSpeed <= 0 {
			return nil, object.fieldError("speed", "must be greater than zero")
		}
		speed = authoredSpeed
	}
	return travelCompiled{points: points, speed: speed}, nil
}

func evaluateTravel(
	ctx context.Context,
	evaluation evaluationContext,
	command model.Command,
	compiled any,
) (evaluatedDispatch, error) {
	payload, ok := compiled.(travelCompiled)
	if !ok {
		return evaluatedDispatch{}, NewConfigurationError("travel received an invalid compiled payload", nil)
	}
	route := make([]device.Location, len(payload.points))
	for index, authored := range payload.points {
		point, err := evaluation.Interpolate(ctx, authored, nil)
		if err != nil {
			return evaluatedDispatch{}, err
		}
		// Parsing before any emission means an unusable route fails without
		// having already moved the device partway along it.
		if route[index], err = parseTravelPoint(point); err != nil {
			return evaluatedDispatch{}, err
		}
	}
	return evaluatedDispatch{
		command: cloneCommand(command),
		value:   travelEvaluated{route: route, speed: payload.speed},
	}, nil
}

// parseTravelPoint resolves the runtime "latitude, longitude" form. Syntax
// validation remains shallow as specified by spec 05 §2.2.
func parseTravelPoint(authored string) (device.Location, error) {
	axes := strings.Split(authored, ",")
	if len(axes) != 2 {
		return device.Location{}, NewConfigurationError(
			"command travel point must be a \"latitude, longitude\" pair", nil)
	}
	latitude, err := strconv.ParseFloat(strings.TrimSpace(axes[0]), 64)
	if err != nil {
		return device.Location{}, NewConfigurationError("command travel latitude is not a number", err)
	}
	longitude, err := strconv.ParseFloat(strings.TrimSpace(axes[1]), 64)
	if err != nil {
		return device.Location{}, NewConfigurationError("command travel longitude is not a number", err)
	}
	return device.Location{Latitude: latitude, Longitude: longitude}, nil
}

func executeTravel(ctx context.Context, state *executionState, evaluated evaluatedDispatch) (commandEffect, error) {
	effect := commandEffect{effectClass: EffectDeviceMutation}
	payload, ok := evaluated.value.(travelEvaluated)
	if !ok {
		return effect, NewConfigurationError("travel received an invalid evaluated payload", nil)
	}
	if err := ctx.Err(); err != nil {
		return effect, err
	}
	driver := state.dependencies.Driver
	if err := driver.SetLocation(ctx, payload.route[0]); err != nil {
		return effect, err
	}
	for index := 0; index+1 < len(payload.route); index++ {
		start, end := payload.route[index], payload.route[index+1]
		delay := travelStepDelay(start, end, payload.speed)
		for step := 1; step <= travelStepsPerSegment; step++ {
			if err := ctx.Err(); err != nil {
				return effect, err
			}
			if err := driver.SetLocation(ctx, travelStepLocation(start, end, step)); err != nil {
				return effect, err
			}
			if err := state.dependencies.Clock.Wait(ctx, delay); err != nil {
				return effect, err
			}
		}
	}
	return effect, nil
}

// travelStepLocation interpolates each axis independently. The final step
// returns the authored end point rather than fifty accumulated additions, so a
// junction is emitted exactly once and lands exactly where it was authored.
func travelStepLocation(start, end device.Location, step int) device.Location {
	if step == travelStepsPerSegment {
		return end
	}
	fraction := float64(step) / travelStepsPerSegment
	return device.Location{
		Latitude:  start.Latitude + (end.Latitude-start.Latitude)*fraction,
		Longitude: start.Longitude + (end.Longitude-start.Longitude)*fraction,
	}
}

func travelStepDelay(start, end device.Location, speed float64) time.Duration {
	stepMetres := haversineMetres(start, end) / travelStepsPerSegment
	return time.Duration(stepMetres * travelSpeedFactor / speed * float64(time.Second))
}

func haversineMetres(start, end device.Location) float64 {
	latitudeDelta := radians(end.Latitude - start.Latitude)
	longitudeDelta := radians(end.Longitude - start.Longitude)
	chord := math.Sin(latitudeDelta/2)*math.Sin(latitudeDelta/2) +
		math.Cos(radians(start.Latitude))*math.Cos(radians(end.Latitude))*
			math.Sin(longitudeDelta/2)*math.Sin(longitudeDelta/2)
	return earthRadiusMetres * 2 * math.Atan2(math.Sqrt(chord), math.Sqrt(1-chord))
}

func radians(degrees float64) float64 { return degrees * math.Pi / 180 }
