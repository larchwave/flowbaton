package cli

import (
	"os"

	"github.com/larchwave/flowbaton/internal/aiengine"
	"github.com/larchwave/flowbaton/internal/explore"
	"github.com/larchwave/flowbaton/internal/explore/export"
	"github.com/larchwave/flowbaton/internal/explore/memory"
	"github.com/larchwave/flowbaton/internal/explore/planning"
	"github.com/larchwave/flowbaton/internal/explore/report"
	"github.com/larchwave/flowbaton/internal/explore/research"
	"github.com/larchwave/flowbaton/internal/explore/run"
)

// ProductionExploreRunner returns the fully assembled explore command:
// chat models from the environment and the production crew over the
// session driver. The zero ExploreRunner stays refusal-only so tests can
// assert the unassembled error; the entry point calls this instead.
func ProductionExploreRunner() ExploreRunner {
	return ExploreRunner{
		NewModels: DefaultExploreModels,
		NewCrew:   DefaultExploreCrew,
	}
}

// DefaultExploreModels builds the tiered chat models from the process
// environment through the aiengine provider wall.
func DefaultExploreModels(getenv func(string) string) (explore.ModelSet, error) {
	return aiengine.ChatModelsFromEnv(getenv)
}

// DefaultExploreCrew assembles the production role implementations over
// one open driver. Memory stores live under the configured state
// directory; the research cache keeps UI maps across sessions.
func DefaultExploreCrew(deps ExploreDeps) (explore.Crew, error) {
	config := deps.Config
	experience := memory.NewExperience(config.StateDir)
	knowledge := memory.NewKnowledge(config.StateDir, os.Getenv)
	cache := memory.NewResearchCache(config.StateDir, memory.DefaultResearchTTL, config.Clock)

	observer := &research.Observer{
		Driver: deps.Driver,
		AppID:  config.AppID,
		Clock:  config.Clock,
	}
	crew := explore.Crew{
		Observer: observer,
		Researcher: &research.Researcher{
			Models: deps.Models,
			Cache:  cache,
			Clock:  config.Clock,
		},
		Planner: &planning.Planner{
			LLM:       deps.Models.Worker,
			Knowledge: knowledge,
		},
		Tester: &run.Tester{
			Driver:   deps.Driver,
			Observer: observer,
			Models:   deps.Models,
			Config:   config,
		},
		Navigator: &run.Navigator{
			Driver:     deps.Driver,
			Observer:   observer,
			Worker:     deps.Models.Worker,
			Config:     config,
			Experience: experience,
		},
		Analyst:    report.Analyst{Manager: deps.Models.Manager},
		Exporter:   export.Exporter{},
		Experience: experience,
		Knowledge:  knowledge,
	}
	return crew, nil
}
