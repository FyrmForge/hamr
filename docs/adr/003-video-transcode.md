# ADR-003: Video transcoding in `pkg/media`

- **Status**: Accepted
- **Date**: 2026-05-03
- **Authors**: Dumitru Vulpe

## Context

`pkg/media`'s `VideoStore.Upload*` previously stored uploads verbatim under
`<category>/<id>/video.mp4` regardless of the actual input container or codec.
Browsers happily play H.264/AAC MP4, but `videoTypes` also accepts QuickTime
(`.mov`), WebM, and AVI — none of which are universally playable in a `<video>`
tag without conversion. Users uploading from phones (frequently `.mov` with
HEVC) saw black or unplayable embeds.

We want consistent, browser-playable output regardless of input, with a clear
opt-in surface so existing callers that already feed in known-good MP4 don't
pay an unnecessary re-encode.

## Decisions

### Activation by population, not a flag

Transcode is an opt-in feature. Rather than introduce a `Transcode.Disable`
boolean that flips behaviour, the activation rule is "any non-zero field on
`VideoTranscodeOptions`". A zero-valued struct preserves the legacy "save raw
bytes" path; populating any field flips the store into "re-encode every upload"
mode.

This matches how `GenerateThumbnail bool` already gates an optional pipeline
step on the same struct, and it keeps the config struct self-describing — an
empty struct visually communicates "off", a populated one communicates "on".
Validation fills in defaults for unset fields so the dev only has to set what
they care about.

**Rejected: explicit `Enabled bool` field.** Same shape, more typing per
caller, and breaks the "set it and it works" idiom.

**Rejected: a separate `media.TranscodeVideo` free function callers compose
themselves.** Cleaner separation of concerns in isolation, but `ImageStore`
already couples its processing config (sizes, quality, format) into the store
config. Two near-identical packages with opposite shapes would be jarring
to use; consistency wins.

### Codec: H.264 video + AAC audio in MP4

H.264 (libx264) is the ubiquitous browser/TV/QuickTime baseline. AAC is the
matching audio codec for the same compatibility surface. MP4 is the container
that streams progressively in `<video>` once `+faststart` reorders the moov
atom.

We pin `-pix_fmt yuv420p` because Safari and many smart-TV decoders refuse
4:2:2/4:2:4 H.264. iPhones routinely produce 4:2:2 from newer iOS versions, so
without this the output would play in Chrome and break in Safari.

We do **not** pin `-level` or `-profile:v` beyond `high`. The level pin proposed
in the design discussion (`level=4.0`) caps source at 1080p30 and would
fail/clamp on 4K phone footage. Letting ffmpeg auto-pick the level keeps the
encoder usable on whatever the source happens to be; callers who want a hard
output cap should set `MaxWidth`/`MaxHeight`.

### CRF=23, preset=medium, audio=128k as defaults

These are the x264 defaults for CRF and preset and a typical AAC stereo bitrate
for music/voice mixed content. They produce a defensible quality/size tradeoff
out of the box. Devs that care can override.

