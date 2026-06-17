// Package media provides high-level image and video upload, processing, and
// serving on top of the storage package. It handles resizing, format
// conversion, thumbnail generation, and URL construction for both local
// filesystem and S3-compatible backends.
package media

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Size constants.
const (
	KB int64 = 1024
	MB int64 = 1024 * 1024
	GB int64 = 1024 * 1024 * 1024
)

// Output format constants.
const (
	FormatWebP = "webp"
	FormatJPEG = "jpeg"
	FormatPNG  = "png"
)

// MediaType distinguishes images from videos.
const (
	TypeImage = "image"
	TypeVideo = "video"
)

// Sentinel errors.
var (
	ErrFileTooLarge   = errors.New("media: file too large")
	ErrUnknownType    = errors.New("media: unsupported file type")
	ErrVideoTooLong   = errors.New("media: video exceeds maximum duration")
	ErrFFmpegNotFound = errors.New("media: ffmpeg not found in PATH")
	// ErrInvalidID is returned by *FromReaderWithID methods when the
	// caller-supplied id is not a canonical UUID. The check is deliberately
	// strict: the id is interpolated into storage paths, so allowing free-form
	// strings opens path-traversal and collision footguns. Callers using a
	// non-UUID identifier scheme should generate a UUID for storage and
	// keep their own id as a separate column.
	ErrInvalidID = errors.New("media: invalid id (must be a canonical UUID)")
	// ErrIDExists is returned by *FromReaderWithID methods when storage
	// already holds bytes under the given id and overwrite=false. Pass
	// overwrite=true to clobber an existing prefix (e.g. retrying after a
	// failed partial write).
	ErrIDExists = errors.New("media: id already exists")
)

// ImageSize defines a named output dimension.
type ImageSize struct {
	Name   string
	Width  int
	Height int
}

// Preset size sets.
var (
	SizesAvatar = []ImageSize{
		{Name: "thumb", Width: 64, Height: 64},
		{Name: "small", Width: 150, Height: 150},
		{Name: "medium", Width: 400, Height: 400},
	}

	SizesCard = []ImageSize{
		{Name: "thumb", Width: 64, Height: 64},
		{Name: "small", Width: 150, Height: 150},
		{Name: "medium", Width: 400, Height: 400},
		{Name: "large", Width: 800, Height: 800},
		{Name: "xlarge", Width: 1200, Height: 1200},
	}

	SizesIcon = []ImageSize{
		{Name: "small", Width: 100, Height: 100},
		{Name: "medium", Width: 200, Height: 200},
		{Name: "large", Width: 400, Height: 400},
	}

	SizeOriginal = []ImageSize{
		{Name: "original", Width: 0, Height: 0},
	}
)

// ImageStoreConfig configures an image store.
type ImageStoreConfig struct {
	Category     string
	Sizes        []ImageSize
	Quality      int
	Format       string
	MaxSize      int64
	SignedExpiry time.Duration
	BaseURL      string
}

func (c *ImageStoreConfig) validate() error {
	if c.Category == "" {
		return fmt.Errorf("media: category must not be empty")
	}
	if len(c.Sizes) == 0 {
		return fmt.Errorf("media: at least one size is required")
	}
	if c.Quality < 1 || c.Quality > 100 {
		return fmt.Errorf("media: quality must be between 1 and 100")
	}
	switch c.Format {
	case FormatWebP, FormatJPEG, FormatPNG:
	default:
		return fmt.Errorf("media: unsupported format %q", c.Format)
	}
	if c.MaxSize <= 0 {
		return fmt.Errorf("media: max size must be positive")
	}
	return nil
}

// VideoStoreConfig configures a video store.
//
// Transcode is opt-in via population: a zero-valued Transcode means
// uploads are stored verbatim (the original behaviour); any non-zero
// field flips the store into "transcode every upload to H.264/AAC MP4
// before saving" mode. See VideoTranscodeOptions for the knobs and the
// defaults applied to fields the caller leaves unset.
type VideoStoreConfig struct {
	Category string
	// MaxSize is the maximum upload size in bytes. It's checked
	// against the input payload, before any transcode runs — we
	// reject oversized uploads at the door rather than after spending
	// CPU. The encoded output is *not* re-checked: a pathological
	// input (low-bitrate exotic codec) can in principle balloon
	// past MaxSize once re-encoded at CRF 23, so callers with hard
	// storage budgets should size MaxSize conservatively or pair it
	// with quota tracking at the storage layer.
	MaxSize           int64
	MaxDuration       float64
	GenerateThumbnail bool
	ThumbnailWidth    int
	BaseURL           string
	SignedExpiry      time.Duration
	Transcode         VideoTranscodeOptions
}

