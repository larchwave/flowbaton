package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/larchwave/flowbaton/internal/explore"
	"github.com/larchwave/flowbaton/internal/strictjson"
)

// DefaultResearchTTL bounds how long a cached UI map stays valid.
const DefaultResearchTTL = 6 * time.Hour

// ResearchCache persists researched UI maps as JSON files under
// stateDir/research-cache with a TTL. It is a cache: any unreadable, stale,
// or unwritable entry degrades to a miss, never to bad data.
type ResearchCache struct {
	dir string
	ttl time.Duration
	now func() time.Time
	mu  sync.Mutex
}

type cachedMap struct {
	SavedAt   time.Time         `json:"saved_at"`
	Screen    cachedSignature   `json:"screen"`
	Sections  []explore.Section `json:"sections"`
	CreatedAt time.Time         `json:"created_at"`
	Markdown  string            `json:"markdown"`
}

type cachedSignature struct {
	AppID      string   `json:"app_id"`
	Salient    []string `json:"salient"`
	TreeDigest string   `json:"tree_digest"`
}

// NewResearchCache returns a cache rooted at stateDir/research-cache. A
// non-positive ttl falls back to DefaultResearchTTL; a nil clock uses wall
// time.
func NewResearchCache(stateDir string, ttl time.Duration, clock func() time.Time) *ResearchCache {
	if ttl <= 0 {
		ttl = DefaultResearchTTL
	}
	if clock == nil {
		clock = time.Now
	}
	return &ResearchCache{dir: filepath.Join(stateDir, "research-cache"), ttl: ttl, now: clock}
}

// Get returns the cached map for screen, or a miss when the entry is
// absent, unreadable, older than the TTL, or filed for another screen.
func (c *ResearchCache) Get(screen explore.ScreenSignature) (*explore.UIMap, bool) {
	path, err := c.entryPath(screen)
	if err != nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var entry cachedMap
	if err := strictjson.Decode(data, &entry); err != nil {
		return nil, false
	}
	if !c.now().Before(entry.SavedAt.Add(c.ttl)) {
		return nil, false
	}
	// The file is named after the digest, so a mismatch here means the
	// entry was written for another screen or another app. A cache serves a
	// miss rather than another screen's map.
	if entry.Screen.TreeDigest != screen.TreeDigest || entry.Screen.AppID != screen.AppID {
		return nil, false
	}
	return &explore.UIMap{
		Screen: explore.ScreenSignature{
			AppID:      entry.Screen.AppID,
			Salient:    entry.Screen.Salient,
			TreeDigest: entry.Screen.TreeDigest,
		},
		Sections:  entry.Sections,
		CreatedAt: entry.CreatedAt,
		Markdown:  entry.Markdown,
	}, true
}

// Put stores the map for screen. A failed write leaves at most a future
// miss, so Put reports nothing.
func (c *ResearchCache) Put(screen explore.ScreenSignature, m *explore.UIMap) {
	if m == nil {
		return
	}
	path, err := c.entryPath(screen)
	if err != nil {
		return
	}
	entry := cachedMap{
		SavedAt: c.now(),
		Screen: cachedSignature{
			AppID:      m.Screen.AppID,
			Salient:    append([]string(nil), m.Screen.Salient...),
			TreeDigest: m.Screen.TreeDigest,
		},
		Sections:  append([]explore.Section(nil), m.Sections...),
		CreatedAt: m.CreatedAt,
		Markdown:  m.Markdown,
	}
	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}

// entryPath names an entry after the WHOLE digest, never Key(). Key renders
// the salient labels, so every change to label selection renames every
// screen at once and orphans what is on disk: after the naming commits of
// 2026-08-31 the contacts cache held one screen under two names, and the
// next session paid a model call to map it again. Key also truncates the
// digest to eight characters, so two screens Same() calls different would
// share one file.
func (c *ResearchCache) entryPath(screen explore.ScreenSignature) (string, error) {
	digest := screen.TreeDigest
	if digest == "" || digest != filepath.Base(digest) {
		return "", fmt.Errorf("research cache: unusable screen digest %q", digest)
	}
	return filepath.Join(c.dir, digest+".json"), nil
}
