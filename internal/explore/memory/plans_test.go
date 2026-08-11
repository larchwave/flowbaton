package memory

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/explore"
)

func TestPlansRoundTrip(t *testing.T) {
	t.Parallel()
	store := NewPlans(t.TempDir())
	plan := &explore.Plan{
		AppID:     "com.example.shop",
		CreatedAt: time.Date(2026, 8, 10, 12, 30, 45, 123456789, time.UTC),
		Scenarios: []explore.Scenario{
			{
				Name:        "add an item to the cart",
				Priority:    explore.PriorityCritical,
				Style:       "normal",
				StartScreen: "catalog-abcdef12",
				Steps:       []string{"tap the first product", "tap Add to cart"},
				Expected:    []string{"the cart badge shows 1", "a confirmation toast appears"},
				Status:      explore.ScenarioPending,
			},
			{
				Name:     "log out",
				Priority: explore.PriorityLow,
				Status:   explore.ScenarioPassed,
				Expected: []string{"the login screen appears"},
			},
		},
	}

	if err := store.SavePlan("shop-main", plan); err != nil {
		t.Fatalf("SavePlan() error = %v", err)
	}
	loaded, err := store.LoadPlan("shop-main")
	if err != nil {
		t.Fatalf("LoadPlan() error = %v", err)
	}
	if loaded.AppID != plan.AppID {
		t.Fatalf("AppID = %q, want %q", loaded.AppID, plan.AppID)
	}
	if !loaded.CreatedAt.Equal(plan.CreatedAt) {
		t.Fatalf("CreatedAt = %v, want %v", loaded.CreatedAt, plan.CreatedAt)
	}
	if !reflect.DeepEqual(loaded.Scenarios, plan.Scenarios) {
		t.Fatalf("Scenarios = %#v, want %#v", loaded.Scenarios, plan.Scenarios)
	}
}

func TestPlansMarkdownIsReadable(t *testing.T) {
	t.Parallel()
	plan := &explore.Plan{
		AppID:     "com.example.shop",
		CreatedAt: time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC),
		Scenarios: []explore.Scenario{{
			Name:     "log out",
			Priority: explore.PriorityHigh,
			Expected: []string{"the login screen appears"},
		}},
	}
	rendered := renderPlan(plan)
	for _, want := range []string{
		"# Exploration plan",
		"App: com.example.shop",
		"## Scenario: log out",
		"Priority: high",
		"### Expected",
		"- the login screen appears",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("renderPlan() lacks %q:\n%s", want, rendered)
		}
	}
}

func TestPlansLoadMissingFileFails(t *testing.T) {
	t.Parallel()
	store := NewPlans(t.TempDir())
	if _, err := store.LoadPlan("absent"); err == nil {
		t.Fatal("LoadPlan() error = nil, want an error")
	}
}

func TestPlansRejectsUnusableNames(t *testing.T) {
	t.Parallel()
	store := NewPlans(t.TempDir())
	for _, name := range []string{"", "..", "a/b", "../escape"} {
		if err := store.SavePlan(name, &explore.Plan{}); err == nil {
			t.Fatalf("SavePlan(%q) error = nil, want an error", name)
		}
	}
}
