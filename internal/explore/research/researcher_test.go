package research

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/explore"
)

type scriptedLLM struct {
	replies  []string
	err      error
	requests []explore.ChatRequest
}

func (s *scriptedLLM) Chat(_ context.Context, req explore.ChatRequest) (explore.ChatResponse, error) {
	s.requests = append(s.requests, req)
	if s.err != nil {
		return explore.ChatResponse{}, s.err
	}
	if len(s.requests) > len(s.replies) {
		return explore.ChatResponse{}, errors.New("script exhausted")
	}
	return explore.ChatResponse{
		Message: explore.Message{Role: explore.RoleAssistant, Text: s.replies[len(s.requests)-1]},
		Usage:   explore.Usage{InputTokens: 1, OutputTokens: 1},
	}, nil
}

type fakeCache struct {
	entries map[string]*explore.UIMap
	puts    int
}

// The fake files entries the way the real cache does, by the whole digest,
// so a test cannot pass by agreeing with a rendered name.
func (f *fakeCache) Get(screen explore.ScreenSignature) (*explore.UIMap, bool) {
	m, ok := f.entries[screen.TreeDigest]
	return m, ok
}

func (f *fakeCache) Put(screen explore.ScreenSignature, m *explore.UIMap) {
	if f.entries == nil {
		f.entries = map[string]*explore.UIMap{}
	}
	f.entries[screen.TreeDigest] = m
	f.puts++
}

func researchTree() device.TreeNode {
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
		clickableNode(map[string]string{
			"class": "android.widget.TextView",
			"text":  "Row",
		}),
		clickableNode(map[string]string{
			"class": "android.widget.TextView",
			"text":  "Row",
		}),
	)
}

func researchState(t *testing.T, root device.TreeNode, shot []byte) *explore.ScreenState {
	t.Helper()
	elements, err := explore.FlattenScreen(root)
	if err != nil {
		t.Fatal(err)
	}
	return &explore.ScreenState{
		Signature:     explore.ComputeSignature("com.example.app", root),
		Hierarchy:     root,
		Elements:      elements,
		ScreenshotPNG: shot,
	}
}

const validSectionsJSON = `{"sections":[{"name":"Actions","notes":"commit or discard",` +
	`"elements":[{"eidx":0,"role":"button","label":"Save"},{"eidx":1,"role":"button","label":"Cancel"}]},` +
	`{"name":"List","notes":"rows","elements":[{"eidx":2,"role":"cell","label":"Row"},{"eidx":3,"role":"cell","label":"Row"}]}]}`

func TestResearchBuildsValidatedMap(t *testing.T) {
	worker := &scriptedLLM{replies: []string{"```json\n" + validSectionsJSON + "\n```"}}
	cache := &fakeCache{}
	created := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	researcher := &Researcher{
		Models: explore.ModelSet{Worker: worker},
		Cache:  cache,
		Clock:  func() time.Time { return created },
	}
	state := researchState(t, researchTree(), nil)
	uiMap, err := researcher.Research(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if len(uiMap.Sections) != 2 || uiMap.Sections[0].Name != "Actions" {
		t.Fatalf("sections %+v", uiMap.Sections)
	}
	if !uiMap.CreatedAt.Equal(created) || !uiMap.Screen.Same(state.Signature) {
		t.Fatalf("map identity %+v", uiMap)
	}
	save := uiMap.Sections[0].Elements[0]
	if save.Locators[0].Kind != explore.LocatorID || save.Locators[0].Value != "com.example:id/save" {
		t.Fatalf("save locator %+v", save.Locators)
	}
	if save.Locators[1].Kind != explore.LocatorPoint || save.Locators[1].Value != "60,40" {
		t.Fatalf("save point locator %+v", save.Locators)
	}
	if !strings.Contains(uiMap.Markdown, "## Actions") || !strings.Contains(uiMap.Markdown, "id=com.example:id/save") {
		t.Fatalf("markdown missing content:\n%s", uiMap.Markdown)
	}
	if cache.puts != 1 {
		t.Fatalf("cache puts %d, want 1", cache.puts)
	}
	if _, ok := cache.entries[state.Signature.TreeDigest]; !ok {
		t.Fatal("cache not keyed by the screen digest")
	}
	if len(worker.requests) != 1 {
		t.Fatalf("worker calls %d, want 1", len(worker.requests))
	}
	if table := worker.requests[0].Messages[1].Text; !strings.Contains(table, "| 0 |") {
		t.Fatalf("prompt missing element table:\n%s", table)
	}
}

func TestElementLocatorLadder(t *testing.T) {
	state := researchState(t, researchTree(), nil)
	counts := map[string]int{}
	for _, element := range state.Elements {
		if label := nodeLabel(element.Node); label != "" {
			counts[label]++
		}
	}
	tests := []struct {
		name     string
		element  explore.FlatElement
		wantKind explore.LocatorKind
		wantVal  string
		point    bool
	}{
		{"identifier wins", state.Elements[0], explore.LocatorID, "com.example:id/save", true},
		{"unique text", state.Elements[1], explore.LocatorText, "Cancel", true},
		{"duplicate text falls to path", state.Elements[2], explore.LocatorPath, "2", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			locators := elementLocators(tt.element, counts, device.Bounds{})
			if locators[0].Kind != tt.wantKind || locators[0].Value != tt.wantVal {
				t.Fatalf("best locator %+v, want %s=%s", locators[0], tt.wantKind, tt.wantVal)
			}
			hasPoint := len(locators) > 1 && locators[1].Kind == explore.LocatorPoint
			if hasPoint != tt.point {
				t.Fatalf("point locator presence %v, want %v (%+v)", hasPoint, tt.point, locators)
			}
		})
	}
}

