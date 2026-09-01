// Package export turns finished exploration runs into runnable two-document
// flow YAML, validated through the flow parser before it is returned.
package export

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/larchwave/flowbaton/internal/explore"
	"github.com/larchwave/flowbaton/internal/flow"
	"go.yaml.in/yaml/v3"
)

// Exporter maps a passing scenario run onto flow commands. Emitted bytes
// always round-trip through flow.ParseBytes; output that fails to parse is
// an error, never silently written.
type Exporter struct{}

var _ explore.Exporter = Exporter{}

// ExportFlow renders the run as one config document plus one command
// sequence, separated by a document marker.
func (Exporter) ExportFlow(result *explore.TestResult, appID string) ([]byte, error) {
	if result == nil {
		return nil, errors.New("export: nil result")
	}
	if appID == "" {
		return nil, errors.New("export: empty app id")
	}
	if result.Status != explore.TestPassed {
		return nil, fmt.Errorf("export: only passing runs export, run status is %q", result.Status)
	}
	// stopApp restates today's default (specs/06-launch-app-semantics.md
	// section 1, launchAppCompiled{stopApp: true}), and is written out
	// because the run depended on it: the navigator kills and relaunches
	// before every scenario, so the flow replays from a screen only a fresh
	// launch produces. Naming it keeps that dependency true if the default
	// ever changes.
	//
	// It is not what fixed mmx36's flows, whatever the commit that added it
	// said -- a bare launchApp already stops the app, which
	// TestLaunchAppSkipsEveryUnauthoredStep pins. What helped was the
	// navigator killing before it launches, and the session relaunching
	// between scenarios instead of carrying one scenario's process into the
	// next. Neither of those puts the app on a known screen either.
	//
	// Not clearState: it would take the data a run depends on with it. The
	// price is that a stop does not reset navigation either -- an app that
	// restores its last screen replays from there, not from the screen the
	// recording began on (see EnsureReady in run/navigator.go for the
	// measurement).
	launch := keyed("launchApp", mapNode(plain("stopApp"), plain("true")))
	// A flow replays from wherever launchApp leaves the app, which is not
	// the screen the scenario was planned against: replaying the flows of
	// three sessions on the simulator, two of thirteen failed on their first
	// action -- one taps text an earlier scenario had typed, the other a
	// toolbar button that exists only in the day view. Naming the screen
	// turns "element not found" into a starting point. Walking to it, when
	// the navigator recorded a walk, turns it into a flow that runs.
	//
	// The refusal below is what a launch-only flow earns: it asserts nothing
	// and passes on replay whatever the app does, which is a green that
	// means nothing on the same tally as a flow that tests something. Six of
	// seventy exported flows were launch-only, three of them from mmx69,
	// where one came from a scenario whose every device call failed against
	// a dead runner and was called passed anyway.
	commands := []*yaml.Node{launch}
	// The walk to the start screen belongs to the flow, not to a comment
	// asking the reader to perform it: the navigator already made it on the
	// device, and its steps replay the same way the run's own do.
	walk, err := stepCommands(result.Prelude, nil)
	if err != nil {
		return nil, err
	}
	commands = append(commands, walk...)
	if screen := strings.TrimSpace(result.Scenario.StartScreen); screen != "" && len(walk) == 0 {
		launch.HeadComment = fmt.Sprintf(
			"recorded on screen %s; launchApp does not navigate there", screen)
	}
	// Only the run's own actions answer whether the flow tests anything.
	// A walk is setup, and a flow that is all setup is the launch-only case
	// wearing more lines.
	own, err := stepCommands(result.Steps, &secretCounter{})
	if err != nil {
		return nil, err
	}
	if len(own) == 0 {
		return nil, errors.New("export: the run has no action to replay, only a launch")
	}
	commands = append(commands, own...)
	data, err := encodeFlow(appID, result.Scenario.Name, commands)
	if err != nil {
		return nil, err
	}
	if _, err := flow.ParseBytes("exported-flow.yaml", data); err != nil {
		return nil, fmt.Errorf("export: emitted flow failed validation: %w", err)
	}
	return data, nil
}

// secretCounter numbers the environment placeholders one export writes. A nil
// counter refuses to mask, which is what the walk needs: a navigator recipe
// that typed a secret is never recorded in the first place.
type secretCounter struct{ n int }

// stepCommands maps steps onto flow commands, skipping the ones that carry
// nothing to replay.
func stepCommands(steps []explore.StepRecord, secrets *secretCounter) ([]*yaml.Node, error) {
	var commands []*yaml.Node
	for _, step := range steps {
		// A masked input holds no replayable text. Export it as an env
		// placeholder so the flow stays runnable once the operator
		// supplies the secret, instead of typing a literal mask. The
		// FLOWBATON_ prefix is what lets a shell variable reach the flow
		// (specs/01 reserved environment), and the SECRET fragment is
		// what the engine's inputText guard and artifact masking key on.
		if step.Action.Kind == explore.ActionInput && step.Action.Masked {
			if secrets == nil {
				return nil, fmt.Errorf("export: step %d: a masked input has no place in a walk", step.Index)
			}
			secrets.n++
			step.Action.Text = fmt.Sprintf("${FLOWBATON_EXPLORE_SECRET_%d}", secrets.n)
		}
		node, err := commandNode(step)
		if err != nil {
			return nil, fmt.Errorf("export: step %d: %w", step.Index, err)
		}
		if node != nil {
			commands = append(commands, node)
		}
	}
	return commands, nil
}

