package run

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
)

// resolvePoint's text and id branch matched the raw tree and tapped the
// centre of the first candidate with bounds. check_visible had the same
// defect twelve lines away and was fixed; this is the half that taps. An
// element with no area centres on (0,0) and one past the screen edge centres
// outside it, and both were reachable by name.
func TestTapByNameLandsOnSomethingTheScreenShows(t *testing.T) {
	t.Parallel()
	label := func(text, bounds string) device.TreeNode {
		return device.TreeNode{Attributes: map[string]string{
			"class": "android.widget.TextView", "text": text, "bounds": bounds,
		}}
	}
	session, driver := inputSession(t, screen("List",
		label("Save", "[0,0][0,0]"),
		label("Delete", "[500,900][600,950]"),
		label("Keep", "[100,600][300,660]"),
	))
	for _, name := range []string{"Save", "Delete"} {
		reply, err := session.handleTap(context.Background(),
			json.RawMessage(`{"text":"`+name+`"}`))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if reply == "" {
			t.Fatalf("%s: empty reply", name)
		}
	}
	for _, point := range driver.tapped {
		t.Logf("tapped %+v", point)
		if point.X <= 0 && point.Y <= 0 {
			t.Errorf("a tap landed on the screen corner: %+v", point)
		}
		if point.X > 400 || point.Y > 800 {
			t.Errorf("a tap landed past the screen edge: %+v", point)
		}
	}
	if len(driver.tapped) != 0 {
		t.Errorf("neither name names anything on screen, so nothing should have been tapped: %+v", driver.tapped)
	}
}
