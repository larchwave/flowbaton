package capability

import "github.com/nohavewho/flowbaton/internal/model"

var configExtensionsV0 = []string{
	"url",
	"jsEngine=graaljs",
	"androidWebViewHierarchy=accessibility",
	"androidWebViewHierarchy=devtools",
}

var cliSubcommandsV0 = []string{
	"test",
	"cloud",
	"record",
	"hierarchy",
	"query",
	"download-samples",
	"login",
	"logout",
	"bugreport",
	"studio",
	"start-device",
	"list-devices",
	"list-cloud-devices",
	"generate-completion",
	"chat",
	"check-syntax",
	"driver-setup",
	"mcp",
}

var cliFlagsV0 = []string{
	"global.-v",
	"global.--version",
	"global.-p",
	"global.--platform",
	"global.--host",
	"global.--port",
	"global.--driver-host-port",
	"global.--device",
	"global.--udid",
	"global.--verbose",
	"test.--config",
	"test.--shard-split",
	"test.--shard-all",
	"test.-c",
	"test.--continuous",
	"test.-e",
	"test.--env",
	"test.--format",
	"test.--test-suite-name",
	"test.--output",
	"test.--debug-output",
	"test.--test-output-dir",
	"test.--flatten-debug-output",
	"test.--include-tags",
	"test.--exclude-tags",
	"test.--headless",
	"test.--screen-size",
	"test.--analyze",
	"test.--api-url",
	"test.--api-key",
	"test.--reinstall-driver",
	"test.--no-reinstall-driver",
	"test.--apple-team-id",
	"test.-p",
	"test.--platform",
	"test.--device",
	"record.--local",
	"hierarchy.--target",
	"hierarchy.--target=devtools",
	"hierarchy.--csv",
	"query.--text",
	"query.--id",
	"start-device.--platform",
	"start-device.--os-version",
	"start-device.--device-locale",
	"start-device.--force-create",
	"driver-setup.--apple-team-id",
	"mcp.--base-dir",
	"mcp.--no-viewer",
	"mcp.--viewer-port",
}

var hostTargetsV0 = []string{
	"darwin-arm64",
	"darwin-amd64",
	"linux-amd64",
	"windows-amd64",
	"linux-arm64",
	"windows-arm64",
}

var devicePlatformsV0 = []string{
	"android-emulator",
	"android-physical",
	"ios-simulator",
	"ios-physical",
	"web",
}

func requiredRegistryKeysV0() map[string]struct{} {
	result := make(map[string]struct{})
	for _, keyword := range model.CommandKeywords() {
		result[entryKey(FeatureCommand, string(keyword))] = struct{}{}
	}
	for _, name := range model.ContractV0().SelectorFields {
		result[entryKey(FeatureSelector, name)] = struct{}{}
	}
	for _, name := range configExtensionsV0 {
		result[entryKey(FeatureConfigExtension, name)] = struct{}{}
	}
	for _, name := range cliSubcommandsV0 {
		result[entryKey(FeatureCLISubcommand, name)] = struct{}{}
	}
	for _, name := range cliFlagsV0 {
		result[entryKey(FeatureCLIFlag, name)] = struct{}{}
	}
	for _, name := range hostTargetsV0 {
		result[entryKey(FeatureHostTarget, name)] = struct{}{}
	}
	for _, name := range devicePlatformsV0 {
		result[entryKey(FeatureDevicePlatform, name)] = struct{}{}
	}
	return result
}

