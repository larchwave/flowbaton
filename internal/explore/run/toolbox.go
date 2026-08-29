package run

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/explore"
	"github.com/larchwave/flowbaton/internal/hierarchy"
	"github.com/larchwave/flowbaton/internal/matching"
	"github.com/larchwave/flowbaton/internal/model"
	"github.com/larchwave/flowbaton/internal/strictjson"
)

const (
	defaultSettleTimeout  = 3 * time.Second
	maxWaitSeconds        = 10
	longPressDurationMS   = 800
	scrollAmountPerSwipe  = 0.4
	defaultEraseCharCount = 50
)

// toolDeps are the injected collaborators one tool session operates on.
type toolDeps struct {
	driver        device.Driver
	observer      explore.Observer
	appID         string
	sleep         func(context.Context, time.Duration) error
	now           func() time.Time
	settleTimeout time.Duration
}

func (d toolDeps) validate() error {
	switch {
	case d.driver == nil:
		return errors.New("explore/run: nil driver")
	case d.observer == nil:
		return errors.New("explore/run: nil observer")
	case d.appID == "":
		return errors.New("explore/run: empty app id")
	default:
		return nil
	}
}

func realSleep(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (d toolDeps) withDefaults() toolDeps {
	filled := d
	if filled.sleep == nil {
		filled.sleep = realSleep
	}
	if filled.now == nil {
		filled.now = time.Now
	}
	if filled.settleTimeout <= 0 {
		filled.settleTimeout = defaultSettleTimeout
	}
	return filled
}

// finishArgs is the model's closing verdict for one scenario run.
type finishArgs struct {
	Status   string          `json:"status"`
	Outcomes []finishOutcome `json:"outcomes,omitempty"`
}

type finishOutcome struct {
	Expected string `json:"expected"`
	Met      bool   `json:"met"`
	Evidence string `json:"evidence,omitempty"`
}

// toolSession holds the mutable state one tool loop accumulates: the newest
// observation, executed steps, notes, checks, and the finish verdict.
type toolSession struct {
	deps    toolDeps
	current *explore.ScreenState
	steps   []explore.StepRecord
	notes   []string
	checks  []explore.OutcomeCheck
	// epoch counts every event after which the screen may differ from what
	// a check measured: each mutating tool call, whether or not its
	// re-observation succeeded, and each observation taken by observe.
	epoch int
	// checkEpochs holds, per entry of checks, the epoch it was measured in;
	// -1 marks a check taken while the table was known to be stale.
	checkEpochs []int
	// stale is set when a step could not be followed by a fresh
	// observation: current then describes an earlier screen, and checks
	// against it are not evidence about the device until observe succeeds.
	stale     bool
	finish    *finishArgs
	recording []string
	record    bool
}

func newToolSession(deps toolDeps, start *explore.ScreenState) (*toolSession, error) {
	filled := deps.withDefaults()
	if err := filled.validate(); err != nil {
		return nil, err
	}
	if start == nil {
		return nil, errors.New("explore/run: nil starting observation")
	}
	return &toolSession{deps: filled, current: start}, nil
}

func targetSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{` +
		`"eidx":{"type":"integer","description":"element index from the newest element table"},` +
		`"text":{"type":"string","description":"visible text of the element"},` +
		`"id":{"type":"string","description":"element identifier"}},` +
		`"additionalProperties":false}`)
}

func directionSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{` +
		`"direction":{"type":"string","enum":["up","down","left","right"]}},` +
		`"required":["direction"],"additionalProperties":false}`)
}

func emptySchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","additionalProperties":false}`)
}

