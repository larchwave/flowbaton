package cli

import (
	"strconv"
	"strings"
)

// The host half of the reserved environment.
//
// specs/01-core-engine.md:101 lists FLOWBATON_DEVICE_UDID and the shard variables
// among the injected defaults, and adds that shell variables prefixed FLOWBATON_
// are auto-injected. Each is something only the host can answer: the engine does
// not know which simulator it holds, which shard it is, or what the operator's
// shell contained.
//
// The two channels are deliberately different. Shell variables are EXTERNAL
// input — an operator's -e outranks them, and the engine strips the reserved
// names out of them, so a stray FLOWBATON_SHARD_ID in a shell cannot forge a
// shard identity. The shard's own variables travel on the reserved channel,
// which outranks everything.

// flowbatonPrefix is the namespace both channels live in.
const flowbatonPrefix = "FLOWBATON_"

// reservedEnvironment is what this shard tells its flows about where they run.
//
// FLOWBATON_SHARD_INDEX is the 0-based position and FLOWBATON_SHARD_ID is the
// 1-based number — the same number the shard-N artifact directory carries, so
// what a flow reports and what an operator sees in the output directory match.
// An unsharded run is shard 1 of 1 rather than absent: a flow that reads these
// on an ordinary run should get an answer, not an empty string.
func reservedEnvironment(shard Shard) map[string]string {
	values := map[string]string{
		"FLOWBATON_SHARD_ID":    strconv.Itoa(shard.Count()),
		"FLOWBATON_SHARD_INDEX": strconv.Itoa(shard.Index),
	}
	if shard.Device != "" {
		values["FLOWBATON_DEVICE_UDID"] = shard.Device
	}
	return values
}

// mergeShellEnvironment adds the shell's FLOWBATON_ variables to the operator's.
//
// Only the prefixed ones: passing the whole environment would hand every flow
// the operator's shell, credentials included, and a flow that interpolates an
// undefined variable is supposed to fail rather than silently pick up whatever
// happened to be exported.
//
// An explicit -e wins, because that is what the operator typed for THIS run; a
// shell variable may be left over from another one.
func mergeShellEnvironment(shell []string, explicit map[string]string) map[string]string {
	merged := map[string]string{}
	for _, entry := range shell {
		key, value, found := strings.Cut(entry, "=")
		if !found || !strings.HasPrefix(key, flowbatonPrefix) {
			continue
		}
		merged[key] = value
	}
	for key, value := range explicit {
		merged[key] = value
	}
	return merged
}
