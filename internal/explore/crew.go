package explore

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

// Crew bundles the role implementations for one exploration session.
type Crew struct {
	Observer   Observer
	Researcher Researcher
	Planner    Planner
	Tester     Tester
	Navigator  Navigator
	Analyst    Analyst
	Exporter   Exporter
	Experience ExperienceStore
	Knowledge  KnowledgeStore
}

func (c Crew) validate() error {
	missing := []string{}
	if c.Observer == nil {
		missing = append(missing, "observer")
	}
	if c.Researcher == nil {
		missing = append(missing, "researcher")
	}
	if c.Planner == nil {
		missing = append(missing, "planner")
	}
	if c.Tester == nil {
		missing = append(missing, "tester")
	}
	if c.Navigator == nil {
		missing = append(missing, "navigator")
	}
	if c.Analyst == nil {
		missing = append(missing, "analyst")
	}
	if len(missing) > 0 {
		return fmt.Errorf("explore: crew missing roles: %v", missing)
	}
	return nil
}

// RunSession drives the deterministic strategic loop: observe, research,
// plan in the current style, execute pending scenarios by priority, and
// report. Tactical decisions live inside the role implementations. The
// session stops when the test budget is spent, planning dries up across a
// full style rotation, or the context ends.
func RunSession(ctx context.Context, config Config, crew Crew) (*SessionReport, error) {
	if config.MaxTests <= 0 {
		return nil, errors.New("explore: test budget must be positive")
	}
	if err := crew.validate(); err != nil {
		return nil, err
	}
	styles := config.Styles
	if len(styles) == 0 {
		styles = []string{"normal"}
	}

	report := &SessionReport{
		AppID:    config.AppID,
		Platform: config.Platform,
		Started:  config.Now(),
	}
	state, err := crew.Navigator.EnsureReady(ctx)
	if err != nil {
		return nil, fmt.Errorf("explore: prepare app: %w", err)
	}

	executed := 0
	dryRounds := 0
	planned := []string{}
	unpromised := []string{}
	for iteration := 0; executed < config.MaxTests && dryRounds < len(styles); iteration++ {
		if err := ctx.Err(); err != nil {
			return finishReport(report, config), err
		}
		if state == nil {
			state, err = crew.Observer.Observe(ctx)
			if err != nil {
				return finishReport(report, config), fmt.Errorf("explore: observe: %w", err)
			}
		}
		uiMap, err := crew.Researcher.Research(ctx, state)
		if err != nil {
			return finishReport(report, config), fmt.Errorf("explore: research: %w", err)
		}
		scenarios, err := crew.Planner.PlanNext(ctx, PlanRequest{
			Map:        uiMap,
			Style:      styles[iteration%len(styles)],
			Existing:   planned,
			Unpromised: unpromised,
			Budget:     config.MaxTests - executed,
		})
		if err != nil {
			return finishReport(report, config), fmt.Errorf("explore: plan: %w", err)
		}
		if len(scenarios) == 0 {
			dryRounds++
			state = nil
			continue
		}
		dryRounds = 0
		plan := Plan{AppID: config.AppID, Scenarios: scenarios}
		for _, scenario := range plan.Pending() {
			if executed >= config.MaxTests {
				break
			}
			planned = append(planned, scenario.Name)
			result, err := crew.Tester.RunScenario(ctx, scenario, state)
			if result != nil {
				report.Results = append(report.Results, *result)
				unpromised = collectUnpromised(unpromised, *result)
			}
			if err != nil {
				return finishReport(report, config), fmt.Errorf("explore: scenario %q: %w", scenario.Name, err)
			}
			executed++
			state = nil
		}
	}

	markdown, err := crew.Analyst.Report(ctx, report)
	if err != nil {
		return finishReport(report, config), fmt.Errorf("explore: report: %w", err)
	}
	report.Markdown = markdown
	return finishReport(report, config), nil
}

func finishReport(report *SessionReport, config Config) *SessionReport {
	report.Finished = config.Now()
	return report
}

// collectUnpromised adds the expected outcomes a run's judge ruled this
// app never offers. Driver probes are the run's own evidence, not
// scenario outcomes, so they never enter the list, and a repeat of one
// already listed is dropped.
func collectUnpromised(known []string, result TestResult) []string {
	for _, check := range result.Outcomes {
		if check.Met || !check.Inapplicable || check.Driver {
			continue
		}
		if slices.Contains(known, check.Expected) {
			continue
		}
		known = append(known, check.Expected)
	}
	return known
}
