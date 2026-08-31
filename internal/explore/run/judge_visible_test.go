package run

import (
	"context"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/explore"
)

// foldedTreeMatch marks an outcome met when the expected words appear in the
// final screen's text, and it read the raw tree. Text on a node the screen
// does not show then passed an expectation nobody could have seen met, which
// is the one wrong answer a testing tool must not give.
func TestTheJudgeReadsOnlyTextTheScreenShows(t *testing.T) {
	t.Parallel()

	label := func(text, bounds string) device.TreeNode {
		return device.TreeNode{Attributes: map[string]string{
			"class": "android.widget.TextView", "text": text, "bounds": bounds,
		}}
	}
	final := &explore.ScreenState{
		Viewport: device.Bounds{Width: 400, Height: 800},
		Hierarchy: device.TreeNode{
			Attributes: map[string]string{"class": "android.widget.FrameLayout", "bounds": "[0,0][400,800]"},
			Children: []device.TreeNode{
				label("Saved", "[0,-900][400,-840]"),
				label("Nothing here yet", "[0,100][400,160]"),
			},
		},
	}
	llm := &scriptedLLM{replies: []explore.Message{
		textReply(`{"met": false, "evidence": "the screen says Nothing here yet"}`),
	}}
	checks := evaluateOutcomes(context.Background(), llm, []string{"Saved"}, nil,
		judgeFacts{Final: final})
	if len(checks) != 1 {
		t.Fatalf("checks = %+v, want one", checks)
	}
	if checks[0].Met {
		t.Fatalf("an outcome passed on text the screen does not show: %+v", checks[0])
	}
}

// The visible half still matches, so the pruning is not blindness.
func TestTheJudgeStillMatchesTextTheScreenShows(t *testing.T) {
	t.Parallel()

	final := &explore.ScreenState{
		Viewport: device.Bounds{Width: 400, Height: 800},
		Hierarchy: device.TreeNode{
			Attributes: map[string]string{"class": "android.widget.FrameLayout", "bounds": "[0,0][400,800]"},
			Children: []device.TreeNode{{
				Attributes: map[string]string{
					"class": "android.widget.TextView", "text": "Saved", "bounds": "[0,100][400,160]",
				},
			}},
		},
	}
	checks := evaluateOutcomes(context.Background(), &scriptedLLM{}, []string{"Saved"}, nil,
		judgeFacts{Final: final})
	if len(checks) != 1 || !checks[0].Met {
		t.Fatalf("checks = %+v, want the visible text to match", checks)
	}
}