// box assembles the device toolbox shared by the Tester and the Navigator.
func (s *toolSession) box() explore.ToolBox {
	specs := []explore.ToolSpec{
		{Name: "tap", Description: "Tap one element addressed by eidx, text, or id.", Schema: targetSchema()},
		{Name: "long_press", Description: "Press and hold one element addressed by eidx, text, or id.", Schema: targetSchema()},
		{Name: "input_text", Description: "Type text into the field that holds keyboard focus. Tap a text-field row first; this fails while nothing on screen takes typed text.", Schema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false}`)},
		{Name: "erase_text", Description: "Erase characters from the field that holds keyboard focus. Tap a text-field row first; this fails while nothing on screen takes typed text.", Schema: json.RawMessage(`{"type":"object","properties":{"characters":{"type":"integer"}},"additionalProperties":false}`)},
		{Name: "press_key", Description: "Press a named key, such as ENTER or HOME.", Schema: json.RawMessage(`{"type":"object","properties":{"key":{"type":"string"}},"required":["key"],"additionalProperties":false}`)},
		{Name: "swipe", Description: "Swipe the screen in one direction.", Schema: directionSchema()},
		{Name: "scroll", Description: "Scroll the screen up or down.", Schema: directionSchema()},
		{Name: "back", Description: "Press the platform back control.", Schema: emptySchema()},
		{Name: "hide_keyboard", Description: "Dismiss the on-screen keyboard.", Schema: emptySchema()},
		{Name: "wait", Description: "Pause for a few seconds (at most 10) while the app catches up.", Schema: json.RawMessage(`{"type":"object","properties":{"seconds":{"type":"integer","minimum":1,"maximum":10}},"required":["seconds"],"additionalProperties":false}`)},
		{Name: "observe", Description: "Capture a fresh observation and return the new element table.", Schema: emptySchema()},
		{Name: "check_visible", Description: "Check that an element with the given index, text, or id is on the current screen.", Schema: targetSchema()},
		{Name: "note", Description: "Record a short free-text finding.", Schema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false}`)},
		{Name: "finish", Description: "End the run with a status and per-outcome results.", Schema: json.RawMessage(`{"type":"object","properties":{` +
			`"status":{"type":"string","enum":["passed","failed"]},` +
			`"outcomes":{"type":"array","items":{"type":"object","properties":{` +
			`"expected":{"type":"string"},"met":{"type":"boolean"},"evidence":{"type":"string"}},` +
			`"required":["expected","met"],"additionalProperties":false}}},` +
			`"required":["status"],"additionalProperties":false}`)},
	}
	handlers := map[string]explore.ToolHandler{
		"tap":           s.handleTap,
		"long_press":    s.handleLongPress,
		"input_text":    s.handleInputText,
		"erase_text":    s.handleEraseText,
		"press_key":     s.handlePressKey,
		"swipe":         s.handleSwipe,
		"scroll":        s.handleScroll,
		"back":          s.handleBack,
		"hide_keyboard": s.handleHideKeyboard,
		"wait":          s.handleWait,
		"observe":       s.handleObserve,
		"check_visible": s.handleCheckVisible,
		"note":          s.handleNote,
		"finish":        s.handleFinish,
	}
	return explore.ToolBox{Specs: specs, Handlers: handlers}
}

