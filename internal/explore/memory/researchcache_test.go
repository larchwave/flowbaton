package memory

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/explore"
)

func sampleMap() *explore.UIMap {
	return &explore.UIMap{
		Screen: explore.ScreenSignature{
			AppID:      "com.example.shop",
			Salient:    []string{"Cart"},
			TreeDigest: "1234567890abcdef",
		},
		Sections: []explore.Section{{
			Name: "Item list",
			Elements: []explore.MappedElement{{
				EIDX:  3,
				Role:  "button",
				Label: "Checkout",
				Locators: []explore.Locator{
					{Kind: explore.LocatorID, Value: "checkout_button"},
					{Kind: explore.LocatorText, Value: "Checkout", Index: 1},
				},
				Notes: "disabled while the cart is empty",
			}},
		}},
		CreatedAt: time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC),
		Markdown:  "# Cart\n\n- [3] Checkout button",
	}
}

func TestResearchCacheRoundTrip(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	cache := NewResearchCache(t.TempDir(), 0, func() time.Time { return now })
	put := sampleMap()
	cache.Put("cart-12345678", put)

	got, ok := cache.Get("cart-12345678")
	if !ok {
		t.Fatal("Get() miss, want hit")
	}
	if got.Markdown != put.Markdown {
		t.Fatalf("Markdown = %q, want %q", got.Markdown, put.Markdown)
	}
	if !reflect.DeepEqual(got.Sections, put.Sections) {
		t.Fatalf("Sections = %#v, want %#v", got.Sections, put.Sections)
	}
	if !reflect.DeepEqual(got.Screen, put.Screen) {
		t.Fatalf("Screen = %#v, want %#v", got.Screen, put.Screen)
	}
	if !got.CreatedAt.Equal(put.CreatedAt) {
		t.Fatalf("CreatedAt = %v, want %v", got.CreatedAt, put.CreatedAt)
	}
}

func TestResearchCacheTTL(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	now := start
	cache := NewResearchCache(t.TempDir(), 0, func() time.Time { return now })
	cache.Put("cart-12345678", sampleMap())

	now = start.Add(DefaultResearchTTL - time.Minute)
	if _, ok := cache.Get("cart-12345678"); !ok {
		t.Fatal("Get() before expiry miss, want hit")
	}
	now = start.Add(DefaultResearchTTL)
	if _, ok := cache.Get("cart-12345678"); ok {
		t.Fatal("Get() at expiry hit, want miss")
	}
}

func TestResearchCacheCustomTTL(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	now := start
	cache := NewResearchCache(t.TempDir(), time.Minute, func() time.Time { return now })
	cache.Put("cart-12345678", sampleMap())

	now = start.Add(59 * time.Second)
	if _, ok := cache.Get("cart-12345678"); !ok {
		t.Fatal("Get() within custom TTL miss, want hit")
	}
	now = start.Add(2 * time.Minute)
	if _, ok := cache.Get("cart-12345678"); ok {
		t.Fatal("Get() past custom TTL hit, want miss")
	}
}

func TestResearchCacheMisses(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	cache := NewResearchCache(stateDir, 0, nil)

	if _, ok := cache.Get("absent"); ok {
		t.Fatal("Get() on an absent key hit, want miss")
	}
	if _, ok := cache.Get("../escape"); ok {
		t.Fatal("Get() on an unusable key hit, want miss")
	}

	// A damaged entry is a miss, never bad data.
	dir := filepath.Join(stateDir, "research-cache")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.Get("broken"); ok {
		t.Fatal("Get() on a damaged entry hit, want miss")
	}
}
