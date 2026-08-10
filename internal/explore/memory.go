package explore

import "context"

// MemoryEntry is one titled note in a per-screen memory file.
type MemoryEntry struct {
	Title string
	Body  string
}

// ExperienceStore persists machine-learned per-screen recipes: what
// worked, what failed, working locator solutions. Injected into agent
// conversations as a table of contents; bodies are fetched on demand.
type ExperienceStore interface {
	// Index lists entry titles for a screen, cheap enough to inject
	// into every conversation.
	Index(ctx context.Context, screen ScreenSignature) ([]string, error)
	// Get fetches one entry body by title.
	Get(ctx context.Context, screen ScreenSignature, title string) (string, error)
	// Record appends or replaces an entry for a screen. Secrets must
	// be redacted before writing.
	Record(ctx context.Context, screen ScreenSignature, entry MemoryEntry) error
}

// KnowledgeStore serves operator-authored hints: credentials pointers,
// form rules, navigation quirks. Matched to screens by pattern.
type KnowledgeStore interface {
	// Match returns the hint bodies that apply to a screen.
	Match(ctx context.Context, screen ScreenSignature) ([]string, error)
}
