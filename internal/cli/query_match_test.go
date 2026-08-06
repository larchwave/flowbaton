package cli

import (
	"strings"
	"testing"

	"github.com/nohavewho/flowbaton/internal/device"
)

// Native platform query uses the same host-side matcher as flow selectors.
// Device drivers supply hierarchy data; CSS remains device-side on web.

func node(attributes map[string]string, children ...device.TreeNode) device.TreeNode {
	return device.TreeNode{Attributes: attributes, Children: children}
}

// A viewport big enough that nothing is filtered for being off-screen; the
// visibility rules are matching's, and they are tested there.
var testViewport = device.Bounds{Width: 1000, Height: 1000}

func TestQueryMatchesByText(t *testing.T) {
	t.Parallel()

	root := node(map[string]string{"bounds": "[0,0][1000,1000]"},
		node(map[string]string{"text": "General", "bounds": "[0,0][100,50]"}),
		node(map[string]string{"text": "Privacy", "bounds": "[0,50][100,100]"}),
	)

	matches, err := matchQuery(root, testViewport, "text=General")
	if err != nil {
		t.Fatalf("matchQuery() error = %v", err)
	}
	if len(matches) != 1 || matches[0].Attributes["text"] != "General" {
		t.Fatalf("matches = %+v, want the one node saying General", matches)
	}
}

func TestQueryMatchesByID(t *testing.T) {
	t.Parallel()

	root := node(map[string]string{"bounds": "[0,0][1000,1000]"},
		node(map[string]string{"resource-id": "com.app:id/save", "bounds": "[0,0][100,50]"}),
		node(map[string]string{"resource-id": "com.app:id/cancel", "bounds": "[0,50][100,100]"}),
	)

	matches, err := matchQuery(root, testViewport, "id=save")
	if err != nil {
		t.Fatalf("matchQuery() error = %v", err)
	}
	if len(matches) != 1 || matches[0].Attributes["resource-id"] != "com.app:id/save" {
		t.Fatalf("matches = %+v, want the save button", matches)
	}
}

// Values use the same regex semantics as flow selectors.
func TestQueryTreatsTheValueAsARegexLikeAFlowDoes(t *testing.T) {
	t.Parallel()

	root := node(map[string]string{"bounds": "[0,0][1000,1000]"},
		node(map[string]string{"text": "General", "bounds": "[0,0][100,50]"}),
		node(map[string]string{"text": "Genesis", "bounds": "[0,50][100,100]"}),
		node(map[string]string{"text": "Privacy", "bounds": "[0,100][100,150]"}),
	)

	matches, err := matchQuery(root, testViewport, "text=Gene.*")
	if err != nil {
		t.Fatalf("matchQuery() error = %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("matches = %+v, want both Gen… nodes", matches)
	}
}

// Every match, not the first. A flow's lookup takes matches[0] because it is
// about to tap ONE thing; a query is asked precisely because the operator wants
// to see how many there are.
func TestQueryReturnsEveryMatch(t *testing.T) {
	t.Parallel()

	root := node(map[string]string{"bounds": "[0,0][1000,1000]"},
		node(map[string]string{"text": "Search", "bounds": "[0,0][100,50]"}),
		node(map[string]string{"text": "Search", "bounds": "[0,50][100,100]"}),
	)

	matches, err := matchQuery(root, testViewport, "text=Search")
	if err != nil {
		t.Fatalf("matchQuery() error = %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("matches = %+v, want both", matches)
	}
}

func TestQueryRefusesAnExpressionItCannotRead(t *testing.T) {
	t.Parallel()

	for _, expression := range []string{"General", "name=General", "text="} {
		_, err := matchQuery(node(nil), testViewport, expression)
		if err == nil {
			t.Fatalf("matchQuery(%q) was accepted", expression)
		}
		if !strings.Contains(err.Error(), "text=") || !strings.Contains(err.Error(), "id=") {
			t.Fatalf("error for %q = %q, want it to say what it does understand", expression, err)
		}
	}
}

// A bad regex has to say so. Reporting "no elements matched" would send the
// operator looking at the screen for something that was never asked for.
func TestQueryRefusesABrokenPattern(t *testing.T) {
	t.Parallel()

	if _, err := matchQuery(node(nil), testViewport, "text=[unclosed"); err == nil {
		t.Fatal("a broken pattern was accepted")
	}
}
