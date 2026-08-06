package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/nohavewho/flowbaton/internal/model"
)

func TestCheckSyntaxFileSuccessHasExactOutputAndSource(t *testing.T) {
	flowPath := filepath.Join(t.TempDir(), "flow.yaml")
	contents := []byte("appId: com.example.app\n---\n- launchApp\n")
	if err := os.WriteFile(flowPath, contents, 0o600); err != nil {
		t.Fatalf("write flow: %v", err)
	}

	var checked Source
	runner := CheckSyntaxRunner{
		Checker: checkerFunc(func(_ context.Context, source Source) error {
			checked = source
			return nil
		}),
		Getwd: func() (string, error) { return "/explicit/cwd", nil },
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if got := runner.Run(context.Background(), []string{flowPath}, bytes.NewReader(nil), &stdout, &stderr); got != ExitOK {
		t.Fatalf("exit = %d, want %d", got, ExitOK)
	}
	if got := stdout.String(); got != "OK\n" {
		t.Fatalf("stdout = %q, want OK newline", got)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
	if checked.Name != flowPath || checked.BaseDir != "/explicit/cwd" || !bytes.Equal(checked.Data, contents) {
		t.Fatalf("checked source = %#v, want path/data with explicit cwd", checked)
	}
}

func TestCheckSyntaxStdinUsesCWDForRelativeLinks(t *testing.T) {
	contents := []byte("appId: com.example.app\n---\n- runFlow: child.yaml\n")
	var checked Source
	runner := CheckSyntaxRunner{
		Checker: checkerFunc(func(_ context.Context, source Source) error {
			checked = source
			return nil
		}),
		Getwd: func() (string, error) { return "/workspace/current", nil },
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if got := runner.Run(context.Background(), []string{"-"}, bytes.NewReader(contents), &stdout, &stderr); got != ExitOK {
		t.Fatalf("exit = %d, want %d", got, ExitOK)
	}
	if checked.Name != "-" || checked.BaseDir != "/workspace/current" || !bytes.Equal(checked.Data, contents) {
		t.Fatalf("stdin source = %#v", checked)
	}
	if got, want := checked.ResolveLink("child.yaml"), filepath.Join("/workspace/current", "child.yaml"); got != want {
		t.Fatalf("resolved stdin link = %q, want %q", got, want)
	}
	if stdout.String() != "OK\n" || stderr.String() != "" {
		t.Fatalf("stdout/stderr = %q/%q, want exact success output", stdout.String(), stderr.String())
	}
}

func TestCheckSyntaxFailureHasNoStdoutAndExactDiagnostic(t *testing.T) {
	flowPath := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(flowPath, []byte("invalid"), 0o600); err != nil {
		t.Fatalf("write flow: %v", err)
	}
	wantErr := model.Diagnostic{
		Code:    "unsupported_capability",
		Message: "AI command assertWithAI is unavailable in v1",
		Source: model.SourceInfo{
			Path:  flowPath,
			Start: model.Position{Line: 4, Column: 3},
		},
	}
	runner := CheckSyntaxRunner{
		Checker: checkerFunc(func(context.Context, Source) error { return wantErr }),
		Getwd:   os.Getwd,
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if got := runner.Run(context.Background(), []string{flowPath}, bytes.NewReader(nil), &stdout, &stderr); got != ExitInvalid {
		t.Fatalf("exit = %d, want %d", got, ExitInvalid)
	}
	if stdout.String() != "" {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if got, want := stderr.String(), wantErr.Error()+"\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestCheckSyntaxUsageAndInputFailuresAreDeterministic(t *testing.T) {
	directory := t.TempDir()
	missing := filepath.Join(directory, "missing.yaml")
	tests := []struct {
		name       string
		args       []string
		stdin      io.Reader
		getwd      func() (string, error)
		wantStderr string
	}{
		{name: "missing operand", args: nil, stdin: bytes.NewReader(nil), getwd: os.Getwd, wantStderr: CheckSyntaxUsage},
		{name: "extra operand", args: []string{"one.yaml", "two.yaml"}, stdin: bytes.NewReader(nil), getwd: os.Getwd, wantStderr: CheckSyntaxUsage},
		{name: "unknown option", args: []string{"--format=json"}, stdin: bytes.NewReader(nil), getwd: os.Getwd, wantStderr: CheckSyntaxUsage},
		{name: "missing file", args: []string{missing}, stdin: bytes.NewReader(nil), getwd: os.Getwd, wantStderr: missing + ": input_not_found: flow file does not exist\n"},
		{name: "directory", args: []string{directory}, stdin: bytes.NewReader(nil), getwd: os.Getwd, wantStderr: directory + ": input_directory: flow path is a directory\n"},
		{name: "stdin read", args: []string{"-"}, stdin: errorReader{}, getwd: os.Getwd, wantStderr: "-: input_unreadable: unable to read flow input\n"},
		{name: "cwd", args: []string{"-"}, stdin: bytes.NewReader(nil), getwd: func() (string, error) { return "", errors.New("cwd unavailable") }, wantStderr: "<input>: cwd_unavailable: unable to determine current working directory\n"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			checkerCalls := 0
			runner := CheckSyntaxRunner{
				Checker: checkerFunc(func(context.Context, Source) error {
					checkerCalls++
					return nil
				}),
				Getwd: test.getwd,
			}
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			if got := runner.Run(context.Background(), test.args, test.stdin, &stdout, &stderr); got != ExitInvalid {
				t.Fatalf("exit = %d, want %d", got, ExitInvalid)
			}
			if stdout.String() != "" || stderr.String() != test.wantStderr {
				t.Fatalf("stdout/stderr = %q/%q, want empty/%q", stdout.String(), stderr.String(), test.wantStderr)
			}
			if checkerCalls != 0 {
				t.Fatalf("checker calls = %d, want zero", checkerCalls)
			}
		})
	}
}

func TestParserCheckerRejectsSyntaxBeforePreflight(t *testing.T) {
	preflightCalls := 0
	checker := ParserChecker{Preflight: preflightFunc(func(context.Context, Source, model.Flow) error {
		preflightCalls++
		return nil
	})}
	source := Source{
		Name:    "/workspace/invalid.yaml",
		BaseDir: "/workspace",
		Data:    []byte("appId: com.example.app\n---\n- tapn: Continue\n"),
	}

	err := checker.Check(context.Background(), source)
	want := "/workspace/invalid.yaml:3:3: unknown_command: unknown command tapn. Did you mean tapOn?"
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
	if preflightCalls != 0 {
		t.Fatalf("preflight calls = %d, want zero", preflightCalls)
	}
}

func TestParserCheckerPassesParsedRootAndBaseToPreflight(t *testing.T) {
	called := false
	baseDir := filepath.Clean(filepath.FromSlash("/workspace/current"))
	source := Source{
		Name:    "-",
		BaseDir: baseDir,
		Data:    []byte("appId: com.example.app\n---\n- runFlow: child.yaml\n"),
	}
	checker := ParserChecker{Preflight: preflightFunc(func(_ context.Context, gotSource Source, root model.Flow) error {
		called = true
		if gotSource.Name != "-" || gotSource.BaseDir != baseDir {
			t.Fatalf("preflight source = %#v", gotSource)
		}
		if got := root.Commands[0].Links[0].ResolvedPath; got != "child.yaml" {
			t.Fatalf("parser link = %q, want cwd-relative child.yaml", got)
		}
		if got, want := gotSource.ResolveLink(root.Commands[0].Links[0].ResolvedPath), filepath.Join(baseDir, "child.yaml"); got != want {
			t.Fatalf("resolved link = %q, want %q", got, want)
		}
		return nil
	})}

	if err := checker.Check(context.Background(), source); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !called {
		t.Fatal("preflight was not called")
	}
}

func TestParserCheckerFailsClosedWithoutPreflight(t *testing.T) {
	checker := ParserChecker{}
	source := Source{
		Name:    "/workspace/valid.yaml",
		BaseDir: "/workspace",
		Data:    []byte("appId: com.example.app\n---\n- launchApp\n"),
	}
	err := checker.Check(context.Background(), source)
	want := "/workspace/valid.yaml: preflight_unavailable: capability preflight is not wired"
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

func TestNewParserCheckerSeedsAlreadyParsedFileRoot(t *testing.T) {
	directory := t.TempDir()
	rootPath := filepath.Join(directory, "root.yaml")
	if err := os.WriteFile(rootPath, []byte("not valid flow YAML\n"), 0o600); err != nil {
		t.Fatalf("write deliberately stale root: %v", err)
	}
	source := Source{
		Name:    rootPath,
		BaseDir: directory,
		Data:    []byte("appId: com.example.app\n---\n- launchApp\n"),
	}

	if err := NewParserChecker().Check(context.Background(), source); err != nil {
		t.Fatalf("Check reparsed the selected file instead of using the parsed root: %v", err)
	}
}

func TestNewParserCheckerResolvesStdinLinksFromSourceBaseDir(t *testing.T) {
	directory := t.TempDir()
	childPath := filepath.Join(directory, "child.yaml")
	if err := os.WriteFile(childPath, []byte("appId: com.example.app\n---\n- back\n"), 0o600); err != nil {
		t.Fatalf("write child flow: %v", err)
	}
	source := Source{
		Name:    "-",
		BaseDir: directory,
		Data:    []byte("appId: com.example.app\n---\n- runFlow: child.yaml\n"),
	}

	if err := NewParserChecker().Check(context.Background(), source); err != nil {
		t.Fatalf("Check stdin graph from Source.BaseDir: %v", err)
	}
}

func TestCheckSyntaxAcceptsSymlinkToRegularFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows developer mode or elevation is required for symlink creation")
	}
	directory := t.TempDir()
	target := filepath.Join(directory, "target.yaml")
	alias := filepath.Join(directory, "alias.yaml")
	contents := []byte("appId: com.example.app\n---\n- launchApp\n")
	if err := os.WriteFile(target, contents, 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Symlink(target, alias); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	var checked Source
	runner := CheckSyntaxRunner{
		Checker: checkerFunc(func(_ context.Context, source Source) error {
			checked = source
			return nil
		}),
		Getwd: func() (string, error) { return directory, nil },
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if exit := runner.Run(context.Background(), []string{alias}, bytes.NewReader(nil), &stdout, &stderr); exit != ExitOK {
		t.Fatalf("exit = %d, stderr = %q", exit, stderr.String())
	}
	if checked.Name != alias || !bytes.Equal(checked.Data, contents) {
		t.Fatalf("checked symlink source = %#v", checked)
	}
}

type checkerFunc func(context.Context, Source) error

func (function checkerFunc) Check(ctx context.Context, source Source) error {
	return function(ctx, source)
}

type preflightFunc func(context.Context, Source, model.Flow) error

func (function preflightFunc) Check(ctx context.Context, source Source, root model.Flow) error {
	return function(ctx, source, root)
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}
