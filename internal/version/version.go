// Package version owns the host binary's release version string.
package version

import (
	"regexp"
	"runtime/debug"
	"strings"
)

const modulePath = "github.com/larchwave/flowbaton"

var taggedModuleVersion = regexp.MustCompile(
	`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`,
)
var pseudoModuleVersion = regexp.MustCompile(`[.-][0-9]{14}-[0-9a-f]{12,}(?:\+[0-9A-Za-z.-]+)?$`)

// Version is overridden by release builds. Development and snapshot builds
// remain explicit instead of pretending to be a tagged release.
var Version = "dev"

func init() {
	if Version != "dev" {
		return
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	if detected := moduleReleaseVersion(info.Main); detected != "" {
		Version = detected
	}
}

func moduleReleaseVersion(module debug.Module) string {
	if module.Path != modulePath || module.Replace != nil ||
		!taggedModuleVersion.MatchString(module.Version) || pseudoModuleVersion.MatchString(module.Version) {
		return ""
	}
	return strings.TrimPrefix(module.Version, "v")
}

// Line is the stable one-line version response used by the CLI.
func Line() string {
	return "flowbaton " + Version
}
