// Package capability owns FlowBaton's executable support classifications and
// graph-wide fail-closed analysis.
package capability

import (
	"fmt"
	"strings"
)

// RegistryVersionV0 identifies the first frozen support registry contract.
const RegistryVersionV0 = "flowbaton.support/v0"

// FeatureKind identifies the catalog containing an entry.
type FeatureKind string

const (
	FeatureCommand         FeatureKind = "command"
	FeatureSelector        FeatureKind = "selector-feature"
	FeatureConfigExtension FeatureKind = "config-extension"
	FeatureCLISubcommand   FeatureKind = "cli-subcommand"
	FeatureCLIFlag         FeatureKind = "cli-flag"
	FeatureHostTarget      FeatureKind = "host-target"
	FeatureDevicePlatform  FeatureKind = "device-platform"
)

// ParseStatus records whether the syntax is recognized by the v0 contract.
type ParseStatus string

const (
	ParseStatusParseable ParseStatus = "parseable"
	ParseStatusOmitted   ParseStatus = "omitted"
)

// RuntimeStatus records the declared v1 execution posture.
type RuntimeStatus string

const (
	RuntimeStatusPlannedV1       RuntimeStatus = "planned-v1"
	RuntimeStatusPlatformLimited RuntimeStatus = "platform-limited"
	RuntimeStatusDeferred        RuntimeStatus = "deferred"
	RuntimeStatusExcluded        RuntimeStatus = "excluded"
)

// Entry is the stable generic support-registry schema.
type Entry struct {
	Kind          FeatureKind   `json:"kind"`
	Name          string        `json:"name"`
	ParseStatus   ParseStatus   `json:"parseStatus"`
	RuntimeStatus RuntimeStatus `json:"runtimeStatus"`
	Platforms     []string      `json:"platforms,omitempty"`
	Reason        string        `json:"reason"`
}

// Registry is an immutable-by-convention versioned entry set.
type Registry struct {
	version string
	entries []Entry
}

// NewRegistry constructs a registry from defensively copied entries.
func NewRegistry(version string, entries []Entry) Registry {
	return Registry{version: version, entries: cloneEntries(entries)}
}

// DefaultRegistry returns the exhaustive v0 support registry.
func DefaultRegistry() Registry {
	return NewRegistry(RegistryVersionV0, defaultEntriesV0())
}

// Version returns the registry contract version.
func (r Registry) Version() string {
	return r.version
}

// Entries returns a deep defensive copy in stable catalog order.
func (r Registry) Entries() []Entry {
	return cloneEntries(r.entries)
}

// Lookup returns one exact catalog entry.
func (r Registry) Lookup(kind FeatureKind, name string) (Entry, bool) {
	for _, entry := range r.entries {
		if entry.Kind == kind && entry.Name == name {
			return cloneEntry(entry), true
		}
	}
	return Entry{}, false
}

// Validate fails closed on missing, duplicate, contradictory, invalid, or
// undeclared entries.
func (r Registry) Validate() error {
	if r.version != RegistryVersionV0 {
		return fmt.Errorf("unsupported registry version %q", r.version)
	}
	required := requiredRegistryKeysV0()
	seen := make(map[string]struct{}, len(r.entries))
	for _, entry := range r.entries {
		key := entryKey(entry.Kind, entry.Name)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate registry entry %s", key)
		}
		seen[key] = struct{}{}
		if _, declared := required[key]; !declared {
			return fmt.Errorf("undeclared registry entry %s", key)
		}
		if err := validateEntry(entry); err != nil {
			return err
		}
	}
	for key := range required {
		if _, exists := seen[key]; !exists {
			return fmt.Errorf("missing registry entry %s", key)
		}
	}
	return nil
}

func validateEntry(entry Entry) error {
	if !validFeatureKinds[entry.Kind] {
		return fmt.Errorf("registry entry %s has invalid kind %q", entry.Name, entry.Kind)
	}
	if strings.TrimSpace(entry.Name) == "" {
		return fmt.Errorf("registry entry has empty name")
	}
	if !validParseStatuses[entry.ParseStatus] {
		return fmt.Errorf("registry entry %s has invalid parse status %q", entry.Name, entry.ParseStatus)
	}
	if !validRuntimeStatuses[entry.RuntimeStatus] {
		return fmt.Errorf("registry entry %s has invalid runtime status %q", entry.Name, entry.RuntimeStatus)
	}
	if entry.ParseStatus == ParseStatusOmitted && entry.RuntimeStatus != RuntimeStatusExcluded {
		return fmt.Errorf("contradictory registry entry %s: omitted syntax cannot be %s", entry.Name, entry.RuntimeStatus)
	}
	if entry.ParseStatus == ParseStatusParseable && entry.RuntimeStatus == RuntimeStatusExcluded {
		return fmt.Errorf("contradictory registry entry %s: excluded syntax must be omitted", entry.Name)
	}
	if (entry.RuntimeStatus == RuntimeStatusDeferred ||
		entry.RuntimeStatus == RuntimeStatusExcluded ||
		entry.RuntimeStatus == RuntimeStatusPlatformLimited) && strings.TrimSpace(entry.Reason) == "" {
		return fmt.Errorf("registry entry %s with status %s requires a reason", entry.Name, entry.RuntimeStatus)
	}
	if entry.RuntimeStatus == RuntimeStatusPlatformLimited && len(entry.Platforms) == 0 {
		return fmt.Errorf("platform-limited registry entry %s requires platforms", entry.Name)
	}
	seenPlatforms := make(map[string]struct{}, len(entry.Platforms))
	for _, platform := range entry.Platforms {
		if !validPlatforms[platform] {
			return fmt.Errorf("registry entry %s names unknown platform %q", entry.Name, platform)
		}
		if _, duplicate := seenPlatforms[platform]; duplicate {
			return fmt.Errorf("registry entry %s repeats platform %q", entry.Name, platform)
		}
		seenPlatforms[platform] = struct{}{}
	}
	return nil
}

func cloneEntries(entries []Entry) []Entry {
	cloned := make([]Entry, len(entries))
	for index, entry := range entries {
		cloned[index] = cloneEntry(entry)
	}
	return cloned
}

func cloneEntry(entry Entry) Entry {
	entry.Platforms = append([]string(nil), entry.Platforms...)
	return entry
}

func entryKey(kind FeatureKind, name string) string {
	return string(kind) + ":" + name
}

var validFeatureKinds = map[FeatureKind]bool{
	FeatureCommand: true, FeatureSelector: true, FeatureConfigExtension: true,
	FeatureCLISubcommand: true, FeatureCLIFlag: true, FeatureHostTarget: true,
	FeatureDevicePlatform: true,
}

var validParseStatuses = map[ParseStatus]bool{
	ParseStatusParseable: true,
	ParseStatusOmitted:   true,
}

var validRuntimeStatuses = map[RuntimeStatus]bool{
	RuntimeStatusPlannedV1:       true,
	RuntimeStatusPlatformLimited: true,
	RuntimeStatusDeferred:        true,
	RuntimeStatusExcluded:        true,
}

var validPlatforms = map[string]bool{
	"all-hosts":     true,
	"android":       true,
	"ios-simulator": true,
	"ios-physical":  true,
	"web":           true,
}
