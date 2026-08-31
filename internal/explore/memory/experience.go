// Package memory implements the filesystem stores of the per-app explore
// state directory: learned per-screen recipes, operator-authored hints,
// saved plans, and cached research maps.
//
// Layout under one state directory:
//
//	knowledge/          operator-authored hints (*.md)
//	experience/         machine-learned recipes, one file per screen key
//	plans/              saved scenario plans as markdown
//	research-cache/     cached UI maps as JSON with a TTL
package memory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/larchwave/flowbaton/internal/explore"
)

// Experience persists machine-learned per-screen recipes as markdown files,
// one file per screen key, one "## <title>" section per entry.
type Experience struct {
	dir string
	// ponytail: one process-wide mutex; per-file locks if write volume matters.
	mu sync.Mutex
}

var _ explore.ExperienceStore = (*Experience)(nil)

// NewExperience returns a store rooted at stateDir/experience.
func NewExperience(stateDir string) *Experience {
	return &Experience{dir: filepath.Join(stateDir, "experience")}
}

// Index lists entry titles for a screen. A screen with no file yet has an
// empty index.
func (e *Experience) Index(ctx context.Context, screen explore.ScreenSignature) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	entries, err := e.readEntries(screen)
	if err != nil {
		return nil, err
	}
	titles := make([]string, 0, len(entries))
	for _, entry := range entries {
		titles = append(titles, entry.Title)
	}
	return titles, nil
}

// Get fetches one entry body by title.
func (e *Experience) Get(ctx context.Context, screen explore.ScreenSignature, title string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	entries, err := e.readEntries(screen)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if entry.Title == title {
			return entry.Body, nil
		}
	}
	return "", fmt.Errorf("experience: no entry %q for screen %q", title, screen.Key())
}

// Record replaces a same-title entry or appends a new one. Secret values are
// redacted before anything reaches disk; an entry whose body is empty after
// redaction is skipped.
func (e *Experience) Record(ctx context.Context, screen explore.ScreenSignature, entry explore.MemoryEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	redacted := explore.MemoryEntry{
		Title: strings.TrimSpace(redactSecrets(entry.Title)),
		Body:  strings.TrimSpace(redactSecrets(entry.Body)),
	}
	if redacted.Title == "" {
		return errors.New("experience: entry title is empty")
	}
	if redacted.Body == "" {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	entries, err := e.readEntries(screen)
	if err != nil {
		return err
	}
	replaced := false
	updated := make([]explore.MemoryEntry, 0, len(entries)+1)
	for _, existing := range entries {
		if existing.Title == redacted.Title {
			updated = append(updated, redacted)
			replaced = true
			continue
		}
		updated = append(updated, existing)
	}
	if !replaced {
		updated = append(updated, redacted)
	}
	path, err := e.filePath(screen)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(e.dir, 0o755); err != nil {
		return fmt.Errorf("experience: create directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(renderEntries(updated)), 0o644); err != nil {
		return fmt.Errorf("experience: write %s: %w", filepath.Base(path), err)
	}
	return nil
}

// filePath names a screen's file after its whole TreeDigest, which is what
// Same() reads and the only thing about a screen that does not move.
//
// ScreenSignature.Key() is the readable name and belongs in reports, not on
// disk. It renders the salient LABELS before a digest TRUNCATED to eight
// characters, so it fails as an identity twice over: changing how labels are
// picked renames every screen at once, and two screens agreeing on eight hex
// characters share one name while Same() calls them different. Experience is
// not a cache -- no TTL, and navigator.Reach REPLAYS what it holds as
// actions on the device -- so a name that can drift or collide is the wrong
// name to file under.
func (e *Experience) filePath(screen explore.ScreenSignature) (string, error) {
	digest := screen.TreeDigest
	if digest == "" || digest != filepath.Base(digest) {
		return "", fmt.Errorf("experience: unusable screen digest %q", digest)
	}
	return filepath.Join(e.dir, digest+".md"), nil
}

func (e *Experience) readEntries(screen explore.ScreenSignature) ([]explore.MemoryEntry, error) {
	path, err := e.filePath(screen)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("experience: read %s: %w", filepath.Base(path), err)
	}
	return splitEntries(string(data)), nil
}

func splitEntries(content string) []explore.MemoryEntry {
	var entries []explore.MemoryEntry
	for _, line := range strings.Split(content, "\n") {
		if title, ok := strings.CutPrefix(line, "## "); ok {
			entries = append(entries, explore.MemoryEntry{Title: strings.TrimSpace(title)})
			continue
		}
		if len(entries) > 0 {
			entries[len(entries)-1].Body += line + "\n"
		}
	}
	for i := range entries {
		entries[i].Body = strings.TrimSpace(entries[i].Body)
	}
	return entries
}

func renderEntries(entries []explore.MemoryEntry) string {
	var builder strings.Builder
	for _, entry := range entries {
		builder.WriteString("## " + entry.Title + "\n\n")
		if entry.Body != "" {
			builder.WriteString(entry.Body + "\n\n")
		}
	}
	return builder.String()
}

// secretValue matches a credential-style assignment so the value after the
// separator can be replaced before writing. Over-redaction is acceptable;
// leaking is not.
var secretValue = regexp.MustCompile(`(?i)\b((?:password|passwd|token|secret|api[_-]?key|credential|key)s?\s*[:=]\s*)(?:"[^"]*"|\S+)`)

func redactSecrets(text string) string {
	return secretValue.ReplaceAllString(text, "${1}***")
}
