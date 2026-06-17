package devserver

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// FileEvent is emitted when a watched file changes and matches a rule.
type FileEvent struct {
	Rule *WatchRule
	Path string
	Time time.Time
}

// Watcher watches the filesystem for changes and emits FileEvents.
type Watcher struct {
	root   string
	rules  []WatchRule
	events chan FileEvent
	done   chan struct{} // closed when loop exits, before events is closed
	logger *slog.Logger

	fsw    *fsnotify.Watcher
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewWatcher creates a file watcher for the given rules.
// root is the base directory to watch (typically ".").
func NewWatcher(root string, rules []WatchRule, logger *slog.Logger) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		_ = fsw.Close()
		return nil, err
	}

	return &Watcher{
		root:   absRoot,
		rules:  rules,
		events: make(chan FileEvent, 64),
		done:   make(chan struct{}),
		logger: logger,
		fsw:    fsw,
	}, nil
}

// Events returns the channel that receives file events.
func (w *Watcher) Events() <-chan FileEvent {
	return w.events
}

// Done returns a channel that is closed when the watcher loop exits.
func (w *Watcher) Done() <-chan struct{} {
	return w.done
}

// Start begins watching the root directory recursively. It returns
// immediately and runs in the background until Stop is called or ctx is done.
func (w *Watcher) Start(ctx context.Context) error {
	ctx, w.cancel = context.WithCancel(ctx)

	if err := w.addWatchDirs(w.root); err != nil {
		return err
	}

	w.wg.Add(1)
	go w.loop(ctx)
	return nil
}

// Stop stops the watcher and waits for the event loop to exit.
func (w *Watcher) Stop() {
	if w.cancel != nil {
		w.cancel()
	}
	w.wg.Wait()
	_ = w.fsw.Close()
}

func (w *Watcher) addWatchDirs(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip inaccessible dirs
		}
		if !d.IsDir() {
			return nil
		}
		name := d.Name()
		if shouldIgnoreDir(name) {
			return filepath.SkipDir
		}
		return w.fsw.Add(path)
	})
}

func shouldIgnoreDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", ".idea", ".vscode", "tmp":
		return true
	}
	return strings.HasPrefix(name, ".")
}

func (w *Watcher) loop(ctx context.Context) {
	defer w.wg.Done()

	// Per-rule debounce timers.
	timers := make(map[string]*time.Timer, len(w.rules))

	// Cleanup runs on every exit path — context cancellation AND the
	// fsnotify Events/Errors channels closing underneath us. Stopping the
	// timers prevents pending debounces from firing, and closing w.done both
	// releases any timer goroutine already blocked on w.events and signals
	// consumers (Done()) that the watcher has stopped.
	defer func() {
		for _, t := range timers {
			t.Stop()
		}
		close(w.done)
	}()

	for {
		select {
		case <-ctx.Done():
			return

		case event, ok := <-w.fsw.Events:
			if !ok {
				return
			}

			// Auto-watch new directories.
			if event.Has(fsnotify.Create) {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					if !shouldIgnoreDir(filepath.Base(event.Name)) {
						_ = w.addWatchDirs(event.Name)
					}
				}
			}

			// Skip non-modification events.
			if !event.Has(fsnotify.Write) && !event.Has(fsnotify.Create) &&
				!event.Has(fsnotify.Remove) && !event.Has(fsnotify.Rename) {
				continue
			}

			rel, err := filepath.Rel(w.root, event.Name)
			if err != nil {
				rel = event.Name
			}
			// Normalize to forward slashes for glob matching.
			rel = filepath.ToSlash(rel)

			for i := range w.rules {
				rule := &w.rules[i]
				if !matchRule(rule, rel) {
					continue
				}

				w.logger.Debug("file changed", "rule", rule.Name, "path", rel)

				// Debounce: reset or create timer.
				if t, ok := timers[rule.Name]; ok {
					t.Stop()
				}

				fe := FileEvent{Rule: rule, Path: rel, Time: time.Now()}
				debounce := rule.Debounce.Duration
				timers[rule.Name] = time.AfterFunc(debounce, func() {
					select {
					case <-w.done:
						return
					default:
					}
					select {
					case w.events <- fe:
					case <-w.done:
					}
				})
			}

		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			w.logger.Error("watcher error", "err", err)
		}
	}
}

// matchRule checks whether a file path matches a rule's watch patterns
// and is not excluded by its ignore patterns.
func matchRule(rule *WatchRule, path string) bool {
	// Check ignore patterns first.
	for _, pattern := range rule.Ignore {
		if matchGlob(pattern, path) {
			return false
		}
	}

	for _, pattern := range rule.Watch {
		if matchGlob(pattern, path) {
			return true
		}
	}
	return false
}

// matchGlob matches a path against a glob pattern, supporting ** for
// recursive directory matching.
func matchGlob(pattern, path string) bool {
	// Normalize.
	pattern = filepath.ToSlash(pattern)
	path = filepath.ToSlash(path)

	if strings.Contains(pattern, "**") {
		return matchDoubleGlob(pattern, path)
	}

	// No ** — try matching against the full path and the basename.
	if matched, _ := filepath.Match(pattern, path); matched {
		return true
	}
	if matched, _ := filepath.Match(pattern, filepath.Base(path)); matched {
		return true
	}
	return false
}

// matchDoubleGlob handles ** patterns by splitting on ** and matching
// the prefix/suffix parts against path segments.
func matchDoubleGlob(pattern, path string) bool {
	parts := strings.SplitN(pattern, "**", 2)
	prefix := parts[0]
	suffix := strings.TrimPrefix(parts[1], "/")

	// Prefix must match the beginning of the path (if non-empty).
	if prefix != "" {
		prefix = strings.TrimSuffix(prefix, "/")
		if !strings.HasPrefix(path, prefix+"/") && path != prefix {
			return false
		}
	}

	// Suffix must match via glob against the filename or path tail.
	if suffix == "" {
		return true
	}

	// Try matching suffix against every possible tail of the path.
	segments := strings.Split(path, "/")
	for i := range segments {
		tail := strings.Join(segments[i:], "/")
		if matched, _ := filepath.Match(suffix, tail); matched {
			return true
		}
	}

	// Also try just the filename.
	if matched, _ := filepath.Match(suffix, filepath.Base(path)); matched {
		return true
	}
	return false
}
