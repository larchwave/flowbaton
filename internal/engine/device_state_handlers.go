package engine

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/model"
)

// Device-state handlers map location, orientation, permission, and airplane
// mode commands to driver operations.

type deviceStateCompiled struct {
	keyword model.CommandKeyword
	// setLocation coordinates retain scalar text until runtime interpolation and
	// numeric validation.
	latitudeExpression  string
	longitudeExpression string
	location            device.Location
	orientation         device.Orientation
	airplaneOn          bool
	permissions         map[string]string
}

type deviceStateEvaluated struct {
	keyword     model.CommandKeyword
	location    device.Location
	orientation device.Orientation
	airplaneOn  bool
	permissions map[string]string
	appID       string
}

func deviceStateHandlerSpecs() []handlerSpec {
	keywords := []model.CommandKeyword{
		model.CommandSetLocation,
		model.CommandSetOrientation,
		model.CommandSetPermissions,
		model.CommandSetAirplaneMode,
		model.CommandToggleAirplaneMode,
	}
	specs := make([]handlerSpec, len(keywords))
	for index, keyword := range keywords {
		specs[index] = handlerSpec{
			keyword: keyword, effectClass: EffectDeviceMutation,
			postAction: postActionNoSettle,
			compile:    pureCompiler(compileDeviceState), evaluate: evaluateDeviceState, execute: executeDeviceState,
		}
	}
	return specs
}

func canonicalOrientation(authored string) (device.Orientation, error) {
	// Normalize the case-insensitive orientation to the driver's uppercase form.
	normalized := strings.ToUpper(authored)
	switch normalized {
	case "PORTRAIT", "LANDSCAPE_LEFT", "LANDSCAPE_RIGHT", "UPSIDE_DOWN":
		return device.Orientation(normalized), nil
	default:
		return "", NewConfigurationError("command setOrientation value is not in the exact supported set", nil)
	}
}

func canonicalAirplaneMode(authored string) (bool, error) {
	switch authored {
	case "enabled":
		return true, nil
	case "disabled":
		return false, nil
	default:
		return false, NewConfigurationError("command setAirplaneMode value must be enabled or disabled", nil)
	}
}

func compileDeviceState(command model.Command) (any, error) {
	if err := rejectCommandEnvelope(command); err != nil {
		return nil, err
	}
	payload := deviceStateCompiled{keyword: command.Kind}
	switch command.Kind {
	case model.CommandToggleAirplaneMode:
		if err := decodeNoArguments(command); err != nil {
			return nil, err
		}
		return payload, nil

	case model.CommandSetOrientation:
		authored, err := decodeString(command)
		if err != nil {
			return nil, err
		}
		if payload.orientation, err = canonicalOrientation(authored); err != nil {
			return nil, err
		}
		return payload, nil

	case model.CommandSetAirplaneMode:
		authored, err := decodeString(command)
		if err != nil {
			return nil, err
		}
		if payload.airplaneOn, err = canonicalAirplaneMode(authored); err != nil {
			return nil, err
		}
		return payload, nil

	case model.CommandSetLocation:
		object, err := decodeObject(command)
		if err != nil {
			return nil, err
		}
		if err := object.rejectUnknown("latitude", "longitude"); err != nil {
			return nil, err
		}
		if payload.latitudeExpression, err = decodeCoordinate(object, command.Kind, "latitude"); err != nil {
			return nil, err
		}
		if payload.longitudeExpression, err = decodeCoordinate(object, command.Kind, "longitude"); err != nil {
			return nil, err
		}
		return payload, nil

	case model.CommandSetPermissions:
		object, err := decodeObject(command)
		if err != nil {
			return nil, err
		}
		// The required `permissions` key uses the shape documented by
		// 04-wire-protocols.md:54. launchApp reuses the same grant decoder.
		authored, exists := object.raw("permissions")
		if !exists {
			return nil, commandDecodeError(command.Kind, "requires field permissions")
		}
		fields, ok := authored.(map[string]any)
		if !ok {
			return nil, object.fieldError("permissions", "must be an object")
		}
		permissions, err := decodePermissionGrants(command.Kind, decodedObject{command: command.Kind, fields: fields})
		if err != nil {
			return nil, err
		}
		payload.permissions = permissions
		return payload, nil

	default:
		return nil, NewConfigurationError("device state keyword is invalid", nil)
	}
}

// decodePermissionGrants validates every authored permission against the exact
// grant set and returns an owned copy, so the authored command can never be
// reached through the driver request.
func decodePermissionGrants(keyword model.CommandKeyword, object decodedObject) (map[string]string, error) {
	names := make([]string, 0, len(object.fields))
	for name := range object.fields {
		names = append(names, name)
	}
	if len(names) == 0 {
		return nil, commandDecodeError(keyword, "requires at least one permission")
	}
	sort.Strings(names)
	grants := make(map[string]string, len(names))
	for _, name := range names {
		grant, _, err := object.optionalString(name)
		if err != nil {
			return nil, err
		}
		// Normalize case-insensitive grants to the driver's lowercase form.
		canonical := strings.ToLower(grant)
		switch canonical {
		case "allow", "deny", "unset":
		default:
			return nil, NewConfigurationError(
				fmt.Sprintf("command %s permission %s must be allow, deny, or unset", keyword, name), nil)
		}
		grants[name] = canonical
	}
	return grants, nil
}

