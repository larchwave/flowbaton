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
		// The report is already built and carries the session header. The
		// caller writes artifacts from whatever it is handed, so throwing it
		// away here costs the failed run the little evidence it has.
		return report, fmt.Errorf("explore: prepare app: %w", err)
	}

	executed := 0
	dryRounds := 0
	planned := []string{}
	unmet := []string{}
	for iteration := 0; executed < config.MaxTests && dryRounds < len(styles); iteration++ {
		if err := ctx.Err(); err != nil {
			return finishReport(report, config), err
		}
		if state == nil {
			state, err = crew.Observer.Observe(ctx)
			if err != nil {
				return abortReport(ctx, crew, report, config), fmt.Errorf("explore: observe: %w", err)
			}
		}
		uiMap, err := crew.Researcher.Research(ctx, state)
		if err != nil {
			return abortReport(ctx, crew, report, config), fmt.Errorf("explore: research: %w", err)
		}
		scenarios, err := crew.Planner.PlanNext(ctx, PlanRequest{
			Map:      uiMap,
			Style:    styles[iteration%len(styles)],
			Existing: planned,
			Unmet:    unmet,
			Budget:   config.MaxTests - executed,
		})
		if err != nil {
			return abortReport(ctx, crew, report, config), fmt.Errorf("explore: plan: %w", err)
		}
		if len(scenarios) == 0 {
			dryRounds++
			state = nil
			continue
		}
		dryRounds = 0
		// The screen the plan was made against. The planner is asked to
		// record it on each scenario and does not always: mmx74's "Open
		// inbox from footer" carried none, so nothing checked where its run
		// began and its flow failed on its first action. The crew is holding
		// the answer either way -- this is the state the UI map came from.
		planScreen := state.Signature.Key()
		plan := Plan{AppID: config.AppID, Scenarios: scenarios}
		for _, scenario := range plan.Pending() {
			if executed >= config.MaxTests {
				break
			}
			planned = append(planned, scenario.Name)
			// The planner wrote this scenario against the UI map of one
			// screen and recorded that screen's key. The relaunch above
			// cannot put the app back on it: it kills and relaunches, and
			// an app that restores its last view relaunches on that view.
			// Nine of thirty-four replayed flows failed on their first
			// action for that reason -- six of them on 2026-09-01 were
			// Calendar, restored into its years view, where not one
			// recorded start screen exists.
			//
			// Reach is skipped for a scenario already standing on its
			// screen, which is the common case and costs nothing.
			reachNote := ""
			var walk []StepRecord
			startScreen := scenario.StartScreen
			if startScreen == "" {
				startScreen = planScreen
			}
			if key := startScreen; key != "" && state != nil &&
				!state.Signature.NamesTheSameScreen(key) {
				reached, reachSteps, reachErr := crew.Navigator.Reach(ctx, key)
				switch {
				case reachErr == nil:
					state, walk = reached, reachSteps
					// A reach that works leaves no trace otherwise, and two
					// sessions of "no failed reach" then read as proof of a
					// navigator that may never have been asked.
					reachNote = fmt.Sprintf(
						"walked to the start screen %q in %d steps", key, len(reachSteps))
				case ctx.Err() != nil:
					return finishReport(report, config), ctx.Err()
				default:
					// Not a failed session. The scenario runs from wherever
					// the relaunch left the app, which is what every
					// scenario did before Reach was wired, and the note is
					// what tells the report's reader why it began somewhere
					// else.
					reachNote = fmt.Sprintf(
						"could not reach the start screen %q: %v", key, reachErr)
				}
			}
			result, err := crew.Tester.RunScenario(ctx, scenario, state)
			if result != nil {
				result.Prelude = walk
				if reachNote != "" {
					result.Notes = append(result.Notes, reachNote)
				}
				if err != nil && result.Verdict == "" {
					// "run stopped before a verdict" is also what a spent step
					// budget says. A reader of the report has to be able to
					// tell a dead device from a slow model (session mmx23).
					result.Verdict = "the run ended on an error: " + truncateCause(err.Error())
				}
				report.Results = append(report.Results, *result)
				unmet = collectUnmet(unmet, *result)
			}
			if err != nil {
				// An app that left the foreground is one relaunch from
				// usable, and the end of this loop relaunches. mmx70 lost
				// five of its six scenarios to one: its first swipe ran, the
				// observation after it answered "none of com.apple.mobilecal
				// is in the foreground", and the five scenarios that had not
				// started were never attempted. WHY the app left is not
				// established -- a direction-only swipe up starts at the
				// centre of the screen, and replaying one against Calendar
				// leaves the app in front. A device that is unreachable
				// still ends the session: no relaunch reaches a dead runner.
				if !errors.Is(err, ErrScreenUnobservable) {
					return abortReport(ctx, crew, report, config),
						fmt.Errorf("explore: scenario %q: %w", scenario.Name, err)
				}
				if last := len(report.Results) - 1; last >= 0 {
					report.Results[last].Notes = append(report.Results[last].Notes,
						"the app left the foreground; relaunched for the next scenario: "+
							truncateCause(err.Error()))
				}
			}
			executed++
			if executed >= config.MaxTests {
				// Nothing left to prepare for. A reset here could only fail
				// a session whose work is already done.
				break
			}
			// Start the next scenario from a freshly launched process
			// rather than from whatever this one left behind. The planner
			// wrote its scenarios against the UI map of the start screen,
			// and the flow exported from a run records actions without the
			// screen they began on (mmx36: 1 of 4 replayed standalone).
			//
			// This does NOT guarantee the start screen. EnsureReady kills
			// and relaunches, which keeps data -- the reminders an earlier
			// scenario created are still there -- but an app that restores
			// its last screen comes back on it. See EnsureReady in
			// run/navigator.go for the measurement.
			state, err = crew.Navigator.EnsureReady(ctx)
			if err != nil {
				return abortReport(ctx, crew, report, config),
					fmt.Errorf("explore: relaunch the app after %q: %w", scenario.Name, err)
			}
		}
	}

	markdown, err := crew.Analyst.Report(ctx, report)
	if err != nil {
		return finishReport(report, config), fmt.Errorf("explore: report: %w", err)
	}
	report.Markdown = markdown
	return finishReport(report, config), nil
}

