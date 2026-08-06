package device

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// safeRecordingBase matches a device-shell- and argv-safe recording filename:
// only [A-Za-z0-9._-] and never a leading '-'. A base outside this set could
// carry shell metacharacters into `adb shell screenrecord` (the device shell
// interprets them) or make `adb pull` / `simctl io` treat it as a flag.
var safeRecordingBase = regexp.MustCompile(`^[A-Za-z0-9._][A-Za-z0-9._-]*$`)

// ValidateRecordingSink guards the untrusted screen-recording output path
// before it reaches `adb shell screenrecord`, `adb pull`, or
// `xcrun simctl io recordVideo`. The recording sink is flow-controlled input,
// so it is validated at this trust boundary: the whole path must not start with
// '-' (argv injection) and the basename must be shell-safe (command injection
// via the on-device shell). It returns the safe basename for callers that build
// an on-device path (e.g. /sdcard/<base>) from it.
func ValidateRecordingSink(sink string) (base string, err error) {
	sink = strings.TrimSpace(sink)
	if sink == "" {
		return "", fmt.Errorf("screen recording requires an output path")
	}
	if strings.HasPrefix(sink, "-") {
		return "", fmt.Errorf("invalid recording output path %q: must not start with '-'", sink)
	}
	base = filepath.Base(sink)
	if !safeRecordingBase.MatchString(base) {
		return "", fmt.Errorf(
			"invalid recording filename %q: only letters, digits, '.', '_', '-' are allowed and it may not start with '-'", base)
	}
	return base, nil
}
