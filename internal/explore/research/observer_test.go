package research

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
)

type fakeDriver struct {
	device.Driver
	static    bool
	staticErr error
	tree      device.TreeNode
	treeErr   error
	shot      []byte
	shotErr   error

	// staticSeq is consumed in order when set, so a test can script a screen
	// that moves for a while and then settles; `static` answers once it runs
	// out. staticCalls counts every answer given.
	staticSeq   []bool
	staticCalls int

	staticReq device.ScreenStaticRequest
	descReq   device.ContentDescriptorRequest
	shotReq   device.ScreenshotRequest

	infoErr error
}

func (f *fakeDriver) DeviceInfo(context.Context) (device.DeviceInfo, error) {
	if f.infoErr != nil {
		return device.DeviceInfo{}, f.infoErr
	}
	return device.DeviceInfo{Platform: "android", WidthGrid: 400, HeightGrid: 800}, nil
}

func (f *fakeDriver) WaitUntilScreenIsStatic(_ context.Context, req device.ScreenStaticRequest) (bool, error) {
	f.staticReq = req
	f.staticCalls++
	if len(f.staticSeq) > 0 {
		answer := f.staticSeq[0]
		f.staticSeq = f.staticSeq[1:]
		return answer, f.staticErr
	}
	return f.static, f.staticErr
}

func (f *fakeDriver) ContentDescriptor(_ context.Context, req device.ContentDescriptorRequest) (device.TreeNode, error) {
	f.descReq = req
	return f.tree, f.treeErr
}

func (f *fakeDriver) TakeScreenshot(_ context.Context, req device.ScreenshotRequest) ([]byte, error) {
	f.shotReq = req
	return f.shot, f.shotErr
}

func testNode(attrs map[string]string, children ...device.TreeNode) device.TreeNode {
	return device.TreeNode{Attributes: attrs, Children: children}
}

func clickableNode(attrs map[string]string) device.TreeNode {
	yes := true
	return device.TreeNode{Attributes: attrs, Clickable: &yes}
}

func testTree() device.TreeNode {
	return testNode(map[string]string{},
		clickableNode(map[string]string{
			"class":       "android.widget.Button",
			"text":        "Save",
			"resource-id": "com.example:id/save",
			"bounds":      "[10,20][110,60]",
		}),
		clickableNode(map[string]string{
			"class":  "android.widget.Button",
			"text":   "Cancel",
			"bounds": "[120,20][220,60]",
		}),
	)
}

func TestObserveCapturesScreenState(t *testing.T) {
	driver := &fakeDriver{static: true, tree: testTree(), shot: []byte("png-bytes")}
	captured := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	observer := &Observer{
		Driver:        driver,
		AppID:         "com.example.app",
		SettleTimeout: 2 * time.Second,
		Clock:         func() time.Time { return captured },
	}
	state, err := observer.Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := driver.staticReq.TimeoutMillis; got != 2000 {
		t.Fatalf("settle timeout %d, want 2000", got)
	}
	if len(driver.descReq.AppIDs) != 1 || driver.descReq.AppIDs[0] != "com.example.app" {
		t.Fatalf("hierarchy request not scoped to app: %+v", driver.descReq)
	}
	if driver.shotReq.Compressed {
		t.Fatal("screenshot request must be uncompressed")
	}
	if len(state.Elements) != 2 {
		t.Fatalf("elements %d, want 2", len(state.Elements))
	}
	if state.Signature.AppID != "com.example.app" || state.Signature.TreeDigest == "" {
		t.Fatalf("bad signature %+v", state.Signature)
	}
	if string(state.ScreenshotPNG) != "png-bytes" {
		t.Fatalf("screenshot %q", state.ScreenshotPNG)
	}
	if !state.CapturedAt.Equal(captured) {
		t.Fatalf("captured at %v", state.CapturedAt)
	}
	if state.DialogActive {
		t.Fatal("no modal surface on this screen")
	}
}

func TestObserveContinuesWhenScreenNotStatic(t *testing.T) {
	driver := &fakeDriver{static: false, tree: testTree(), shot: []byte("p")}
	notes := []string{}
	observer := &Observer{
		Driver: driver,
		AppID:  "com.example.app",
		// This test is about capturing a screen that never settles, not
		// about how long the observer is willing to wait for one.
		SettleTimeout: 10 * time.Millisecond,
		Logf:          func(format string, args ...any) { notes = append(notes, format) },
	}
	state, err := observer.Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state == nil {
		t.Fatal("state must be captured anyway")
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "still moving") {
		t.Fatalf("expected an unsettled note, got %v", notes)
	}
}

