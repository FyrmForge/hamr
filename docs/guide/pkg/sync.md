# Sync — S3 File Synchronization

## CLI

```bash
hamr sync [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--dir` | `static` | Local directory to sync |
| `--watch` | `false` | Watch for changes after initial sync |
| `--endpoint` | env `S3_ENDPOINT` | S3 endpoint URL |
| `--bucket` | env `S3_BUCKET` | S3 bucket name |
| `--region` | env `S3_REGION` | S3 region |
| `--access-key` | env `S3_ACCESS_KEY` | S3 access key |
| `--secret-key` | env `S3_SECRET_KEY` | S3 secret key |
| `--path-style` | `true` | Use path-style addressing (required for MinIO) |

```bash
hamr sync                              # one-shot sync of static/ to S3
hamr sync --watch                      # watch for changes and sync continuously
hamr sync --dir dist --bucket my-cdn   # sync a different directory to a specific bucket
```

S3 credentials can be provided via flags or environment variables (`S3_ENDPOINT`, `S3_BUCKET`, `S3_REGION`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`).

---

`pkg/sync` performs a one-shot upload of all files in a directory, then optionally watches for filesystem changes and syncs them continuously.

## SyncAll

Uploads every file under a directory to the storage backend in a single pass. If the storage backend implements `BucketEnsurer`, the bucket is created automatically before uploading.

```go
err := sync.SyncAll(ctx, s3Store, "static")
```

This walks the directory tree, skipping directories and `.gitkeep` files, and uploads each file using the relative path as the storage key. For example, `static/css/app.css` becomes the key `css/app.css`.

## WatchAndSync

Watches a directory for filesystem changes and syncs them to the storage backend. It blocks until the context is cancelled or the watcher encounters a fatal error.

```go
// Typically run in a goroutine or as the main loop
err := sync.WatchAndSync(ctx, s3Store, "static")
```

Behavior:
- **Create/Write** events upload the changed file to storage.
- **Remove/Rename** events delete the corresponding key from storage.
- New subdirectories are automatically added to the watch list.
- Errors on individual file operations are logged but do not stop the watcher.

A common pattern is to call `SyncAll` first for the initial upload, then `WatchAndSync` for ongoing changes:

```go
if err := sync.SyncAll(ctx, s3Store, "static"); err != nil {
    return err
}
return sync.WatchAndSync(ctx, s3Store, "static")
```

## BucketEnsurer interface

Storage backends can optionally implement this interface to have their bucket created before the first sync:

```go
type BucketEnsurer interface {
    EnsureBucket(ctx context.Context) error
}
```

`SyncAll` checks for this interface via type assertion and calls `EnsureBucket` if available. This is useful for S3/MinIO backends in development where the bucket may not exist yet.

## Key helper

Converts a filesystem path into an S3 object key relative to the base directory. Returns an empty string for paths that should be skipped (e.g. `.gitkeep` files).

```go
key := sync.Key("static", "static/css/app.css")
// key == "css/app.css"

key := sync.Key("static", "static/.gitkeep")
// key == "" (skipped)
```