// afterMutation settles, re-observes, and records one executed step. The
// returned text carries the step status and, on success, the fresh element
// table.
func (s *toolSession) afterMutation(ctx context.Context, tool string, args json.RawMessage, action explore.Action, execErr error) (string, error) {
	// The device may have changed even when the call reports failure or the
	// re-observation below cannot be taken, so the epoch moves on regardless.
	s.epoch++
	if err := ctx.Err(); err != nil {
		return "", err
	}
	before := s.current.Signature
	step := explore.StepRecord{Index: len(s.steps), Action: action, Before: before, After: before, At: s.deps.now()}
	// Re-observe after a failed call too: a driver error does not prove
	// the device did nothing, and the table must not describe a screen
	// that may be gone.
	s.settle(ctx)
	observation, obsErr := s.deps.observer.Observe(ctx)
	if obsErr != nil {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if transportGone(obsErr) {
			return "", fmt.Errorf("%w: observe after %s: %v", explore.ErrDeviceUnreachable, tool, obsErr)
		}
		s.stale = true
		if execErr == nil {
			execErr = fmt.Errorf("observe after %s: %w", tool, obsErr)
		}
	} else {
		step.After = observation.Signature
		s.replaceCurrent(observation)
	}
	switch {
	case execErr != nil:
		var miss explore.TargetMissError
		step.Status = explore.StepFailed
		step.ErrText = execErr.Error()
		step.TargetMiss = errors.As(execErr, &miss)
	case step.After.Same(before):
		step.Status = explore.StepNoChange
	default:
		step.Status = explore.StepOK
	}
	s.steps = append(s.steps, step)
	if step.Status != explore.StepFailed && s.record {
		s.recording = append(s.recording, recordLine(tool, args))
	}
	switch step.Status {
	case explore.StepFailed:
		// The failure reply carries the fresh table too: the screen was
		// re-observed above, and a model still aiming at the old indexes
		// would miss. It is sent even when the signature did not move --
		// the digest folds digits and ignores geometry, and the table's rows
		// do not -- so the model always holds the rows the session holds.
		// When no observation could be taken, say so instead of repeating a
		// table that may be gone.
		if s.stale {
			return fmt.Sprintf("%s failed: %s\n\nthe screen could not be re-observed; call observe before the next action", tool, step.ErrText), nil
		}
		if step.After.Same(before) {
			return fmt.Sprintf("%s failed: %s\n\nthe screen did not change\n\n%s", tool, step.ErrText, elementTable(s.current)), nil
		}
		return fmt.Sprintf("%s failed: %s\n\nthe screen changed anyway\n\n%s", tool, step.ErrText, elementTable(s.current)), nil
	case explore.StepNoChange:
		return fmt.Sprintf("%s done, but the screen did not change\n\n%s", tool, elementTable(s.current)), nil
	default:
		return fmt.Sprintf("%s ok, screen changed\n\n%s", tool, elementTable(s.current)), nil
	}
}

func (s *toolSession) settle(ctx context.Context) {
	timeout := s.deps.settleTimeout.Milliseconds()
	// Settling is best effort: the follow-up observation is the source of
	// truth, so a settle failure must not fail the step.
	_, _ = s.deps.driver.WaitForAppToSettle(ctx, device.SettleRequest{AppID: s.deps.appID, TimeoutMillis: &timeout})
}

func recordLine(tool string, args json.RawMessage) string {
	compact := &bytes.Buffer{}
	if len(args) == 0 || json.Compact(compact, args) != nil {
		return tool + " {}"
	}
	return tool + " " + compact.String()
}

func (s *toolSession) handleTap(ctx context.Context, args json.RawMessage) (string, error) {
	var in targetArgs
	if err := strictjson.Decode(args, &in); err != nil {
		return "", fmt.Errorf("tap arguments: %w", err)
	}
	action := explore.Action{Kind: explore.ActionTap, Target: in.locator(s.current), Text: in.describe()}
	point, err := resolvePoint(s.current, in)
	if err != nil {
		return s.afterMutation(ctx, "tap", args, action, err)
	}
	return s.afterMutation(ctx, "tap", args, action, s.deps.driver.Tap(ctx, device.TapRequest{Point: point}))
}

func (s *toolSession) handleLongPress(ctx context.Context, args json.RawMessage) (string, error) {
	var in targetArgs
	if err := strictjson.Decode(args, &in); err != nil {
		return "", fmt.Errorf("long_press arguments: %w", err)
	}
	action := explore.Action{Kind: explore.ActionLongPress, Target: in.locator(s.current), Text: in.describe()}
	point, err := resolvePoint(s.current, in)
	if err != nil {
		return s.afterMutation(ctx, "long_press", args, action, err)
	}
	request := device.LongPressRequest{Point: point, DurationMillis: longPressDurationMS}
	return s.afterMutation(ctx, "long_press", args, action, s.deps.driver.LongPress(ctx, request))
}

