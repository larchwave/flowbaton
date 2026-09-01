package run

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/explore"
)

// navigatorLoopBound caps the tool-loop turns Reach may spend on one key.
const navigatorLoopBound = 8

// ReachError reports that the navigator could not bring the app to the
// requested screen.
type ReachError struct {
	Key    string
	Reason string
}

func (e *ReachError) Error() string {
	return fmt.Sprintf("explore/run: screen %q not reached: %s", e.Key, e.Reason)
}

// Navigator prepares the app for exploration and steers it to named
// screens, replaying learned recipes before spending model turns.
type Navigator struct {
	Driver   device.Driver
	Observer explore.Observer
	Worker   explore.LLM
	Config   explore.Config
	// Experience optionally stores and serves "reach <key>" recipes.
	Experience explore.ExperienceStore
	// unreachable remembers the routes the worker already failed to walk
	// this session, keyed origin -> target. mmx71 sent the same reach five
	// times, once per scenario, and all five spent the whole turn budget on
	// the same key: forty worker turns for nothing. The first failure is
	// information the session already has.
	unreachable map[string]bool
	// Sleep is injected by tests; nil means real sleeping.
	Sleep func(context.Context, time.Duration) error
}

var _ explore.Navigator = (*Navigator)(nil)

func (n *Navigator) deps() toolDeps {
	return toolDeps{
		driver:   n.Driver,
		observer: n.Observer,
		appID:    n.Config.AppID,
		sleep:    n.Sleep,
		now:      n.Config.Now,
	}.withDefaults()
}

