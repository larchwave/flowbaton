package explore

import "time"

// ActionKind names one device action the tester can take. Kinds map onto
// flow command keywords at export time.
type ActionKind string

// Tester action kinds.
const (
	ActionTap       ActionKind = "tap"
	ActionLongPress ActionKind = "longPress"
	ActionInput     ActionKind = "input"
	ActionErase     ActionKind = "erase"
	ActionSwipe     ActionKind = "swipe"
	ActionScroll    ActionKind = "scroll"
	ActionBack      ActionKind = "back"
	ActionPressKey  ActionKind = "pressKey"
	ActionHideKeys  ActionKind = "hideKeyboard"
	ActionOpenLink  ActionKind = "openLink"
	ActionLaunch    ActionKind = "launchApp"
	ActionStop      ActionKind = "stopApp"
	ActionWait      ActionKind = "wait"
	ActionVerify    ActionKind = "verify"
)

// Action is one concrete device interaction or check.
type Action struct {
	Kind ActionKind
	// Target locates the element the action addresses, nil for
	// screen-level actions (back, swipe by direction, launch).
	Target *Locator
	// Text is input text, key name, link, or assertion text by kind.
	Text string
	// Direction is up/down/left/right for swipe and scroll kinds.
	Direction string
}

// StepStatus reports how one executed step ended.
type StepStatus string

// Step outcomes.
const (
	StepOK       StepStatus = "ok"
	StepFailed   StepStatus = "failed"
	StepRecov    StepStatus = "recovered"
	StepNoChange StepStatus = "no-change"
)

// StepRecord is one executed step of a scenario run, with the screen
// signatures around it so state transitions are auditable.
type StepRecord struct {
	Index   int
	Action  Action
	Status  StepStatus
	Note    string
	Before  ScreenSignature
	After   ScreenSignature
	At      time.Time
	ErrText string
	// TargetMiss marks a step that failed because the screen has no element
	// the agent's target names. The device answered; the agent aimed at
	// nothing, so a report must not read this as broken equipment.
	TargetMiss bool
}

// TargetMissError is the error a target that resolves to no element returns.
// The message is the whole error: the agent reads it as a tool result, so it
// carries no wrapper text.
type TargetMissError struct {
	Reason string
}

func (e TargetMissError) Error() string {
	return e.Reason
}

// OutcomeCheck is one expected outcome with its verification result.
type OutcomeCheck struct {
	Expected string
	Met      bool
	Evidence string
}

// TestStatus is the final verdict for one scenario run.
type TestStatus string

// Scenario run verdicts.
const (
	TestPassed  TestStatus = "passed"
	TestFailed  TestStatus = "failed"
	TestStopped TestStatus = "stopped"
)

// TestResult is the full record of one scenario execution.
type TestResult struct {
	Scenario Scenario
	Status   TestStatus
	Steps    []StepRecord
	Outcomes []OutcomeCheck
	Notes    []string
	Started  time.Time
	Finished time.Time
	// Verdict is the supervisor summary when the pilot ran, or the
	// outcome-matching summary otherwise.
	Verdict string
}

// SessionReport aggregates a whole exploration session.
type SessionReport struct {
	AppID    string
	Platform string
	Results  []TestResult
	// Markdown is the rendered analyst report.
	Markdown string
	Usage    Usage
	Started  time.Time
	Finished time.Time
}