The CRF effective range is **1–51**, not 0–51, even though libx264 itself
accepts 0 as a lossless mode. Reserving 0 as the default-fill sentinel ("the
caller did not set this field") is what makes activation-by-population work
without an extra `Enabled` flag. The cost is that callers cannot request
lossless transcodes through `VideoTranscodeOptions`; rare for web video and
the workaround (run ffmpeg yourself, hand the result to `UploadFromReader`
with `Transcode` zero) is straightforward. This tradeoff is documented on
`VideoTranscodeOptions.CRF`.

### Scale filter: chained `scale=...,scale=trunc(iw/2)*2:trunc(ih/2)*2`

libx264 requires even dimensions on both axes. The original draft used
`force_divisible_by=2` on the scale filter, which is concise but only landed
in libavfilter ~ffmpeg 4.4 (2021). Older base images (Debian buster, RHEL 8
base, Ubuntu 18.04 LTS without backports) ship ffmpeg 3.x or early 4.x and
fail at runtime with "Option not found". `checkFFmpeg` only verifies the
binary exists, not its version, so the failure would only surface at first
upload.

The chained form (`scale=...,scale=trunc(iw/2)*2:trunc(ih/2)*2`) works on
every ffmpeg version that has the scale filter and also fixes a separate bug
in the single-axis branches: `scale='min(iw,W)':-2` on a source with odd
width would pass the odd width through unchanged, and libx264 then rejects
"width not divisible by 2". The post-pass `trunc(iw/2)*2` normalises both
axes regardless of which filter branch produced them.

### Result MIME reflects what's on disk

When transcoding runs, `VideoUploadResult.MimeType` reports `video/mp4`
regardless of the input container, since the bytes saved are H.264/AAC MP4.
With transcoding off it carries the source MIME unchanged. Callers
persisting `MimeType` to a DB or using it to set Content-Type when serving
would otherwise mislabel the stored asset (e.g. claim `video/webm` while
serving MP4 bytes), so the package fixes the field at the boundary.

### `+faststart` via temp file, not stdout pipe

`+faststart` requires a seekable output — ffmpeg writes the file once and then
relocates the moov atom to the front in a second pass. stdout pipes are not
seekable, so the flag is silently a no-op (or errors, depending on ffmpeg
version) when output is piped.

`transcodeVideoToMP4` therefore writes ffmpeg's output to a file in
`os.MkdirTemp` and reads it back into memory before returning. The temp dir is
removed via `defer`. This costs one extra disk round-trip per upload but is
the only correct way to get faststart from a stdin-fed transcode.

**Rejected: fragmented MP4 (`-movflags frag_keyframe+empty_moov`).** Streamable
without faststart, but produces a slightly different on-disk format that some
older Safari versions handle poorly. Not worth the compatibility risk to save
a temp file.

### `MaxSize` is checked against input bytes, not output

Transcoding usually shrinks consumer phone footage, so checking the encoded
output against `MaxSize` is rarely a binding constraint. More importantly,
discovering "your upload is too big" *after* tens of seconds of CPU is a
worse UX than rejecting it at the door. The check stays where it is —
pre-transcode — and we document this on the field.

### Thumbnail extracted from raw bytes, not encoded output

Decoupling thumbnail success from transcode success: a transcode quirk that
trips libx264 shouldn't also lose the thumbnail. The 1-second seek hits the
same timeline regardless of which codec we ultimately store, so the captured
frame is identical either way.

### `result.FileSize` reflects what was persisted

When transcoding is enabled, `FileSize` is the encoded byte count; when
disabled, it's the input byte count. The dev's view of "how much storage am
I using" is the on-disk number, so we report what's actually on disk.

### Sync API, async pattern is the dev's choice

`Upload*` runs the transcode inline and returns when bytes are persisted.
This matches `ImageStore.Upload`. Image processing is fast enough to live on
the request goroutine; video transcoding is not, but rather than introduce a
separate `Transcode(ctx, id)` method or a built-in queue, we leave async
handling to the caller — `pkg/async.Group` is a fire-and-forget worker pool
with bounded concurrency, panic recovery, and graceful shutdown that fits
this use case directly.

The docstring and human guide both call this out: "transcoding is slow, hand
`Upload*` to a worker pool".

**Rejected: split the API into `Upload*` (saves raw) + `Transcode(ctx, id)`.**
Cleaner separation, but introduces an asymmetry with `ImageStore`, requires
new "is it transcoded yet?" status plumbing, and forces every caller to wire
both halves even when sync is fine for them. The package's pattern is "expose
primitives, dev composes" — `async.Group` IS the worker primitive we already
ship.

## Scope

### New types
- `media.VideoTranscodeOptions` (CRF, Preset, AudioBitrate, MaxWidth, MaxHeight)
- `VideoTranscodeOptions.IsZero() bool`
- `VideoStoreConfig.Transcode VideoTranscodeOptions` field

### New functions
- `transcodeVideoToMP4(ctx, raw, opts) ([]byte, error)` in `pkg/media/video.go`
- `buildScaleFilter(maxW, maxH int) string` helper

### Modified
- `VideoStoreConfig.validate()` — defaults + bounds-check on transcode fields
- `VideoStore.upload()` — call transcode when configured, save encoded bytes
- `VideoUploadResult.FileSize` semantics (now = on-disk bytes)
- Docs: `docs/guide/pkg/media.md`, `llmsdocs/llms.txt`, `llmsdocs/llms-full.txt`

### Tests
- Validation table extended with bad CRF / bad preset / negative max-dim cases
- `TestVideoTranscodeOptions_IsZero` and a defaults-fill test
- `TestVideoStoreConfigValidation_FillsDefaults_VariousActivators`: each of
  CRF/AudioBitrate/MaxWidth/MaxHeight as the activating field exercises the
  default-fill so a regression that only triggers on `Preset` is caught
- Happy-path: WebM input → H.264/AAC MP4 output, ffprobe-verified for
  codecs, `pix_fmt yuv420p`, and `MimeType=video/mp4` on the result
- Zero-config: bytes preserved verbatim and `MimeType` reports source MIME
- `MaxWidth` AND `MaxHeight` single-axis clamps each tested separately
  (the buildScaleFilter branches use different syntax)
- Thumbnail-from-raw regression: same source uploaded with and without
  transcode produces byte-identical thumbnails, proving the thumbnail
  pipeline still reads `raw` after the wiring change
- `+faststart` real verification: `mp4TopLevelBoxOrder` walks the MP4 box
  layout and asserts `moov` lands before `mdat` (which is the actual
  promise of `+faststart`); a stdout-pipe regression that produces a
  parseable-but-trailing-moov MP4 would now fail this canary

A MOV-input happy-path test was intentionally omitted: Go's
`http.DetectContentType` doesn't recognise the `qt  ` major-brand QuickTime
files that ffmpeg writes by default, so the upload is rejected at MIME sniff
before transcode ever runs. That's a pre-existing limitation of the
reader-based upload path's MIME detection, not a regression introduced here.
