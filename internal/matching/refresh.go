package matching

import (
	"fmt"

	"github.com/nohavewho/flowbaton/internal/hierarchy"
)

// RefreshError reports that refresh-by-attributes did not identify one node.
type RefreshError struct {
	Matches int
}

func (e *RefreshError) Error() string {
	return fmt.Sprintf("refresh requires exactly one attribute match; found %d", e.Matches)
}

// Refresh finds the unique current element whose attributes equal the previous
// element's attributes after excluding bounds.
func Refresh(root, previous *hierarchy.Element) (*hierarchy.Element, error) {
	if previous == nil {
		return nil, &RefreshError{}
	}
	matches := make([]*hierarchy.Element, 0, 1)
	for _, candidate := range hierarchy.Walk(root) {
		if attributesEqualIgnoringBounds(candidate.Node.Attributes, previous.Node.Attributes) {
			matches = append(matches, candidate)
		}
	}
	if len(matches) != 1 {
		return nil, &RefreshError{Matches: len(matches)}
	}
	return matches[0], nil
}

func attributesEqualIgnoringBounds(left, right map[string]string) bool {
	leftCount, rightCount := 0, 0
	for key, value := range left {
		if key == "bounds" {
			continue
		}
		leftCount++
		if rightValue, exists := right[key]; !exists || rightValue != value {
			return false
		}
	}
	for key := range right {
		if key != "bounds" {
			rightCount++
		}
	}
	return leftCount == rightCount
}
