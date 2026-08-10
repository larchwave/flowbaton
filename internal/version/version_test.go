package version

import (
	"runtime/debug"
	"testing"
)

func TestLineReportsDevelopmentBuildHonestly(t *testing.T) {
	if got, want := Line(), "flowbaton dev"; got != want {
		t.Fatalf("Line() = %q, want %q", got, want)
	}
}

func TestModuleReleaseVersionAcceptsTaggedInstall(t *testing.T) {
	for _, test := range []struct {
		name    string
		module  debug.Module
		version string
	}{
		{name: "release", module: debug.Module{Path: modulePath, Version: "v1.2.3"}, version: "1.2.3"},
		{name: "prerelease", module: debug.Module{Path: modulePath, Version: "v1.2.3-rc.1"}, version: "1.2.3-rc.1"},
		{name: "build metadata", module: debug.Module{Path: modulePath, Version: "v1.2.3+build.4"}, version: "1.2.3+build.4"},
		{name: "development", module: debug.Module{Path: modulePath, Version: "(devel)"}},
		{name: "pseudo version", module: debug.Module{Path: modulePath, Version: "v0.0.0-20260810120000-abcdefabcdef"}},
		{name: "dirty pseudo version", module: debug.Module{Path: modulePath, Version: "v0.1.2-0.20260810120000-abcdefabcdef+dirty"}},
		{name: "leading zero", module: debug.Module{Path: modulePath, Version: "v01.2.3"}},
		{name: "other module", module: debug.Module{Path: "example.com/flowbaton", Version: "v1.2.3"}},
		{name: "local replacement", module: debug.Module{Path: modulePath, Version: "v1.2.3", Replace: &debug.Module{Path: "../flowbaton"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := moduleReleaseVersion(test.module); got != test.version {
				t.Fatalf("moduleReleaseVersion(%+v) = %q, want %q", test.module, got, test.version)
			}
		})
	}
}