// VideoTranscodeOptions configures the optional H.264/AAC MP4 transcode
// pass run by VideoStore.Upload* before bytes are persisted.
//
// Activation is by population, not a flag: a zero-value struct means "do
// not transcode, save the upload bytes verbatim". As soon as any field
// is non-zero the transcode pass runs on every upload, and any fields
// left at their zero value are filled in with sensible defaults during
// store construction (see validate()).
//
// The transcoder pins libx264 with `-pix_fmt yuv420p` and `-profile:v
// high` for broad browser/QuickTime/smart-TV compatibility (Safari and
// many TV decoders refuse 4:2:2 H.264, which iPhone 4K footage commonly
// produces); these aren't configurable on purpose. The output container
// is MP4 with `+faststart` so the moov atom lands at the front and the
// file streams progressively.
type VideoTranscodeOptions struct {
	// CRF is the H.264 constant-rate-factor. Effective range is 1–51;
	// lower = higher quality and larger files. 18–28 is the practical
	// band; the package default is 23 (the x264 baseline).
	//
	// CRF=0 is **not** lossless mode here even though libx264 accepts
	// it as such: 0 is the Go zero value and we use it as the "caller
	// did not set this" sentinel for the default-fill path, so passing
	// CRF=0 ends up as CRF=23 on the encoder. Lossless H.264 for web
	// video is vanishingly rare and produces huge files, and the clean
	// default-fill API is worth losing that one edge case. Callers who
	// genuinely need lossless transcoding should run ffmpeg themselves
	// and feed the result to UploadFromReader with Transcode left zero.
	CRF int

	// Preset is the x264 speed/compression tradeoff. One of: ultrafast,
	// superfast, veryfast, faster, fast, medium, slow, slower, veryslow,
	// placebo. Default: "medium".
	Preset string

	// AudioBitrate is passed verbatim to ffmpeg's -b:a flag (e.g.
	// "128k", "192k"). Default: "128k".
	AudioBitrate string

	// MaxWidth and MaxHeight clamp output dimensions while preserving
	// aspect ratio. Zero on either axis means "no clamp on this axis".
	// When both are zero the source dimensions are preserved.
	//
	// IMPORTANT: setting either of these on its own is enough to flip
	// IsZero() to false and activate the full transcode pass — there
	// is no "clamp size but otherwise leave bytes alone" mode. If you
	// only want size clamping, you still get an H.264/AAC re-encode
	// of every upload. Callers who pass through known-good MP4 should
	// either leave Transcode entirely zero, or accept the re-encode.
	MaxWidth, MaxHeight int
}

// IsZero reports whether the options struct is in its zero state, i.e.
// the caller has not requested transcoding. Used by VideoStore.upload to
// decide between "save raw bytes" and "transcode then save".
func (o VideoTranscodeOptions) IsZero() bool {
	return o == VideoTranscodeOptions{}
}

// validX264Presets is the closed set accepted by libx264's -preset flag
// at the version of ffmpeg we target. Validating here gives the caller a
// clear error at NewLocal/S3VideoStore time instead of a cryptic ffmpeg
// stderr at first upload.
var validX264Presets = map[string]bool{
	"ultrafast": true, "superfast": true, "veryfast": true,
	"faster": true, "fast": true, "medium": true,
	"slow": true, "slower": true, "veryslow": true, "placebo": true,
}

func (c *VideoStoreConfig) validate() error {
	if c.Category == "" {
		return fmt.Errorf("media: category must not be empty")
	}
	if c.MaxSize <= 0 {
		return fmt.Errorf("media: max size must be positive")
	}
	if c.MaxDuration <= 0 {
		return fmt.Errorf("media: max duration must be positive")
	}
	if c.GenerateThumbnail && c.ThumbnailWidth <= 0 {
		return fmt.Errorf("media: thumbnail width must be positive when thumbnail generation is enabled")
	}
	if !c.Transcode.IsZero() {
		// CRF effective range is 1..51. Zero is accepted here but
		// treated as "use default" by the fill below, not as lossless.
		// See VideoTranscodeOptions.CRF for the sentinel rationale.
		if c.Transcode.CRF < 0 || c.Transcode.CRF > 51 {
			return fmt.Errorf("media: transcode CRF must be in 0..51 (0 means use default 23)")
		}
		if c.Transcode.MaxWidth < 0 {
			return fmt.Errorf("media: transcode max width must be non-negative")
		}
		if c.Transcode.MaxHeight < 0 {
			return fmt.Errorf("media: transcode max height must be non-negative")
		}
		if c.Transcode.Preset != "" && !validX264Presets[c.Transcode.Preset] {
			return fmt.Errorf("media: transcode preset %q is not a valid x264 preset", c.Transcode.Preset)
		}
		// Fill defaults for unset fields. This mutates c (validate has a
		// pointer receiver) so the store ends up holding the resolved
		// values — runtime never has to re-derive them.
		if c.Transcode.CRF == 0 {
			c.Transcode.CRF = 23
		}
		if c.Transcode.Preset == "" {
			c.Transcode.Preset = "medium"
		}
		if c.Transcode.AudioBitrate == "" {
			c.Transcode.AudioBitrate = "128k"
		}
	}
	return nil
}

