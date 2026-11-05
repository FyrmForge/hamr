# Storage and media

Code against the `storage.FileStorage` interface and the same services work with local disk in development and S3 in production. The `media` package builds on it for image and video uploads.

**Package references:** [Storage](pkg/storage.md), [Media](pkg/media.md)

## The FileStorage interface

All storage backends implement a unified interface:

```go
type FileStorage interface {
    Save(ctx context.Context, path string, r io.Reader) error
    Open(ctx context.Context, path string) (io.ReadCloser, error)
    Delete(ctx context.Context, path string) error
    Exists(ctx context.Context, path string) (bool, error)
    List(ctx context.Context, prefix string) ([]string, error) // keys under prefix ("" = all)
}
```

Write code against the interface so backends are swappable:

```go
type UserService struct {
    storage storage.FileStorage
}

func (s *UserService) UploadAvatar(ctx context.Context, userID string, file io.Reader) error {
    return s.storage.Save(ctx, fmt.Sprintf("avatars/%s.jpg", userID), file)
}
```

## Local storage

```go
localStore, err := storage.NewLocalStorage("./uploads",
    storage.WithLocalLogger(logger),
)
```

Creates the base directory if it doesn't exist. Files are stored relative to the base path. The scaffold reads the base path from `STORAGE_PATH` (default `./uploads`).

## S3 storage

Works with AWS S3, RustFS, and Cloudflare R2:

```go
s3Store, err := storage.NewS3Storage(storage.S3Config{
    Endpoint:        envS3Endpoint, // S3_ENDPOINT, e.g. http://localhost:9000 (RustFS)
    Bucket:          envS3Bucket,   // S3_BUCKET
    Region:          envS3Region,   // S3_REGION
    AccessKeyID:     envS3AccessKey, // S3_ACCESS_KEY
    SecretAccessKey: envS3SecretKey, // S3_SECRET_KEY
    UsePathStyle:    true,           // required for RustFS
},
    storage.WithS3Logger(logger),
    storage.WithPublicRead(false),          // true: EnsureBucket also applies a public-read policy
    storage.WithMaxUploadBuffer(128<<20),   // cap for buffering non-seekable bodies (default 64 MiB)
)
if err != nil {
    return err
}

// Create the bucket if it doesn't exist (no-op otherwise).
if err := s3Store.EnsureBucket(ctx); err != nil {
    return err
}
```

The scaffold reads `S3_ENDPOINT`, `S3_BUCKET`, `S3_REGION`, `S3_ACCESS_KEY` and `S3_SECRET_KEY` but does not call `EnsureBucket` — call it at startup (or create the bucket out of band), otherwise the first upload fails against a fresh RustFS volume.

`Save` streams seekable readers (files, `*bytes.Reader`) directly. Non-seekable readers are buffered in memory up to `WithMaxUploadBuffer`; larger ones fail.

