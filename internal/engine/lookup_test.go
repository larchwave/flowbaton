package engine_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/engine"
	"github.com/nohavewho/flowbaton/internal/enginetest"
	"github.com/nohavewho/flowbaton/internal/model"
)

func TestElementLookupAdjustedTimeout(t *testing.T) {
	start := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		optional    bool
		interaction *time.Time
		override    *time.Duration
		want        time.Duration
	}{
		{name: "required default", want: 17 * time.Second},
		{name: "optional default", optional: true, want: 7 * time.Second},
		{name: "required subtracts elapsed", interaction: timePtr(start.Add(-3 * time.Second)), want: 14 * time.Second},
		{name: "optional subtracts elapsed", optional: true, interaction: timePtr(start.Add(-3 * time.Second)), want: 4 * time.Second},
		{name: "elapsed floors at zero", interaction: timePtr(start.Add(-18 * time.Second)), want: 0},
		{name: "future interaction does not increase timeout", interaction: timePtr(start.Add(time.Second)), want: 17 * time.Second},
		{name: "explicit timeout is not adjusted", interaction: timePtr(start.Add(-3 * time.Second)), override: durationPtr(2 * time.Second), want: 2 * time.Second},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clock := enginetest.NewFakeClock(start)
			lookup := engine.NewElementLookup(enginetest.NewFakeDriver(), clock)
			if test.interaction != nil {
				lookup.RecordInteraction(*test.interaction)
			}

			got := lookup.AdjustedTimeout(engine.LookupOptions{
				Optional: test.optional,
				Timeout:  test.override,
			})
			if got != test.want {
				t.Fatalf("AdjustedTimeout() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestElementLookupFindNormalizesFiltersAndMatches(t *testing.T) {
	clock := enginetest.NewFakeClock(time.Unix(0, 0))
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo: []enginetest.Result[device.DeviceInfo]{{
			Value: device.DeviceInfo{WidthGrid: 100, HeightGrid: 100},
		}},
		ContentDescriptor: []enginetest.Result[device.TreeNode]{{
			Value: device.TreeNode{
				Attributes: map[string]string{"bounds": "[0,0][100,100]"},
				Children: []device.TreeNode{
					{Attributes: map[string]string{"text": "Ready", "bounds": "[10,20][40,50]"}},
					{Attributes: map[string]string{"text": "Ready", "bounds": "[200,200][240,240]"}},
				},
			},
		}},
	})
	lookup := engine.NewElementLookup(driver, clock)
	text := "Ready"

	element, err := lookup.Find(context.Background(), model.ElementSelector{TextRegex: &text}, engine.LookupOptions{})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if element == nil {
		t.Fatal("Find() returned nil element")
	}
	wantBounds := device.Bounds{X: 10, Y: 20, Width: 30, Height: 30}
	if element.Bounds != wantBounds {
		t.Fatalf("Find() bounds = %+v, want %+v", element.Bounds, wantBounds)
	}

	wantMethods := []enginetest.Method{enginetest.MethodDeviceInfo, enginetest.MethodContentDescriptor}
	actions := driver.Actions()
	gotMethods := make([]enginetest.Method, len(actions))
	for index := range actions {
		gotMethods[index] = actions[index].Method
	}
	if !reflect.DeepEqual(gotMethods, wantMethods) {
		t.Fatalf("driver methods = %v, want %v", gotMethods, wantMethods)
	}
}

func TestElementLookupFindRequiredMissReturnsOperationError(t *testing.T) {
	driver := emptyHierarchyDriver()
	lookup := engine.NewElementLookup(driver, enginetest.NewFakeClock(time.Unix(0, 0)))
	missing := "missing"

	element, err := lookup.Find(context.Background(), model.ElementSelector{TextRegex: &missing}, engine.LookupOptions{Timeout: durationPtr(0)})
	if element != nil {
		t.Fatalf("Find() element = %+v, want nil", element)
	}
	var operationErr *engine.OperationError
	if !errors.As(err, &operationErr) {
		t.Fatalf("Find() error = %T %v, want *engine.OperationError", err, err)
	}
}

func TestElementLookupFindOptionalMissIsNilElementWithoutError(t *testing.T) {
	driver := emptyHierarchyDriver()
	lookup := engine.NewElementLookup(driver, enginetest.NewFakeClock(time.Unix(0, 0)))
	missing := "missing"

	element, err := lookup.Find(context.Background(), model.ElementSelector{TextRegex: &missing}, engine.LookupOptions{
		Optional: true,
		Timeout:  durationPtr(0),
	})
	if err != nil {
		t.Fatalf("Find() error = %v, want nil", err)
	}
	if element != nil {
		t.Fatalf("Find() element = %+v, want nil", element)
	}
}

func TestElementLookupCancellationBeforeFindMakesNoDriverCalls(t *testing.T) {
	driver := enginetest.NewFakeDriver()
	lookup := engine.NewElementLookup(driver, enginetest.NewFakeClock(time.Unix(0, 0)))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := lookup.Find(ctx, model.ElementSelector{}, engine.LookupOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Find() error = %v, want context.Canceled", err)
	}
	if actions := driver.Actions(); len(actions) != 0 {
		t.Fatalf("driver actions = %v, want none", actions)
	}
}

