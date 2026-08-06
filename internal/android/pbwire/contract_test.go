package pbwire

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// The method constants in methods.go are hand-written, which is exactly the
// thing that rots when the frozen proto moves. This reads
// proto/flowbaton_android.proto and holds the two together in both directions:
// every rpc must have a constant with the exact gRPC path, the streaming flag
// must match (addMedia is the only client-streaming rpc), and no constant may
// exist without a matching rpc.

var rpcLinePattern = regexp.MustCompile(
	`(?m)^\s*rpc\s+(\w+)\s*\(\s*(stream\s+)?\w+\s*\)\s+returns\s+\(\s*\w+\s*\)\s*;`)

func TestMethodConstantsCoverTheFrozenProto(t *testing.T) {
	t.Parallel()

	source := loadFrozenProto(t)
	protoPackage := submatch(t, source, `(?m)^package\s+([A-Za-z0-9_.]+)\s*;`)
	service := submatch(t, source, `(?m)^service\s+(\w+)\s*\{`)

	rpcs := rpcLinePattern.FindAllStringSubmatch(source, -1)
	if len(rpcs) != 12 {
		t.Fatalf("the frozen proto declares %d rpcs; this package transcribed 12", len(rpcs))
	}

	methods := MethodByRPC()
	declared := make(map[string]bool, len(rpcs))
	for _, rpc := range rpcs {
		name, streaming := rpc[1], rpc[2] != ""
		declared[name] = true
		constant, ok := methods[name]
		if !ok {
			t.Errorf("rpc %s has no method constant", name)
			continue
		}
		want := "/" + protoPackage + "." + service + "/" + name
		if constant != want {
			t.Errorf("method for rpc %s = %q, want %q", name, constant, want)
		}
		if IsClientStreaming(constant) != streaming {
			t.Errorf("IsClientStreaming(%q) = %v, the frozen proto declares %v",
				constant, IsClientStreaming(constant), streaming)
		}
	}
	for name := range methods {
		if !declared[name] {
			t.Errorf("method constant for %q has no matching rpc in the frozen proto", name)
		}
	}
}

func TestStreamAddMediaIsTheAddMediaMethod(t *testing.T) {
	t.Parallel()

	if StreamAddMedia != MethodAddMedia {
		t.Fatalf("StreamAddMedia = %q, want the addMedia method %q", StreamAddMedia, MethodAddMedia)
	}
}

func submatch(t *testing.T, source, pattern string) string {
	t.Helper()
	match := regexp.MustCompile(pattern).FindStringSubmatch(source)
	if match == nil {
		t.Fatalf("the frozen proto matches nothing for %s", pattern)
	}
	return match[1]
}

func loadFrozenProto(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "..", "proto", "flowbaton_android.proto")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the frozen proto: %v", err)
	}
	return string(data)
}
