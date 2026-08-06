package engine

import (
	"path/filepath"
	"sort"
	"strings"
)

// The reserved environment.
//
// specs/01-core-engine.md:101 names FLOWBATON_SHARD_ID and FLOWBATON_SHARD_INDEX as
// reserved and stripped from external input, and lists FLOWBATON_FILENAME,
// FLOWBATON_DEVICE_UDID and the shard variables as injected defaults.
//
// Two of those the host knows and the engine cannot: which shard this is and
// which device it holds. One the engine knows and the host cannot:
// FLOWBATON_FILENAME differs per flow, and only the engine knows which flow is
// running. So the host supplies its two through Dependencies and the engine
// adds the third per flow.
//
// They are applied LAST, after the flow's own env and after the operator's
// overlay, because a reserved name that a flow could overwrite is not reserved.

// reservedEnvironmentPrefix is the namespace the reserved names live in.
const reservedEnvironmentPrefix = "FLOWBATON_"

// inlineFlowPathMarker separates a containing file from the source position of
// an inline runFlow. inlineRunFlowPath builds paths with it, and FLOWBATON_FILENAME
// declines to overwrite the containing file's name when it sees one — an inline
// subflow has no file of its own, it lives in its parent's.
const inlineFlowPathMarker = "#runFlow:inline:"

// validateReservedEnvironment refuses a key that is not a reserved name.
//
// The field exists for names only the host may set, and it outranks everything
// else. A key without the prefix would let the host quietly overwrite a flow's
// own variable through a channel the flow author cannot see — a worse outcome
// than a refusal at the boundary.
func validateReservedEnvironment(values map[string]string) error {
	keys := make([]string, 0, len(values))
	for key := range values {
		if !strings.HasPrefix(key, reservedEnvironmentPrefix) {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	sort.Strings(keys)
	return NewConfigurationError(
		"reserved environment keys must start with "+reservedEnvironmentPrefix+
			": "+strings.Join(keys, ", "), nil)
}

// reservedEnvironmentFor returns what a single flow sees: the host's reserved
// variables plus this flow's own file name.
func reservedEnvironmentFor(reserved map[string]string, flowPath string) map[string]string {
	values := cloneStringMap(reserved)
	if values == nil {
		values = map[string]string{}
	}
	if base, ok := flowFileName(flowPath); ok {
		values["FLOWBATON_FILENAME"] = base
	}
	return values
}

// flowFileName reports the file a flow came from, if it came from one.
//
// An inline runFlow has no file of its own — its path is the containing file
// plus a source position — so it keeps whatever FLOWBATON_FILENAME is already in
// scope, which is its parent's. Naming it after the synthetic path would put
// "root.yaml#runFlow:inline:12:3:140" in a variable called FILENAME.
func flowFileName(flowPath string) (string, bool) {
	if flowPath == "" || strings.Contains(flowPath, inlineFlowPathMarker) {
		return "", false
	}
	base := filepath.Base(flowPath)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "", false
	}
	return base, true
}
