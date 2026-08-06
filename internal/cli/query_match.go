package cli

import (
	"fmt"
	"strings"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/hierarchy"
	"github.com/larchwave/flowbaton/internal/matching"
	"github.com/larchwave/flowbaton/internal/model"
)

// Matching a query on the host, not the device.
//
// Device agents do not expose a query-evaluation route. Query pulls the
// hierarchy and uses internal/matching on the host, following the lookup path
// used by flow assertions.

// queryKeys are what an expression may name. specs/03-cli-tooling.md:16 says
// "find elements by text/id".
const queryKeys = "text=<regex> or id=<regex>"

// matchQuery finds every element the expression names, in the same order and
// under the same visibility rules a flow's lookup uses.
func matchQuery(
	root device.TreeNode, viewport device.Bounds, expression string,
) ([]device.TreeNode, error) {
	selector, err := querySelector(expression)
	if err != nil {
		return nil, err
	}
	normalized, err := hierarchy.New(root)
	if err != nil {
		return nil, err
	}
	matches, err := matching.Find(hierarchy.FilterVisible(normalized, viewport), selector)
	if err != nil {
		return nil, err
	}
	// Every match, not the first: a flow's lookup takes one because it is about
	// to tap it, while a query is asked precisely to see how many there are.
	found := make([]device.TreeNode, 0, len(matches))
	for _, match := range matches {
		found = append(found, match.Node)
	}
	return found, nil
}

// querySelector turns `text=General` into the same selector a flow would build.
func querySelector(expression string) (model.ElementSelector, error) {
	key, value, found := strings.Cut(strings.TrimSpace(expression), "=")
	if !found || strings.TrimSpace(value) == "" {
		return model.ElementSelector{}, fmt.Errorf(
			"query expression %q names no value; want %s", expression, queryKeys)
	}
	switch strings.TrimSpace(key) {
	case "text":
		return model.ElementSelector{TextRegex: &value}, nil
	case "id":
		return model.ElementSelector{IDRegex: &value}, nil
	default:
		return model.ElementSelector{}, fmt.Errorf(
			"query cannot match on %q; want %s", key, queryKeys)
	}
}
