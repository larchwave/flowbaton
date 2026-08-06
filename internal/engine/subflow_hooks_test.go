package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/larchwave/flowbaton/internal/capability"
	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/model"
)

// The flow contract runs start hooks, the subflow body, and completion hooks in
// that order. A declared start hook must never suppress authored body commands.

func hookFlowFiles(t *testing.T, childHeader string) string {
	t.Helper()
	root := t.TempDir()
	write := func(name, contents string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("main.yaml", "appId: com.example\n---\n- runFlow: child.yaml\n")
	write("child.yaml", "appId: com.example\n"+childHeader+"---\n- assertTrue: \"false\"\n")
	return filepath.Join(root, "main.yaml")
}

func runHookFlow(t *testing.T, path string) error {
	t.Helper()
	program, err := Prepare(context.Background(),
		model.ExecutionPlan{SelectedRoots: []string{path}}, capability.FileLoader{})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	_, err = Execute(context.Background(), program, Dependencies{
		ExecutionID: "subflow-hooks",
		Driver:      enginetest.NewFakeDriver(),
		Clock:       newAdvancingClock(),
		JSFactory:   tapJSFactory(t),
		Controller:  NoopController{},
	})
	return err
}

func TestASubflowsBodyRunsEvenWhenItDeclaresAStartHook(t *testing.T) {
	t.Parallel()

	path := hookFlowFiles(t, "onFlowStart:\n  - assertTrue: \"true\"\n")
	if err := runHookFlow(t, path); err == nil {
		t.Fatal("the child's body did not run: a failing subflow reported success")
	}
}

// A child without hooks also executes its body.
func TestASubflowWithNoHooksStillRunsItsBody(t *testing.T) {
	t.Parallel()

	if err := runHookFlow(t, hookFlowFiles(t, "")); err == nil {
		t.Fatal("a failing subflow with no hooks reported success")
	}
}

// A completion hook also preserves body execution.
func TestASubflowsBodyRunsAlongsideACompletionHook(t *testing.T) {
	t.Parallel()

	path := hookFlowFiles(t, "onFlowComplete:\n  - assertTrue: \"true\"\n")
	if err := runHookFlow(t, path); err == nil {
		t.Fatal("the child's body did not run beside its completion hook")
	}
}