// typingMayLandInSecureField reports whether this screen's typed text could
// end up in a password-style field: a secure input is present and no plain
// text input holds focus. iOS publishes no usable focus (hasFocus is false
// once the keyboard is open), so any secure field on an iOS screen masks.
// Over-redaction is acceptable; leaking is not — a masked recipe that
// replays "***" simply fails the reach and falls back to the worker model.
// ponytail: iOS over-redacts username typing on login screens; a per-node
// keyboard-focus signal from the runner would tighten it.
func typingMayLandInSecureField(state *explore.ScreenState) bool {
	secureSeen := false
	plainFocused := false
	for _, element := range state.Elements {
		if explore.IsSecureTextInput(element.Node) {
			secureSeen = true
			continue
		}
		if explore.IsTextInput(element.Node) && element.Node.Focused != nil && *element.Node.Focused {
			plainFocused = true
		}
	}
	return secureSeen && !plainFocused
}

func (s *toolSession) handleInputText(ctx context.Context, args json.RawMessage) (string, error) {
	var in struct {
		Text string `json:"text"`
	}
	if err := strictjson.Decode(args, &in); err != nil {
		return "", fmt.Errorf("input_text arguments: %w", err)
	}
	if in.Text == "" {
		return "", errors.New("input_text needs non-empty text")
	}
	action := explore.Action{Kind: explore.ActionInput, Text: in.Text}
	recorded := args
	masked := typingMayLandInSecureField(s.current)
	if masked {
		action.Text = explore.MaskedText
		action.Masked = true
		// The marker is schema-invalid for input_text on purpose: a replay
		// that missed the guard would still fail to type it, and it cannot
		// collide with a legitimate literal "***".
		recorded = json.RawMessage(`{"masked":true}`)
	}
	request := device.InputTextRequest{Text: in.Text, AppIDs: []string{s.deps.appID}}
	execErr := s.deps.driver.InputText(ctx, request)
	if masked && execErr != nil {
		// Device errors quote what was typed (Android's no-focus message
		// does), and ErrText flows into step logs and reports.
		execErr = errors.New(strings.ReplaceAll(execErr.Error(), in.Text, explore.MaskedText))
	}
	return s.afterMutation(ctx, "input_text", recorded, action, execErr)
}

func (s *toolSession) handleEraseText(ctx context.Context, args json.RawMessage) (string, error) {
	var in struct {
		Characters int `json:"characters"`
	}
	if err := strictjson.Decode(args, &in); err != nil {
		return "", fmt.Errorf("erase_text arguments: %w", err)
	}
	if in.Characters < 0 {
		return "", errors.New("erase_text characters must not be negative")
	}
	if in.Characters == 0 {
		in.Characters = defaultEraseCharCount
	}
	action := explore.Action{Kind: explore.ActionErase, Text: fmt.Sprintf("%d", in.Characters)}
	request := device.EraseTextRequest{CharactersToErase: uint32(in.Characters), AppIDs: []string{s.deps.appID}}
	return s.afterMutation(ctx, "erase_text", args, action, s.deps.driver.EraseText(ctx, request))
}

func (s *toolSession) handlePressKey(ctx context.Context, args json.RawMessage) (string, error) {
	var in struct {
		Key string `json:"key"`
	}
	if err := strictjson.Decode(args, &in); err != nil {
		return "", fmt.Errorf("press_key arguments: %w", err)
	}
	if strings.TrimSpace(in.Key) == "" {
		return "", errors.New("press_key needs a key name")
	}
	action := explore.Action{Kind: explore.ActionPressKey, Text: in.Key}
	request := device.PressKeyRequest{Code: device.KeyCode(in.Key), AppIDs: []string{s.deps.appID}}
	return s.afterMutation(ctx, "press_key", args, action, s.deps.driver.PressKey(ctx, request))
}

