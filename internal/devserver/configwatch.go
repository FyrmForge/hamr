package devserver

import (
	"context"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

// WaitForConfigChangeOrQuit blocks until the file at path is written or
// created, ctx is cancelled, or a HotkeyQuit comes in (in which case it
// returns context.Canceled). Non-quit hotkeys are silently consumed. A
// nil hotkeys channel is safe (blocks forever).
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
			if action == HotkeyQuit {
				return context.Canceled
			}
			// Silently consume non-quit hotkeys.
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

