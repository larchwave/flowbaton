package ios

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
)

// `- swipe: {direction: UP}` hands the driver a
// direction. specs/02-device-drivers.md §1 defines direction-only and
// element-plus-direction shapes beside explicit points. The driver resolves
// device geometry and applies the shared grid rule from line 43 so gesture
// distance remains uniform across platforms.

// swipeRecorder answers deviceInfo with a known screen and records the swipe
// points the driver derived from it.
type swipeRecorder struct {
	mu       sync.Mutex
	requests []SwipeV2Request
}

func (recorder *swipeRecorder) handler(t *testing.T, width, height float64) http.HandlerFunc {
	t.Helper()
	return func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/deviceInfo":
			writeJSON(t, writer, map[string]any{
				"widthPoints": width, "heightPoints": height,
				"widthPixels": width * 3, "heightPixels": height * 3,
			})
		case "/swipeV2":
			var body SwipeV2Request
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode swipeV2 body: %v", err)
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			recorder.mu.Lock()
			recorder.requests = append(recorder.requests, body)
			recorder.mu.Unlock()
		default:
			t.Errorf("unexpected request to %s", request.URL.Path)
			writer.WriteHeader(http.StatusNotFound)
		}
	}
}

func (recorder *swipeRecorder) only(t *testing.T) SwipeV2Request {
	t.Helper()
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.requests) != 1 {
		t.Fatalf("swipeV2 was called %d time(s), want once", len(recorder.requests))
	}
	return recorder.requests[0]
}

func TestADirectionalSwipeIsConvertedToPointsOnTheGrid(t *testing.T) {
	t.Parallel()

	// A 400x800 screen: centre is 200,400; the ten-percent edges are 80 and 40.
	for _, test := range []struct {
		direction device.Direction
		wantEndX  float64
		wantEndY  float64
	}{
		{"UP", 200, 80},
		{"DOWN", 200, 720},
		{"LEFT", 40, 400},
		{"RIGHT", 360, 400},
	} {
		t.Run(string(test.direction), func(t *testing.T) {
			t.Parallel()
			recorder := &swipeRecorder{}
			driver := newTestDriver(t, recorder.handler(t, 400, 800))

			err := driver.Swipe(context.Background(), device.SwipeRequest{
				Direction: test.direction, DurationMillis: 250,
			})
			if err != nil {
				t.Fatalf("Swipe(%s) = %v", test.direction, err)
			}

			swipe := recorder.only(t)
			want := SwipeV2Request{
				StartX: 200, StartY: 400,
				EndX: test.wantEndX, EndY: test.wantEndY,
				Duration: 0.25,
			}
			if !reflect.DeepEqual(swipe, want) {
				t.Fatalf("swipeV2 = %#v, want %#v", swipe, want)
			}
		})
	}
}

func TestASwipeFromAnElementStartsAtTheElementNotTheCentre(t *testing.T) {
	t.Parallel()

	// The element-point shape. Starting at the centre instead would swipe the
	// wrong row of a list, which looks like a flaky test rather than a bug.
	recorder := &swipeRecorder{}
	driver := newTestDriver(t, recorder.handler(t, 400, 800))

	err := driver.Swipe(context.Background(), device.SwipeRequest{
		ElementPoint: &device.Point{X: 120, Y: 600}, Direction: "UP",
	})
	if err != nil {
		t.Fatalf("Swipe() = %v", err)
	}

	swipe := recorder.only(t)
	want := SwipeV2Request{StartX: 120, StartY: 600, EndX: 120, EndY: 80, Duration: defaultSwipeSeconds}
	if !reflect.DeepEqual(swipe, want) {
		t.Fatalf("swipeV2 = %#v, want %#v", swipe, want)
	}
}

func TestAnExplicitPointSwipeStillNeverAsksForTheScreen(t *testing.T) {
	t.Parallel()

	// The shape that already worked must not grow a deviceInfo round trip: the
	// caller already resolved the points, and an extra request per swipe is a
	// cost every flow pays for a number nobody reads.
	recorder := &swipeRecorder{}
	driver := newTestDriver(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/deviceInfo" {
			t.Error("an explicit-point swipe asked for deviceInfo")
		}
		recorder.handler(t, 400, 800)(writer, request)
	})

	err := driver.Swipe(context.Background(), device.SwipeRequest{
		Start: &device.Point{X: 1, Y: 2}, End: &device.Point{X: 3, Y: 4}, DurationMillis: 1000,
	})
	if err != nil {
		t.Fatalf("Swipe() = %v", err)
	}

	swipe := recorder.only(t)
	want := SwipeV2Request{StartX: 1, StartY: 2, EndX: 3, EndY: 4, Duration: 1}
	if !reflect.DeepEqual(swipe, want) {
		t.Fatalf("swipeV2 = %#v, want %#v", swipe, want)
	}
}

func TestASwipeWithNeitherPointsNorADirectionIsStillRefused(t *testing.T) {
	t.Parallel()

	// The refusal that has to survive. An empty request is a caller bug, and
	// swiping the centre to the centre would report success for a gesture that
	// moved nothing.
	driver := newTestDriver(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/swipeV2" {
			t.Error("an empty swipe request reached the device")
		}
		writeJSON(t, writer, map[string]any{
			"widthPoints": 400.0, "heightPoints": 800.0,
			"widthPixels": 1200.0, "heightPixels": 2400.0,
		})
	})

	err := driver.Swipe(context.Background(), device.SwipeRequest{})
	if err == nil {
		t.Fatal("Swipe() accepted a request with no points and no direction")
	}
	// The MESSAGE is asserted, not just the error: without the explicit guard an
	// empty request still gets refused, by the direction switch, complaining that
	// "" is not a direction. That is the wrong thing to tell someone whose
	// request had no direction field at all — and it costs a deviceInfo round
	// trip to say it.
	if !strings.Contains(err.Error(), "either both points or a direction") {
		t.Fatalf("Swipe() error = %v, want it to name the missing points", err)
	}
}

func TestAnUnknownSwipeDirectionIsRefusedRatherThanGuessed(t *testing.T) {
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

	err := driver.Swipe(context.Background(), device.SwipeRequest{Direction: "DIAGONAL"})
	if err == nil {
		t.Fatal("Swipe() accepted the direction DIAGONAL")
	}
}
