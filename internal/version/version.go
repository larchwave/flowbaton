// Package version owns the host binary's link-time version string.
package version

// Version is overridden by release builds. Development and snapshot builds
// remain explicit instead of pretending to be a tagged release.
var Version = "dev"

// Line is the stable one-line version response used by the CLI.
func Line() string {
	return "flowbaton " + Version
}
