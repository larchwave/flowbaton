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
	// Sleep is injected by tests; nil means real sleeping.
	Sleep func(context.Context, time.Duration) error
	// SettleTimeout bounds the post-launch settle; zero means the default.
	SettleTimeout time.Duration
}

var _ explore.Navigator = (*Navigator)(nil)

func (n *Navigator) deps() toolDeps {
	return toolDeps{
		driver:        n.Driver,
		observer:      n.Observer,
		appID:         n.Config.AppID,
		sleep:         n.Sleep,
		now:           n.Config.Now,
		settleTimeout: n.SettleTimeout,
	}.withDefaults()
}

// EnsureReady launches the app, settles, and observes. One failed
// observation earns one kill-and-relaunch retry before giving up.
func (n *Navigator) EnsureReady(ctx context.Context) (*explore.ScreenState, error) {
	deps := n.deps()
	if err := deps.validate(); err != nil {
		return nil, err
	}
	launch := func() error {
		return n.Driver.LaunchApp(ctx, device.LaunchAppRequest{AppID: deps.appID})
	}
	if err := launch(); err != nil {
		return nil, fmt.Errorf("explore/run: launch %s: %w", deps.appID, err)
	}
	settle := toolSession{deps: deps}
	settle.settle(ctx)
	state, err := n.Observer.Observe(ctx)
	if err == nil {
		return state, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if killErr := n.Driver.KillApp(ctx, device.AppRequest{AppID: deps.appID}); killErr != nil {
		return nil, fmt.Errorf("explore/run: recover %s after failed observation: %w", deps.appID, killErr)
	}
	if launchErr := launch(); launchErr != nil {
		return nil, fmt.Errorf("explore/run: relaunch %s: %w", deps.appID, launchErr)
	}
	settle.settle(ctx)
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
	if body, ok := n.recipe(ctx, origin, key); ok {
		if session.replay(ctx, box, body) && screenMatches(session.current, key) {
			return session.current, nil
		}
	}
	if n.Worker == nil {
		return nil, &ReachError{Key: key, Reason: "no recipe worked and no worker model is configured"}
	}
	conversation := []explore.Message{
		{Role: explore.RoleSystem, Text: "You steer a mobile app to a named screen using only the given tools. " +
			"Ground every action in the newest element table. Call finish with status passed once the screen is reached."},
		{Role: explore.RoleUser, Text: fmt.Sprintf("Bring the app to the screen named %q.\n\n%s", key, elementTable(session.current))},
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
			return nil, &ReachError{Key: key, Reason: "the model finished on a screen that does not match"}
		}
		if !loop.Exhausted {
			conversation = append(conversation, explore.Message{
				Role: explore.RoleUser,
				Text: "The target screen is not reached yet. Keep acting through tools.",
			})
		}
	}
	return nil, &ReachError{Key: key, Reason: fmt.Sprintf("not reached within %d turns", navigatorLoopBound)}
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
