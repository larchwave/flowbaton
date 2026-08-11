package memory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/larchwave/flowbaton/internal/explore"
)

// Plans saves and loads scenario plans as readable markdown under
// stateDir/plans. Save then Load returns an equal Plan value.
type Plans struct {
	dir string
}

// NewPlans returns a store rooted at stateDir/plans.
func NewPlans(stateDir string) *Plans {
	return &Plans{dir: filepath.Join(stateDir, "plans")}
}

// SavePlan writes the plan to <name>.md.
func (p *Plans) SavePlan(name string, plan *explore.Plan) error {
	if plan == nil {
		return errors.New("plans: nil plan")
	}
	path, err := p.planPath(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(p.dir, 0o755); err != nil {
		return fmt.Errorf("plans: create directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(renderPlan(plan)), 0o644); err != nil {
		return fmt.Errorf("plans: write %s: %w", filepath.Base(path), err)
	}
	return nil
}

// LoadPlan reads <name>.md back into a Plan.
func (p *Plans) LoadPlan(name string) (*explore.Plan, error) {
	path, err := p.planPath(name)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("plans: read %s: %w", filepath.Base(path), err)
	}
	plan, err := parsePlan(string(data))
	if err != nil {
		return nil, fmt.Errorf("plans: parse %s: %w", filepath.Base(path), err)
	}
	return plan, nil
}

func (p *Plans) planPath(name string) (string, error) {
	if name == "" || name != filepath.Base(name) || name == "." || name == ".." {
		return "", fmt.Errorf("plans: unusable plan name %q", name)
	}
	return filepath.Join(p.dir, name+".md"), nil
}

func renderPlan(plan *explore.Plan) string {
	var b strings.Builder
	b.WriteString("# Exploration plan\n\n")
	b.WriteString("App: " + plan.AppID + "\n")
	b.WriteString("Created: " + plan.CreatedAt.Format(time.RFC3339Nano) + "\n")
	for _, scenario := range plan.Scenarios {
		b.WriteString("\n## Scenario: " + scenario.Name + "\n\n")
		writeField(&b, "Priority", string(scenario.Priority))
		writeField(&b, "Style", scenario.Style)
		writeField(&b, "Status", string(scenario.Status))
		writeField(&b, "Start screen", scenario.StartScreen)
		writeBullets(&b, "Steps", scenario.Steps)
		writeBullets(&b, "Expected", scenario.Expected)
	}
	return b.String()
}

func writeField(b *strings.Builder, label, value string) {
	if value != "" {
		b.WriteString(label + ": " + value + "\n")
	}
}

func writeBullets(b *strings.Builder, label string, items []string) {
	if len(items) == 0 {
		return
	}
	b.WriteString("\n### " + label + "\n\n")
	for _, item := range items {
		b.WriteString("- " + item + "\n")
	}
}

func parsePlan(content string) (*explore.Plan, error) {
	plan := &explore.Plan{}
	var scenario *explore.Scenario
	bullets := ""
	flush := func() {
		if scenario != nil {
			plan.Scenarios = append(plan.Scenarios, *scenario)
		}
	}
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "## Scenario:"):
			flush()
			scenario = &explore.Scenario{Name: strings.TrimSpace(strings.TrimPrefix(trimmed, "## Scenario:"))}
			bullets = ""
		case trimmed == "### Steps":
			bullets = "steps"
		case trimmed == "### Expected":
			bullets = "expected"
		case bullets != "" && strings.HasPrefix(trimmed, "- "):
			if scenario == nil {
				return nil, errors.New("bullet outside a scenario")
			}
			item := strings.TrimPrefix(trimmed, "- ")
			if bullets == "steps" {
				scenario.Steps = append(scenario.Steps, item)
			} else {
				scenario.Expected = append(scenario.Expected, item)
			}
		case scenario == nil && strings.HasPrefix(trimmed, "App:"):
			plan.AppID = strings.TrimSpace(strings.TrimPrefix(trimmed, "App:"))
		case scenario == nil && strings.HasPrefix(trimmed, "Created:"):
			created, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(strings.TrimPrefix(trimmed, "Created:")))
			if err != nil {
				return nil, fmt.Errorf("created timestamp: %w", err)
			}
			plan.CreatedAt = created
		case scenario != nil && strings.HasPrefix(trimmed, "Priority:"):
			scenario.Priority = explore.Priority(strings.TrimSpace(strings.TrimPrefix(trimmed, "Priority:")))
		case scenario != nil && strings.HasPrefix(trimmed, "Style:"):
			scenario.Style = strings.TrimSpace(strings.TrimPrefix(trimmed, "Style:"))
		case scenario != nil && strings.HasPrefix(trimmed, "Status:"):
			scenario.Status = explore.ScenarioStatus(strings.TrimSpace(strings.TrimPrefix(trimmed, "Status:")))
		case scenario != nil && strings.HasPrefix(trimmed, "Start screen:"):
			scenario.StartScreen = strings.TrimSpace(strings.TrimPrefix(trimmed, "Start screen:"))
		}
	}
	flush()
	return plan, nil
}
