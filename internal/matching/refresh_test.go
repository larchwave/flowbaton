package matching

import (
	"errors"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
)

func TestRefreshMatchesExactlyOneNodeByAttributesIgnoringBounds(t *testing.T) {
	t.Parallel()

	previous := mustHierarchy(t, matchNode("target", map[string]string{
		"resource-id": "com.example:id/target",
		"text":        "Target",
		"bounds":      "[0,0][10,10]",
	}))
	current := mustHierarchy(t, matchNode("root", nil,
		matchNode("other", map[string]string{"resource-id": "other", "bounds": "[0,0][5,5]"}),
		matchNode("target", map[string]string{
			"resource-id": "com.example:id/target",
			"text":        "Target",
			"bounds":      "[20,30][40,50]",
		}),
	))

	refreshed, err := Refresh(current, previous)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got := refreshed.Node.Attributes["name"]; got != "target" {
		t.Fatalf("Refresh name = %q", got)
	}
	if got := refreshed.Bounds; got.X != 20 || got.Y != 30 || got.Width != 20 || got.Height != 20 {
		t.Fatalf("Refresh bounds = %#v", got)
	}
}

func TestRefreshRequiresExactlyOneAttributeMatch(t *testing.T) {
	t.Parallel()

	previous := mustHierarchy(t, matchNode("target", map[string]string{"text": "Target", "bounds": "[0,0][1,1]"}))
	tests := []struct {
		name string
		root []string
		want int
	}{
		{name: "missing", root: []string{"other"}, want: 0},
		{name: "ambiguous", root: []string{"target", "target"}, want: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			children := makeMatchChildren(test.root)
			_, err := Refresh(mustHierarchy(t, matchNode("root", nil, children...)), previous)
			var refreshErr *RefreshError
			if !errors.As(err, &refreshErr) {
				t.Fatalf("Refresh error = %T %v, want *RefreshError", err, err)
			}
			if refreshErr.Matches != test.want {
				t.Fatalf("Refresh matches = %d, want %d", refreshErr.Matches, test.want)
			}
		})
	}
}

func makeMatchChildren(names []string) []device.TreeNode {
	children := make([]device.TreeNode, len(names))
	for index, name := range names {
		children[index] = matchNode(name, map[string]string{"text": "Target", "bounds": "[10,10][20,20]"})
	}
	return children
}
