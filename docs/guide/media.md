# pkg/media

Image and video upload, processing, and serving on top of the `pkg/storage` package. The media package handles resizing, format conversion, thumbnail generation, and URL construction for both local filesystem and S3-compatible backends.

## Stores

### ImageStore

Handles image upload, processing (resize + format conversion), serving, and deletion.

```go
// Local filesystem backend
store, err := media.NewLocalImageStore(localStorage, "/uploads", media.ImageStoreConfig{
    Category: "avatars",
    Sizes:    media.SizesAvatar,
    Quality:  85,
    Format:   media.FormatWebP,
    MaxSize:  5 * media.MB,
}, media.WithLogger(logger))

// S3-compatible backend
store, err := media.NewS3ImageStore(s3Storage, media.ImageStoreConfig{
    Category:    "avatars",
    Sizes:       media.SizesAvatar,
    Quality:     85,
    Format:      media.FormatWebP,
    MaxSize:     5 * media.MB,
    BaseURL:     "https://cdn.example.com",
    SignedExpiry: 15 * time.Minute,
}, media.WithLogger(logger))
```

### VideoStore

Handles video upload, duration validation, optional thumbnail extraction, serving, and deletion. Requires `ffmpeg` and `ffprobe` in PATH.

```go
// Local filesystem backend
store, err := media.NewLocalVideoStore(localStorage, "/uploads", media.VideoStoreConfig{
    Category:          "clips",
    MaxSize:           100 * media.MB,
    MaxDuration:       120, // seconds
    GenerateThumbnail: true,
    ThumbnailWidth:    640,
}, media.WithLogger(logger))

// S3-compatible backend
store, err := media.NewS3VideoStore(s3Storage, media.VideoStoreConfig{
    Category:          "clips",
    MaxSize:           100 * media.MB,
    MaxDuration:       120,
    GenerateThumbnail: true,
    ThumbnailWidth:    640,
    BaseURL:           "https://cdn.example.com",
    SignedExpiry:       15 * time.Minute,
}, media.WithLogger(logger))
```

## Configuration

### ImageStoreConfig

| Field         | Type            | Description                                              |
|---------------|-----------------|----------------------------------------------------------|
| `Category`    | `string`        | Storage prefix/folder for this media type (e.g. "avatars") |
| `Sizes`       | `[]ImageSize`   | Output size variants to generate                          |
| `Quality`     | `int`           | Output quality 1-100                                     |
| `Format`      | `string`        | Output format: `FormatWebP`, `FormatJPEG`, or `FormatPNG` |
| `MaxSize`     | `int64`         | Maximum upload size in bytes                              |
| `BaseURL`     | `string`        | CDN/public base URL for S3 stores                         |
| `SignedExpiry` | `time.Duration` | Pre-signed URL expiry for S3 stores (0 = unsigned)       |

### VideoStoreConfig

| Field               | Type            | Description                                              |
|---------------------|-----------------|----------------------------------------------------------|
| `Category`          | `string`        | Storage prefix/folder for this media type                 |
| `MaxSize`           | `int64`         | Maximum upload size in bytes                              |
| `MaxDuration`       | `float64`       | Maximum video duration in seconds                         |
| `GenerateThumbnail` | `bool`          | Extract a JPEG thumbnail at the 1-second mark            |
| `ThumbnailWidth`    | `int`           | Thumbnail width in pixels (height auto-scaled)            |
| `BaseURL`           | `string`        | CDN/public base URL for S3 stores                         |
| `SignedExpiry`       | `time.Duration` | Pre-signed URL expiry for S3 stores (0 = unsigned)       |

## Preset sizes

Ready-made `[]ImageSize` slices for common use cases:

| Preset         | Sizes                                                                  |
|----------------|------------------------------------------------------------------------|
| `SizesAvatar`  | thumb (64x64), small (150x150), medium (400x400)                       |
| `SizesCard`    | thumb (64x64), small (150x150), medium (400x400), large (800x800), xlarge (1200x1200) |
| `SizesIcon`    | small (100x100), medium (200x200), large (400x400)                     |
| `SizeOriginal` | original (0x0) -- format-converted only, no resize                     |