func defaultEntriesV0() []Entry {
	entries := make([]Entry, 0, len(requiredRegistryKeysV0()))
	for _, keyword := range model.CommandKeywords() {
		entry := Entry{
			Kind:          FeatureCommand,
			Name:          string(keyword),
			ParseStatus:   ParseStatusParseable,
			RuntimeStatus: RuntimeStatusPlannedV1,
			Platforms:     []string{"android", "ios-simulator"},
			Reason:        "declared FlowBaton v1 command",
		}
		switch keyword {
		case model.CommandAssertNoDefectsWithAI, model.CommandAssertWithAI, model.CommandExtractTextWithAI:
			entry.Reason = "AI command executes against an injected provider; fails closed without one"
		case model.CommandClearKeychain:
			entry.Platforms = []string{"ios-simulator"}
			entry.Reason = "keychain state applies to the iOS simulator surface"
		case model.CommandSetAirplaneMode, model.CommandToggleAirplaneMode:
			entry.Platforms = []string{"android"}
			entry.Reason = "airplane-mode automation is declared for Android v1"
		}
		entries = append(entries, entry)
	}

	for _, name := range model.ContractV0().SelectorFields {
		entry := Entry{
			Kind: FeatureSelector, Name: name, ParseStatus: ParseStatusParseable,
			RuntimeStatus: RuntimeStatusPlannedV1,
			Platforms:     []string{"android", "ios-simulator"},
			Reason:        "declared FlowBaton v1 selector feature",
		}
		if name == "css" {
			// Implemented, but only the Web/CDP driver can resolve a css query.
			// platform-limited is the honest status: the registry's runtime
			// check is platform-blind, so the driver capability check in the
			// element lookup is what refuses css on a mobile driver.
			entry.RuntimeStatus = RuntimeStatusPlatformLimited
			entry.Platforms = []string{"web"}
			entry.Reason = "CSS selection is resolved by the Web/CDP driver and is web-only"
		}
		entries = append(entries, entry)
	}

	entries = append(entries,
		Entry{FeatureConfigExtension, "url", ParseStatusParseable, RuntimeStatusPlatformLimited, []string{"web"}, "Web flow targeting is served by the Web/CDP driver and is web-only"},
		Entry{FeatureConfigExtension, "jsEngine=graaljs", ParseStatusParseable, RuntimeStatusPlannedV1, []string{"android", "ios-simulator"}, "FlowBaton v1 maps the documented GraalJS token to goja"},
		Entry{FeatureConfigExtension, "androidWebViewHierarchy=accessibility", ParseStatusParseable, RuntimeStatusPlatformLimited, []string{"android"}, "accessibility hierarchy is Android-only"},
		Entry{FeatureConfigExtension, "androidWebViewHierarchy=devtools", ParseStatusParseable, RuntimeStatusPlatformLimited, []string{"android"}, "the Chrome DevTools WebView merge rides the Android abstract CDP socket and is Android-only"},
	)

	for _, name := range cliSubcommandsV0 {
		entries = append(entries, cliSubcommandEntry(name))
	}
	for _, name := range cliFlagsV0 {
		entries = append(entries, cliFlagEntry(name))
	}
	entries = append(entries,
		Entry{FeatureHostTarget, "darwin-arm64", ParseStatusParseable, RuntimeStatusPlannedV1, []string{"android", "ios-simulator"}, "v1 GA-gated host"},
		Entry{FeatureHostTarget, "darwin-amd64", ParseStatusParseable, RuntimeStatusPlannedV1, []string{"android", "ios-simulator"}, "v1 GA-gated host"},
		Entry{FeatureHostTarget, "linux-amd64", ParseStatusParseable, RuntimeStatusPlannedV1, []string{"android"}, "v1 GA-gated host"},
		Entry{FeatureHostTarget, "windows-amd64", ParseStatusParseable, RuntimeStatusPlannedV1, []string{"android"}, "v1 GA-gated host"},
		Entry{FeatureHostTarget, "linux-arm64", ParseStatusParseable, RuntimeStatusDeferred, []string{"android"}, "build-only experimental host is post-v1 GA"},
		Entry{FeatureHostTarget, "windows-arm64", ParseStatusParseable, RuntimeStatusDeferred, []string{"android"}, "build-only experimental host is post-v1 GA"},
		Entry{FeatureDevicePlatform, "android-emulator", ParseStatusParseable, RuntimeStatusPlannedV1, []string{"android"}, "v1 device surface"},
		Entry{FeatureDevicePlatform, "android-physical", ParseStatusParseable, RuntimeStatusPlannedV1, []string{"android"}, "v1 device surface"},
		Entry{FeatureDevicePlatform, "ios-simulator", ParseStatusParseable, RuntimeStatusPlannedV1, []string{"ios-simulator"}, "v1 device surface"},
		Entry{FeatureDevicePlatform, "ios-physical", ParseStatusParseable, RuntimeStatusDeferred, []string{"ios-physical"}, "physical iOS automation is post-v1"},
		Entry{FeatureDevicePlatform, "web", ParseStatusParseable, RuntimeStatusPlannedV1, []string{"web"}, "Web/CDP execution surface"},
	)
	return entries
}

func cliSubcommandEntry(name string) Entry {
	entry := Entry{
		Kind: FeatureCLISubcommand, Name: name, ParseStatus: ParseStatusParseable,
		RuntimeStatus: RuntimeStatusPlannedV1, Platforms: []string{"all-hosts"},
		Reason: "declared FlowBaton v1 CLI command",
	}
	switch name {
	case "cloud", "download-samples", "login", "logout", "bugreport", "studio", "list-cloud-devices":
		entry.ParseStatus = ParseStatusOmitted
		entry.RuntimeStatus = RuntimeStatusExcluded
		entry.Reason = "not part of the approved local v1 product surface"
	case "chat", "mcp":
		entry.RuntimeStatus = RuntimeStatusDeferred
		entry.Reason = "post-v1 AI/MCP surface"
	}
	return entry
}

func cliFlagEntry(name string) Entry {
	entry := Entry{
		Kind: FeatureCLIFlag, Name: name, ParseStatus: ParseStatusParseable,
		RuntimeStatus: RuntimeStatusPlannedV1, Platforms: []string{"all-hosts"},
		Reason: "declared FlowBaton v1 CLI flag",
	}
	switch name {
	case "test.--headless", "test.--screen-size", "test.--analyze", "test.--api-url", "test.--api-key", "hierarchy.--target=devtools", "mcp.--base-dir", "mcp.--no-viewer", "mcp.--viewer-port":
		entry.RuntimeStatus = RuntimeStatusDeferred
		entry.Reason = "flag belongs to a deferred Web, AI, devtools, or MCP surface"
	}
	return entry
}
