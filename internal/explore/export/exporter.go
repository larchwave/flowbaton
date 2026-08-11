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
	commands := []*yaml.Node{plain("launchApp")}
	for _, step := range result.Steps {
		node, err := commandNode(step)
		if err != nil {
			return nil, fmt.Errorf("export: step %d: %w", step.Index, err)
		}
		if node != nil {
			commands = append(commands, node)
		}
	}
	data, err := encodeFlow(appID, result.Scenario.Name, commands)
	if err != nil {
		return nil, err
	}
	if _, err := flow.ParseBytes("exported-flow.yaml", data); err != nil {
		return nil, fmt.Errorf("export: emitted flow failed validation: %w", err)
	}
	return data, nil
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