func TestResearchRetriesUnknownIndexOnce(t *testing.T) {
	first := `{"sections":[{"name":"Main","elements":[{"eidx":0,"label":"Save"},{"eidx":99,"label":"Ghost"}]}]}`
	second := `{"sections":[{"name":"Main","elements":[{"eidx":0,"label":"Save"},{"eidx":77,"label":"Still ghost"}]}]}`
	worker := &scriptedLLM{replies: []string{first, second}}
	researcher := &Researcher{Models: explore.ModelSet{Worker: worker}}
	uiMap, err := researcher.Research(context.Background(), researchState(t, researchTree(), nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(worker.requests) != 2 {
		t.Fatalf("worker calls %d, want 2", len(worker.requests))
	}
	complaint := worker.requests[1].Messages[len(worker.requests[1].Messages)-1].Text
	if !strings.Contains(complaint, "99") {
		t.Fatalf("complaint must name the bad eidx: %q", complaint)
	}
	elements := uiMap.Sections[0].Elements
	if len(elements) != 1 || elements[0].EIDX != 0 {
		t.Fatalf("still-unknown entries must be dropped: %+v", elements)
	}
}

func TestResearchVisionMergesNotes(t *testing.T) {
	worker := &scriptedLLM{replies: []string{validSectionsJSON}}
	vision := &scriptedLLM{replies: []string{
		`{"elements":[{"eidx":0,"label":"Save changes","notes":"green accent"},{"eidx":1,"notes":"plain"},{"eidx":42,"notes":"ghost"}]}`,
	}}
	researcher := &Researcher{Models: explore.ModelSet{Worker: worker, Vision: vision}}
	state := researchState(t, researchTree(), []byte("png"))
	uiMap, err := researcher.Research(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if len(vision.requests) != 1 {
		t.Fatalf("vision calls %d, want 1", len(vision.requests))
	}
	if img := vision.requests[0].Messages[1].ImagePNG; string(img) != "png" {
		t.Fatalf("vision request must carry the screenshot, got %q", img)
	}
	save := uiMap.Sections[0].Elements[0]
	if save.Label != "Save changes" || !strings.Contains(save.Notes, "green accent") {
		t.Fatalf("vision merge missing on save: %+v", save)
	}
	cancel := uiMap.Sections[0].Elements[1]
	if cancel.Label != "Cancel" || cancel.Notes != "plain" {
		t.Fatalf("vision merge wrong on cancel: %+v", cancel)
	}
}

func TestResearchVisionSkippedWhenNil(t *testing.T) {
	worker := &scriptedLLM{replies: []string{validSectionsJSON}}
	researcher := &Researcher{Models: explore.ModelSet{Worker: worker}}
	if _, err := researcher.Research(context.Background(), researchState(t, researchTree(), []byte("png"))); err != nil {
		t.Fatal(err)
	}
	if len(worker.requests) != 1 {
		t.Fatalf("worker calls %d, want 1", len(worker.requests))
	}
}

func TestResearchCacheHitSkipsModel(t *testing.T) {
	state := researchState(t, researchTree(), nil)
	cached := &explore.UIMap{
		Screen: state.Signature,
		Sections: []explore.Section{{
			Name:     "Cached",
			Elements: []explore.MappedElement{{EIDX: 0, Label: "Save"}},
		}},
		Markdown: "# cached",
	}
	worker := &scriptedLLM{}
	researcher := &Researcher{
		Models: explore.ModelSet{Worker: worker},
		Cache:  &fakeCache{entries: map[string]*explore.UIMap{state.Signature.TreeDigest: cached}},
	}
	uiMap, err := researcher.Research(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if len(worker.requests) != 0 {
		t.Fatalf("cache hit must not call the model, got %d calls", len(worker.requests))
	}
	if uiMap.Markdown != "# cached" || len(uiMap.Sections) != 1 {
		t.Fatalf("cached map content lost: %+v", uiMap)
	}
	uiMap.Sections[0].Name = "mutated"
	uiMap.Sections[0].Elements[0].Label = "mutated"
	if cached.Sections[0].Name != "Cached" || cached.Sections[0].Elements[0].Label != "Save" {
		t.Fatal("returned map must be a copy of the cached one")
	}
}

func TestResearchFailsClosed(t *testing.T) {
	state := researchState(t, researchTree(), nil)
	t.Run("nil state", func(t *testing.T) {
		researcher := &Researcher{Models: explore.ModelSet{Worker: &scriptedLLM{}}}
		if _, err := researcher.Research(context.Background(), nil); err == nil {
			t.Fatal("nil state must fail")
		}
	})
	t.Run("no worker", func(t *testing.T) {
		if _, err := (&Researcher{}).Research(context.Background(), state); err == nil {
			t.Fatal("missing worker must fail")
		}
	})
	t.Run("model error", func(t *testing.T) {
		boom := errors.New("boom")
		researcher := &Researcher{Models: explore.ModelSet{Worker: &scriptedLLM{err: boom}}}
		if _, err := researcher.Research(context.Background(), state); !errors.Is(err, boom) {
			t.Fatalf("err = %v, want wrapped boom", err)
		}
	})
	t.Run("malformed reply", func(t *testing.T) {
		researcher := &Researcher{Models: explore.ModelSet{Worker: &scriptedLLM{replies: []string{"not json"}}}}
		if _, err := researcher.Research(context.Background(), state); err == nil {
			t.Fatal("malformed reply must fail")
		}
	})
	t.Run("vision error", func(t *testing.T) {
		boom := errors.New("lens cap on")
		researcher := &Researcher{Models: explore.ModelSet{
			Worker: &scriptedLLM{replies: []string{validSectionsJSON}},
			Vision: &scriptedLLM{err: boom},
		}}
		shot := researchState(t, researchTree(), []byte("png"))
		if _, err := researcher.Research(context.Background(), shot); !errors.Is(err, boom) {
			t.Fatalf("err = %v, want wrapped boom", err)
		}
	})
}

// A vision model corrupts a reply now and then (a stray quote after a
// two-digit index on a live session, 2026-08-28). Notes are enrichment, so
// one retry is cheaper than losing the session; a second bad reply fails.
func TestResearchVisionRetriesOnceOnAnUnreadableReply(t *testing.T) {
	bad := `{"elements":[{"eidx":10","label":"","notes":"x"}]}`
	good := `{"elements":[{"eidx":0,"label":"Save changes","notes":"green accent"}]}`
	state := researchState(t, researchTree(), []byte("png"))

	vision := &scriptedLLM{replies: []string{bad, good}}
	researcher := &Researcher{Models: explore.ModelSet{Worker: &scriptedLLM{replies: []string{validSectionsJSON}}, Vision: vision}}
	uiMap, err := researcher.Research(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if len(vision.requests) != 2 || uiMap.Sections[0].Elements[0].Label != "Save changes" {
		t.Fatalf("vision calls %d, save = %+v", len(vision.requests), uiMap.Sections[0].Elements[0])
	}

	vision = &scriptedLLM{replies: []string{bad, bad}}
	researcher = &Researcher{Models: explore.ModelSet{Worker: &scriptedLLM{replies: []string{validSectionsJSON}}, Vision: vision}}
	// Quoting the rejected reply is what separates an unreadable answer from
	// a provider that is down; a transport failure never carries one. That is
	// the fact worth pinning, rather than the wrapper's wording.
	_, err = researcher.Research(context.Background(), state)
	if err == nil || !strings.Contains(err.Error(), "reply begins") {
		t.Fatalf("second bad reply must fail the pass with the reply quoted, got %v", err)
	}
}