You can also define custom sizes:

```go
sizes := []media.ImageSize{
    {Name: "thumb", Width: 80, Height: 80},
    {Name: "banner", Width: 1920, Height: 480},
}
```

## Upload flow

### Images

```go
// From a multipart file header (typical in an Echo handler)
result, err := store.Upload(ctx, fileHeader)

// From an io.Reader
result, err := store.UploadFromReader(ctx, reader, size)
```

`Upload` validates the file size, detects the MIME type, generates all configured size variants via `ffmpeg`, and saves each variant to storage. It returns an `*ImageUploadResult` containing the generated `ID`, `MediaType`, and `MimeType`.

### Videos

```go
result, err := store.Upload(ctx, fileHeader)
```

`Upload` validates the file size, detects the MIME type, probes the duration via `ffprobe`, saves the video, and optionally generates a thumbnail. It returns a `*VideoUploadResult` containing the `ID`, `MediaType`, `MimeType`, `Duration`, `FileSize`, and `ThumbnailPath`.

## Delete

Remove all stored files for a given media ID:

```go
err := imageStore.Delete(ctx, id)
err := videoStore.Delete(ctx, id)
```

Image deletion removes all size variants. Video deletion removes the video file and (best-effort) its thumbnail.

## URL construction

### GetMedia / GetMediaCtx

Both stores provide `GetMedia` (unsigned URLs) and `GetMediaCtx` (pre-signed URLs for S3 stores with `SignedExpiry` configured).

```go
ref := store.GetMedia(id)          // unsigned
ref := store.GetMediaCtx(ctx, id)  // signed if configured
```

### ImageRef

Returned by `ImageStore.GetMedia` / `GetMediaCtx`.

```go
ref.Size("medium")  // URL for the "medium" variant
ref.Thumb()         // URL for "thumb" variant, falls back to smallest
ref.Smallest()      // URL for the smallest configured size (by width)
ref.Biggest()       // URL for the largest configured size (by width)
```

### VideoRef

Returned by `VideoStore.GetMedia` / `GetMediaCtx`.

```go
ref.Video()      // URL for the video file
ref.Thumbnail()  // URL for the thumbnail (empty string if not generated)
```

## ServeHandler

Both stores provide an Echo handler for serving media files. For local stores it serves directly from the filesystem; for S3 stores it proxies through the storage layer.

```go
e.GET("/uploads/*", imageStore.ServeHandler())
e.GET("/videos/*", videoStore.ServeHandler())
```

Responses include `Cache-Control: public, max-age=31536000, immutable` headers.

## DetectType helper

Sniffs the MIME type from a multipart file header and returns the media type:

```go
mediaType, mimeType, err := media.DetectType(fileHeader)
// mediaType is "image" or "video"
// mimeType is e.g. "image/jpeg", "video/mp4"
// err is ErrUnknownType if the file is not a supported image or video
```

Supported image types: JPEG, PNG, WebP, GIF, HEIC, HEIF.
Supported video types: MP4, QuickTime, WebM, AVI.

## Error sentinels

| Error               | Meaning                                       |
|---------------------|-----------------------------------------------|
| `ErrFileTooLarge`   | Upload exceeds `MaxSize`                       |
| `ErrUnknownType`    | File is not a supported image or video format  |
| `ErrVideoTooLong`   | Video duration exceeds `MaxDuration`           |
| `ErrFFmpegNotFound` | `ffmpeg` or `ffprobe` not found in PATH        |

## Size constants

Convenience constants for specifying `MaxSize`:

```go
media.KB  // 1024
media.MB  // 1024 * 1024
media.GB  // 1024 * 1024 * 1024
```

## Format constants

Output format identifiers for `ImageStoreConfig.Format`:

```go
media.FormatWebP  // "webp"
media.FormatJPEG  // "jpeg"
media.FormatPNG   // "png"
```

## Options

Both store constructors accept variadic options:

```go
media.WithLogger(logger)  // set a custom *slog.Logger (defaults to slog.Default())
```
