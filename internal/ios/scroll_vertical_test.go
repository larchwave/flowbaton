package ios

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
)

// `- scrollUntilVisible: {direction: RIGHT}` reaches the driver as a
// ScrollVertical along the X axis. The engine's course (interaction_scroll_course)
// treats RIGHT as the mirror of DOWN: it reveals what lies to the right, so the
// finger drags right-to-left. Before issue #19 every horizontal request fell
// through to the vertical drag and the page moved up instead of sideways.

func TestScrollVerticalDragsAlongTheAuthoredAxis(t *testing.T) {
	t.Parallel()

	// A 400x800 screen, amount 0.5: half the axis, centred on 200,400.
	for _, test := range []struct {
		direction device.Direction
		want      SwipeV2Request
	}{
		{"UP", SwipeV2Request{StartX: 200, StartY: 200, EndX: 200, EndY: 600}},
		{"DOWN", SwipeV2Request{StartX: 200, StartY: 600, EndX: 200, EndY: 200}},
		{"LEFT", SwipeV2Request{StartX: 100, StartY: 400, EndX: 300, EndY: 400}},
		{"RIGHT", SwipeV2Request{StartX: 300, StartY: 400, EndX: 100, EndY: 400}},
	} {
		t.Run(string(test.direction), func(t *testing.T) {
			t.Parallel()
			recorder := &swipeRecorder{}
			driver := newTestDriver(t, recorder.handler(t, 400, 800))

			err := driver.ScrollVertical(context.Background(), device.ScrollVerticalRequest{
				Direction: test.direction, Amount: 0.5,
			})
			if err != nil {
				t.Fatalf("ScrollVertical(%s) = %v", test.direction, err)
			}

			want := test.want
			want.Duration = defaultSwipeSeconds
			if swipe := recorder.only(t); !reflect.DeepEqual(swipe, want) {
				t.Fatalf("swipeV2 = %#v, want %#v", swipe, want)
			}
		})
	}
}

func TestScrollVerticalRefusesAnUnknownDirectionInsteadOfScrollingDown(t *testing.T) {
	t.Parallel()

	driver := newTestDriver(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/swipeV2" {
			t.Error("an unknown direction reached the device")
		}
		writeJSON(t, writer, map[string]any{
			"widthPoints": 400.0, "heightPoints": 800.0,
			"widthPixels": 1200.0, "heightPixels": 2400.0,
		})
	})

	err := driver.ScrollVertical(context.Background(), device.ScrollVerticalRequest{Direction: "DIAGONAL"})
	if err == nil {
		t.Fatal("ScrollVertical() accepted the direction DIAGONAL")
	}
}
