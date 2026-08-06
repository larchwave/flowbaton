package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	flowcli "github.com/larchwave/flowbaton/internal/cli"
)

func TestEveryCompletionSubcommandIsActuallyDispatched(t *testing.T) {
	// The drift guard for generate-completion: the completion advertises
	// flowcli.TopLevelSubcommands, so each one must have a real dispatch branch
	// here. A "--__drift_probe__" argument makes each runner reject during
	// arg-parse (before any device or subprocess work), so a dispatched command
	// returns its own error while an undispatched one falls through to
	// topLevelUsage — which is exactly what this asserts against.
	for _, command := range flowcli.TopLevelSubcommands {
		var stdout, stderr bytes.Buffer
		run([]string{command, "--__drift_probe__"}, bytes.NewReader(nil), &stdout, &stderr)
		if stderr.String() == topLevelUsage {
			t.Fatalf("subcommand %q is advertised by completion but not dispatched by main", command)
		}
	}
	// Negative control: a command that is NOT dispatched must fall through.
	var stdout, stderr bytes.Buffer
	run([]string{"__definitely_not_a_command__"}, bytes.NewReader(nil), &stdout, &stderr)
	if stderr.String() != topLevelUsage {
		t.Fatalf("an unknown command did not fall through to usage: %q", stderr.String())
	}
}

func TestRunVersionWritesOnlyStableVersionLine(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if got, want := run([]string{"--version"}, bytes.NewReader(nil), &stdout, &stderr), 0; got != want {
		t.Fatalf("run exit = %d, want %d", got, want)
	}
	if got, want := stdout.String(), "flowbaton dev\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunRejectsUnsupportedArgumentShapesWithDeterministicUsage(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "no arguments", args: nil},
		{name: "wrong flag", args: []string{"--help"}},
		{name: "extra version argument", args: []string{"--version", "unexpected"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			if got, want := run(test.args, bytes.NewReader(nil), &stdout, &stderr), 2; got != want {
				t.Fatalf("run exit = %d, want %d", got, want)
			}
			if got := stdout.String(); got != "" {
				t.Fatalf("stdout = %q, want empty", got)
			}
			if got, want := stderr.String(), topLevelUsage; got != want {
				t.Fatalf("stderr = %q, want %q", got, want)
			}
		})
	}
}

func TestRunDispatchesCheckSyntaxWithExactSuccessContract(t *testing.T) {
	flowPath := filepath.Join(t.TempDir(), "valid.yaml")
	if err := os.WriteFile(flowPath, []byte("appId: com.example.app\n---\n- launchApp\n"), 0o600); err != nil {
		t.Fatalf("write flow: %v", err)
	}
	checkerCalls := 0
	checker := commandCheckerFunc(func(context.Context, flowcli.Source) error {
		checkerCalls++
		return nil
	})
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exit := runWithChecker(
		[]string{"check-syntax", flowPath},
		bytes.NewReader(nil),
		&stdout,
		&stderr,
		checker,
		func() (string, error) { return "/workspace", nil },
	)
	if exit != 0 || stdout.String() != "OK\n" || stderr.String() != "" {
		t.Fatalf("exit/stdout/stderr = %d/%q/%q, want 0/OK newline/empty", exit, stdout.String(), stderr.String())
	}
	if checkerCalls != 1 {
		t.Fatalf("checker calls = %d, want 1", checkerCalls)
	}
}