func TestElementLookupFindPollsUntilMatchAndCachesDeviceInfo(t *testing.T) {
	clock := enginetest.NewFakeClock(time.Unix(0, 0))
	driver := hierarchySequenceDriver(emptyNode(), emptyNode(), visibleNode("Ready"))
	lookup := engine.NewElementLookup(driver, clock)
	text := "Ready"
	done := make(chan struct {
		found bool
		err   error
	}, 1)
	go func() {
		element, err := lookup.Find(context.Background(), model.ElementSelector{TextRegex: &text}, engine.LookupOptions{Timeout: durationPtr(time.Second)})
		done <- struct {
			found bool
			err   error
		}{found: element != nil, err: err}
	}()

	advancePendingTimer(t, clock, 100*time.Millisecond)
	advancePendingTimer(t, clock, 100*time.Millisecond)
	select {
	case result := <-done:
		if result.err != nil || !result.found {
			t.Fatalf("Find() = found %v, error %v; want match", result.found, result.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Find() did not finish")
	}
	if got := countDriverMethod(driver, enginetest.MethodDeviceInfo); got != 1 {
		t.Fatalf("DeviceInfo calls = %d, want cached single call", got)
	}
	if got := countDriverMethod(driver, enginetest.MethodContentDescriptor); got != 3 {
		t.Fatalf("ContentDescriptor calls = %d, want 3", got)
	}
}

func TestElementLookupCancellationDuringPollPreventsLaterCalls(t *testing.T) {
	clock := enginetest.NewFakeClock(time.Unix(0, 0))
	driver := hierarchySequenceDriver(emptyNode(), visibleNode("Ready"))
	lookup := engine.NewElementLookup(driver, clock)
	text := "Ready"
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := lookup.Find(ctx, model.ElementSelector{TextRegex: &text}, engine.LookupOptions{Timeout: durationPtr(time.Second)})
		done <- err
	}()

	waitForPendingTimer(t, clock)
	cancel()
	if err := receiveError(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("Find() error = %v, want context.Canceled", err)
	}
	clock.Advance(time.Second)
	if got := countDriverMethod(driver, enginetest.MethodContentDescriptor); got != 1 {
		t.Fatalf("ContentDescriptor calls after cancellation = %d, want 1", got)
	}
}

func emptyHierarchyDriver() *enginetest.FakeDriver {
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo:        []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{WidthGrid: 100, HeightGrid: 100}}},
		ContentDescriptor: []enginetest.Result[device.TreeNode]{{Value: device.TreeNode{Attributes: map[string]string{}}}},
	})
	return driver
}

func durationPtr(value time.Duration) *time.Duration { return &value }

func timePtr(value time.Time) *time.Time { return &value }

// CSS is a query, not a value, so it cannot be matched against a captured
// attribute: the driver resolves it in the page and the lookup keeps only the
// hierarchy nodes the driver named. The rest of the selector still applies, so
// `css` composes with text, index and the relational fields like any other
// field rather than short-circuiting them.
func TestElementLookupResolvesCSSThroughTheDriver(t *testing.T) {
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{
		Capabilities: []device.Capabilities{
			{Platform: "web", Features: map[string]bool{"cssSelector": true}},
		},
		DeviceInfo: []enginetest.Result[device.DeviceInfo]{{
			Value: device.DeviceInfo{WidthGrid: 100, HeightGrid: 100},
		}},
		ContentDescriptor: []enginetest.Result[device.TreeNode]{{
			Value: device.TreeNode{
				Attributes: map[string]string{"bounds": "[0,0][100,100]"},
				Children: []device.TreeNode{
					{Attributes: map[string]string{"text": "Ready", "css": "#other", "bounds": "[10,20][40,50]"}},
					{Attributes: map[string]string{"text": "Ready", "css": "#login", "bounds": "[50,20][90,50]"}},
				},
			},
		}},
		QueryOnDeviceElements: []enginetest.Result[[]device.TreeNode]{{
			Value: []device.TreeNode{{Attributes: map[string]string{"css": "#login"}}},
		}},
	})
	lookup := engine.NewElementLookup(driver, enginetest.NewFakeClock(time.Unix(0, 0)))
	css, text := "#login", "Ready"

	element, err := lookup.Find(context.Background(),
		model.ElementSelector{CSS: &css, TextRegex: &text}, engine.LookupOptions{})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if element == nil {
		t.Fatal("Find() = nil; the css match was dropped")
	}
	// Both nodes carry the text; only the one the driver named may win.
	if got := element.Node.Attributes["css"]; got != "#login" {
		t.Fatalf("matched css = %q, want #login", got)
	}
}

// A css selector on a driver that cannot resolve one must fail loudly. Silently
// ignoring it would match the first node with the same text on any platform,
// which is a wrong element rather than a missing one.
func TestElementLookupRefusesCSSWhenTheDriverCannotResolveIt(t *testing.T) {
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{
		Capabilities: []device.Capabilities{
			{Platform: "android", Features: map[string]bool{"cssSelector": false}},
		},
		DeviceInfo: []enginetest.Result[device.DeviceInfo]{{
			Value: device.DeviceInfo{WidthGrid: 100, HeightGrid: 100},
		}},
		ContentDescriptor: []enginetest.Result[device.TreeNode]{{
			Value: device.TreeNode{Attributes: map[string]string{"bounds": "[0,0][100,100]"}},
		}},
	})
	lookup := engine.NewElementLookup(driver, enginetest.NewFakeClock(time.Unix(0, 0)))
	css := "#login"

	_, err := lookup.Find(context.Background(), model.ElementSelector{CSS: &css}, engine.LookupOptions{})
	if err == nil {
		t.Fatal("Find() error = nil; a css selector on a non-web driver must fail")
	}
}
