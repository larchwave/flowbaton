package model

import (
	"slices"
	"strings"
)

// PressKeyCodes is the supported pressKey set in canonical UPPER_SNAKE form.
// It includes standard device keys and Android TV remote keys. The parser and
// compiler normalize authored values through NormalizePressKey before lookup.
// The order keeps parser diagnostics deterministic.
var PressKeyCodes = []string{
	"HOME", "LOCK", "ENTER", "BACKSPACE", "BACK", "POWER", "TAB",
	"VOLUME_UP", "VOLUME_DOWN",
	"REMOTE_DPAD_UP", "REMOTE_DPAD_DOWN", "REMOTE_DPAD_LEFT", "REMOTE_DPAD_RIGHT", "REMOTE_DPAD_CENTER",
	"REMOTE_MEDIA_PLAY_PAUSE", "REMOTE_MEDIA_STOP", "REMOTE_MEDIA_NEXT", "REMOTE_MEDIA_PREVIOUS",
	"REMOTE_MEDIA_REWIND", "REMOTE_MEDIA_FAST_FORWARD",
	"REMOTE_SYSTEM_NAVIGATION_UP", "REMOTE_SYSTEM_NAVIGATION_DOWN",
	"REMOTE_BUTTON_A", "REMOTE_BUTTON_B", "REMOTE_MENU",
	"TV_INPUT", "TV_INPUT_HDMI_1", "TV_INPUT_HDMI_2", "TV_INPUT_HDMI_3",
}

// NormalizePressKey folds an authored pressKey value to its canonical form.
// Key names are case-insensitive, with spaces and underscores interchangeable.
func NormalizePressKey(authored string) string {
	return strings.ReplaceAll(strings.ToUpper(authored), " ", "_")
}

// PressKeyCanonical returns the canonical KeyCode string for an authored value
// and whether it is a supported key.
func PressKeyCanonical(authored string) (string, bool) {
	canonical := NormalizePressKey(authored)
	return canonical, slices.Contains(PressKeyCodes, canonical)
}
