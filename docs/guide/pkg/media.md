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

Handles video upload, duration validation, optional H.264/AAC transcode, optional thumbnail extraction, serving, and deletion. Requires `ffmpeg` and `ffprobe` in PATH.

```go
// Local filesystem backend
store, err := media.NewLocalVideoStore(localStorage, "/uploads", media.VideoStoreConfig{
    Category:          "clips",
    MaxSize:           100 * media.MB,
    MaxDuration:       120, // seconds
    GenerateThumbnail: true,
    ThumbnailWidth:    640,
    // Optional: re-encode every upload to H.264/AAC MP4 with +faststart
    // before saving. Leave Transcode zero-valued to store bytes verbatim.
    Transcode: media.VideoTranscodeOptions{
        Preset:    "medium",
        CRF:       23,
        MaxWidth:  1920,
        MaxHeight: 1080,
    },
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

| Field               | Type                    | Description                                              |
|---------------------|-------------------------|----------------------------------------------------------|
| `Category`          | `string`                | Storage prefix/folder for this media type                 |
| `MaxSize`           | `int64`                 | Maximum upload size in bytes (checked against the input — see Video transcoding below) |
| `MaxDuration`       | `float64`               | Maximum video duration in seconds                         |
| `GenerateThumbnail` | `bool`                  | Extract a JPEG thumbnail at the 1-second mark            |
| `ThumbnailWidth`    | `int`                   | Thumbnail width in pixels (height auto-scaled)            |
| `BaseURL`           | `string`                | CDN/public base URL for S3 stores                         |
| `SignedExpiry`      | `time.Duration`         | Pre-signed URL expiry for S3 stores (0 = unsigned)       |
| `Transcode`         | `VideoTranscodeOptions` | Re-encode uploads to H.264/AAC MP4 before saving — zero-valued = save bytes verbatim |

### VideoTranscodeOptions

Activation is by population, not a flag: a zero-valued `VideoTranscodeOptions` means "do not transcode". Any non-zero field flips the store into "transcode every upload" mode, and unset fields are filled in with the defaults below at store construction.

| Field          | Type     | Default    | Description                                              |
|----------------|----------|------------|----------------------------------------------------------|
| `CRF`          | `int`    | `23`       | x264 constant-rate-factor (effective range `1–51`); lower = better quality, larger files |
| `Preset`       | `string` | `"medium"` | x264 preset: `ultrafast`, `superfast`, `veryfast`, `faster`, `fast`, `medium`, `slow`, `slower`, `veryslow`, `placebo` |
| `AudioBitrate` | `string` | `"128k"`   | Passed verbatim to ffmpeg's `-b:a`                       |
| `MaxWidth`     | `int`    | `0`        | Clamp output width while preserving aspect; `0` = no clamp |
| `MaxHeight`    | `int`    | `0`        | Clamp output height while preserving aspect; `0` = no clamp |

A few things to know about these fields:

- **`CRF=0` is not lossless** even though libx264 itself accepts it that way — the Go zero value is reserved as the "caller did not set this" sentinel for the default-fill, so passing `CRF: 0` ends up at `CRF=23` on the encoder. Callers who genuinely need lossless H.264 should run ffmpeg themselves and hand the result to `UploadFromReader` with `Transcode` left zero.
- **`MaxWidth`/`MaxHeight` activate the full transcode pass** — there is no "clamp size but otherwise leave bytes alone" mode. Setting only `MaxWidth: 1920` re-encodes every upload to H.264/AAC MP4. If your inputs are already known-good MP4, leave `Transcode` zero and accept whatever dimensions came in.
- The encoder pins `-pix_fmt yuv420p` and `-profile:v high` for broad browser/QuickTime/smart-TV compatibility (Safari and many TV decoders refuse 4:2:2 H.264, which iPhone 4K footage commonly produces). These aren't exposed as knobs.

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
// From a multipart file header (typical in an Echo handler).
result, err := store.Upload(ctx, fileHeader)

// From an io.Reader. The id is generated internally as a fresh UUID.
result, err := store.UploadFromReader(ctx, reader, size)

// From an io.Reader, using the caller's pre-minted UUID for the storage
// path. Useful when the caller already wrote a row in its own DB and
// needs the bytes stored under that same id (e.g. an async-processing
// worker pulling jobs off a queue). Pass overwrite=true to allow
// clobbering an existing prefix — typical for retry-after-failure.
result, err := store.UploadFromReaderWithID(ctx, id, reader, size, overwrite)
```

`Upload` / `UploadFromReader` validate the file size, detect the MIME type, generate all configured size variants via `ffmpeg`, and save each variant to storage. They return an `*ImageUploadResult` containing the generated `ID`, `MediaType`, and `MimeType`.

`UploadFromReaderWithID` adds two contracts on top:

- The supplied `id` MUST be in the **36-character canonical UUID form** (`xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx`). Other forms accepted by `uuid.Parse` (32-char hyphenless, `urn:uuid:...`, `{...}` braced) and the empty string are deliberately rejected — they'd let the same logical UUID land at two different storage keys (silent bifurcation), and the empty string would defeat the method's whole purpose by silently auto-generating an id. All bad inputs return `ErrInvalidID`.
- With `overwrite=false`, the call returns `ErrIDExists` if storage already holds bytes under `id`. Workers retrying a failed job typically pass `overwrite=true` so the retry replaces any residue from a partial write.

