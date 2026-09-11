package devserver

import (
	"context"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

// WaitForConfigChangeOrQuit blocks until the file at path is written or
// created (returns nil), ctx is cancelled, a HotkeyQuit comes in (returns
// context.Canceled), or a HotkeyRestart comes in (returns ErrRestart — retry
// now). Restart is honored here because a parse error is not the only reason
// the last attempt failed: the config can be valid and startup still fail on a
// clashing port or a bad .env, and neither of those touches hamr.toml, so
// waiting on a file write would hang forever. Other hotkeys are silently
// consumed. A nil hotkeys channel is safe (blocks forever).
func WaitForConfigChangeOrQuit(ctx context.Context, path string, hotkeys <-chan HotkeyAction) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer func() { _ = fsw.Close() }()

	dir := filepath.Dir(absPath)
	if err := fsw.Add(dir); err != nil {
		return err
	}

	base := filepath.Base(absPath)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case action := <-hotkeys:
			switch action {
			case HotkeyQuit:
				return context.Canceled
			case HotkeyRestart:
				return ErrRestart
			}
			// Silently consume every other hotkey.
		case event, ok := <-fsw.Events:
			if !ok {
				return nil
			}
			if filepath.Base(event.Name) != base {
				continue
			}
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
				return nil
			}
		case _, ok := <-fsw.Errors:
			if !ok {
				return nil
			}
		}
	}
}
