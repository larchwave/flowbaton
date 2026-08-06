package v0

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestAndroidJVMDeclarationsAreLinkedToCanonicalDescriptor(t *testing.T) {
	assertLanguageDescriptorHash(
		t,
		"android-grpc.json",
		filepath.Join("..", "..", "drivers", "android", "core", "src", "main", "java", "dev", "nohavewho", "flowbaton", "driver", "contract", "AndroidWireContractV0.java"),
		regexp.MustCompile(`DESCRIPTOR_SHA256\s*=\s*"([0-9a-f]{64})"`),
	)
}

func TestIOSSwiftDeclarationsAreLinkedToCanonicalDescriptor(t *testing.T) {
	assertLanguageDescriptorHash(
		t,
		"ios-http.json",
		filepath.Join("..", "..", "drivers", "ios", "Sources", "FlowBatonIOSRunner", "WireContractV0.swift"),
		regexp.MustCompile(`descriptorSHA256\s*=\s*"([0-9a-f]{64})"`),
	)
}

func assertLanguageDescriptorHash(t *testing.T, descriptorPath, languagePath string, pattern *regexp.Regexp) {
	t.Helper()
	descriptor, err := os.ReadFile(descriptorPath)
	if err != nil {
		t.Fatalf("read canonical descriptor %s: %v", descriptorPath, err)
	}
	languageSource, err := os.ReadFile(languagePath)
	if err != nil {
		t.Fatalf("read language declaration %s: %v", languagePath, err)
	}
	match := pattern.FindSubmatch(languageSource)
	if len(match) != 2 {
		t.Fatalf("%s does not declare its canonical descriptor SHA-256", languagePath)
	}
	want := fmt.Sprintf("%x", sha256.Sum256(descriptor))
	if got := string(match[1]); got != want {
		t.Fatalf("%s descriptor SHA-256 = %s, want %s from %s", languagePath, got, want, descriptorPath)
	}
}