// ImageUploadResult is returned after a successful image upload.
type ImageUploadResult struct {
	ID        string
	MediaType string
	MimeType  string
	sizes     []ImageSize
	category  string
	format    string
}

// Path returns the storage path for the given size variant.
func (r *ImageUploadResult) Path(size string) string {
	return fmt.Sprintf("%s/%s/%s.%s", r.category, r.ID, size, r.format)
}

// VideoUploadResult is returned after a successful video upload.
type VideoUploadResult struct {
	ID            string
	MediaType     string
	MimeType      string
	Duration      float64
	FileSize      int64
	ThumbnailPath string
	category      string
}

// Path returns the storage path for the video file.
func (r *VideoUploadResult) Path() string {
	return fmt.Sprintf("%s/%s/video.mp4", r.category, r.ID)
}

// Option configures a media store.
type Option func(*options)

type options struct {
	logger *slog.Logger
}

func defaultOptions() *options {
	return &options{logger: slog.Default()}
}

// WithLogger sets the logger for the store.
func WithLogger(l *slog.Logger) Option {
	return func(o *options) { o.logger = l }
}

// Supported MIME types.
var imageTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/webp": true,
	"image/gif":  true,
	"image/heic": true,
	"image/heif": true,
}

var videoTypes = map[string]bool{
	"video/mp4":       true,
	"video/quicktime": true,
	"video/webm":      true,
	"video/x-msvideo": true,
}

// DetectType sniffs the MIME type from the file header and returns the media
// type (TypeImage or TypeVideo), the MIME string, or ErrUnknownType.
func DetectType(fh *multipart.FileHeader) (mediaType string, mimeType string, err error) {
	f, err := fh.Open()
	if err != nil {
		return "", "", fmt.Errorf("media: open file: %w", err)
	}
	defer func() { _ = f.Close() }()

	buf := make([]byte, 512)
	n, err := io.ReadAtLeast(f, buf, 1)
	if err != nil {
		return "", "", fmt.Errorf("media: read file header: %w", err)
	}

	detected := http.DetectContentType(buf[:n])
	// Normalize — http.DetectContentType may return params like charset.
	detected = strings.SplitN(detected, ";", 2)[0]
	detected = strings.TrimSpace(detected)

	if imageTypes[detected] {
		return TypeImage, detected, nil
	}
	if videoTypes[detected] {
		return TypeVideo, detected, nil
	}

	// Deliberately do NOT fall back to the multipart Content-Type header: it is
	// attacker-controlled and the actual upload gate (Image/VideoStore.upload)
	// is content-sniff-only. Trusting the header here would make DetectType
	// accept files the upload would then reject — a spoofing bypass for callers
	// that use DetectType as a pre-check.
	return "", "", ErrUnknownType
}

// scopedServeKey maps an incoming request URL path to the storage key for a
// store mounted at urlPrefix and confined to category. It strips urlPrefix,
// normalizes the remainder with path.Clean (collapsing ./ and ../ so a crafted
// request can't escape), and requires the result to live under "category/".
//
// It returns ok=false — which serve handlers translate to 404 — for any path
// outside the URL prefix or the store's category. This is what keeps a handler
// from proxying objects that belong to another category, or anywhere else in
// the bucket / storage root: the only keys it will resolve are this store's own
// "category/…" objects.
func scopedServeKey(reqPath, urlPrefix, category string) (key string, ok bool) {
	rel := strings.TrimPrefix(reqPath, urlPrefix+"/")
	if rel == reqPath {
		return "", false // request is not under this store's URL prefix
	}
	rel = strings.TrimPrefix(path.Clean("/"+rel), "/")
	if category != "" && !strings.HasPrefix(rel, category+"/") {
		return "", false // outside this store's category prefix
	}
	return rel, true
}

// validateCanonicalUUID enforces the contract for *FromReaderWithID: the
// id must be in the 36-character canonical form
// `xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx`. uuid.Parse is permissive — it
// also accepts the hyphenless 32-char form, urn:uuid:..., and {…} braced
// forms — and accepting any of those would let the same logical UUID
// land at two different storage keys (silent bifurcation). Strict
// canonical aligns exactly with what uuid.New().String() produces, which
// is what every realistic caller mints.
func validateCanonicalUUID(id string) error {
	if len(id) != 36 {
		return ErrInvalidID
	}
	if _, err := uuid.Parse(id); err != nil {
		return ErrInvalidID
	}
	return nil
}

// formatExt returns the file extension for the given format.
func formatExt(format string) string {
	switch format {
	case FormatWebP:
		return "webp"
	case FormatJPEG:
		return "jpeg"
	case FormatPNG:
		return "png"
	default:
		return format
	}
}
