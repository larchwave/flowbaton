package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/nohavewho/flowbaton/internal/capability"
	"github.com/nohavewho/flowbaton/internal/enginetest"
	"github.com/nohavewho/flowbaton/internal/model"
)

// A flow's own `env:` block takes precedence over operator input and a parent
// runFlow environment. specs/03-cli-tooling.md:30 applies injected values first
// and the flow's declarations last.

func envFlow(t *testing.T, header, body string) *Program {
	t.Helper()
	path := filepath.Join(t.TempDir(), "flow.yaml")
	contents := "appId: com.example\n" + header + "---\n" + body + "\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	program, err := Prepare(context.Background(),
		model.ExecutionPlan{SelectedRoots: []string{path}}, capability.FileLoader{})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	return program
}

func runWithExternal(t *testing.T, program *Program, external map[string]string) error {
	t.Helper()
	_, err := Execute(context.Background(), program, Dependencies{
		ExecutionID:         "env-precedence",
		ExternalEnvironment: external,
		Driver:              enginetest.NewFakeDriver(),
		Clock:               newAdvancingClock(),
		JSFactory:           tapJSFactory(t),
		Controller:          NoopController{},
	})
	return err
}

func TestAFlowsOwnEnvBeatsTheOperatorsEnv(t *testing.T) {
	t.Parallel()

	program := envFlow(t, "env:\n  NAME: config\n", "- assertTrue: ${NAME == \"config\"}")
	if err := runWithExternal(t, program, map[string]string{"NAME": "external"}); err != nil {
		t.Fatalf("the operator's -e won over the flow's own env: %v", err)
	}
}

// The operator's value is still there for anything the flow does NOT declare,
// which is what -e is for.
func TestTheOperatorsEnvStillArrivesForUndeclaredNames(t *testing.T) {
	t.Parallel()

	program := envFlow(t, "env:\n  NAME: config\n", "- assertTrue: ${OTHER == \"external\"}")
	if err := runWithExternal(t, program, map[string]string{"OTHER": "external"}); err != nil {
		t.Fatalf("an undeclared name did not reach the flow: %v", err)
	}
}
