// Package cli contains side-effect-free command orchestration for FlowBaton.
package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/larchwave/flowbaton/internal/flow"
	"github.com/larchwave/flowbaton/internal/model"
)

const (
	ExitOK      = 0
	ExitInvalid = 2

	CheckSyntaxUsage = "usage: flowbaton check-syntax FILE|-\n"
)

// Source is one selected syntax-check root. BaseDir is always the invocation
// working directory; this makes links from stdin resolve from cwd without
// changing their user-facing source name from "-".
type Source struct {
	Name    string
	BaseDir string
	Data    []byte
}

// ResolveLink resolves a parser-produced link against the invocation
// working directory. Absolute links remain absolute.
func (source Source) ResolveLink(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(source.BaseDir, path))
}

// Checker owns parse plus recursive capability preflight for one selected root.
// A successful return is the sole authority for printing OK.
type Checker interface {
	Check(context.Context, Source) error
}

// Preflight is the narrow integration boundary required from the recursive
// graph/capability package. It receives the parsed root and the cwd resolution
// context, and must remain side-effect-free.
type Preflight interface {
	Check(context.Context, Source, model.Flow) error
}

// ParserChecker parses the selected root before invoking recursive preflight.
// A missing preflight fails closed and can never produce a successful check.
type ParserChecker struct {
	Preflight Preflight
}

func (checker ParserChecker) Check(ctx context.Context, source Source) error {
	root, err := flow.Parse(source.Name, bytes.NewReader(source.Data))
	if err != nil {
		return err
	}
	if checker.Preflight == nil {
		return model.Diagnostic{
			Code:    "preflight_unavailable",
			Message: "capability preflight is not wired",
			Source:  model.SourceInfo{Path: source.Name},
		}
	}
	return checker.Preflight.Check(ctx, source, root)
}

// CheckSyntaxRunner enforces the command's exact stdout/stderr/exit contract.
type CheckSyntaxRunner struct {
	Checker Checker
	Getwd   func() (string, error)
}

func (runner CheckSyntaxRunner) Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 1 || args[0] == "" || (strings.HasPrefix(args[0], "-") && args[0] != "-") {
		_, _ = io.WriteString(stderr, CheckSyntaxUsage)
		return ExitInvalid
	}

	getwd := runner.Getwd
	if getwd == nil {
		getwd = os.Getwd
	}
	baseDir, err := getwd()
	if err != nil {
		writeDiagnostic(stderr, model.Diagnostic{
			Code:    "cwd_unavailable",
			Message: "unable to determine current working directory",
			Source:  model.SourceInfo{Path: "<input>"},
		})
		return ExitInvalid
	}

	source, err := readSource(args[0], baseDir, stdin)
	if err != nil {
		writeDiagnostic(stderr, err)
		return ExitInvalid
	}
	if runner.Checker == nil {
		writeDiagnostic(stderr, model.Diagnostic{
			Code:    "preflight_unavailable",
			Message: "capability preflight is not wired",
			Source:  model.SourceInfo{Path: source.Name},
		})
		return ExitInvalid
	}
	if err := runner.Checker.Check(ctx, source); err != nil {
		writeDiagnostic(stderr, err)
		return ExitInvalid
	}

	_, _ = io.WriteString(stdout, "OK\n")
	return ExitOK
}

func readSource(name, baseDir string, stdin io.Reader) (Source, error) {
	if name == "-" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return Source{}, model.Diagnostic{
				Code:    "input_unreadable",
				Message: "unable to read flow input",
				Source:  model.SourceInfo{Path: "-"},
			}
		}
		return Source{Name: name, BaseDir: baseDir, Data: data}, nil
	}

	info, err := os.Stat(name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Source{}, model.Diagnostic{
				Code:    "input_not_found",
				Message: "flow file does not exist",
				Source:  model.SourceInfo{Path: name},
			}
		}
		return Source{}, model.Diagnostic{
			Code:    "input_unreadable",
			Message: "unable to read flow file",
			Source:  model.SourceInfo{Path: name},
		}
	}
	if info.IsDir() {
		return Source{}, model.Diagnostic{
			Code:    "input_directory",
			Message: "flow path is a directory",
			Source:  model.SourceInfo{Path: name},
		}
	}
	if !info.Mode().IsRegular() {
		return Source{}, model.Diagnostic{
			Code:    "input_non_regular",
			Message: "flow path is not a regular file",
			Source:  model.SourceInfo{Path: name},
		}
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return Source{}, model.Diagnostic{
			Code:    "input_unreadable",
			Message: "unable to read flow file",
			Source:  model.SourceInfo{Path: name},
		}
	}
	return Source{Name: name, BaseDir: baseDir, Data: data}, nil
}

func writeDiagnostic(writer io.Writer, err error) {
	_, _ = fmt.Fprintln(writer, err)
}