> [!NOTE]
> When `hamr dev` walks the RustFS container's host port (because 9000 was busy), `S3_ENDPOINT` is auto-rewritten in the spawned site's env. Standalone `hamr sync` reads the same walks file, so neither the app code nor the sync watcher needs to know the port shifted. See [Port walks](hamr-dev.md#port-walks).

### Pre-signed URLs

Pre-signed URLs are temporary, time-limited links that grant access to private S3 objects without requiring authentication. S3 storage supports this via `SignableStorage`:

```go
url, err := s3Store.SignURL(ctx, "avatars/user-123.jpg", 15*time.Minute)

// Force a download instead of inline display:
url, err := s3Store.SignURL(ctx, "docs/report-v2.pdf", 15*time.Minute,
    storage.WithAttachment("Quarterly Report.pdf"))
```

`WithAttachment` bakes a `Content-Disposition: attachment` header into the
signature — it cannot be appended to an already-signed URL, since S3 signs the
query parameters.

## Scaffolding

The `hamr new` wizard configures storage:

```sh
hamr new myapp --storage local    # local filesystem
hamr new myapp --storage s3       # S3-compatible (generates RustFS in docker-compose)
hamr new myapp --static-s3        # sync static assets to S3 bucket
```

S3 scaffolding generates:
- `S3_*` env vars in `.env`
- RustFS service in `docker/docker-compose.yaml`
- Storage initialization in `cmd/site/main.go`, passed to routes as `web.Deps.FileStorage` (the interface)

The media stores below take the concrete `*storage.LocalStorage` / `*storage.S3Storage`, so construct them in `main.go` next to the storage backend and add them to `web.Deps`.

## Media processing

The `media` package builds on `storage` to handle image and video upload, processing, and serving.

All processing shells out to `ffmpeg` — image resizing/format conversion as well as video probing, thumbnails and transcoding (`ffprobe`). Both must be on `PATH` in production; the scaffolded Alpine image doesn't include them (see [Deployment](deployment.md#docker-image)). Missing binaries surface as `media.ErrFFmpegNotFound`.

### ImageStore

```go
// Local: files served by the app under urlPrefix.
imageStore, err := media.NewLocalImageStore(localStore, "/uploads", media.ImageStoreConfig{
    Category: "avatars",
    Sizes:    media.SizesAvatar,  // thumb 64x64, small 150x150, medium 400x400
    Quality:  85,
    Format:   media.FormatWebP,
    MaxSize:  5 * media.MB,
}, media.WithLogger(logger))

// S3: public URLs from BaseURL, or pre-signed URLs when SignedExpiry > 0.
imageStore, err := media.NewS3ImageStore(s3Store, media.ImageStoreConfig{
    Category:     "avatars",
    Sizes:        media.SizesAvatar,
    Quality:      85,
    Format:       media.FormatWebP,
    MaxSize:      5 * media.MB,
    BaseURL:      "https://cdn.example.com", // used by GetMedia
    SignedExpiry: 15 * time.Minute,          // used by GetMediaCtx
}, media.WithLogger(logger))
```

### Upload

```go
// From a multipart file header (typical in Echo handler).
result, err := imageStore.Upload(ctx, fileHeader)

// From an io.Reader. The id is generated internally as a fresh UUID.
result, err := imageStore.UploadFromReader(ctx, reader, size)

// From an io.Reader, using the caller's pre-minted UUID for the storage
// path. Useful when the caller already has a row in its DB referencing
// `id` (async-processing worker, etc.). Pass overwrite=true to clobber
// an existing prefix — typical for retry-after-failure.
result, err := imageStore.UploadFromReaderWithID(ctx, id, reader, size, overwrite)
```

`Upload` / `UploadFromReader` validate file size, detect MIME type, generate all configured size variants via `ffmpeg`, and save each variant.

`UploadFromReaderWithID` adds two contracts:

- `id` MUST be the **36-char canonical UUID** (`xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx`). Other `uuid.Parse`-valid forms (hyphenless, urn, braced) and the empty string are rejected with `ErrInvalidID` — accepting them would let the same logical UUID land at two different storage keys, or silently auto-generate an id and defeat the method's purpose.
- With `overwrite=false`, returns `ErrIDExists` if bytes already live under `id`. The check is **best-effort** — concurrent `*WithID` callers can race on Save, and a failed upload's `cleanupPartial` can erase a concurrent winner's writes. Use a DB-level unique constraint for hard exclusion.

On `ErrFileTooLarge` (size pre-check), `ErrInvalidID`, and `ErrIDExists` the reader is **not consumed** (rejection happens before any read). Callers relying on the upload to drain a network/pipe reader must discard or reuse it explicitly.

On any error after the first save, the method best-effort deletes any variants already written.

### Upload handler

```go
// POST /settings/avatar
func (h *handler) UploadAvatar(c echo.Context) error {
    fh, err := c.FormFile("avatar")
    if err != nil {
        return echo.NewHTTPError(http.StatusBadRequest, "missing file")
    }

    result, err := h.images.Upload(c.Request().Context(), fh)
    switch {
    case errors.Is(err, media.ErrFileTooLarge):
        return echo.NewHTTPError(http.StatusRequestEntityTooLarge, "image too large")
    case errors.Is(err, media.ErrUnknownType):
        return echo.NewHTTPError(http.StatusUnsupportedMediaType, "unsupported image type")
    case err != nil:
        return err
    }

    // Persist result.ID on the user row; build URLs from it on render.
    return respond.HTML(c, http.StatusOK, avatarPreview(h.images.GetMedia(result.ID).Thumb()))
}
```

The server's default request body limit is 2 MB (`server.WithMaxBodySize`), checked before the handler runs. Raise it to at least your largest `MaxSize` or bigger uploads are rejected with 413 before `ErrFileTooLarge` can fire.

### URL construction

```go
ref := imageStore.GetMedia(id)
ref.Thumb()           // URL for thumbnail
ref.Size("medium")    // URL for specific size
ref.Biggest()         // URL for largest size

// S3 stores with SignedExpiry: every URL is pre-signed.
ref = imageStore.GetMediaCtx(ctx, id)

// One-off signed URL for a specific object (S3 only; errors on local stores).
url, err := imageStore.SignedURL(ctx, result.Path("medium"), 5*time.Minute,
    storage.WithAttachment("avatar.webp"))

// Remove every size variant.
err = imageStore.Delete(ctx, id)
```

### Serve handler

```go
e.GET("/uploads/*", imageStore.ServeHandler())
```

The handler only serves keys under the store's own category and sets `public, max-age=31536000, immutable` — upload ids are fresh UUIDs, so the URL never points at different bytes.

### VideoStore

Handles video upload, duration validation, optional H.264/AAC transcoding, and optional thumbnail extraction (requires `ffmpeg`/`ffprobe`):

```go
videoStore, err := media.NewLocalVideoStore(localStore, "/uploads", media.VideoStoreConfig{
    Category:          "clips",
    MaxSize:           100 * media.MB,
    MaxDuration:       120, // seconds
    GenerateThumbnail: true,
    ThumbnailWidth:    640,
    // Optional: re-encode every upload to H.264/AAC MP4 before saving.
    // Leave the struct zero-valued to store bytes verbatim.
    Transcode: media.VideoTranscodeOptions{Preset: "medium"},
}, media.WithLogger(logger))
```

Video stores expose the same three upload entry points as images:

```go
result, err := videoStore.Upload(ctx, fileHeader)
result, err := videoStore.UploadFromReader(ctx, reader, size)
result, err := videoStore.UploadFromReaderWithID(ctx, id, reader, size, overwrite)
```

`UploadFromReaderWithID` follows the same UUID-validation and existence-check rules as the image variant. `NewS3VideoStore(s3Store, cfg)` is the S3 counterpart, with the same `BaseURL` / `SignedExpiry` behaviour as images.

```go
ref := videoStore.GetMedia(id) // or GetMediaCtx(ctx, id) for signed URLs
ref.Video()                    // URL of video.mp4
ref.Thumbnail()                // URL of thumb.jpg, "" when GenerateThumbnail is off
```

`Delete`, `SignedURL` and `ServeHandler` work as on `ImageStore`.

When `Transcode` is configured, every upload is re-encoded with `+faststart` for progressive playback. The pass is slow — call `Upload*` from a worker pool such as `pkg/async.Group` rather than from the request goroutine. See [pkg/media](pkg/media.md) for the full set of transcode knobs and details.

### Preset sizes

| Preset | Sizes |
|--------|-------|
| `SizesAvatar` | thumb (64x64), small (150x150), medium (400x400) |
| `SizesCard` | thumb through xlarge (1200x1200) |
| `SizesIcon` | small (100x100), medium (200x200), large (400x400) |
| `SizeOriginal` | format-converted only, no resize |
