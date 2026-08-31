package memory_test

import (
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/explore"
	"github.com/larchwave/flowbaton/internal/explore/memory"
)

// A cached UI map belongs to the screen it was researched on, and the screen
// is its digest. Filing it under the rendered name orphaned every entry the
// moment label selection changed: after the naming commits of 2026-08-31 the
// contacts cache held activate-to-dismiss-search-c855b1ed.json and
// search-no-results-for-zzznosuchcontact-c855b1ed.json, one screen paid for
// twice.
func TestResearchCacheSurvivesARenamedScreen(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cache := memory.NewResearchCache(dir, time.Hour, time.Now)
	before := explore.ScreenSignature{
		AppID:      "com.example.app",
		Salient:    []string{"Activate to dismiss", "Search"},
		TreeDigest: "c855b1edc855b1ed",
	}
	cache.Put(before, &explore.UIMap{Screen: before, Markdown: "the map"})

	after := explore.ScreenSignature{
		AppID:      "com.example.app",
		Salient:    []string{"Search", "No Results"},
		TreeDigest: "c855b1edc855b1ed",
	}
	cached, ok := cache.Get(after)
	if !ok || cached == nil || cached.Markdown != "the map" {
		t.Fatalf("cache missed a screen it had already mapped: ok=%v", ok)
	}
}
