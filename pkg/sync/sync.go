// Package sync provides file-watching and S3-syncing for static assets.
// It performs an initial one-shot upload of all files in a directory, then
// optionally watches for filesystem changes and syncs them continuously.
package sync

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/FyrmForge/hamr/pkg/storage"
	"github.com/fsnotify/fsnotify"
)

// BucketEnsurer is optionally implemented by storage backends that need
// their bucket created before first use (e.g. S3/MinIO in dev mode).
type BucketEnsurer interface {
	EnsureBucket(ctx context.Context) error
}

// SyncAll performs a one-shot upload of every file under dir to the storage backend.
// If the store implements BucketEnsurer, the bucket is created first.
func SyncAll(ctx context.Context, store storage.FileStorage, dir string) error {
	if be, ok := store.(BucketEnsurer); ok {
		if err := be.EnsureBucket(ctx); err != nil {
			return fmt.Errorf("ensure bucket: %w", err)
		}
	}

	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		key := Key(dir, path)
		if key == "" {
			return nil
		}
		if err := uploadFile(ctx, store, path, key); err != nil {
			return fmt.Errorf("upload %s: %w", key, err)
		}
		slog.Info("synced", "key", key)
		return nil
	})
}

// WatchAndSync watches dir for filesystem changes and syncs them to the
// storage backend. It blocks until ctx is cancelled or the watcher errors.
func WatchAndSync(ctx context.Context, store storage.FileStorage, dir string) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create watcher: %w", err)
	}
	defer watcher.Close()

	if err := addWatchDirs(watcher, dir); err != nil {
		return fmt.Errorf("watch directories: %w", err)
	}

	slog.Info("watching for static asset changes", "dir", dir)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			key := Key(dir, event.Name)
			if key == "" {
				continue
			}

			switch {
			case event.Has(fsnotify.Create), event.Has(fsnotify.Write):
				info, err := os.Stat(event.Name)
				if err != nil {
					continue
				}
				if info.IsDir() {
					_ = addWatchDirs(watcher, event.Name)
					continue
				}
				if err := uploadFile(ctx, store, event.Name, key); err != nil {
					slog.Warn("sync upload failed", "key", key, "error", err)
				} else {
					slog.Info("synced", "key", key)
				}

			case event.Has(fsnotify.Remove), event.Has(fsnotify.Rename):
				if err := store.Delete(ctx, key); err != nil {
					slog.Warn("sync delete failed", "key", key, "error", err)
				} else {
					slog.Info("deleted", "key", key)
				}
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			slog.Warn("watcher error", "error", err)
		}
	}
}

// Key converts a filesystem path into an S3 object key relative to dir.
func Key(dir, path string) string {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return ""
	}
	return strings.ReplaceAll(rel, string(filepath.Separator), "/")
}

func uploadFile(ctx context.Context, store storage.FileStorage, path, key string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return store.Save(ctx, key, f)
}

func addWatchDirs(w *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return w.Add(path)
		}
		return nil
	})
}
