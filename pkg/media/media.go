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
	"strings"
	"time"
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
	Category    string
	Sizes       []ImageSize
	Quality     int
	Format      string
	MaxSize     int64
	SignedExpiry time.Duration
	BaseURL     string
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
type VideoStoreConfig struct {
	Category          string
	MaxSize           int64
	MaxDuration       float64
	GenerateThumbnail bool
	ThumbnailWidth    int
	BaseURL           string
	SignedExpiry       time.Duration
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
	"video/mp4":        true,
	"video/quicktime":  true,
	"video/webm":       true,
	"video/x-msvideo":  true,
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

	// Fallback: check the Content-Type header from the upload.
	ct := fh.Header.Get("Content-Type")
	ct = strings.SplitN(ct, ";", 2)[0]
	ct = strings.TrimSpace(ct)

	if imageTypes[ct] {
		return TypeImage, ct, nil
	}
	if videoTypes[ct] {
		return TypeVideo, ct, nil
	}

	return "", "", ErrUnknownType
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
