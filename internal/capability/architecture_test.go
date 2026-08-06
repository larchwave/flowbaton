package capability

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestCapabilityPackageHasNoMutationSurfaceImports(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read capability package: %v", err)
	}
	forbidden := []string{
		"/internal/device",
		"/internal/assets",
		"/internal/cli",
		"/internal/session",
		"/internal/js",
		"/internal/android",
		"/internal/ios",
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), entry.Name(), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse imports for %s: %v", entry.Name(), err)
		}
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("unquote import %s: %v", spec.Path.Value, err)
			}
			for _, fragment := range forbidden {
				if strings.Contains(path, fragment) {
					t.Fatalf("%s imports mutation surface %s", entry.Name(), path)
				}
			}
		}
	}
}
