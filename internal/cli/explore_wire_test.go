package cli

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/explore"
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
		crew.Exporter == nil || crew.Experience == nil || crew.Knowledge == nil {
		t.Fatalf("assembled crew has a nil role: %+v", crew)
	}
	if _, err := crew.Experience.Index(context.Background(), explore.ScreenSignature{AppID: "com.example.app", TreeDigest: "d"}); err != nil {
		t.Fatalf("experience store unusable: %v", err)
	}
}

// fakeExploreDriver is the minimal driver stand-in for assembly tests; the
// crew never operates it here.
type fakeExploreDriver struct {
	device.Driver
}