// EnsureReady relaunches the app from a stopped process, settles, and
// observes. One failed observation earns one kill-and-relaunch retry before
// giving up.
//
// It kills before launching so the app starts from a stopped process rather
// than resuming a live one, and so a scenario does not inherit the process
// state the one before it left. On mmx36 that moved the exported flows from
// 1 of 4 replaying standalone to 4 of 4 -- one session's count, not a rate
// this promises to hold, because the paragraph below names what still breaks
// a replay.
//
// What it does NOT do is put the app on a known screen, whatever this
// comment said before 2026-08-30. Measured on iOS 26.2: Reminders killed on
// a list-detail screen relaunches on that same screen, because the system
// restores it. A scenario, and a replayed export, can therefore still begin
// somewhere the run never started -- the symptom is a step 2 that cannot
// find an element the recording did. Only clearState defeats restoration,
// and it takes the data later scenarios need with it.
//
// The kill does keep data: an app relaunched still holds whatever earlier
// scenarios created.
func (n *Navigator) EnsureReady(ctx context.Context) (*explore.ScreenState, error) {
	deps := n.deps()
	if err := deps.validate(); err != nil {
		return nil, err
	}
	launch := func() error {
		// An app that was not running has nothing to stop, and the platforms
		// differ on whether that is an error. It never is one here: the point
		// of the kill is the state after it, and a stopped app is in it.
		_ = n.Driver.KillApp(ctx, device.AppRequest{AppID: deps.appID})
		return n.Driver.LaunchApp(ctx, device.LaunchAppRequest{AppID: deps.appID})
	}
	if err := launch(); err != nil {
		return nil, fmt.Errorf("explore/run: launch %s: %w", deps.appID, err)
	}
	state, err := n.Observer.Observe(ctx)
	if err == nil {
		return state, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	// launch kills first, so the retry is already the kill-and-relaunch it
	// was written to be; a second explicit kill here would only stop an app
	// that launch is about to stop anyway.
	if launchErr := launch(); launchErr != nil {
		return nil, fmt.Errorf("explore/run: relaunch %s: %w", deps.appID, launchErr)
	}
	state, err = n.Observer.Observe(ctx)
	if err != nil {
		return nil, fmt.Errorf("explore/run: app not observable after relaunch: %w", err)
	}
	return state, nil
}

// Reach brings the app to the screen named by key: a stored recipe is
// replayed first, then a small bounded tool loop on the worker model. A
// successful model-driven path is recorded back into the store.
func (n *Navigator) Reach(ctx context.Context, key string) (*explore.ScreenState, error) {
	if strings.TrimSpace(key) == "" {
		return nil, &ReachError{Key: key, Reason: "empty screen key"}
	}
	state, err := n.EnsureReady(ctx)
	if err != nil {
		return nil, err
	}
	if screenMatches(state, key) {
		return state, nil
	}
	session, err := newToolSession(n.deps(), state)
	if err != nil {
		return nil, err
	}
	session.record = true
	box := session.box()
	origin := state.Signature
	// A recipe is replayed whatever happened before: replaying costs no
	// model turns, which is the thing being rationed here.
	if body, ok := n.recipe(ctx, origin, key); ok {
		if session.replay(ctx, box, body) && screenMatches(session.current, key) {
			return session.current, nil
		}
	}
	// Only this route is refused. Another origin is another way there.
	route := origin.Key() + " -> " + key
	if n.unreachable[route] {
		return nil, &ReachError{Key: key,
			Reason: "the worker did not reach it from this screen earlier in this session"}
	}
	if n.Worker == nil {
		return nil, &ReachError{Key: key, Reason: "no recipe worked and no worker model is configured"}
	}
	conversation := []explore.Message{
		{Role: explore.RoleSystem, Text: "You steer a mobile app to a named screen using only the given tools. " +
			"Ground every action in the newest element table. Call finish with status passed once the screen is reached."},
		{Role: explore.RoleUser, Text: fmt.Sprintf("Bring the app to the screen named %q.%s\n\n%s",
			key, screenWordsSentence(key), elementTable(session.current))},
	}
	for turn := 0; turn < navigatorLoopBound; turn++ {
		loop, loopErr := explore.RunToolLoop(ctx, n.Worker, conversation, box, 1)
		conversation = pruneElementTables(loop.Messages)
		if loopErr != nil {
			return nil, fmt.Errorf("explore/run: reach %q: %w", key, loopErr)
		}
		if screenMatches(session.current, key) {
			// A recording that masked a secret is not worth keeping: its
			// replay would be refused on every future reach anyway.
			if n.Experience != nil && len(session.recording) > 0 && !recordingHoldsMask(session.recording) {
				entry := explore.MemoryEntry{Title: "reach " + key, Body: strings.Join(session.recording, "\n")}
				// Recipe persistence is advisory: a store failure must
				// not fail a reach that succeeded on the device.
				_ = n.Experience.Record(ctx, origin, entry)
			}
			return session.current, nil
		}
		if loop.Stopped {
			n.rememberUnreachable(route)
			return nil, &ReachError{Key: key, Reason: "the model finished on a screen that does not match"}
		}
		if !loop.Exhausted {
			conversation = append(conversation, explore.Message{
				Role: explore.RoleUser,
				Text: "The target screen is not reached yet. Keep acting through tools.",
			})
		}
	}
	n.rememberUnreachable(route)
	return nil, &ReachError{Key: key, Reason: fmt.Sprintf("not reached within %d turns", navigatorLoopBound)}
}

// screenWordsSentence renders a key's labels for a prompt, and says nothing
// when the key is a bare digest with no labels to render.
func screenWordsSentence(key string) string {
	words := explore.ScreenKeyWords(key)
	if words == "" {
		return ""
	}
	return fmt.Sprintf(" Its labels read %q.", words)
}

// rememberUnreachable records that the worker could not walk this route, so
// the next scenario does not spend the same budget on the same failure.
// Only a worker failure is remembered: a recipe that stops working is worth
// re-deriving, and a route the worker never tried is not a failure.
func (n *Navigator) rememberUnreachable(route string) {
	if n.unreachable == nil {
		n.unreachable = map[string]bool{}
	}
	n.unreachable[route] = true
}

func (n *Navigator) recipe(ctx context.Context, screen explore.ScreenSignature, key string) (string, bool) {
	if n.Experience == nil {
		return "", false
	}
	title := "reach " + key
	titles, err := n.Experience.Index(ctx, screen)
	if err != nil {
		return "", false
	}
	for _, candidate := range titles {
		if candidate != title {
			continue
		}
		body, getErr := n.Experience.Get(ctx, screen, title)
		if getErr != nil || strings.TrimSpace(body) == "" {
			return "", false
		}
		return body, true
	}
	return "", false
}

// replay executes recorded tool lines ("tool {json args}") through the
// toolbox. Any unknown tool or failed step abandons the replay.
// maskedRecordingLine reports whether one recorded tool line is a masked
// input: the {"masked":true} marker handleInputText writes, or the literal
// mask text that earlier recordings stored. That earlier shape collides with
// a user really typing "***" — refusing that replay costs one worker
// fallback; replaying a masked secret would type the mask into a login and
// submit it.
func maskedRecordingLine(name, rawArgs string) bool {
	if name != "input_text" {
		return false
	}
	return strings.Contains(rawArgs, `"masked":true`) ||
		strings.Contains(rawArgs, `"`+explore.MaskedText+`"`)
}

// recordingHoldsMask reports whether any line of a recording masked its
// input, which makes the whole recording non-replayable.
func recordingHoldsMask(lines []string) bool {
	for _, line := range lines {
		name, rawArgs, _ := strings.Cut(strings.TrimSpace(line), " ")
		if maskedRecordingLine(name, rawArgs) {
			return true
		}
	}
	return false
}

func (s *toolSession) replay(ctx context.Context, box explore.ToolBox, body string) bool {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, rawArgs, _ := strings.Cut(line, " ")
		if rawArgs == "" {
			rawArgs = "{}"
		}
		if maskedRecordingLine(name, rawArgs) {
			// A masked secret is not replayable: typing the literal mask
			// and then submitting would send bad credentials. Fail the
			// replay so the reach falls back to the worker model.
			return false
		}
		handler, ok := box.Handlers[name]
		if !ok {
			return false
		}
		before := len(s.steps)
		if _, err := handler(ctx, []byte(rawArgs)); err != nil {
			return false
		}
		if len(s.steps) > before && s.steps[len(s.steps)-1].Status == explore.StepFailed {
			return false
		}
	}
	return true
}

// screenMatches reports whether the observation looks like the named
// screen: the key appears in a salient label or an element label.
func screenMatches(state *explore.ScreenState, key string) bool {
	if state == nil {
		return false
	}
	want := strings.ToLower(strings.TrimSpace(key))
	if want == "" {
		return false
	}
	// The signature answers whether the key names it: the whole key, or the
	// labels alone. The crew asks the same question before it sends a reach,
	// so the two cannot disagree and send a reach that ends on its first
	// check, after a kill and a launch the crew had just paid for.
	if state.Signature.NamesTheSameScreen(want) {
		return true
	}
	for _, salient := range state.Signature.Salient {
		if strings.Contains(strings.ToLower(salient), want) {
			return true
		}
	}
	for _, element := range state.Elements {
		if strings.Contains(strings.ToLower(elementLabel(element.Node)), want) {
			return true
		}
	}
	return false
}
