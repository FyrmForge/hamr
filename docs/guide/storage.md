# Storage — Pluggable File Storage

`hamr/pkg/storage` provides a file storage abstraction with local filesystem and
S3-compatible backends (AWS S3, MinIO, Cloudflare R2).

## Quick Start

```go
import "github.com/FyrmForge/hamr/pkg/storage"
```

## FileStorage Interface

Every storage backend implements:

```go
type FileStorage interface {
    Save(ctx context.Context, path string, r io.Reader) error
    Open(ctx context.Context, path string) (io.ReadCloser, error)
    Delete(ctx context.Context, path string) error
    Exists(ctx context.Context, path string) (bool, error)
}
```

- `Save` overwrites existing files and creates intermediate directories
- `Delete` is idempotent — deleting a non-existent file returns nil
- `Open` returns a `ReadCloser` — the caller must close it

## SignableStorage Interface

Extends `FileStorage` with pre-signed URL generation:

```go
type SignableStorage interface {
    FileStorage
    SignURL(ctx context.Context, path string, expiry time.Duration) (string, error)
}
```

Only `S3Storage` implements this interface.

## Local Storage

```go
store, err := storage.NewLocalStorage("./uploads",
    storage.WithLocalLogger(logger),
)
```

Creates the base directory if it doesn't exist. Files are stored relative to the base
path.

```go
// Save a file
err := store.Save(ctx, "avatars/user-123.jpg", file)

// Read a file
rc, err := store.Open(ctx, "avatars/user-123.jpg")
defer rc.Close()

// Check existence
exists, err := store.Exists(ctx, "avatars/user-123.jpg")

// Delete
err := store.Delete(ctx, "avatars/user-123.jpg")
```

## S3 Storage

Works with AWS S3, MinIO, and Cloudflare R2.

```go
store, err := storage.NewS3Storage(storage.S3Config{
    Endpoint:        "http://localhost:9000",  // MinIO
    Bucket:          "uploads",
    Region:          "us-east-1",
    AccessKeyID:     os.Getenv("AWS_ACCESS_KEY_ID"),
    SecretAccessKey:  os.Getenv("AWS_SECRET_ACCESS_KEY"),
    UsePathStyle:    true,  // required for MinIO
}, storage.WithS3Logger(logger))
```

Same `FileStorage` API, plus pre-signed URLs:

```go
url, err := store.SignURL(ctx, "avatars/user-123.jpg", 15*time.Minute)
```

### S3Config

| Field | Description |
|-------|-------------|
| `Endpoint` | Service URL (e.g. `http://localhost:9000` for MinIO) |
| `Bucket` | Bucket name |
| `Region` | AWS region |
| `AccessKeyID` | AWS access key |
| `SecretAccessKey` | AWS secret key |
| `UsePathStyle` | `true` for MinIO / path-style addressing |

## Using the Interface

Write code against the interface so backends are swappable:

```go
type UserService struct {
    storage storage.FileStorage
}

func (s *UserService) UploadAvatar(ctx context.Context, userID string, file io.Reader) error {
    return s.storage.Save(ctx, fmt.Sprintf("avatars/%s.jpg", userID), file)
}
```

In production, inject S3. In tests, inject local storage pointing at `t.TempDir()`.

## API Reference

```go
// Interfaces
type FileStorage interface {
    Save(ctx context.Context, path string, r io.Reader) error
    Open(ctx context.Context, path string) (io.ReadCloser, error)
    Delete(ctx context.Context, path string) error
    Exists(ctx context.Context, path string) (bool, error)
}
type SignableStorage interface {
    FileStorage
    SignURL(ctx context.Context, path string, expiry time.Duration) (string, error)
}

// Local
func NewLocalStorage(basePath string, opts ...LocalOption) (*LocalStorage, error)
func WithLocalLogger(l *slog.Logger) LocalOption

// S3
type S3Config struct {
    Endpoint        string
    Bucket          string
    Region          string
    AccessKeyID     string
    SecretAccessKey  string
    UsePathStyle    bool
}
func NewS3Storage(cfg S3Config, opts ...S3Option) (*S3Storage, error)
func WithS3Logger(l *slog.Logger) S3Option
```
