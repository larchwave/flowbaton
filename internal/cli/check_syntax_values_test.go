package cli

import (
	"context"
	"strings"
	"testing"
)

// check_syntax is what a model calls before spending a device run, so anything
// Execute refuses should be refused here. Parsing plus capability preflight
// catches shape and support, never a VALUE: the engine's compile step is where
// `repeat: -1` and a 100% coordinate are rejected, and that step needs no
// device -- compileProgram runs "before any runtime or device dependency is
// constructed".
func TestCheckSyntaxRefusesValuesTheEngineWillRefuse(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	checker := NewParserChecker()
	refused := map[string]string{
		"negative repeat":        "appId: com.example\n---\n- tapOn:\n    text: Hi\n    repeat: -1\n",
		"percentage at 100":      "appId: com.example\n---\n- tapOn:\n    point: '100%,50%'\n",
		"negative absolute":      "appId: com.example\n---\n- tapOn:\n    point: '-1,30'\n",
		"non integer coordinate": "appId: com.example\n---\n- tapOn:\n    point: 'left,30'\n",
	}
	for name, yaml := range refused {
		t.Run(name, func(t *testing.T) {
			err := checker.Check(context.Background(), Source{
				Name: "-", BaseDir: base, ConfineTo: base, Data: []byte(yaml),
			})
			if err == nil {
				t.Errorf("check reported ok for a flow the engine refuses")
			}
		})
	}
}

// Compilation happens before interpolation and defers an expression to
// evaluation, so a flow whose value arrives from the environment must still
// check clean. Rejecting these would make the tool lie the other way.
func TestCheckSyntaxKeepsAcceptingDeferredValues(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	checker := NewParserChecker()
	accepted := map[string]string{
		"plain flow":           "appId: com.example\n---\n- launchApp\n",
		"interpolated point":   "appId: com.example\n---\n- tapOn:\n    point: '${POINT}'\n",
		"interpolated text":    "appId: com.example\n---\n- tapOn:\n    text: '${LABEL}'\n",
		"valid repeat":         "appId: com.example\n---\n- tapOn:\n    text: Hi\n    repeat: 3\n",
		"percentage below 100": "appId: com.example\n---\n- tapOn:\n    point: '99%,50%'\n",
	}
	for name, yaml := range accepted {
		t.Run(name, func(t *testing.T) {
			if err := checker.Check(context.Background(), Source{
				Name: "-", BaseDir: base, ConfineTo: base, Data: []byte(yaml),
			}); err != nil {
				t.Errorf("check refused a valid flow: %v", err)
			}
		})
	}
	_ = strings.TrimSpace
}
