package cli

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/explore"
	"github.com/larchwave/flowbaton/internal/explore/research"
	"github.com/larchwave/flowbaton/internal/explore/run"
)

type wireLLM struct{}

func (wireLLM) Chat(context.Context, explore.ChatRequest) (explore.ChatResponse, error) {
	return explore.ChatResponse{}, errors.New("wire test model is inert")
}

func TestProductionExploreRunnerCarriesBothSeams(t *testing.T) {
	runner := ProductionExploreRunner()
	if runner.NewModels == nil || runner.NewCrew == nil {
		t.Fatalf("production runner left a seam nil: %+v", runner)
	}
}

func TestDefaultExploreModelsFailsClosedWithoutKeys(t *testing.T) {
	models, err := DefaultExploreModels(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if models.Worker != nil || models.Manager != nil || models.Vision != nil {
		t.Fatalf("expected a zero model set without keys, got %+v", models)
	}
}

func TestDefaultExploreCrewAssemblesEveryRole(t *testing.T) {
	inert := wireLLM{}
	clock := func() time.Time { return time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC) }
	crew, err := DefaultExploreCrew(ExploreDeps{
		Driver: &fakeExploreDriver{},
		Models: explore.ModelSet{Worker: inert, Manager: inert, Vision: inert},
		Config: explore.Config{
			AppID:           "com.example.app",
			Platform:        "android",
			StateDir:        t.TempDir(),
			OutputDir:       t.TempDir(),
			MaxTests:        1,
			MaxStepsPerTest: 5,
			Clock:           clock,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if crew.Observer == nil || crew.Researcher == nil || crew.Planner == nil ||
		crew.Tester == nil || crew.Navigator == nil || crew.Analyst == nil ||
		crew.Exporter == nil {
		t.Fatalf("assembled crew has a nil role: %+v", crew)
	}
	// The experience store reaches the session through the navigator, which
	// is the only thing that reads or writes it. The Crew carried a second
	// copy that nothing read.
	navigator, ok := crew.Navigator.(*run.Navigator)
	if !ok || navigator.Experience == nil {
		t.Fatalf("the assembled navigator holds no experience store: %+v", crew.Navigator)
	}
	if _, err := navigator.Experience.Index(context.Background(), explore.ScreenSignature{AppID: "com.example.app", TreeDigest: "d"}); err != nil {
		t.Fatalf("experience store unusable: %v", err)
	}
}

// fakeExploreDriver is the minimal driver stand-in for assembly tests; the
// crew never operates it here.
type fakeExploreDriver struct {
	device.Driver
}

// Both of the observer's diagnostics run through Logf, and a nil Logf drops
// them: the "screen still moving, capturing anyway" line is exactly what
// explains a session whose first observation caught a screen mid-animation.
func TestTheAssembledObserverReportsItsDiagnostics(t *testing.T) {
	var out bytes.Buffer
	crew, err := DefaultExploreCrew(ExploreDeps{
		Driver: &fakeExploreDriver{},
		Models: explore.ModelSet{Worker: wireLLM{}, Manager: wireLLM{}, Vision: wireLLM{}},
		Stdout: &out,
		Config: explore.Config{
			AppID: "com.example.app", Platform: "android",
			StateDir: t.TempDir(), OutputDir: t.TempDir(),
			MaxTests: 1, MaxStepsPerTest: 5,
			Clock: func() time.Time { return time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC) },
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	observer, ok := crew.Observer.(*research.Observer)
	if !ok {
		t.Fatalf("crew.Observer is %T, not the production observer", crew.Observer)
	}
	if observer.Logf == nil {
		t.Fatal("the assembled observer has no way to report why a capture looked wrong")
	}
	observer.Logf("screen still moving after %s, capturing anyway", "3s")
	if got := out.String(); got != "screen still moving after 3s, capturing anyway\n" {
		t.Fatalf("diagnostic did not reach the session output: %q", got)
	}
}
