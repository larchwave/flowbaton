// Package matching implements exact platform-neutral selector matching over a
// normalized hierarchy.
package matching

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/nohavewho/flowbaton/internal/hierarchy"
	"github.com/nohavewho/flowbaton/internal/model"
)

// RegexError identifies an invalid selector expression without panicking.
type RegexError struct {
	Field   string
	Pattern string
	Err     error
}

func (e *RegexError) Error() string {
	return fmt.Sprintf("invalid %s regex %q: %v", e.Field, e.Pattern, e.Err)
}

func (e *RegexError) Unwrap() error { return e.Err }

type exactPattern struct {
	raw      string
	compiled *regexp.Regexp
}

type compiledSelector struct {
	selector            model.ElementSelector
	text                *exactPattern
	id                  *exactPattern
	below               *compiledSelector
	above               *compiledSelector
	leftOf              *compiledSelector
	rightOf             *compiledSelector
	containsChild       *compiledSelector
	containsDescendants []*compiledSelector
	childOf             *compiledSelector
	index               *int
}

// Find returns matching elements in deterministic hierarchy order.
func Find(root *hierarchy.Element, selector model.ElementSelector) ([]*hierarchy.Element, error) {
	compiled, err := compileSelector(&selector)
	if err != nil {
		return nil, err
	}
	return compiled.find(root), nil
}

func compileSelector(selector *model.ElementSelector) (*compiledSelector, error) {
	if selector == nil {
		return nil, nil
	}
	textPattern, err := compilePattern("text", selector.TextRegex)
	if err != nil {
		return nil, err
	}
	idPattern, err := compilePattern("id", selector.IDRegex)
	if err != nil {
		return nil, err
	}
	compiled := &compiledSelector{selector: *selector, text: textPattern, id: idPattern}
	if selector.Index != nil {
		// Index is stored raw until interpolation finishes. Resolve it here so
		// ranking remains integer-typed and invalid values fail before lookup.
		value, convErr := strconv.Atoi(strings.TrimSpace(*selector.Index))
		if convErr != nil {
			return nil, fmt.Errorf("selector index %q is not an integer: %w", *selector.Index, convErr)
		}
		compiled.index = &value
	}
	nested := []struct {
		source      *model.ElementSelector
		destination **compiledSelector
	}{
		{selector.Below, &compiled.below},
		{selector.Above, &compiled.above},
		{selector.LeftOf, &compiled.leftOf},
		{selector.RightOf, &compiled.rightOf},
		{selector.ContainsChild, &compiled.containsChild},
		{selector.ChildOf, &compiled.childOf},
	}
	for _, entry := range nested {
		value, compileErr := compileSelector(entry.source)
		if compileErr != nil {
			return nil, compileErr
		}
		*entry.destination = value
	}
	compiled.containsDescendants = make([]*compiledSelector, len(selector.ContainsDescendants))
	for index := range selector.ContainsDescendants {
		value, compileErr := compileSelector(&selector.ContainsDescendants[index])
		if compileErr != nil {
			return nil, compileErr
		}
		compiled.containsDescendants[index] = value
	}
	return compiled, nil
}

func (selector *compiledSelector) find(root *hierarchy.Element) []*hierarchy.Element {
	// childOf narrows where to look. Its anchor resolves to ONE element and the
	// rest of the selector is evaluated inside that element, the element
	// included: `{id: general, childOf: {id: general}}` resolves the row itself,
	// and `{childOf: {id: general}}` enumerates the row plus its three children.
	//
	// An anchor that resolves to nothing means there is nowhere to look, which
	// is not the same as looking everywhere.
	scope := root
	if selector.childOf != nil {
		anchors := selector.childOf.find(root)
		if len(anchors) == 0 {
			return []*hierarchy.Element{}
		}
		scope = anchors[0]
	}
	result := make([]*hierarchy.Element, 0)
	for _, element := range hierarchy.Walk(scope) {
		if selector.matchesBasic(element) {
			result = append(result, element)
		}
	}
	// Structural filters first, then the deepest-node reduction. The other way
	// round throws away every container before `containsChild`/
	// `containsDescendants` can select one, and those selectors carry no traits
	// of their own, so the basic pass initially includes the whole tree.
	result = selector.filterStructural(root, result)
	// The reduction belongs to resolving a text/id match, not to the pipeline.
	// A selector whose only filter is structural keeps the whole chain:
	// `containsDescendants: [text: General]` enumerates every ancestor of that
	// label, six or more of them, not just the innermost one. The first in
	// document order is the tree root, whose text is empty, which is what
	// reading that selector back yields.
	if selector.hasBasicFilter() {
		result = deepest(result)
	}
	result = applyDirection(root, result, selector.below, directionBelow)
	result = applyDirection(root, result, selector.above, directionAbove)
	result = applyDirection(root, result, selector.leftOf, directionLeft)
	result = applyDirection(root, result, selector.rightOf, directionRight)
	return selector.rank(result)
}