func decodeDirection(args json.RawMessage, tool string, allowed ...string) (string, error) {
	var in struct {
		Direction string `json:"direction"`
	}
	if err := strictjson.Decode(args, &in); err != nil {
		return "", fmt.Errorf("%s arguments: %w", tool, err)
	}
	direction := strings.ToLower(in.Direction)
	for _, candidate := range allowed {
		if direction == candidate {
			return direction, nil
		}
	}
	return "", fmt.Errorf("%s direction must be one of %s", tool, strings.Join(allowed, ", "))
}

func (s *toolSession) handleSwipe(ctx context.Context, args json.RawMessage) (string, error) {
	direction, err := decodeDirection(args, "swipe", "up", "down", "left", "right")
	if err != nil {
		return "", err
	}
	action := explore.Action{Kind: explore.ActionSwipe, Direction: direction}
	request := device.SwipeRequest{Direction: device.Direction(strings.ToUpper(direction))}
	return s.afterMutation(ctx, "swipe", args, action, s.deps.driver.Swipe(ctx, request))
}

func (s *toolSession) handleScroll(ctx context.Context, args json.RawMessage) (string, error) {
	direction, err := decodeDirection(args, "scroll", "up", "down")
	if err != nil {
		return "", err
	}
	action := explore.Action{Kind: explore.ActionScroll, Direction: direction}
	request := device.ScrollVerticalRequest{Direction: device.Direction(strings.ToUpper(direction)), Amount: scrollAmountPerSwipe}
	return s.afterMutation(ctx, "scroll", args, action, s.deps.driver.ScrollVertical(ctx, request))
}

func (s *toolSession) handleBack(ctx context.Context, args json.RawMessage) (string, error) {
	err := s.deps.driver.BackPress(ctx)
	if errors.Is(err, device.ErrUnsupported) {
		note := "back is unsupported on this platform"
		s.notes = append(s.notes, note)
		return note, nil
	}
	return s.afterMutation(ctx, "back", args, explore.Action{Kind: explore.ActionBack}, err)
}

func (s *toolSession) handleHideKeyboard(ctx context.Context, args json.RawMessage) (string, error) {
	action := explore.Action{Kind: explore.ActionHideKeys}
	return s.afterMutation(ctx, "hide_keyboard", args, action, s.deps.driver.HideKeyboard(ctx))
}

func (s *toolSession) handleWait(ctx context.Context, args json.RawMessage) (string, error) {
	var in struct {
		Seconds int `json:"seconds"`
	}
	if err := strictjson.Decode(args, &in); err != nil {
		return "", fmt.Errorf("wait arguments: %w", err)
	}
	if in.Seconds <= 0 || in.Seconds > maxWaitSeconds {
		return "", fmt.Errorf("wait seconds must be between 1 and %d", maxWaitSeconds)
	}
	action := explore.Action{Kind: explore.ActionWait, Text: fmt.Sprintf("%ds", in.Seconds)}
	err := s.deps.sleep(ctx, time.Duration(in.Seconds)*time.Second)
	return s.afterMutation(ctx, "wait", args, action, err)
}

func (s *toolSession) handleObserve(ctx context.Context, _ json.RawMessage) (string, error) {
	observation, err := s.deps.observer.Observe(ctx)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		if transportGone(err) {
			return "", fmt.Errorf("%w: observe: %v", explore.ErrDeviceUnreachable, err)
		}
		return "", fmt.Errorf("observe: %w", err)
	}
	s.replaceCurrent(observation)
	return elementTable(observation), nil
}

// transportGone reports whether err says the driver's endpoint is no longer
// there at all -- a refused or failed dial -- as opposed to an endpoint that
// answered with a failure.
func transportGone(err error) bool {
	var opErr *net.OpError
	return errors.As(err, &opErr) || errors.Is(err, syscall.ECONNREFUSED)
}

// replaceCurrent installs a fresh observation and opens a new epoch.
func (s *toolSession) replaceCurrent(observation *explore.ScreenState) {
	s.current = observation
	s.stale = false
	s.epoch++
}

