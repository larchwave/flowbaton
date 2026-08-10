// Package explore implements autonomous AI-driven exploratory testing of
// mobile applications on top of the platform-neutral device driver surface.
//
// The package is peripheral, like internal/aiengine: it depends on
// internal/device (Driver, TreeNode) and internal/model (command keywords for
// flow export), and is wired together only in internal/cli. It must never be
// imported by internal/engine, internal/device, or internal/capability.
//
// The workflow is deterministic at the strategic level and AI-driven at the
// tactical level: an exploration session observes the current screen,
// researches it into a UI map, plans prioritized scenarios, executes them
// step by step through the driver, verifies outcomes, reports findings, and
// exports passing runs as runnable flow YAML. Learned per-screen recipes and
// operator-authored hints persist across sessions.
package explore

import (
	"errors"
	"time"
)

// ErrNoAIProvider reports that exploration cannot start because no AI
// provider is configured. Exploration is AI-driven and fails closed.
var ErrNoAIProvider = errors.New("explore: no AI provider configured")

// ErrBudgetExhausted reports that the session test budget was spent.
var ErrBudgetExhausted = errors.New("explore: test budget exhausted")

// Config carries the resolved settings for one exploration session.
// internal/cli parses flags and environment into this struct.
type Config struct {
	// AppID is the application under exploration (bundle id or package).
	AppID string
	// Platform is the selected execution platform token name
	// (android, ios, ios-physical, web).
	Platform string
	// StateDir is the per-app persistent directory holding knowledge,
	// learned recipes, plans, and research caches across sessions.
	StateDir string
	// OutputDir receives per-session artifacts: reports, exported flows,
	// screenshots, recordings.
	OutputDir string
	// MaxTests bounds how many scenarios the session may execute.
	// Zero means the caller must supply a positive budget; the runner
	// refuses a zero budget rather than defaulting to unlimited.
	MaxTests int
	// MaxStepsPerTest bounds the tester tool loop for one scenario.
	MaxStepsPerTest int
	// Styles are planning style names rotated across iterations.
	Styles []string
	// PilotEnabled turns on the supervisor conversation. When disabled,
	// scenario verdicts fall back to expected-outcome matching alone.
	PilotEnabled bool
	// RecordVideo starts a screen recording for the session when the
	// platform supports it.
	RecordVideo bool
	// SessionName tags entities the tester creates so reports and
	// cleanup can identify them.
	SessionName string
	// Clock supplies time; nil means real time.
	Clock func() time.Time
}

// Now returns the configured clock time, or wall time when unset.
func (c Config) Now() time.Time {
	if c.Clock != nil {
		return c.Clock()
	}
	return time.Now()
}