func (selector *compiledSelector) rank(candidates []*hierarchy.Element) []*hierarchy.Element {
	if selector.index != nil {
		sort.SliceStable(candidates, func(left, right int) bool {
			return indexLess(candidates[left], candidates[right])
		})
		index := *selector.index
		if index < 0 {
			index += len(candidates)
		}
		if index < 0 || index >= len(candidates) {
			return []*hierarchy.Element{}
		}
		return []*hierarchy.Element{candidates[index]}
	}
	sort.SliceStable(candidates, func(left, right int) bool {
		return isClickable(candidates[left]) && !isClickable(candidates[right])
	})
	return candidates
}

func indexLess(left, right *hierarchy.Element) bool {
	if left.HasBounds != right.HasBounds {
		return left.HasBounds
	}
	if !left.HasBounds {
		return left.Order < right.Order
	}
	if left.Bounds.Y != right.Bounds.Y {
		return left.Bounds.Y < right.Bounds.Y
	}
	if left.Bounds.X != right.Bounds.X {
		return left.Bounds.X < right.Bounds.X
	}
	return left.Order < right.Order
}

func isClickable(element *hierarchy.Element) bool {
	return element.Node.Clickable != nil && *element.Node.Clickable
}

func (selector *compiledSelector) matchesBasic(element *hierarchy.Element) bool {
	if selector.text != nil && !matchesAnyText(*selector.text, element) {
		return false
	}
	if selector.id != nil && !matchesID(*selector.id, element) {
		return false
	}
	if !matchesSize(selector.selector.Size, element) || !matchesTraits(selector.selector.Traits, element) {
		return false
	}
	return matchesBool(selector.selector.Enabled, element.Node.Enabled) &&
		matchesBool(selector.selector.Selected, element.Node.Selected) &&
		matchesBool(selector.selector.Checked, element.Node.Checked) &&
		matchesBool(selector.selector.Focused, element.Node.Focused)
}

// hasBasicFilter reports whether the selector says anything about an element on
// its own, as opposed to only about its relatives.
func (selector *compiledSelector) hasBasicFilter() bool {
	return selector.text != nil || selector.id != nil ||
		selector.selector.Size != nil || len(selector.selector.Traits) > 0 ||
		selector.selector.Enabled != nil || selector.selector.Selected != nil ||
		selector.selector.Checked != nil || selector.selector.Focused != nil
}

func deepest(candidates []*hierarchy.Element) []*hierarchy.Element {
	set := make(map[*hierarchy.Element]struct{}, len(candidates))
	for _, candidate := range candidates {
		set[candidate] = struct{}{}
	}
	result := make([]*hierarchy.Element, 0, len(candidates))
	for _, candidate := range candidates {
		if !hasMatchingDescendant(candidate, set) {
			result = append(result, candidate)
		}
	}
	return result
}

func hasMatchingDescendant(candidate *hierarchy.Element, set map[*hierarchy.Element]struct{}) bool {
	stack := append([]*hierarchy.Element(nil), candidate.Children...)
	for len(stack) > 0 {
		index := len(stack) - 1
		current := stack[index]
		stack = stack[:index]
		if _, exists := set[current]; exists {
			return true
		}
		stack = append(stack, current.Children...)
	}
	return false
}

// filterStructural keeps candidates whose relatives are IN the set the nested
// selector resolves to. The nested selector is resolved once, through the same
// pipeline as a top-level one — including the deepest-node reduction — and the
// test is set membership.
//
// Membership, not a fresh per-candidate match: `containsChild: {text: General}`
// resolves to the one row that holds the label, and
// `containsDescendants: [text: General]` to every ancestor of it.
func (selector *compiledSelector) filterStructural(root *hierarchy.Element, candidates []*hierarchy.Element) []*hierarchy.Element {
	var childSet map[*hierarchy.Element]struct{}
	if selector.containsChild != nil {
		childSet = elementSet(selector.containsChild.find(root))
	}
	descendantSets := make([]map[*hierarchy.Element]struct{}, len(selector.containsDescendants))
	for index, descendantSelector := range selector.containsDescendants {
		descendantSets[index] = elementSet(descendantSelector.find(root))
	}

	result := candidates[:0]
	for _, candidate := range candidates {
		if childSet != nil && !hasDirectChildIn(candidate, childSet) {
			continue
		}
		matchesAllDescendants := true
		for _, set := range descendantSets {
			if !hasDescendantIn(candidate, set) {
				matchesAllDescendants = false
				break
			}
		}
		if !matchesAllDescendants {
			continue
		}
		result = append(result, candidate)
	}
	return result
}

func elementSet(elements []*hierarchy.Element) map[*hierarchy.Element]struct{} {
	set := make(map[*hierarchy.Element]struct{}, len(elements))
	for _, element := range elements {
		set[element] = struct{}{}
	}
	return set
}