// decodeCoordinate reads one setLocation coordinate as the text it was
// authored as. A YAML number is rendered rather than parsed so that both forms
// take the same path to the same parse in evaluateDeviceState -- one place that
// can reject a coordinate, instead of two that can disagree.
func decodeCoordinate(object decodedObject, keyword model.CommandKeyword, field string) (string, error) {
	raw, exists := object.raw(field)
	if !exists {
		return "", commandDecodeError(keyword, "requires "+field)
	}
	switch value := raw.(type) {
	case string:
		return value, nil
	case int64:
		return strconv.FormatInt(value, 10), nil
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64), nil
	default:
		return "", object.fieldError(field, "must be a number or a string")
	}
}

// evaluateCoordinate interpolates scalar coordinate text and performs runtime
// numeric validation.
func evaluateCoordinate(
	ctx context.Context,
	evaluation evaluationContext,
	keyword model.CommandKeyword,
	field string,
	expression string,
) (float64, error) {
	interpolated, err := evaluation.Interpolate(ctx, expression, nil)
	if err != nil {
		return 0, err
	}
	parsed, err := strconv.ParseFloat(strings.TrimSpace(interpolated), 64)
	if err != nil {
		return 0, NewConfigurationError(fmt.Sprintf(
			"command %s %s must be a number, got %q", keyword, field, interpolated), nil)
	}
	return parsed, nil
}

func evaluateDeviceState(
	ctx context.Context,
	evaluation evaluationContext,
	command model.Command,
	compiled any,
) (evaluatedDispatch, error) {
	payload, ok := compiled.(deviceStateCompiled)
	if !ok || payload.keyword != command.Kind {
		return evaluatedDispatch{}, NewConfigurationError(
			fmt.Sprintf("%s received an invalid compiled payload", command.Kind), nil)
	}
	evaluated := evaluatedDispatch{command: cloneCommand(command), value: deviceStateEvaluated{}}
	value := deviceStateEvaluated{
		keyword:     payload.keyword,
		location:    payload.location,
		orientation: payload.orientation,
		airplaneOn:  payload.airplaneOn,
	}
	if payload.keyword == model.CommandSetLocation {
		latitude, err := evaluateCoordinate(ctx, evaluation, command.Kind, "latitude", payload.latitudeExpression)
		if err != nil {
			return evaluated, err
		}
		longitude, err := evaluateCoordinate(ctx, evaluation, command.Kind, "longitude", payload.longitudeExpression)
		if err != nil {
			return evaluated, err
		}
		value.location = device.Location{Latitude: latitude, Longitude: longitude}
	}
	if payload.keyword == model.CommandSetPermissions {
		// setPermissions targets the active app, and the grant map is copied so
		// the driver request never aliases compiled state.
		appID, err := evaluatedActiveAppID(ctx, evaluation, command.Kind)
		if err != nil {
			return evaluated, err
		}
		value.appID = appID
		value.permissions = make(map[string]string, len(payload.permissions))
		for name, grant := range payload.permissions {
			value.permissions[name] = grant
		}
	}
	evaluated.value = value
	return evaluated, nil
}

func executeDeviceState(ctx context.Context, state *executionState, evaluated evaluatedDispatch) (commandEffect, error) {
	effect := commandEffect{effectClass: EffectDeviceMutation}
	payload, ok := evaluated.value.(deviceStateEvaluated)
	if !ok {
		return effect, NewConfigurationError("device state command received an invalid evaluated payload", nil)
	}
	if err := ctx.Err(); err != nil {
		return effect, err
	}
	driver := state.dependencies.Driver
	var err error
	switch payload.keyword {
	case model.CommandSetLocation:
		err = driver.SetLocation(ctx, payload.location)
	case model.CommandSetOrientation:
		err = driver.SetOrientation(ctx, payload.orientation)
	case model.CommandSetAirplaneMode:
		err = driver.SetAirplaneMode(ctx, device.AirplaneModeRequest{Enabled: payload.airplaneOn})
	case model.CommandSetPermissions:
		err = driver.SetPermissions(ctx, device.PermissionsRequest{
			AppID: payload.appID, Permissions: payload.permissions,
		})
	case model.CommandToggleAirplaneMode:
		// Toggle reads the current state before writing its inverse, so the
		// read failure surfaces rather than being masked by a blind write.
		var current bool
		if current, err = driver.IsAirplaneModeEnabled(ctx); err != nil {
			return effect, err
		}
		if err = ctx.Err(); err != nil {
			return effect, err
		}
		err = driver.SetAirplaneMode(ctx, device.AirplaneModeRequest{Enabled: !current})
	default:
		return effect, NewConfigurationError("device state command keyword is invalid", nil)
	}
	if err != nil {
		return effect, err
	}
	return effect, nil
}
