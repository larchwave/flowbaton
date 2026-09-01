// Package run implements the execution roles of exploration mode: the
// Tester tool loop, the Pilot supervisor conversation, and the Navigator
// that brings the app to a usable screen.
package run

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/explore"
	"github.com/larchwave/flowbaton/internal/hierarchy"
	"github.com/larchwave/flowbaton/internal/matching"
	"github.com/larchwave/flowbaton/internal/model"
	"github.com/larchwave/flowbaton/internal/strictjson"
)

// elementTableHeading marks tool results that carry an element table, so
// stale tables can be pruned from the conversation between turns.
const elementTableHeading = "elements on screen"

const maxTableRows = 60

// elementLabel returns the human-visible label of a node.
func elementLabel(node device.TreeNode) string {
	return explore.ElementLabel(node)
}

func elementRole(node device.TreeNode) string {
	return explore.ElementRole(node)
}

func elementID(node device.TreeNode) string {
	for _, key := range []string{"resource-id", "id"} {
		if value := node.Attributes[key]; value != "" {
			return value
		}
	}
	return ""
}

// fieldContent returns what a text field currently holds, which is not its
// label: an empty field labels itself with the word it prompts with, so
// "searchField \"Search\"" reads the same whether the field is empty or holds
// that word. mmx56 filed a [High] defect on exactly that ambiguity. Captured
// on iOS 26.2, 2026-08-31: the empty shortcuts field sends hintText and
// accessibilityText alone, the filled contacts field adds text and value.
// Android sends the content in text and nothing when the field is empty.
func fieldContent(node device.TreeNode) string {
	for _, key := range []string{"text", "value"} {
		if content := strings.TrimSpace(node.Attributes[key]); content != "" {
			return content
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
		if label := explore.ControlLabel(element.Node); label != "" {
			fmt.Fprintf(builder, " %q", explore.Truncate(label, 60))
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
		// The tester picks from this table, and it picked an element that had
		// scrolled off the screen twice in one mmx59 scenario. Saying so here
		// spends a word instead of a turn.
		if bounds, ok := explore.ElementBounds(element.Node); ok {
			if _, err := tapPoint(bounds, state.Viewport, "element"); err != nil {
				builder.WriteString(" off-screen")
			}
		}
		if explore.IsTextInput(element.Node) {
			builder.WriteString(" text-field")
			if fieldContent(element.Node) == "" {
				builder.WriteString(" empty")
			}
		}
		if element.Node.Focused != nil && *element.Node.Focused {
			builder.WriteString(" focused")
		}
		// Which way a switch is set is the whole point of a row that has one,
		// and it is nowhere else on the screen.
		if state, ok := explore.ControlState(element.Node); ok {
			builder.WriteString(" " + state)
		}
		builder.WriteString("\n")
	}
	if rows == 0 {
		builder.WriteString("(no interactive elements)\n")
	}
	return builder.String()
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

// decodeTarget reads tool arguments that name one element. A row name
// written into the id field (`{"id":"e5"}`) is read as that row unless an
// element on the screen really carries that id; weak models mix the two
// up and burned their step budget on the miss.
func decodeTarget(args json.RawMessage, state *explore.ScreenState) (targetArgs, error) {
	var in targetArgs
	if err := strictjson.Decode(args, &in); err != nil {
		return targetArgs{}, err
	}
	if in.EIDX != nil || in.Text != "" || !rowName.MatchString(in.ID) {
		return in, nil
	}
	// Ask the id selector itself, over the whole tree: it also matches the
	// suffix after the last "/" (Android's "pkg:id/e5"), and a node the
	// flattened table left out still answers to its id.
	if idSelectsSomething(state, in.ID) {
		return in, nil
	}
	index, err := strconv.Atoi(in.ID[1:])
	if err != nil {
		return in, nil
	}
	return targetArgs{EIDX: &index}, nil
}

var rowName = regexp.MustCompile(`^e[0-9]+$`)

func idSelectsSomething(state *explore.ScreenState, id string) bool {
	root, err := state.VisibleTree()
	if err != nil {
		return true
	}
	found, err := matching.Find(root, model.ElementSelector{IDRegex: &id})
	return err != nil || len(found) > 0
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

// locator answers the selector the flow exporter will write for a target.
// An eidx climbs the same ladder as the research map: an id, else a
// label unique on this screen, else the tap point; a tree path only when
// the element has no bounds, since no flow selector can express it.
func (a targetArgs) locator(state *explore.ScreenState) *explore.Locator {
	switch {
	case a.Text != "":
		return &explore.Locator{Kind: explore.LocatorText, Value: a.Text}
	case a.ID != "":
		return &explore.Locator{Kind: explore.LocatorID, Value: a.ID}
	case a.EIDX != nil:
		for _, element := range state.Elements {
			if element.EIDX == *a.EIDX {
				return elementLocator(state, element)
			}
		}
	}
	return nil
}

func elementLocator(state *explore.ScreenState, element explore.FlatElement) *explore.Locator {
	if id := elementID(element.Node); id != "" {
		return &explore.Locator{Kind: explore.LocatorID, Value: id}
	}
	if label := elementLabel(element.Node); label != "" && labelCount(state, label) == 1 {
		if pattern, ok := generalizeCount(state, label); ok {
			return &explore.Locator{Kind: explore.LocatorText, Value: pattern}
		}
		// Escaped: a text selector is compiled as a regexp, so an unescaped
		// "a*b" also matches "b" and "aab", and an unbalanced bracket does
		// not compile at all and fails the step.
		return &explore.Locator{Kind: explore.LocatorText, Value: regexp.QuoteMeta(label)}
	}
	// Both fallbacks below say where the element is and nothing about what it
	// is. The label that was not unique enough to select on still names it
	// for whoever reads the report.
	label := elementLabel(element.Node)
	if bounds, ok := explore.ElementBounds(element.Node); ok {
		center := hierarchy.VisibleCenter(bounds, state.Viewport)
		return &explore.Locator{Kind: explore.LocatorPoint, Value: explore.PointLocator(center), Label: label}
	}
	return &explore.Locator{Kind: explore.LocatorPath, Value: element.Path, Label: label}
}

// digitRun matches the digit runs of a label.
var digitRun = regexp.MustCompile(`[0-9]+`)

// generalizeCount answers a text selector that survives a changed count.
// A label unique on one screen can still carry app state -- "All, 12
// reminders" names a row by how much is in it -- and a flow written with
// that literal stops matching the moment the data differs. Every text
// selector reaches the device as a regexp anchored to the whole value, so
// the digits can become \d+ while the rest is escaped and keeps meaning
// itself.
//
// Answers false when there is nothing to generalize, or when generalizing
// would aim the selector at a second row as well: a selector that matches
// two rows taps the wrong one, which is worse than a brittle one that taps
// nothing.
func generalizeCount(state *explore.ScreenState, label string) (string, bool) {
	if !digitRun.MatchString(label) {
		return "", false
	}
	// QuoteMeta escapes the metacharacters and leaves digits alone, so the
	// digit runs of the quoted text are still the digit runs of the label.
	pattern := digitRun.ReplaceAllString(regexp.QuoteMeta(label), `\d+`)
	// A label that is nothing but a count identifies nothing once the count
	// is gone: the pattern would be `\d+`, which the device resolves to
	// every number on the screen -- a price, a page number, a badge.
	if strings.TrimSpace(strings.ReplaceAll(pattern, `\d+`, "")) == "" {
		return "", false
	}
	if selectorMatchCount(state, pattern) != 1 {
		return "", false
	}
	return pattern, true
}

// selectorMatchCount answers how many elements the DEVICE would find for a
// text selector, by running the matcher the device runs.
//
// Counting rows of the flattened table instead is strictly weaker in two
// ways that both end with the flow tapping the wrong element: the table
// drops nodes the matcher still searches, and it reads one attribute per
// node where the matcher tries text, hintText and accessibilityText
// independently. A tree it cannot read answers -1, which reads as "not
// unique" and keeps the literal label.
func selectorMatchCount(state *explore.ScreenState, pattern string) int {
	// The full tree on purpose, unlike every other matcher here: this one is
	// not choosing what to touch, it is asking whether the pattern could ever
	// be ambiguous. A row scrolled out of view is one scroll from being a
	// second match, and the comment above says which way to err.
	root, err := state.FullTree()
	if err != nil {
		return -1
	}
	found, err := matching.Find(root, model.ElementSelector{TextRegex: &pattern})
	if err != nil {
		return -1
	}
	return len(found)
}

func labelCount(state *explore.ScreenState, label string) int {
	count := 0
	for _, element := range state.Elements {
		if elementLabel(element.Node) == label {
			count++
		}
	}
	return count
}

// tapPoint returns where to tap an element, or a miss when the point is not
// on the screen. hierarchy.VisibleCenter clips to the viewport, but an
// element that does not touch the viewport at all falls back to its own
// geometric centre, which is off the device: session mmx59 tapped 372,-51
// twice, both taps hit nothing, the scenario passed anyway, and the flow it
// exported cannot run -- the engine refuses a negative point outright.
//
// A tap that cannot land is a miss the tester can answer, by scrolling to
// the element and observing again. A viewport of no size means nobody
// measured, so only the coordinates themselves are judged there.
func tapPoint(bounds, viewport device.Bounds, describe string) (device.Point, error) {
	point := hierarchy.VisibleCenter(bounds, viewport)
	offScreen := point.X < 0 || point.Y < 0
	if viewport.Width > 0 && viewport.Height > 0 {
		left, top := float64(viewport.X), float64(viewport.Y)
		offScreen = offScreen ||
			point.X < left || point.X >= left+float64(viewport.Width) ||
			point.Y < top || point.Y >= top+float64(viewport.Height)
	}
	if offScreen {
		return device.Point{}, explore.TargetMissError{
			Reason: fmt.Sprintf("%s is off the screen; scroll to it and observe again", describe),
		}
	}
	return point, nil
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
			return tapPoint(bounds, state.Viewport, fmt.Sprintf("element e%d", *args.EIDX))
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
	root, err := state.VisibleTree()
	if err != nil {
		return device.Point{}, fmt.Errorf("normalize screen tree: %w", err)
	}
	found, err := matching.Find(root, selector)
	if err != nil {
		return device.Point{}, fmt.Errorf("selector for %s: %w", args.describe(), err)
	}
	for _, candidate := range found {
		if candidate.HasBounds {
			return tapPoint(candidate.Bounds, state.Viewport, args.describe())
		}
	}
	return device.Point{}, explore.TargetMissError{
		Reason: fmt.Sprintf("no element matched %s on the current screen", args.describe()),
	}
}