func TestCheckSyntaxSubprocessReportsExactParserDiagnostic(t *testing.T) {
	flowPath := filepath.Join(t.TempDir(), "invalid.yaml")
	if err := os.WriteFile(flowPath, []byte("appId: com.example.app\n---\n- tapn: Continue\n"), 0o600); err != nil {
		t.Fatalf("write flow: %v", err)
	}
	exit, stdout, stderr := invokeCLIProcess(t, []string{"check-syntax", flowPath}, "")
	if exit != 2 {
		t.Fatalf("exit = %d, want 2", exit)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	want := flowPath + ":3:3: unknown_command: unknown command tapn. Did you mean tapOn?\n"
	if stderr != want {
		t.Fatalf("stderr = %q, want %q", stderr, want)
	}
}

func TestCheckSyntaxStdinSubprocessUsesDashAsDiagnosticPath(t *testing.T) {
	stdin := "appId: com.example.app\n---\n- tapn: Continue\n"
	exit, stdout, stderr := invokeCLIProcess(t, []string{"check-syntax", "-"}, stdin)
	if exit != 2 || stdout != "" {
		t.Fatalf("exit/stdout = %d/%q, want 2/empty", exit, stdout)
	}
	want := "-:3:3: unknown_command: unknown command tapn. Did you mean tapOn?\n"
	if stderr != want {
		t.Fatalf("stderr = %q, want %q", stderr, want)
	}
}

func TestCheckSyntaxStdinSubprocessRejectsRemovedRhinoEngineExactly(t *testing.T) {
	stdin := "appId: com.example.app\njsEngine: rhino\n---\n- launchApp\n"
	exit, stdout, stderr := invokeCLIProcess(t, []string{"check-syntax", "-"}, stdin)
	if exit != 2 || stdout != "" {
		t.Fatalf("exit/stdout = %d/%q, want 2/empty", exit, stdout)
	}
	want := "-:2:1: unsupported_capability: feature is not classified by the v0 support registry [config-extension/jsEngine=rhino]\n"
	if stderr != want {
		t.Fatalf("stderr = %q, want %q", stderr, want)
	}
}

func TestCheckSyntaxStdinSubprocessRejectsCyclicAliasWithoutCrash(t *testing.T) {
	stdin := "appId: com.example.app\nfoo: &foo\n  self: *foo\n---\n- launchApp\n"
	exit, stdout, stderr := invokeCLIProcess(t, []string{"check-syntax", "-"}, stdin)
	if exit != 2 || stdout != "" {
		t.Fatalf("exit/stdout = %d/%q, want 2/empty", exit, stdout)
	}
	want := "-:3:9: cyclic_yaml_alias: YAML alias cycle is not supported\n"
	if stderr != want {
		t.Fatalf("stderr length = %d, want deterministic diagnostic %q", len(stderr), want)
	}
}

func TestCheckSyntaxStdinSubprocessRejectsMixedSwipeVariantsExactly(t *testing.T) {
	stdin := "appId: com.example.app\n---\n- swipe: {direction: DOWN, start: '10,20', end: '90,80'}\n"
	exit, stdout, stderr := invokeCLIProcess(t, []string{"check-syntax", "-"}, stdin)
	if exit != 2 || stdout != "" {
		t.Fatalf("exit/stdout = %d/%q, want 2/empty", exit, stdout)
	}
	want := "-:3:10: conflicting_command_fields: swipe variants are mutually exclusive\n"
	if stderr != want {
		t.Fatalf("stderr = %q, want %q", stderr, want)
	}
}

func TestCheckSyntaxSubprocessAcceptsValidFileAndStdinGraph(t *testing.T) {
	directory := t.TempDir()
	fileRoot := filepath.Join(directory, "file-root.yaml")
	writeCLIFlow(t, fileRoot, "- launchApp\n")

	t.Run("file", func(t *testing.T) {
		exit, stdout, stderr := invokeCLIProcess(t, []string{"check-syntax", fileRoot}, "")
		if exit != 0 || stdout != "OK\n" || stderr != "" {
			t.Fatalf("exit/stdout/stderr = %d/%q/%q, want 0/OK newline/empty", exit, stdout, stderr)
		}
	})

	child := filepath.Join(directory, "child.yaml")
	writeCLIFlow(t, child, "- back\n")
	stdin := "appId: com.example.app\n---\n- runFlow: child.yaml\n"
	t.Run("stdin relative child", func(t *testing.T) {
		exit, stdout, stderr := invokeCLIProcessInDir(t, []string{"check-syntax", "-"}, stdin, directory)
		if exit != 0 || stdout != "OK\n" || stderr != "" {
			t.Fatalf("exit/stdout/stderr = %d/%q/%q, want 0/OK newline/empty", exit, stdout, stderr)
		}
	})
}

func TestCheckSyntaxSubprocessRejectsRecursiveGraphFailuresExactly(t *testing.T) {
	t.Run("unsupported child", func(t *testing.T) {
		directory := t.TempDir()
		root := filepath.Join(directory, "root.yaml")
		child := filepath.Join(directory, "child.yaml")
		writeCLIFlow(t, root, "- runFlow: child.yaml\n")
		// The child uses an unknown configuration key so recursive validation
		// must report the nested failure path. Unlike an unsupported feature, an
		// unknown key remains invalid as the supported command set grows.
		if err := os.WriteFile(child, []byte(
			"appId: com.example.app\nfutureMagic: true\n---\n- back\n"), 0o600); err != nil {
			t.Fatalf("write child flow: %v", err)
		}
		canonicalRoot := canonicalTestPath(t, root)
		canonicalChild := canonicalTestPath(t, child)

		exit, stdout, stderr := invokeCLIProcess(t, []string{"check-syntax", root}, "")
		if exit != 2 || stdout != "" {
			t.Fatalf("exit/stdout = %d/%q, want 2/empty", exit, stdout)
		}
		want := fmt.Sprintf(
			"%s:2:1: unsupported_capability: feature is not classified by the v0 support registry "+
				"[config-extension/futureMagic]; chain: %s -> %s@%s:3:12\n",
			canonicalChild, canonicalRoot, canonicalChild, root,
		)
		if stderr != want {
			t.Fatalf("stderr = %q, want %q", stderr, want)
		}
	})

	t.Run("missing child", func(t *testing.T) {
		directory := t.TempDir()
		root := filepath.Join(directory, "root.yaml")
		missing := filepath.Join(directory, "missing.yaml")
		writeCLIFlow(t, root, "- runFlow: missing.yaml\n")
		canonicalRoot := canonicalTestPath(t, root)
		_, cause := os.Stat(missing)
		if cause == nil {
			t.Fatal("missing fixture unexpectedly exists")
		}

		exit, stdout, stderr := invokeCLIProcess(t, []string{"check-syntax", root}, "")
		if exit != 2 || stdout != "" {
			t.Fatalf("exit/stdout = %d/%q, want 2/empty", exit, stdout)
		}
		want := fmt.Sprintf(
			"%s:3:12: missing_link: linked path does not exist; chain: %s -> %s@%s:3:12: %v\n",
			root, canonicalRoot, missing, root, cause,
		)
		if stderr != want {
			t.Fatalf("stderr = %q, want %q", stderr, want)
		}
	})

	t.Run("active cycle", func(t *testing.T) {
		directory := t.TempDir()
		root := filepath.Join(directory, "root.yaml")
		child := filepath.Join(directory, "child.yaml")
		writeCLIFlow(t, root, "- runFlow: child.yaml\n")
		writeCLIFlow(t, child, "- runFlow: root.yaml\n")
		canonicalRoot := canonicalTestPath(t, root)
		canonicalChild := canonicalTestPath(t, child)

		exit, stdout, stderr := invokeCLIProcess(t, []string{"check-syntax", root}, "")
		if exit != 2 || stdout != "" {
			t.Fatalf("exit/stdout = %d/%q, want 2/empty", exit, stdout)
		}
		want := fmt.Sprintf(
			"%s:3:12: active_cycle: flow link creates an active-path cycle; chain: %s -> %s@%s:3:12 | %s -> %s@%s:3:12\n",
			canonicalChild, canonicalRoot, canonicalChild, root, canonicalChild, canonicalRoot, canonicalChild,
		)
		if stderr != want {
			t.Fatalf("stderr = %q, want %q", stderr, want)
		}
	})
}

func TestCLIHelperProcess(t *testing.T) {
	if os.Getenv("FLOWBATON_CLI_HELPER") != "1" {
		return
	}
	separator := -1
	for index, argument := range os.Args {
		if argument == "--" {
			separator = index
			break
		}
	}
	if separator < 0 {
		os.Exit(97)
	}
	os.Exit(run(os.Args[separator+1:], os.Stdin, os.Stdout, os.Stderr))
}

func invokeCLIProcess(t *testing.T, args []string, stdin string) (int, string, string) {
	t.Helper()
	return invokeCLIProcessInDir(t, args, stdin, "")
}

func invokeCLIProcessInDir(t *testing.T, args []string, stdin, directory string) (int, string, string) {
	t.Helper()
	processArgs := append([]string{"-test.run=^TestCLIHelperProcess$", "--"}, args...)
	command := exec.Command(os.Args[0], processArgs...)
	command.Env = append(os.Environ(), "FLOWBATON_CLI_HELPER=1")
	command.Dir = directory
	command.Stdin = bytes.NewBufferString(stdin)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if err == nil {
		return 0, stdout.String(), stderr.String()
	}
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) {
		t.Fatalf("run helper process: %v", err)
	}
	return exitError.ExitCode(), stdout.String(), stderr.String()
}

func writeCLIFlow(t *testing.T, path, commands string) {
	t.Helper()
	contents := "appId: com.example.app\n---\n" + commands
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write flow %s: %v", path, err)
	}
}

func canonicalTestPath(t *testing.T, path string) string {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("canonicalize %s: %v", path, err)
	}
	return filepath.Clean(canonical)
}

type commandCheckerFunc func(context.Context, flowcli.Source) error

func (function commandCheckerFunc) Check(ctx context.Context, source flowcli.Source) error {
	return function(ctx, source)
}
