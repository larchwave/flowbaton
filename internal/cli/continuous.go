package cli

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/larchwave/flowbaton/internal/engine"
)

// Continuous mode: `-c/--continuous`, "(file-watch rerun)" per
// specs/03-cli-tooling.md:20, "continuous mode → file watcher" per :29. The
// The implementation watches file versions and reruns affected roots.
//
// Two decisions carry the mode:
//
// A FAILED RUN IS NOT A REASON TO STOP. Not a red suite, and not a flow that
// cannot even be parsed. Saving a file mid-edit is the ordinary event in the
// loop this mode exists for; a watcher that exits on it makes the operator
// restart the watcher every time they save early, which is every time.
//
// THE WATCH SET IS THE DEPENDENCY CLOSURE, read off the preflight rather than
// re-derived. Editing a subflow is the ordinary case — that is where the steps
// live — and a watcher that knew only the selected roots would sit still
// through it. When preflight never got far enough to produce a closure, the
// fallback is every file under the roots the operator named: at that point the
// run has no idea what it depends on, and watching too much only costs a stat.

// defaultPollInterval is how often the watch set is re-stamped.
//
// Polling rather than a filesystem-notification dependency: the project runs on
// two runtime dependencies (docs/dependency-policy.md), and a stat per watched
// file twice a second is not the cost that would justify a third.
const defaultPollInterval = 500 * time.Millisecond

// fileStamp is a file's observed version. Size joins the modification time
// because a same-second save is common — an editor writes twice in a
// keystroke — and mtime granularity is not guaranteed to separate them.
type fileStamp struct {
	modified time.Time
	size     int64
	present  bool
}

// runAttempt is one execution's verdict plus what it turned out to depend on.
type runAttempt struct {
	code int
	// watched is the set to watch for the NEXT run. It is populated even when
	// the attempt failed, because a failed attempt is exactly the one whose
	// files are about to be edited.
	watched []string
	// snapshot is `watched` stamped at the moment the run committed to that set
	// — after preflight, before any flow executed. Stamping it in the loop
	// instead would be wrong twice over: too early and the keys are the walk's
	// rather than the closure's, too late and an edit made while the suite was
	// running is already baked in and never triggers the rerun it should.
	snapshot map[string]fileStamp
}

// runContinuously reruns until the context is cancelled, which in a terminal is
// ^C and in a test is the harness.
func (runner TestRunner) runContinuously(
	ctx context.Context,
	options TestOptions,
	stdout io.Writer,
	stderr io.Writer,
) int {
	for {
		attempt := runner.runOnce(ctx, options, stdout, stderr)
		if ctx.Err() != nil {
			return attempt.code
		}
		if anyChanged(attempt.snapshot, stampFiles(attempt.watched)) {
			// An edit landed while the suite was running. Rerun immediately
			// rather than waiting for another one — the operator has already
			// made the change they are waiting to see.
			continue
		}

		fmt.Fprintf(stdout, "watching %d file(s); edit one to rerun\n", len(attempt.watched))
		if !runner.waitForChange(ctx, attempt.snapshot, attempt.watched) {
			// The context ended. The exit code is the last completed run's,
			// because that is the verdict the operator was looking at; reporting
			// OK because the MODE ended cleanly would call a red suite green.
			return attempt.code
		}
	}
}

// waitForChange polls until a watched file changes or the context ends. It
// reports whether a change is what woke it.
func (runner TestRunner) waitForChange(
	ctx context.Context,
	snapshot map[string]fileStamp,
	watched []string,
) bool {
	ticker := time.NewTicker(runner.pollInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			if anyChanged(snapshot, stampFiles(watched)) {
				return true
			}
		}
	}
}

func (runner TestRunner) pollInterval() time.Duration {
	if runner.PollInterval > 0 {
		return runner.PollInterval
	}
	return defaultPollInterval
}

// watchSetFor is the dependency closure of a prepared program: every flow the
// preflight loaded, plus every script and media file it linked.
func watchSetFor(program *engine.Program) []string {
	if program == nil {
		return nil
	}
	unique := map[string]struct{}{}
	for _, path := range program.FlowPaths() {
		unique[path] = struct{}{}
	}
	// Edge targets cover the files a flow depends on that are not themselves
	// flows — runScript sources and media. Editing one changes what the suite
	// does just as much as editing a step.
	for _, edge := range program.Graph().Edges {
		if edge.To != "" {
			unique[edge.To] = struct{}{}
		}
	}
	return sortedKeys(unique)
}

// walkRoots is the degraded watch set, used when no closure exists yet: every
// regular file under each root, and each root that is itself a file.
//
// This is deliberately wider than flow selection. A run that failed before
// preflight does not know what it depends on, and the
// file about to be fixed may be one discovery never selected.
//
// Paths are kept as given rather than canonicalized. The closure's are
// loader-canonical (/private/var/... on macOS) and these are not, so the same
// file can appear under both names in a merged snapshot — which costs one inert
// map entry and nothing else, because check only ever reads the keys the
// current watch set names.
func walkRoots(roots []string) []string {
	unique := map[string]struct{}{}
	for _, root := range roots {
		info, err := os.Stat(root)
		if err != nil {
			// A root that does not exist is still worth watching: it may be
			// about to be created, and that is a change.
			unique[root] = struct{}{}
			continue
		}
		if !info.IsDir() {
			unique[root] = struct{}{}
			continue
		}
		_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			// An unreadable subtree is not a reason to abandon the walk; it is a
			// subtree that cannot trigger a rerun.
			if err != nil || entry.IsDir() {
				return nil
			}
			unique[path] = struct{}{}
			return nil
		})
	}
	return sortedKeys(unique)
}

func stampFiles(paths []string) map[string]fileStamp {
	stamps := make(map[string]fileStamp, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			// Absence is a stamp of its own. A deleted flow is a change, and so
			// is one that reappears.
			stamps[path] = fileStamp{}
			continue
		}
		stamps[path] = fileStamp{modified: info.ModTime(), size: info.Size(), present: true}
	}
	return stamps
}

// mergeStamps keeps the EARLIER observation wherever there is one and takes the
// later one only to fill a gap.
//
// Both halves are load-bearing. The early (walk) stamps are taken before the
// suite could touch anything; the later (closure) stamps cover files the walk
// never saw, such as a shared subflow living outside the roots the operator
// named. Replacing rather than merging would trade one blind spot for another.
func mergeStamps(earlier, later map[string]fileStamp) map[string]fileStamp {
	merged := make(map[string]fileStamp, len(earlier)+len(later))
	for path, stamp := range later {
		merged[path] = stamp
	}
	for path, stamp := range earlier {
		merged[path] = stamp
	}
	return merged
}

// anyChanged checks only paths the snapshot has an observation for. A path
// that just entered the watch set is not a change — the set widened, the files
// did not move.
func anyChanged(snapshot, current map[string]fileStamp) bool {
	for path, was := range snapshot {
		now, observed := current[path]
		if !observed {
			continue
		}
		if was != now {
			return true
		}
	}
	return false
}

func sortedKeys(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