func TestObserveContinuesWhenSettleUnsupported(t *testing.T) {
	driver := &fakeDriver{staticErr: device.ErrUnsupported, tree: testTree(), shot: []byte("p")}
	observer := &Observer{Driver: driver, AppID: "com.example.app"}
	if _, err := observer.Observe(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestObserveFailsOnErrors(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name   string
		driver *fakeDriver
	}{
		{"settle error", &fakeDriver{staticErr: boom}},
		{"hierarchy error", &fakeDriver{static: true, treeErr: boom}},
		{"screenshot error", &fakeDriver{static: true, tree: testTree(), shotErr: boom}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observer := &Observer{Driver: tt.driver, AppID: "com.example.app"}
			if _, err := observer.Observe(context.Background()); !errors.Is(err, boom) {
				t.Fatalf("err = %v, want wrapped boom", err)
			}
		})
	}
}

func TestObserveValidatesInputs(t *testing.T) {
	if _, err := (&Observer{AppID: "a"}).Observe(context.Background()); err == nil {
		t.Fatal("nil driver must fail")
	}
	if _, err := (&Observer{Driver: &fakeDriver{}}).Observe(context.Background()); err == nil {
		t.Fatal("blank app id must fail")
	}
}

func TestObserveRespectsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	observer := &Observer{Driver: &fakeDriver{static: true, tree: testTree()}, AppID: "a"}
	if _, err := observer.Observe(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestDialogActiveHeuristic(t *testing.T) {
	content := testNode(map[string]string{"class": "android.widget.FrameLayout"},
		clickableNode(map[string]string{"class": "android.widget.Button", "text": "Go"}))
	tests := []struct {
		name string
		root device.TreeNode
		want bool
	}{
		{
			"late alert subtree",
			testNode(nil, content, testNode(map[string]string{"class": "android.app.AlertDialogLayout"})),
			true,
		},
		{
			"nested dialog in late subtree",
			testNode(nil, content, testNode(map[string]string{"class": "android.widget.FrameLayout"},
				testNode(map[string]string{"type": "SheetPresentation"}))),
			true,
		},
		{
			"first subtree named dialog does not count",
			testNode(nil, testNode(map[string]string{"class": "DialogHost"}), content),
			false,
		},
		{
			"no modal surface",
			testNode(nil, content, testNode(map[string]string{"class": "android.widget.LinearLayout"})),
			false,
		},
		{
			"single subtree",
			testNode(nil, content),
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dialogActive(tt.root); got != tt.want {
				t.Fatalf("dialogActive = %v, want %v", got, tt.want)
			}
		})
	}
}

// Measured on a booted simulator with a focused field in com.apple.reminders:
// the tree is 342 nodes with the keyboard and 301 without it, so the keyboard
// is 41 rows -- a row per key -- in the element table the tester reads. In
// session mmx34 the model spent four steps tapping "the element with id
// Return" instead of pressing a key, which is what those rows invite. It has
// press_key and hide_keyboard for the keyboard; it needs the app's elements.
func TestObserveAsksForTheAppsOwnElementsWithoutTheKeyboardKeys(t *testing.T) {
	driver := &fakeDriver{static: true}
	observer := &Observer{Driver: driver, AppID: "com.example.app"}
	if _, err := observer.Observe(context.Background()); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if !driver.descReq.ExcludeKeyboardElements {
		t.Fatalf("hierarchy request = %+v, want the keyboard keys left out", driver.descReq)
	}
}

// Everything downstream that selects an element by name prunes the tree to
// this box. An observation without it prunes nothing, so a name would reach
// an element with no area or one past the screen edge.
func TestObservationCarriesTheScreenItWasTakenOn(t *testing.T) {
	driver := &fakeDriver{static: true, tree: device.TreeNode{
		Attributes: map[string]string{"class": "android.widget.FrameLayout", "bounds": "[0,0][400,800]"},
	}}
	observer := &Observer{Driver: driver, AppID: "com.example.app"}
	state, err := observer.Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Viewport.Width != 400 || state.Viewport.Height != 800 {
		t.Fatalf("observation viewport = %+v, want the screen the driver reports", state.Viewport)
	}
}

// A driver that cannot say how big its own screen is cannot be asked what is
// on it, so the observation fails rather than arriving unprunable.
func TestObserveFailsWhenTheScreenSizeIsUnknown(t *testing.T) {
	driver := &fakeDriver{static: true, infoErr: errors.New("no screen"), tree: device.TreeNode{
		Attributes: map[string]string{"class": "android.widget.FrameLayout", "bounds": "[0,0][400,800]"},
	}}
	if _, err := (&Observer{Driver: driver, AppID: "com.example.app"}).Observe(context.Background()); err == nil {
		t.Fatal("want an error when the screen size is unknown")
	}
}