func hasDirectChildIn(candidate *hierarchy.Element, set map[*hierarchy.Element]struct{}) bool {
	for _, child := range candidate.Children {
		if _, exists := set[child]; exists {
			return true
		}
	}
	return false
}

func hasDescendantIn(candidate *hierarchy.Element, set map[*hierarchy.Element]struct{}) bool {
	stack := append([]*hierarchy.Element(nil), candidate.Children...)
	for len(stack) > 0 {
		index := len(stack) - 1
		current := stack[index]
		stack = stack[:index]
		if _, exists := set[current]; exists {
			return true
		}
		stack = append(stack, current.Children...)
	}
	return false
}

type direction uint8

const (
	directionBelow direction = iota
	directionAbove
	directionLeft
	directionRight
)

func applyDirection(root *hierarchy.Element, candidates []*hierarchy.Element, anchor *compiledSelector, relation direction) []*hierarchy.Element {
	if anchor == nil {
		return candidates
	}
	anchors := anchor.find(root)
	result := candidates[:0]
	for _, candidate := range candidates {
		if matchesAnyAnchor(candidate, anchors, relation) {
			result = append(result, candidate)
		}
	}
	return result
}

// matchesAnyAnchor keeps a candidate when its origin stands in the requested
// direction from any anchor. Survivors retain hierarchy traversal order.
func matchesAnyAnchor(candidate *hierarchy.Element, anchors []*hierarchy.Element, relation direction) bool {
	if !candidate.HasBounds {
		return false
	}
	for _, anchor := range anchors {
		if anchor.HasBounds && isDirectionalMatch(candidate, anchor, relation) {
			return true
		}
	}
	return false
}

func isDirectionalMatch(candidate, anchor *hierarchy.Element, relation direction) bool {
	switch relation {
	case directionBelow:
		return candidate.Bounds.Y > anchor.Bounds.Y
	case directionAbove:
		return candidate.Bounds.Y < anchor.Bounds.Y
	case directionLeft:
		return candidate.Bounds.X < anchor.Bounds.X
	case directionRight:
		return candidate.Bounds.X > anchor.Bounds.X
	default:
		return false
	}
}

func matchesSize(size *model.SizeSelector, element *hierarchy.Element) bool {
	if size == nil {
		return true
	}
	if !element.HasBounds {
		return false
	}
	tolerance := 0
	if size.Tolerance != nil {
		tolerance = *size.Tolerance
	}
	if size.Width != nil && absoluteDifference(element.Bounds.Width, *size.Width) > int64(tolerance) {
		return false
	}
	return size.Height == nil || absoluteDifference(element.Bounds.Height, *size.Height) <= int64(tolerance)
}

func matchesTraits(traits []model.ElementTrait, element *hierarchy.Element) bool {
	for _, trait := range traits {
		switch trait {
		case model.ElementTraitText:
			if _, exists := element.Node.Attributes["text"]; !exists {
				return false
			}
		case model.ElementTraitSquare:
			if !element.HasBounds || element.Bounds.Height <= 0 ||
				math.Abs(1-float64(element.Bounds.Width)/float64(element.Bounds.Height)) >= 0.03 {
				return false
			}
		case model.ElementTraitLongText:
			text, exists := element.Node.Attributes["text"]
			if !exists || utf8.RuneCountInString(text) <= 200 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func matchesBool(want, actual *bool) bool {
	return want == nil || actual != nil && *actual == *want
}

func absoluteDifference(left, right int) int64 {
	difference := int64(left) - int64(right)
	if difference < 0 {
		return -difference
	}
	return difference
}

func compilePattern(field string, raw *string) (*exactPattern, error) {
	if raw == nil {
		return nil, nil
	}
	compiled, err := regexp.Compile(`(?ims:^(?:` + *raw + `)$)`)
	if err != nil {
		return nil, &RegexError{Field: field, Pattern: *raw, Err: err}
	}
	return &exactPattern{raw: *raw, compiled: compiled}, nil
}

func matchesAnyText(pattern exactPattern, element *hierarchy.Element) bool {
	// Each absent text-bearing attribute reads as empty text. Accessibility-only
	// nodes therefore remain eligible for empty-text selectors, while patterns
	// are applied consistently to text, hint text, and accessibility text.
	for _, key := range []string{"text", "hintText", "accessibilityText"} {
		if pattern.matches(element.Node.Attributes[key]) {
			return true
		}
	}
	return false
}

func matchesID(pattern exactPattern, element *hierarchy.Element) bool {
	for _, key := range []string{"resource-id", "id"} {
		value, exists := element.Node.Attributes[key]
		if !exists {
			continue
		}
		if pattern.matches(value) || pattern.matches(value[strings.LastIndex(value, "/")+1:]) {
			return true
		}
	}
	return false
}

func (pattern exactPattern) matches(value string) bool {
	if value == pattern.raw || pattern.compiled.MatchString(value) {
		return true
	}
	normalized := strings.ReplaceAll(value, "\n", " ")
	return normalized == pattern.raw || pattern.compiled.MatchString(normalized)
}