`ErrIDExists` is a **best-effort precheck**, not a hard guarantee. Two concurrent `*WithID` calls with the same id and `overwrite=false` can both pass the existence check and race on Save (last-write-wins on S3, clobber on local FS). The same race exists for `cleanupPartial` on a failed upload — variant deletes from a losing call can erase a concurrent winner's writes. Callers that need hard exclusion should serialize at the source-of-truth layer — typically a unique constraint on the DB row that owns the id.

On `ErrFileTooLarge` (size pre-check), `ErrInvalidID`, and `ErrIDExists` the reader `r` is **not consumed** — all three are detected before `detectMIME` runs. Callers relying on the upload to drain a network or pipe reader must discard or reuse `r` explicitly on these errors. (`ErrFileTooLarge` can also fire after the MIME sniff against the actual buffered byte count; in that case `r` has been drained.)

On any error after the first variant has been written, the method makes a best-effort attempt to delete the variants it has already saved. Cleanup failures are logged via the configured `slog` logger and do not mask the original error.

### Videos

```go
// From a multipart file header.
result, err := store.Upload(ctx, fileHeader)

// From an io.Reader (id generated internally).
result, err := store.UploadFromReader(ctx, reader, size)

// From an io.Reader using the caller's UUID; same contract as the
// image variant.
result, err := store.UploadFromReaderWithID(ctx, id, reader, size, overwrite)
```

`Upload` / `UploadFromReader` validate the file size, detect the MIME type, probe the duration via `ffprobe`, optionally transcode to H.264/AAC MP4 (when `Transcode` is configured), save the video, and optionally generate a thumbnail. They return a `*VideoUploadResult` containing the `ID`, `MediaType`, `MimeType`, `Duration`, `FileSize`, and `ThumbnailPath`. `FileSize` reflects what was actually written to storage — i.e. the encoded bytes when transcoding is enabled, the input bytes otherwise.

`UploadFromReaderWithID` follows the same UUID-validation and existence-check rules as the image variant (strict canonical only; `ErrIDExists` is best-effort, not a hard concurrency guarantee).

Thumbnail generation/save failures are **logged and ignored** — the video bytes are still stored, and `result.ThumbnailPath` stays empty when this branch fails. There is no partial-write window after the video Save because that's the only step that can fail once the id is fixed.

#### Video transcoding

Populate `VideoStoreConfig.Transcode` to re-encode every upload to an H.264/AAC MP4 with `+faststart` (moov atom relocated to the front for progressive streaming) before bytes hit storage. Leave it zero-valued and the store keeps the existing behaviour of saving the upload bytes verbatim.

A few details worth knowing:

- **Activation is by population.** Setting any single field on `Transcode` flips the store into transcode mode; the remaining fields fall back to the documented defaults during validation. There is no `Disable` flag — the way to opt out is to leave the struct zero. **This includes `MaxWidth`/`MaxHeight`** — adding only a size clamp also activates the full re-encode.
- **`MaxSize` is checked against the input bytes**, before the transcode runs. The encoded MP4 is almost always smaller than the source for consumer phone footage, so we don't re-check after — the dev's intent for `MaxSize` is "what will I accept from a client", not "what will I store on disk". A pathological input (low-bitrate exotic codec) could in principle balloon past `MaxSize` once re-encoded; pair `MaxSize` with quota tracking at the storage layer if your storage budget is a hard ceiling.
- **`MimeType` on the result reflects what's on disk.** When transcoding is enabled the result reports `video/mp4` regardless of input container, since the bytes saved are always H.264/AAC MP4. With transcoding off it carries the source MIME unchanged.
- **Thumbnail extraction runs against the raw upload**, not the encoded output, so a transcode-induced quirk doesn't ripple into thumbnail success and the captured frame matches the original timeline exactly.
- **The transcode pass is slow** — tens of seconds for a one-minute clip on `medium` preset. Hand `Upload*` to a worker pool so it doesn't block the request goroutine; `pkg/async`'s `Group` is a good fit:

```go
videoWorkers := async.NewGroup(async.WithLimit(4))
defer videoWorkers.Close()

videoWorkers.Go(func() {
    _, err := videoStore.UploadFromReaderWithID(
        context.Background(), id, bytes.NewReader(buf), int64(len(buf)), true,
    )
    if err != nil {
        logger.Error("transcode failed", "id", id, "err", err)
    }
})
```

- **`+faststart` requires a seekable output**, so the transcode internally writes to a temp file and reads it back rather than streaming through stdout. The temp file lives in `os.TempDir()` for the duration of the encode and is removed before the function returns.

### Sentinel errors

| Error | When |
|---|---|
| `ErrFileTooLarge` | `size > MaxSize` (cheap pre-check) or buffered bytes exceed `MaxSize` after MIME sniff |
| `ErrUnknownType` | sniffed MIME isn't in the package's allow list |
| `ErrVideoTooLong` | `ffprobe` duration > `MaxDuration` |
| `ErrInvalidID` | `*WithID` variants: `id` is empty or not in 36-char canonical UUID form |
| `ErrIDExists` | `*WithID` variants with `overwrite=false`: storage already holds bytes under `id` (best-effort precheck — see TOCTOU caveat above) |
| `ErrFFmpegNotFound` | required ffmpeg/ffprobe binary missing from `PATH` |

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