// abortReport finishes a report for a session that ended early. The runs
// that did complete are the evidence the operator came for -- a device
// that died in the third scenario must not take the first one's passing
// flow with it (session mmx22, 2026-08-30). Writing the prose is
// best-effort: a report the analyst cannot produce must never replace the
// failure that ended the session.
func abortReport(ctx context.Context, crew Crew, report *SessionReport, config Config) *SessionReport {
	if len(report.Results) > 0 && crew.Analyst != nil {
		if markdown, err := crew.Analyst.Report(ctx, report); err == nil {
			report.Markdown = markdown
		}
	}
	return finishReport(report, config)
}

// truncateCause keeps an error readable in a report line.
func truncateCause(cause string) string { return Truncate(cause, 160) }

func finishReport(report *SessionReport, config Config) *SessionReport {
	report.Finished = config.Now()
	return report
}

// collectUnmet adds the expected outcomes a run looked for and did not find.
// Driver probes are the run's own evidence, not scenario outcomes, so they
// never enter the list, and a repeat of one already listed is dropped.
//
// Every unmet outcome counts, not only the ones a judge called inapplicable.
// That narrower feed never fired once, which left the planner free to rebuild
// a scenario around the same missing thing every round.
func collectUnmet(known []string, result TestResult) []string {
	for _, check := range result.Outcomes {
		if check.Met || check.Driver {
			continue
		}
		if slices.Contains(known, check.Expected) {
			continue
		}
		known = append(known, check.Expected)
	}
	return known
}