// commandNode maps one step onto a flow command. A nil node without error
// means the step is skipped: failed steps, waits, and app lifecycle steps
// already covered by the leading launchApp.
func commandNode(step explore.StepRecord) (*yaml.Node, error) {
	if step.Status == explore.StepFailed {
		return nil, nil
	}
	action := step.Action
	switch action.Kind {
	case explore.ActionWait, explore.ActionLaunch, explore.ActionStop:
		return nil, nil
	case explore.ActionTap:
		selector, err := selectorNode(action)
		if err != nil {
			return nil, err
		}
		return keyed("tapOn", selector), nil
	case explore.ActionLongPress:
		selector, err := selectorNode(action)
		if err != nil {
			return nil, err
		}
		return keyed("longPressOn", selector), nil
	case explore.ActionVerify:
		selector, err := selectorNode(action)
		if err != nil {
			return nil, err
		}
		return keyed("assertVisible", selector), nil
	case explore.ActionInput:
		if action.Text == "" {
			return nil, errors.New("input step carries no text")
		}
		return keyed("inputText", quoted(action.Text)), nil
	case explore.ActionErase:
		if action.Text == "" {
			return plain("eraseText"), nil
		}
		count, err := strconv.Atoi(action.Text)
		if err != nil {
			return nil, fmt.Errorf("erase count %q is not an integer", action.Text)
		}
		return keyed("eraseText", intNode(count)), nil
	case explore.ActionSwipe:
		if action.Direction == "" {
			return nil, errors.New("swipe step carries no direction")
		}
		return keyed("swipe", mapNode(plain("direction"), plain(strings.ToUpper(action.Direction)))), nil
	case explore.ActionScroll:
		// The flow scroll command takes no arguments; the authored
		// direction stays behind in the step log.
		return plain("scroll"), nil
	case explore.ActionBack:
		return plain("back"), nil
	case explore.ActionPressKey:
		if action.Text == "" {
			return nil, errors.New("pressKey step carries no key name")
		}
		return keyed("pressKey", plain(action.Text)), nil
	case explore.ActionHideKeys:
		return plain("hideKeyboard"), nil
	case explore.ActionOpenLink:
		if action.Text == "" {
			return nil, errors.New("openLink step carries no link")
		}
		return keyed("openLink", quoted(action.Text)), nil
	}
	return nil, fmt.Errorf("no flow mapping for action kind %q", action.Kind)
}

// selectorNode climbs the locator ladder: id object first, quoted text
// second, an explicit point object as the last resort. A verify step
// without a locator asserts on its text.
func selectorNode(action explore.Action) (*yaml.Node, error) {
	target := action.Target
	if target == nil {
		if action.Kind == explore.ActionVerify && action.Text != "" {
			return quoted(action.Text), nil
		}
		return nil, errors.New("step carries no locator")
	}
	switch target.Kind {
	case explore.LocatorID:
		pairs := []*yaml.Node{plain("id"), quoted(target.Value)}
		if target.Index > 0 {
			pairs = append(pairs, plain("index"), intNode(target.Index))
		}
		return mapNode(pairs...), nil
	case explore.LocatorText:
		if target.Index > 0 {
			return mapNode(plain("text"), quoted(target.Value), plain("index"), intNode(target.Index)), nil
		}
		return quoted(target.Value), nil
	case explore.LocatorPoint:
		return mapNode(plain("point"), quoted(target.Value)), nil
	}
	return nil, fmt.Errorf("locator kind %q has no stable flow selector", target.Kind)
}

func encodeFlow(appID, name string, commands []*yaml.Node) ([]byte, error) {
	config := []*yaml.Node{plain("appId"), plain(appID)}
	if name != "" {
		config = append(config, plain("name"), quoted(name))
	}
	config = append(config, plain("tags"), seqNode(plain("explored")))
	buffer := &bytes.Buffer{}
	encoder := yaml.NewEncoder(buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(mapNode(config...)); err != nil {
		return nil, fmt.Errorf("export: encode config: %w", err)
	}
	if err := encoder.Encode(seqNode(commands...)); err != nil {
		return nil, fmt.Errorf("export: encode commands: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("export: close encoder: %w", err)
	}
	return buffer.Bytes(), nil
}

func plain(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Value: value}
}

func quoted(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Style: yaml.DoubleQuotedStyle, Value: value}
}

func intNode(value int) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(value)}
}

func mapNode(pairs ...*yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Content: pairs}
}

func seqNode(items ...*yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.SequenceNode, Content: items}
}

func keyed(keyword string, value *yaml.Node) *yaml.Node {
	return mapNode(plain(keyword), value)
}
