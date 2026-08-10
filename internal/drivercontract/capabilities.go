// Package drivercontract owns the immutable, side-effect-free capability
// documents shared by selected-platform preflight and production drivers.
package drivercontract

import "github.com/larchwave/flowbaton/internal/model"

const (
	PlatformAndroid      = "android"
	PlatformIOSSimulator = "ios-simulator"
	PlatformWeb          = "web"
)

const commandFeaturePrefix = "command:"
const commandValueFeaturePrefix = "command-value:"

// Document is one production driver's capability contract. Features returns
// a fresh map so callers cannot mutate the process-wide source of truth.
type Document struct {
	Platform string
	features map[string]bool
}

func CommandFeature(command string) string {
	return commandFeaturePrefix + command
}

func CommandValueFeature(command, value string) string {
	return commandValueFeaturePrefix + command + ":" + value
}

func (document Document) Features() map[string]bool {
	features := make(map[string]bool, len(document.features))
	for name, supported := range document.features {
		features[name] = supported
	}
	return features
}

func (document Document) SupportsCommand(command model.CommandKeyword) bool {
	return document.features[CommandFeature(string(command))]
}

func (document Document) SupportsCommandValue(command model.CommandKeyword, value string) bool {
	return document.features[CommandValueFeature(string(command), value)]
}

// Documents returns the exact capability documents consumed by the Android,
// iOS Simulator, and Web production drivers.
func Documents() []Document {
	return []Document{androidDocument(), iosSimulatorDocument(), webDocument()}
}

func Android() Document      { return androidDocument() }
func IOSSimulator() Document { return iosSimulatorDocument() }
func Web() Document          { return webDocument() }

func androidDocument() Document {
	features := map[string]bool{
		"proxy":                 true,
		"airplaneMode":          true,
		"backPress":             true,
		"browserChoice":         true,
		"androidChromeDevTools": true,
		"screenRecording":       true,
		"onDeviceQuery":         false,
		"deviceLogCapture":      true,
		"crashArtifacts":        true,
	}
	addCommands(features, nil)
	addCommandValues(features, model.CommandPressKey,
		"ENTER", "BACK", "HOME", "LOCK", "VOLUME_UP", "VOLUME_DOWN", "POWER")
	return Document{Platform: PlatformAndroid, features: features}
}

func iosSimulatorDocument() Document {
	features := map[string]bool{
		"proxy":                 false,
		"airplaneMode":          false,
		"androidChromeDevTools": false,
		"screenRecording":       true,
		"onDeviceQuery":         false,
		"deviceLogCapture":      true,
		"crashArtifacts":        true,
		"browserChoice":         false,
		"backPress":             false,
	}
	addCommands(features, map[model.CommandKeyword]bool{
		model.CommandBack:               true,
		model.CommandOpenBrowser:        true,
		model.CommandSetAirplaneMode:    true,
		model.CommandToggleAirplaneMode: true,
	})
	addCommandValues(features, model.CommandPressKey, "ENTER", "BACKSPACE", "TAB")
	return Document{Platform: PlatformIOSSimulator, features: features}
}

func webDocument() Document {
	features := map[string]bool{
		"cssSelector":           true,
		"backPress":             true,
		"browserChoice":         true,
		"appLifecycle":          false,
		"proxy":                 false,
		"airplaneMode":          false,
		"androidChromeDevTools": false,
		"screenRecording":       false,
		"onDeviceQuery":         true,
		"deviceLogCapture":      false,
		"crashArtifacts":        false,
	}
	addCommands(features, map[model.CommandKeyword]bool{
		model.CommandKillApp:            true,
		model.CommandClearState:         true,
		model.CommandClearKeychain:      true,
		model.CommandSetPermissions:     true,
		model.CommandSetOrientation:     true,
		model.CommandStartRecording:     true,
		model.CommandStopRecording:      true,
		model.CommandAddMedia:           true,
		model.CommandSetAirplaneMode:    true,
		model.CommandToggleAirplaneMode: true,
	})
	addCommandValues(features, model.CommandPressKey, "ENTER", "BACKSPACE", "TAB")
	return Document{Platform: PlatformWeb, features: features}
}

func addCommands(features map[string]bool, unsupported map[model.CommandKeyword]bool) {
	for _, command := range model.CommandKeywords() {
		if !unsupported[command] {
			features[CommandFeature(string(command))] = true
		}
	}
}

func addCommandValues(features map[string]bool, command model.CommandKeyword, values ...string) {
	for _, value := range values {
		features[CommandValueFeature(string(command), value)] = true
	}
}
