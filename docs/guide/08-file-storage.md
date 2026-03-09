# File Storage

By coding against a storage interface, you can develop with local storage and switch to S3 in production without changing handler code. This guide covers the FileStorage interface, local and S3 backends, uploads, media processing, and pre-signed URLs.

**Package references:** [Storage](pkg/storage.md), [Media](pkg/media.md)

---

## FileStorage Interface

All storage backends implement a unified interface:

```go
type FileStorage interface {
    Save(ctx context.Context, path string, r io.Reader) error
    Open(ctx context.Context, path string) (io.ReadCloser, error)
    Delete(ctx context.Context, path string) error
    Exists(ctx context.Context, path string) (bool, error)
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

---

## Local Storage

```go
store, err := storage.NewLocalStorage("./uploads",
    storage.WithLocalLogger(logger),
)
```

Creates the base directory if it doesn't exist. Files are stored relative to the base path.

---

## S3 Storage

Works with AWS S3, MinIO, and Cloudflare R2:

```go
store, err := storage.NewS3Storage(storage.S3Config{
    Endpoint:       "http://localhost:9000",  // MinIO
    Bucket:         "uploads",
    Region:         "us-east-1",
    AccessKeyID:    os.Getenv("AWS_ACCESS_KEY_ID"),
    SecretAccessKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
    UsePathStyle:   true,  // required for MinIO
}, storage.WithS3Logger(logger))
```

### Pre-Signed URLs

Pre-signed URLs are temporary, time-limited links that grant access to private S3 objects without requiring authentication. S3 storage supports this via `SignableStorage`:

```go
url, err := store.SignURL(ctx, "avatars/user-123.jpg", 15*time.Minute)
```

---

## Scaffolding

The `hamr new` wizard configures storage:

```bash
hamr new myapp --storage local    # local filesystem
hamr new myapp --storage s3       # S3-compatible (generates MinIO in docker-compose)
hamr new myapp --static-s3        # sync static assets to S3 bucket
```

S3 scaffolding generates:
- S3 env vars in `.env`
- MinIO service in `docker/docker-compose.yaml`
- Storage initialization in `cmd/server/main.go`

---

## Media Processing

The `media` package builds on `storage` to handle image and video upload, processing, and serving.

### ImageStore

```go
store, err := media.NewLocalImageStore(localStorage, "/uploads", media.ImageStoreConfig{
    Category: "avatars",
    Sizes:    media.SizesAvatar,  // thumb 64x64, small 150x150, medium 400x400
    Quality:  85,
    Format:   media.FormatWebP,
    MaxSize:  5 * media.MB,
}, media.WithLogger(logger))
```

### Upload

```go
// From a multipart file header (typical in Echo handler)
result, err := store.Upload(ctx, fileHeader)

// From an io.Reader
result, err := store.UploadFromReader(ctx, reader, size)
```

Upload validates file size, detects MIME type, generates all configured size variants via `ffmpeg`, and saves each variant.

### URL Construction

```go
ref := store.GetMedia(id)
ref.Thumb()           // URL for thumbnail
ref.Size("medium")    // URL for specific size
ref.Biggest()         // URL for largest size
```

### Serve Handler

```go
e.GET("/uploads/*", imageStore.ServeHandler())
```

### VideoStore

Handles video upload, duration validation, and optional thumbnail extraction (requires `ffmpeg`/`ffprobe`):

```go
store, err := media.NewLocalVideoStore(localStorage, "/uploads", media.VideoStoreConfig{
    Category:          "clips",
    MaxSize:           100 * media.MB,
    MaxDuration:       120, // seconds
    GenerateThumbnail: true,
    ThumbnailWidth:    640,
}, media.WithLogger(logger))
```

### Preset Sizes

| Preset | Sizes |
|--------|-------|
| `SizesAvatar` | thumb (64x64), small (150x150), medium (400x400) |
| `SizesCard` | thumb through xlarge (1200x1200) |
| `SizesIcon` | small (100x100), medium (200x200), large (400x400) |
| `SizeOriginal` | format-converted only, no resize |

---

## Next Steps

- [Static Assets](09-static-assets.md) — CSS, JS vendoring, S3 sync
- [Authentication](07-authentication.md) — Protecting upload endpoints
