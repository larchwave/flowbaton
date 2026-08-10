package capability

//go:generate go run ./cmd/export-registry -output ../../contracts/v0/support-registry.json

import (
	"github.com/larchwave/flowbaton/internal/drivercontract"
	"github.com/larchwave/flowbaton/internal/model"
)

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
	"serve",
	"db",
	"auth",
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
	"test.--api-url",
	"test.--api-key",
	"test.--reinstall-driver",
	"test.--no-reinstall-driver",
	"test.-p",
	"test.--platform",
	"test.--device",
	"test.--udid",
	"record.--local",
	"record.--config",
	"record.--shard-split",
	"record.--shard-all",
	"record.-c",
	"record.--continuous",
	"record.-e",
	"record.--env",
	"record.--format",
	"record.--test-suite-name",
	"record.--output",
	"record.--debug-output",
	"record.--test-output-dir",
	"record.--flatten-debug-output",
	"record.--include-tags",
	"record.--exclude-tags",
	"record.--headless",
	"record.--screen-size",
	"record.--api-url",
	"record.--api-key",
	"record.--reinstall-driver",
	"record.--no-reinstall-driver",
	"record.-p",
	"record.--platform",
	"record.--device",
	"record.--udid",
	"list-devices.-p",
	"list-devices.--platform",
	"hierarchy.-p",
	"hierarchy.--platform",
	"hierarchy.--device",
	"hierarchy.--udid",
	"hierarchy.--app-id",
	"hierarchy.--target",
	"hierarchy.--target=devtools",
	"hierarchy.--csv",
	"hierarchy.--compact",
	"query.-p",
	"query.--platform",
	"query.--device",
	"query.--udid",
	"query.--app-id",
	"query.--text",
	"query.--id",
	"bugreport.-p",
	"bugreport.--platform",
	"bugreport.--device",
	"bugreport.--udid",
	"bugreport.--output",
	"bugreport.-o",
	"start-device.-p",
	"start-device.--platform",
	"start-device.--device",
	"start-device.--udid",
	"start-device.--os-version",
	"start-device.--device-locale",
	"start-device.--device-model",
	"start-device.--system-image",
	"start-device.--force-create",
	"driver-setup.-p",
	"driver-setup.--platform",
	"mcp.--base-dir",
	"mcp.--no-viewer",
	"mcp.--viewer-port",
	"serve.--address",
	"serve.--database-url",
	"serve.--tls-cert",
	"serve.--tls-key",
	"serve.--client-ca",
	"serve.--signing-key",
	"serve.--signing-key-id",
	"serve.--node-id",
	"serve.--public-address",
	"serve.--inventory",
	"serve.--worker-concurrency",
	"serve.--worker-poll",
	"serve.--worker-claim",
	"serve.--worker-timeout",
	"serve.--node-heartbeat",
	"db.--database-url",
	"auth.keygen.--key-id",
	"auth.keygen.--private-key",
	"auth.keygen.--public-key",
	"auth.cert-map.--database-url",
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
		platforms := driverCommandPlatforms(keyword)
		entry := Entry{
			Kind:          FeatureCommand,
			Name:          string(keyword),
			ParseStatus:   ParseStatusParseable,
			RuntimeStatus: RuntimeStatusPlannedV1,
			Platforms:     platforms,
			Reason:        "command support comes from production driver capability documents",
		}
		if len(platforms) != 3 {
			entry.RuntimeStatus = RuntimeStatusPlatformLimited
			entry.Reason = "production drivers expose this command only on the listed platforms"
		}
		switch keyword {
		case model.CommandAssertNoDefectsWithAI, model.CommandAssertWithAI, model.CommandExtractTextWithAI:
			entry.Reason = "AI command executes against an injected provider; fails closed without one"
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
			// platform-limited lets selected-platform preflight refuse css
			// before a mobile driver is opened.
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

func driverCommandPlatforms(keyword model.CommandKeyword) []string {
	documents := driverCapabilityDocuments()
	platforms := make([]string, 0, len(documents))
	for _, document := range documents {
		if document.SupportsCommand(keyword) {
			platforms = append(platforms, document.Platform)
		}
	}
	return platforms
}

func driverCommandValuePlatforms(keyword model.CommandKeyword, value string) []string {
	documents := driverCapabilityDocuments()
	platforms := make([]string, 0, len(documents))
	for _, document := range documents {
		if document.SupportsCommandValue(keyword, value) {
			platforms = append(platforms, document.Platform)
		}
	}
	return platforms
}

func driverCapabilityDocuments() []drivercontract.Document {
	return drivercontract.Documents()
}

func cliSubcommandEntry(name string) Entry {
	entry := Entry{
		Kind: FeatureCLISubcommand, Name: name, ParseStatus: ParseStatusParseable,
		RuntimeStatus: RuntimeStatusPlannedV1, Platforms: []string{"all-hosts"},
		Reason: "declared FlowBaton v1 CLI command",
	}
	switch name {
	case "cloud", "download-samples", "login", "logout", "studio", "list-cloud-devices":
		entry.ParseStatus = ParseStatusOmitted
		entry.RuntimeStatus = RuntimeStatusExcluded
		entry.Reason = "not part of the approved local v1 product surface"
	case "chat":
		entry.ParseStatus = ParseStatusOmitted
		entry.RuntimeStatus = RuntimeStatusExcluded
		entry.Reason = "not part of the approved local v1 product surface"
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
	case "hierarchy.--target", "hierarchy.--target=devtools":
		entry.RuntimeStatus = RuntimeStatusPlatformLimited
		entry.Platforms = []string{"android"}
		entry.Reason = "Chrome DevTools WebView hierarchy is Android-only"
	case "query.--text", "query.--id":
		entry.ParseStatus = ParseStatusOmitted
		entry.RuntimeStatus = RuntimeStatusExcluded
		entry.Reason = "query uses one positional expression instead of these option spellings"
	case "global.-v", "global.-p", "global.--platform", "global.--host", "global.--port", "global.--driver-host-port", "global.--device", "global.--udid", "global.--verbose":
		entry.ParseStatus = ParseStatusOmitted
		entry.RuntimeStatus = RuntimeStatusExcluded
		entry.Reason = "the top-level parser does not accept this option spelling"
	case "test.--headless", "test.--screen-size", "record.--headless", "record.--screen-size":
		entry.RuntimeStatus = RuntimeStatusPlatformLimited
		entry.Platforms = []string{"web"}
		entry.Reason = "browser window options apply only to the Web/CDP driver"
	case "start-device.--system-image":
		entry.RuntimeStatus = RuntimeStatusPlatformLimited
		entry.Platforms = []string{"android"}
		entry.Reason = "Android virtual devices select SDK system image packages"
	case "mcp.--base-dir", "mcp.--no-viewer", "mcp.--viewer-port":
		entry.RuntimeStatus = RuntimeStatusPlannedV1
		entry.Reason = "implemented MCP server option"
	}
	return entry
}
