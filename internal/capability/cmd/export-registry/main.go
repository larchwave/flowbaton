package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/larchwave/flowbaton/internal/capability"
)

type snapshot struct {
	SchemaVersion   int                `json:"schema_version"`
	ContractVersion string             `json:"contract_version"`
	RegistryVersion string             `json:"registry_version"`
	Entries         []capability.Entry `json:"entries"`
}

func main() {
	output := flag.String("output", "", "path to the support-registry JSON snapshot")
	flag.Parse()
	if *output == "" {
		fatalf("-output is required")
	}
	registry := capability.DefaultRegistry()
	if err := registry.Validate(); err != nil {
		fatalf("validate registry: %v", err)
	}
	contents, err := json.MarshalIndent(snapshot{
		SchemaVersion:   1,
		ContractVersion: "v0",
		RegistryVersion: registry.Version(),
		Entries:         registry.Entries(),
	}, "", "  ")
	if err != nil {
		fatalf("encode registry: %v", err)
	}
	contents = append(contents, '\n')
	path, err := filepath.Abs(*output)
	if err != nil {
		fatalf("resolve output path: %v", err)
	}
	if err := os.WriteFile(path, contents, 0o644); err != nil {
		fatalf("write %s: %v", path, err)
	}
}

func fatalf(format string, arguments ...any) {
	_, _ = fmt.Fprintf(os.Stderr, "export support registry: "+format+"\n", arguments...)
	os.Exit(1)
}