func (s *toolSession) handleCheckVisible(ctx context.Context, args json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var in targetArgs
	if err := strictjson.Decode(args, &in); err != nil {
		return "", fmt.Errorf("check_visible arguments: %w", err)
	}
	check := explore.OutcomeCheck{Expected: "visible: " + in.describe()}
	if in.EIDX != nil {
		// An index names a row of the newest table, so it is visible exactly
		// when that table still lists it.
		if label, ok := elementByIndex(s.current, *in.EIDX); ok {
			check.Met = true
			check.Evidence = fmt.Sprintf("e%d %q is in the newest element table", *in.EIDX, label)
		} else {
			check.Evidence = fmt.Sprintf("no element e%d in the newest element table", *in.EIDX)
		}
		return s.recordCheck(check), nil
	}
	if in.Text == "" && in.ID == "" {
		return "", errors.New("check_visible needs eidx, text, or id")
	}
	selector := model.ElementSelector{}
	if in.Text != "" {
		text := in.Text
		selector.TextRegex = &text
	} else {
		id := in.ID
		selector.IDRegex = &id
	}
	root, err := hierarchy.New(s.current.Hierarchy)
	if err != nil {
		return "", fmt.Errorf("normalize screen tree: %w", err)
	}
	found, findErr := matching.Find(root, selector)
	switch {
	case findErr != nil:
		check.Evidence = "selector error: " + findErr.Error()
	case len(found) == 0:
		check.Evidence = "no matching element in the current tree"
	default:
		check.Met = true
		check.Evidence = fmt.Sprintf("matched %d element(s), first: %q", len(found), elementLabel(found[0].Node))
	}
	return s.recordCheck(check), nil
}

// recordCheck keeps one check_visible result for the verdict and renders
// the tool reply.
func (s *toolSession) recordCheck(check explore.OutcomeCheck) string {
	s.checks = append(s.checks, check)
	epoch := s.epoch
	suffix := ""
	if s.stale {
		epoch = -1
		suffix = " (measured on a table the last step could not refresh; call observe)"
	}
	s.checkEpochs = append(s.checkEpochs, epoch)
	if check.Met {
		return "visible: " + check.Evidence + suffix
	}
	return "not visible: " + check.Evidence + suffix
}

// checksOnFinalScreen returns the checks measured in the current epoch:
// nothing that could change the screen -- a mutating call, even one whose
// re-observation failed, or a fresh observe -- happened after them. A
// screen key is an eight-hex digest prefix and a step count misses
// observe, so neither is the test; handing the judge an older check
// invites a pass on what is no longer true.
func (s *toolSession) checksOnFinalScreen() []explore.OutcomeCheck {
	var current []explore.OutcomeCheck
	for i, check := range s.checks {
		if s.checkEpochs[i] == s.epoch {
			current = append(current, check)
		}
	}
	return current
}

func elementByIndex(state *explore.ScreenState, eidx int) (string, bool) {
	for _, element := range state.Elements {
		if element.EIDX == eidx {
			return elementLabel(element.Node), true
		}
	}
	return "", false
}

func (s *toolSession) handleNote(ctx context.Context, args json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var in struct {
		Text string `json:"text"`
	}
	if err := strictjson.Decode(args, &in); err != nil {
		return "", fmt.Errorf("note arguments: %w", err)
	}
	if strings.TrimSpace(in.Text) == "" {
		return "", errors.New("note needs non-empty text")
	}
	s.notes = append(s.notes, in.Text)
	return "noted", nil
}

func (s *toolSession) handleFinish(ctx context.Context, args json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var in finishArgs
	if err := strictjson.Decode(args, &in); err != nil {
		return "", fmt.Errorf("finish arguments: %w", err)
	}
	if in.Status != "passed" && in.Status != "failed" {
		return "", fmt.Errorf("finish status must be passed or failed, got %q", in.Status)
	}
	s.finish = &in
	return "finishing", explore.ErrStopRequested
}
