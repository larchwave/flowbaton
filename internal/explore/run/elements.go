// Package run implements the execution roles of exploration mode: the
// Tester tool loop, the Pilot supervisor conversation, and the Navigator
// that brings the app to a usable screen.
package run

import (
	"fmt"
	"strings"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/explore"
	"github.com/larchwave/flowbaton/internal/hierarchy"
	"github.com/larchwave/flowbaton/internal/matching"
	"github.com/larchwave/flowbaton/internal/model"
)

// elementTableHeading marks tool results that carry an element table, so
// stale tables can be pruned from the conversation between turns.
const elementTableHeading = "elements on screen"

const maxTableRows = 60

// elementLabel returns the human-visible label of a node.
func elementLabel(node device.TreeNode) string {
	for _, key := range []string{"text", "label", "name", "hintText", "accessibilityText"} {
		if value := strings.TrimSpace(node.Attributes[key]); value != "" {
			return value
		}
	}
	return ""
}

func elementRole(node device.TreeNode) string {
	role := node.Attributes["class"]
	if role == "" {
		role = node.Attributes["type"]
	}
	if dot := strings.LastIndex(role, "."); dot >= 0 {
		role = role[dot+1:]
	}
	return role
}

func elementID(node device.TreeNode) string {
	for _, key := range []string{"resource-id", "id"} {
		if value := node.Attributes[key]; value != "" {
			return value
		}
	}
	return ""
}

// elementTable renders the newest observation as the model-facing element
// list. Only the latest table is kept in a conversation.
func elementTable(state *explore.ScreenState) string {
	builder := &strings.Builder{}
	fmt.Fprintf(builder, "%s %q:\n", elementTableHeading, state.Signature.Key())
	rows := 0
	for _, element := range state.Elements {
		if rows >= maxTableRows {
			fmt.Fprintf(builder, "(+%d more elements)\n", len(state.Elements)-rows)
			break
		}
		rows++
		fmt.Fprintf(builder, "e%d", element.EIDX)
		if role := elementRole(element.Node); role != "" {
			fmt.Fprintf(builder, " %s", role)
		}
		if label := elementLabel(element.Node); label != "" {
			fmt.Fprintf(builder, " %q", truncate(label, 60))
		}
		if id := elementID(element.Node); id != "" {
			fmt.Fprintf(builder, " id=%s", id)
		}
		if element.Node.Clickable != nil && *element.Node.Clickable {
			builder.WriteString(" clickable")
		}
		// Typing goes to whatever holds keyboard focus, so both marks are
		// load-bearing: which rows accept text, and whether a tap took focus.
		// The focus mark is Android-only in practice: iOS publishes UI focus,
		// which a text field with the keyboard open reports as false.
		if explore.IsTextInput(element.Node) {
			builder.WriteString(" text-field")
		}
		if element.Node.Focused != nil && *element.Node.Focused {
			builder.WriteString(" focused")
		}
		builder.WriteString("\n")
	}
	if rows == 0 {
		builder.WriteString("(no interactive elements)\n")
	}
	return builder.String()
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max] + "…"
}

// elementTableMarker is the exact start of a rendered table: the heading
// followed by the quoted signature. A scenario name that merely contains the
// heading words does not match it.
const elementTableMarker = elementTableHeading + " \""

// pruneElementTables replaces every element table except the newest with a
// short placeholder, keeping the text before each table (a tool result's
// status line, or the opening scenario text). Tables sit in tool results and
// in the opening user message alike. The input slice is not mutated.
func pruneElementTables(messages []explore.Message) []explore.Message {
	last := -1
	for index := len(messages) - 1; index >= 0; index-- {
		if strings.Contains(messages[index].Text, elementTableMarker) {
			last = index
			break
		}
	}
	pruned := append([]explore.Message(nil), messages...)
	for index := range pruned {
		if index == last {
			continue
		}
		if at := strings.Index(pruned[index].Text, elementTableMarker); at >= 0 {
			pruned[index].Text = pruned[index].Text[:at] + "(element table pruned; call observe for a fresh one)"
		}
	}
	return pruned
}

// targetArgs address one element by exactly one of eidx, text, or id.
type targetArgs struct {
	EIDX *int   `json:"eidx,omitempty"`
	Text string `json:"text,omitempty"`
	ID   string `json:"id,omitempty"`
}

func (a targetArgs) describe() string {
	switch {
	case a.EIDX != nil:
		return fmt.Sprintf("e%d", *a.EIDX)
	case a.Text != "":
		return fmt.Sprintf("text %q", a.Text)
	case a.ID != "":
		return fmt.Sprintf("id %q", a.ID)
	default:
		return "no target"
	}
}

func (a targetArgs) locator(state *explore.ScreenState) *explore.Locator {
	switch {
	case a.Text != "":
		return &explore.Locator{Kind: explore.LocatorText, Value: a.Text}
	case a.ID != "":
		return &explore.Locator{Kind: explore.LocatorID, Value: a.ID}
	case a.EIDX != nil:
		for _, element := range state.Elements {
			if element.EIDX == *a.EIDX {
				return &explore.Locator{Kind: explore.LocatorPath, Value: element.Path}
			}
		}
	}
	return nil
}

// resolvePoint finds the tap point for a target in the current observation.
func resolvePoint(state *explore.ScreenState, args targetArgs) (device.Point, error) {
	modes := 0
	if args.EIDX != nil {
		modes++
	}
	if args.Text != "" {
		modes++
	}
	if args.ID != "" {
		modes++
	}
	if modes != 1 {
		return device.Point{}, explore.TargetMissError{Reason: "target needs exactly one of eidx, text, or id"}
	}
	if args.EIDX != nil {
		for _, element := range state.Elements {
			if element.EIDX != *args.EIDX {
				continue
			}
			bounds, ok := explore.ElementBounds(element.Node)
			if !ok {
				return device.Point{}, explore.TargetMissError{
					Reason: fmt.Sprintf("element e%d has no usable bounds", *args.EIDX),
				}
			}
			return hierarchy.Center(bounds), nil
		}
		return device.Point{}, explore.TargetMissError{
			Reason: fmt.Sprintf("no element e%d in the newest element table; call observe", *args.EIDX),
		}
	}
	selector := model.ElementSelector{}
	if args.Text != "" {
		text := args.Text
		selector.TextRegex = &text
	} else {
		id := args.ID
		selector.IDRegex = &id
	}
	root, err := hierarchy.New(state.Hierarchy)
	if err != nil {
		return device.Point{}, fmt.Errorf("normalize screen tree: %w", err)
	}
	found, err := matching.Find(root, selector)
	if err != nil {
		return device.Point{}, fmt.Errorf("selector for %s: %w", args.describe(), err)
	}
	for _, candidate := range found {
		if candidate.HasBounds {
			return hierarchy.Center(candidate.Bounds), nil
		}
	}
	return device.Point{}, explore.TargetMissError{
		Reason: fmt.Sprintf("no element matched %s on the current screen", args.describe()),
	}
}
