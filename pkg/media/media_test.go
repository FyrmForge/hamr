package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"net/textproto"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/FyrmForge/hamr/pkg/storage"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// testJPEG creates a minimal valid JPEG in memory.
func testJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{R: 255, G: 0, B: 0, A: 255})
		}
	}
	var buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}))
	return buf.Bytes()
}

// testPNG creates a minimal valid PNG in memory.
func testPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

// fakeMultipartFile builds a *multipart.FileHeader from raw bytes.
func fakeMultipartFile(t *testing.T, name string, contentType string, data []byte) *multipart.FileHeader {
	t.Helper()
	var b bytes.Buffer
	w := multipart.NewWriter(&b)

	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, name))
	h.Set("Content-Type", contentType)

	part, err := w.CreatePart(h)
	require.NoError(t, err)
	_, err = part.Write(data)
	require.NoError(t, err)
	require.NoError(t, w.Close())

	r := multipart.NewReader(&b, w.Boundary())
	form, err := r.ReadForm(int64(len(data)) + 1024)
	require.NoError(t, err)

	fhs := form.File["file"]
	require.Len(t, fhs, 1)
	return fhs[0]
}

func hasFFmpeg() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}

// ---------------------------------------------------------------------------
// Core types tests
// ---------------------------------------------------------------------------

func TestSizeConstants(t *testing.T) {
	assert.Equal(t, int64(1024), KB)
	assert.Equal(t, int64(1024*1024), MB)
	assert.Equal(t, int64(1024*1024*1024), GB)
}

func TestFormatConstants(t *testing.T) {
	assert.Equal(t, "webp", FormatWebP)
	assert.Equal(t, "jpeg", FormatJPEG)
	assert.Equal(t, "png", FormatPNG)
}

func TestMediaTypeConstants(t *testing.T) {
	assert.Equal(t, "image", TypeImage)
	assert.Equal(t, "video", TypeVideo)
}

func TestPresetSizes(t *testing.T) {
	assert.Len(t, SizesAvatar, 3)
	assert.Equal(t, "thumb", SizesAvatar[0].Name)
	assert.Equal(t, 64, SizesAvatar[0].Width)

	assert.Len(t, SizesCard, 5)
	assert.Equal(t, "xlarge", SizesCard[4].Name)
	assert.Equal(t, 1200, SizesCard[4].Width)

	assert.Len(t, SizesIcon, 3)
	assert.Len(t, SizeOriginal, 1)
	assert.Equal(t, "original", SizeOriginal[0].Name)
	assert.Equal(t, 0, SizeOriginal[0].Width)
}

func TestImageStoreConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     ImageStoreConfig
		wantErr string
	}{
		{
			name:    "empty category",
			cfg:     ImageStoreConfig{Sizes: SizesAvatar, Quality: 85, Format: FormatWebP, MaxSize: MB},
			wantErr: "category",
		},
		{
			name:    "no sizes",
			cfg:     ImageStoreConfig{Category: "test", Quality: 85, Format: FormatWebP, MaxSize: MB},
			wantErr: "size",
		},
		{
			name:    "invalid quality low",
			cfg:     ImageStoreConfig{Category: "test", Sizes: SizesAvatar, Quality: 0, Format: FormatWebP, MaxSize: MB},
			wantErr: "quality",
		},
		{
			name:    "invalid quality high",
			cfg:     ImageStoreConfig{Category: "test", Sizes: SizesAvatar, Quality: 101, Format: FormatWebP, MaxSize: MB},
			wantErr: "quality",
		},
		{
			name:    "invalid format",
			cfg:     ImageStoreConfig{Category: "test", Sizes: SizesAvatar, Quality: 85, Format: "bmp", MaxSize: MB},
			wantErr: "format",
		},
		{
			name:    "zero max size",
			cfg:     ImageStoreConfig{Category: "test", Sizes: SizesAvatar, Quality: 85, Format: FormatWebP, MaxSize: 0},
			wantErr: "max size",
		},
		{
			name: "valid config",
			cfg:  ImageStoreConfig{Category: "test", Sizes: SizesAvatar, Quality: 85, Format: FormatWebP, MaxSize: MB},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.validate()
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, strings.ToLower(err.Error()), tt.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestVideoStoreConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     VideoStoreConfig
		wantErr string
	}{
		{
			name:    "empty category",
			cfg:     VideoStoreConfig{MaxSize: MB, MaxDuration: 60},
			wantErr: "category",
		},
		{
			name:    "zero max size",
			cfg:     VideoStoreConfig{Category: "clips", MaxDuration: 60},
			wantErr: "max size",
		},
		{
			name:    "zero duration",
			cfg:     VideoStoreConfig{Category: "clips", MaxSize: MB},
			wantErr: "duration",
		},
		{
			name:    "thumbnail without width",
			cfg:     VideoStoreConfig{Category: "clips", MaxSize: MB, MaxDuration: 60, GenerateThumbnail: true},
			wantErr: "thumbnail width",
		},
		{
			name: "valid config",
			cfg:  VideoStoreConfig{Category: "clips", MaxSize: MB, MaxDuration: 60},
		},
		{
			name: "valid with thumbnail",
			cfg:  VideoStoreConfig{Category: "clips", MaxSize: MB, MaxDuration: 60, GenerateThumbnail: true, ThumbnailWidth: 320},
		},
		{
			name: "transcode CRF too high",
			cfg: VideoStoreConfig{
				Category: "clips", MaxSize: MB, MaxDuration: 60,
				Transcode: VideoTranscodeOptions{CRF: 60, Preset: "medium"},
			},
			wantErr: "crf",
		},
		{
			name: "transcode CRF negative",
			cfg: VideoStoreConfig{
				Category: "clips", MaxSize: MB, MaxDuration: 60,
				Transcode: VideoTranscodeOptions{CRF: -1, Preset: "medium"},
			},
			wantErr: "crf",
		},
		{
			name: "transcode invalid preset",
			cfg: VideoStoreConfig{
				Category: "clips", MaxSize: MB, MaxDuration: 60,
				Transcode: VideoTranscodeOptions{Preset: "lightning"},
			},
			wantErr: "preset",
		},
		{
			name: "transcode negative max width",
			cfg: VideoStoreConfig{
				Category: "clips", MaxSize: MB, MaxDuration: 60,
				Transcode: VideoTranscodeOptions{Preset: "medium", MaxWidth: -1},
			},
			wantErr: "width",
		},
		{
			name: "transcode negative max height",
			cfg: VideoStoreConfig{
				Category: "clips", MaxSize: MB, MaxDuration: 60,
				Transcode: VideoTranscodeOptions{Preset: "medium", MaxHeight: -1},
			},
			wantErr: "height",
		},
		{
			name: "transcode minimal populated",
			cfg: VideoStoreConfig{
				Category: "clips", MaxSize: MB, MaxDuration: 60,
				Transcode: VideoTranscodeOptions{Preset: "fast"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.validate()
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, strings.ToLower(err.Error()), tt.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestVideoTranscodeOptions_IsZero(t *testing.T) {
	assert.True(t, VideoTranscodeOptions{}.IsZero())
	assert.False(t, VideoTranscodeOptions{CRF: 23}.IsZero())
	assert.False(t, VideoTranscodeOptions{Preset: "medium"}.IsZero())
	assert.False(t, VideoTranscodeOptions{AudioBitrate: "128k"}.IsZero())
	assert.False(t, VideoTranscodeOptions{MaxWidth: 1920}.IsZero())
	assert.False(t, VideoTranscodeOptions{MaxHeight: 1080}.IsZero())
}

// TestVideoStoreConfigValidation_FillsTranscodeDefaults locks the
// "validate mutates the config to fill in defaults" contract — the
// store ends up holding resolved values so the upload hot path doesn't
// re-derive them on every call. The test asserts the exact defaults
// documented on VideoTranscodeOptions; if those defaults change, this
// test should be updated alongside the docstring.
func TestVideoStoreConfigValidation_FillsTranscodeDefaults(t *testing.T) {
	cfg := VideoStoreConfig{
		Category: "clips", MaxSize: MB, MaxDuration: 60,
		// Only one field set — the others should be filled in.
		Transcode: VideoTranscodeOptions{Preset: "fast"},
	}
	require.NoError(t, cfg.validate())
	assert.Equal(t, 23, cfg.Transcode.CRF)
	assert.Equal(t, "fast", cfg.Transcode.Preset)
	assert.Equal(t, "128k", cfg.Transcode.AudioBitrate)
	assert.Equal(t, 0, cfg.Transcode.MaxWidth, "MaxWidth=0 means no clamp; default must NOT introduce one")
	assert.Equal(t, 0, cfg.Transcode.MaxHeight, "MaxHeight=0 means no clamp; default must NOT introduce one")
}

// ---------------------------------------------------------------------------
// DetectType tests
// ---------------------------------------------------------------------------

func TestDetectType_JPEG(t *testing.T) {
	data := testJPEG(t, 10, 10)
	fh := fakeMultipartFile(t, "photo.jpg", "image/jpeg", data)

	mediaType, mime, err := DetectType(fh)
	require.NoError(t, err)
	assert.Equal(t, TypeImage, mediaType)
	assert.Equal(t, "image/jpeg", mime)
}

func TestDetectType_PNG(t *testing.T) {
	data := testPNG(t, 10, 10)
	fh := fakeMultipartFile(t, "icon.png", "image/png", data)

	mediaType, mime, err := DetectType(fh)
	require.NoError(t, err)
	assert.Equal(t, TypeImage, mediaType)
	assert.Equal(t, "image/png", mime)
}

func TestDetectType_Unknown(t *testing.T) {
	fh := fakeMultipartFile(t, "data.bin", "application/octet-stream", []byte("not an image or video"))

	_, _, err := DetectType(fh)
	assert.ErrorIs(t, err, ErrUnknownType)
}

func TestDetectType_IgnoresSpoofedContentTypeHeader(t *testing.T) {
	// Content sniffs as non-media, but the (attacker-controlled) multipart
	// Content-Type header claims image/jpeg. DetectType must sniff, not trust
	// the header — otherwise it disagrees with the sniff-only upload gate.
	fh := fakeMultipartFile(t, "evil.jpg", "image/jpeg", []byte("this is plain text, not an image"))

	_, _, err := DetectType(fh)
	assert.ErrorIs(t, err, ErrUnknownType,
		"DetectType must not trust the spoofable Content-Type header")
}

// ---------------------------------------------------------------------------
// ImageRef tests
// ---------------------------------------------------------------------------

func TestImageRef_Size(t *testing.T) {
	ref := ImageRef{
		id:       "abc-123",
		category: "headshots",
		format:   "webp",
		sizes:    SizesCard,
		baseURL:  "/uploads",
	}

	assert.Equal(t, "/uploads/headshots/abc-123/medium.webp", ref.Size("medium"))
	assert.Equal(t, "/uploads/headshots/abc-123/small.webp", ref.Size("small"))
}

func TestImageRef_Thumb(t *testing.T) {
	ref := ImageRef{
		id:       "abc-123",
		category: "headshots",
		format:   "webp",
		sizes:    SizesCard,
		baseURL:  "/uploads",
	}

	assert.Equal(t, "/uploads/headshots/abc-123/thumb.webp", ref.Thumb())
}

func TestImageRef_Thumb_FallsBackToSmallest(t *testing.T) {
	ref := ImageRef{
		id:       "abc-123",
		category: "icons",
		format:   "webp",
		sizes:    SizesIcon, // no "thumb" size
		baseURL:  "/uploads",
	}

	// SizesIcon smallest is "small" at 100x100
	assert.Equal(t, "/uploads/icons/abc-123/small.webp", ref.Thumb())
}

func TestImageRef_SmallestBiggest(t *testing.T) {
	ref := ImageRef{
		id:       "abc-123",
		category: "headshots",
		format:   "webp",
		sizes:    SizesCard,
		baseURL:  "/uploads",
	}

	assert.Equal(t, "/uploads/headshots/abc-123/thumb.webp", ref.Smallest())
	assert.Equal(t, "/uploads/headshots/abc-123/xlarge.webp", ref.Biggest())
}

func TestImageRef_SignedURLs(t *testing.T) {
	ref := ImageRef{
		id:       "abc-123",
		category: "documents",
		format:   "jpeg",
		sizes:    SizeOriginal,
		signFn: func(path string) string {
			return "https://bucket.s3.example.com/" + path + "?sig=abc"
		},
	}

	url := ref.Size("original")
	assert.Equal(t, "https://bucket.s3.example.com/documents/abc-123/original.jpeg?sig=abc", url)
}

func TestImageRef_EmptySizes(t *testing.T) {
	ref := ImageRef{id: "x", sizes: nil}
	assert.Equal(t, "", ref.Smallest())
	assert.Equal(t, "", ref.Biggest())
}

// ---------------------------------------------------------------------------
// VideoRef tests
// ---------------------------------------------------------------------------

func TestVideoRef_Video(t *testing.T) {
	ref := VideoRef{
		id:           "vid-123",
		category:     "clips",
		baseURL:      "/uploads",
		hasThumbnail: true,
	}

	assert.Equal(t, "/uploads/clips/vid-123/video.mp4", ref.Video())
	assert.Equal(t, "/uploads/clips/vid-123/thumb.jpg", ref.Thumbnail())
}

func TestVideoRef_NoThumbnail(t *testing.T) {
	ref := VideoRef{
		id:           "vid-123",
		category:     "clips",
		baseURL:      "/uploads",
		hasThumbnail: false,
	}

	assert.Equal(t, "", ref.Thumbnail())
}

func TestVideoRef_SignedURLs(t *testing.T) {
	ref := VideoRef{
		id:           "vid-123",
		category:     "clips",
		hasThumbnail: true,
		signFn: func(path string) string {
			return "https://s3.example.com/" + path + "?sig=xyz"
		},
	}

	assert.Equal(t, "https://s3.example.com/clips/vid-123/video.mp4?sig=xyz", ref.Video())
	assert.Equal(t, "https://s3.example.com/clips/vid-123/thumb.jpg?sig=xyz", ref.Thumbnail())
}

// ---------------------------------------------------------------------------
// ImageUploadResult / VideoUploadResult path tests
// ---------------------------------------------------------------------------

func TestImageUploadResult_Path(t *testing.T) {
	r := &ImageUploadResult{
		ID:       "abc-123",
		category: "headshots",
		format:   "webp",
	}
	assert.Equal(t, "headshots/abc-123/small.webp", r.Path("small"))
}

func TestVideoUploadResult_Path(t *testing.T) {
	r := &VideoUploadResult{
		ID:       "vid-456",
		category: "clips",
	}
	assert.Equal(t, "clips/vid-456/video.mp4", r.Path())
}

// ---------------------------------------------------------------------------
// Image store tests (with real local storage, requires ffmpeg)
// ---------------------------------------------------------------------------

func TestImageStore_Upload_FileTooLarge(t *testing.T) {
	store, err := storage.NewLocalStorage(t.TempDir())
	require.NoError(t, err)

	is, err := NewLocalImageStore(store, "/uploads", ImageStoreConfig{
		Category: "test",
		Sizes:    SizesAvatar,
		Quality:  85,
		Format:   FormatJPEG,
		MaxSize:  100, // tiny limit
	})
	require.NoError(t, err)

	data := testJPEG(t, 100, 100)
	fh := fakeMultipartFile(t, "big.jpg", "image/jpeg", data)

	_, err = is.Upload(context.Background(), fh)
	assert.ErrorIs(t, err, ErrFileTooLarge)
}

func TestImageStore_Upload_UnsupportedType(t *testing.T) {
	if !hasFFmpeg() {
		t.Skip("ffmpeg not available")
	}

	store, err := storage.NewLocalStorage(t.TempDir())
	require.NoError(t, err)

	is, err := NewLocalImageStore(store, "/uploads", ImageStoreConfig{
		Category: "test",
		Sizes:    SizesAvatar,
		Quality:  85,
		Format:   FormatJPEG,
		MaxSize:  10 * MB,
	})
	require.NoError(t, err)

	fh := fakeMultipartFile(t, "data.txt", "text/plain", []byte("hello world this is some text content"))

	_, err = is.Upload(context.Background(), fh)
	assert.ErrorIs(t, err, ErrUnknownType)
}

func TestImageStore_UploadAndDelete(t *testing.T) {
	if !hasFFmpeg() {
		t.Skip("ffmpeg not available")
	}

	store, err := storage.NewLocalStorage(t.TempDir())
	require.NoError(t, err)

	is, err := NewLocalImageStore(store, "/uploads", ImageStoreConfig{
		Category: "headshots",
		Sizes:    SizesAvatar,
		Quality:  85,
		Format:   FormatJPEG,
		MaxSize:  4 * MB,
	})
	require.NoError(t, err)

	data := testJPEG(t, 500, 500)
	fh := fakeMultipartFile(t, "photo.jpg", "image/jpeg", data)

	result, err := is.Upload(context.Background(), fh)
	require.NoError(t, err)
	assert.NotEmpty(t, result.ID)
	assert.Equal(t, TypeImage, result.MediaType)
	assert.Equal(t, "image/jpeg", result.MimeType)

	// Verify all size variants were saved.
	ctx := context.Background()
	for _, sz := range SizesAvatar {
		path := result.Path(sz.Name)
		exists, err := store.Exists(ctx, path)
		require.NoError(t, err)
		assert.True(t, exists, "size %q should exist at %s", sz.Name, path)
	}

	// Delete all variants.
	require.NoError(t, is.Delete(ctx, result.ID))

	for _, sz := range SizesAvatar {
		path := result.Path(sz.Name)
		exists, err := store.Exists(ctx, path)
		require.NoError(t, err)
		assert.False(t, exists, "size %q should be deleted", sz.Name)
	}
}

func TestImageStore_UploadFromReader(t *testing.T) {
	if !hasFFmpeg() {
		t.Skip("ffmpeg not available")
	}

	store, err := storage.NewLocalStorage(t.TempDir())
	require.NoError(t, err)

	is, err := NewLocalImageStore(store, "/uploads", ImageStoreConfig{
		Category: "headshots",
		Sizes:    SizeOriginal,
		Quality:  85,
		Format:   FormatJPEG,
		MaxSize:  4 * MB,
	})
	require.NoError(t, err)

	data := testJPEG(t, 200, 200)
	result, err := is.UploadFromReader(context.Background(), bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	assert.NotEmpty(t, result.ID)
	assert.Equal(t, TypeImage, result.MediaType)
}

func TestImageStore_GetMedia(t *testing.T) {
	store, err := storage.NewLocalStorage(t.TempDir())
	require.NoError(t, err)

	is, err := NewLocalImageStore(store, "/uploads", ImageStoreConfig{
		Category: "headshots",
		Sizes:    SizesCard,
		Quality:  85,
		Format:   FormatWebP,
		MaxSize:  4 * MB,
	})
	require.NoError(t, err)

	ref := is.GetMedia("test-id")
	assert.Equal(t, "/uploads/headshots/test-id/medium.webp", ref.Size("medium"))
	assert.Equal(t, "/uploads/headshots/test-id/thumb.webp", ref.Thumb())
	assert.Equal(t, "/uploads/headshots/test-id/thumb.webp", ref.Smallest())
	assert.Equal(t, "/uploads/headshots/test-id/xlarge.webp", ref.Biggest())
}

// ---------------------------------------------------------------------------
// MIME detection tests
// ---------------------------------------------------------------------------

func TestDetectMIME_JPEG(t *testing.T) {
	data := testJPEG(t, 10, 10)
	mime, buf, err := detectMIME(bytes.NewReader(data), 0)
	require.NoError(t, err)
	assert.Equal(t, "image/jpeg", mime)
	assert.Equal(t, data, buf)
}

func TestDetectMIME_PNG(t *testing.T) {
	data := testPNG(t, 10, 10)
	mime, buf, err := detectMIME(bytes.NewReader(data), 0)
	require.NoError(t, err)
	assert.Equal(t, "image/png", mime)
	assert.Equal(t, data, buf)
}

// ---------------------------------------------------------------------------
// JPEG quality scale tests
// ---------------------------------------------------------------------------

func TestJpegQScale(t *testing.T) {
	assert.Equal(t, 1, jpegQScale(95))
	assert.Equal(t, 1, jpegQScale(100))
	assert.Equal(t, 31, jpegQScale(10))
	assert.Equal(t, 31, jpegQScale(5))

	// Mid-range should be somewhere in between.
	mid := jpegQScale(50)
	assert.True(t, mid > 1 && mid < 31, "mid-range quality should be between 1 and 31, got %d", mid)
}

// ---------------------------------------------------------------------------
// formatExt tests
// ---------------------------------------------------------------------------

func TestFormatExt(t *testing.T) {
	assert.Equal(t, "webp", formatExt(FormatWebP))
	assert.Equal(t, "jpeg", formatExt(FormatJPEG))
	assert.Equal(t, "png", formatExt(FormatPNG))
	assert.Equal(t, "tiff", formatExt("tiff"))
}

// ---------------------------------------------------------------------------
// NewLocalImageStore validation tests
// ---------------------------------------------------------------------------

func TestNewLocalImageStore_InvalidConfig(t *testing.T) {
	store, err := storage.NewLocalStorage(t.TempDir())
	require.NoError(t, err)

	_, err = NewLocalImageStore(store, "/uploads", ImageStoreConfig{})
	assert.Error(t, err)
}

func TestNewLocalVideoStore_InvalidConfig(t *testing.T) {
	store, err := storage.NewLocalStorage(t.TempDir())
	require.NoError(t, err)

	_, err = NewLocalVideoStore(store, "/uploads", VideoStoreConfig{})
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// normalizeMIME tests
// ---------------------------------------------------------------------------

func TestNormalizeMIME(t *testing.T) {
	jpegData := testJPEG(t, 2, 2)
	mime := normalizeMIME(jpegData)
	assert.Equal(t, "image/jpeg", mime)

	pngData := testPNG(t, 2, 2)
	mime = normalizeMIME(pngData)
	assert.Equal(t, "image/png", mime)
}

// ---------------------------------------------------------------------------
// UploadFromReaderWithID tests — image
// ---------------------------------------------------------------------------

func TestImageStore_UploadFromReaderWithID_HappyPath(t *testing.T) {
	if !hasFFmpeg() {
		t.Skip("ffmpeg not available")
	}

	store, err := storage.NewLocalStorage(t.TempDir())
	require.NoError(t, err)
	is, err := NewLocalImageStore(store, "/uploads", ImageStoreConfig{
		Category: "headshots",
		Sizes:    SizeOriginal,
		Quality:  85,
		Format:   FormatJPEG,
		MaxSize:  4 * MB,
	})
	require.NoError(t, err)

	data := testJPEG(t, 200, 200)
	id := uuid.New().String()
	ctx := context.Background()
	storagePath := "headshots/" + id + "/original.jpeg"

	result, err := is.UploadFromReaderWithID(ctx, id, bytes.NewReader(data), int64(len(data)), false)
	require.NoError(t, err)
	// The supplied id MUST be the result id — the whole point of the
	// *WithID variant is that the caller's row + storage path agree.
	assert.Equal(t, id, result.ID)

	// Bytes are at the path the caller can predict from `id`. Read them
	// back for symmetry with the exists/overwrite tests — proves the
	// happy path actually wrote *something* rather than just succeeding
	// with empty output.
	exists, err := store.Exists(ctx, storagePath)
	require.NoError(t, err)
	assert.True(t, exists)
	bytesAfter := readAll(t, store, ctx, storagePath)
	assert.NotEmpty(t, bytesAfter, "happy-path upload must produce non-empty bytes")
}

func TestImageStore_UploadFromReaderWithID_InvalidID(t *testing.T) {
	store, err := storage.NewLocalStorage(t.TempDir())
	require.NoError(t, err)
	is, err := NewLocalImageStore(store, "/uploads", ImageStoreConfig{
		Category: "headshots",
		Sizes:    SizeOriginal,
		Quality:  85,
		Format:   FormatJPEG,
		MaxSize:  4 * MB,
	})
	require.NoError(t, err)

	// Strict canonical (36-char `8-4-4-4-12`) — anything else is rejected.
	cases := []struct {
		name string
		id   string
	}{
		{"empty", ""},
		{"whitespace-only", "                                    "}, // 36 spaces — len OK, parse fails
		{"path-traversal", "../etc/passwd"},
		{"slashed", "a/b"},
		{"control-char", "abc\x00def"},
		{"plain-string", "not-a-uuid"},
		{"truncated-uuid", "12345678-1234-1234-1234-1234567890"}, // 35 chars
		{"hyphenless-32", "12345678123412341234123456789012"},     // valid uuid.Parse but not canonical
		{"braced", "{12345678-1234-1234-1234-123456789012}"},      // accepted by uuid.Parse, rejected here
		{"urn-form", "urn:uuid:12345678-1234-1234-1234-123456789012"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := testJPEG(t, 8, 8)
			_, err := is.UploadFromReaderWithID(context.Background(), tc.id, bytes.NewReader(data), int64(len(data)), false)
			assert.ErrorIs(t, err, ErrInvalidID)
		})
	}
}

func TestImageStore_UploadFromReaderWithID_ExistsRejection(t *testing.T) {
	if !hasFFmpeg() {
		t.Skip("ffmpeg not available")
	}

	store, err := storage.NewLocalStorage(t.TempDir())
	require.NoError(t, err)
	is, err := NewLocalImageStore(store, "/uploads", ImageStoreConfig{
		Category: "headshots",
		Sizes:    SizeOriginal,
		Quality:  85,
		Format:   FormatJPEG,
		MaxSize:  4 * MB,
	})
	require.NoError(t, err)

	id := uuid.New().String()
	first := testJPEG(t, 200, 200)
	second := testJPEG(t, 50, 50) // distinguishably-sized — proves bytes if non-clobbered

	ctx := context.Background()
	storagePath := "headshots/" + id + "/original.jpeg"

	_, err = is.UploadFromReaderWithID(ctx, id, bytes.NewReader(first), int64(len(first)), false)
	require.NoError(t, err)
	bytesBefore := readAll(t, store, ctx, storagePath)

	// Second call with overwrite=false → rejected.
	_, err = is.UploadFromReaderWithID(ctx, id, bytes.NewReader(second), int64(len(second)), false)
	assert.ErrorIs(t, err, ErrIDExists)

	// Crucially: the original bytes must still be intact. Without this
	// read-back the test would pass even if the rejection happened AFTER
	// a clobber.
	bytesAfter := readAll(t, store, ctx, storagePath)
	assert.Equal(t, bytesBefore, bytesAfter, "rejected write must not have clobbered the original bytes")
}

func TestImageStore_UploadFromReaderWithID_OverwriteAllowed(t *testing.T) {
	if !hasFFmpeg() {
		t.Skip("ffmpeg not available")
	}

	store, err := storage.NewLocalStorage(t.TempDir())
	require.NoError(t, err)
	is, err := NewLocalImageStore(store, "/uploads", ImageStoreConfig{
		Category: "headshots",
		Sizes:    SizeOriginal,
		Quality:  85,
		Format:   FormatJPEG,
		MaxSize:  4 * MB,
	})
	require.NoError(t, err)

	id := uuid.New().String()
	first := testJPEG(t, 200, 200)
	second := testJPEG(t, 50, 50) // smaller — output will differ

	ctx := context.Background()
	storagePath := "headshots/" + id + "/original.jpeg"

	_, err = is.UploadFromReaderWithID(ctx, id, bytes.NewReader(first), int64(len(first)), false)
	require.NoError(t, err)
	bytesBefore := readAll(t, store, ctx, storagePath)

	// overwrite=true succeeds even though the prefix is populated.
	_, err = is.UploadFromReaderWithID(ctx, id, bytes.NewReader(second), int64(len(second)), true)
	require.NoError(t, err)

	// And the bytes actually changed — without this assertion the test
	// would pass even if overwrite=true were a no-op.
	bytesAfter := readAll(t, store, ctx, storagePath)
	assert.NotEqual(t, bytesBefore, bytesAfter, "overwrite=true must actually replace the bytes")
}

// readAll opens a storage path and returns the full contents. Test helper
// used by the *WithID exists/overwrite assertions.
func readAll(t *testing.T, store storage.FileStorage, ctx context.Context, path string) []byte {
	t.Helper()
	rc, err := store.Open(ctx, path)
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()
	out, err := io.ReadAll(rc)
	require.NoError(t, err)
	return out
}

// ---------------------------------------------------------------------------
// UploadFromReader / UploadFromReaderWithID tests — video
// ---------------------------------------------------------------------------

func TestVideoStore_UploadFromReader_FileTooLarge(t *testing.T) {
	store, err := storage.NewLocalStorage(t.TempDir())
	require.NoError(t, err)
	vs, err := NewLocalVideoStore(store, "/uploads", VideoStoreConfig{
		Category:    "intros",
		MaxSize:     1 * KB,
		MaxDuration: 60,
	})
	require.NoError(t, err)

	// A reader claiming a size larger than MaxSize is rejected before any
	// bytes are read — that's the cheap pre-check.
	_, err = vs.UploadFromReader(context.Background(), bytes.NewReader([]byte("x")), 2*KB)
	assert.ErrorIs(t, err, ErrFileTooLarge)
}

func TestVideoStore_UploadFromReaderWithID_InvalidID(t *testing.T) {
	store, err := storage.NewLocalStorage(t.TempDir())
	require.NoError(t, err)
	vs, err := NewLocalVideoStore(store, "/uploads", VideoStoreConfig{
		Category:    "intros",
		MaxSize:     1 * MB,
		MaxDuration: 60,
	})
	require.NoError(t, err)

	cases := []string{
		"",                                              // empty — rejected at WithID entry
		"../etc/passwd",                                 // path traversal
		"not-a-uuid",                                    // garbage
		"12345678123412341234123456789012",              // hyphenless 32-char
		"{12345678-1234-1234-1234-123456789012}",        // braced
		"urn:uuid:12345678-1234-1234-1234-123456789012", // urn
	}
	for _, id := range cases {
		_, err := vs.UploadFromReaderWithID(context.Background(), id, bytes.NewReader([]byte("x")), 1, false)
		assert.ErrorIs(t, err, ErrInvalidID, "id=%q", id)
	}
}

// generateTestMP4 is a thin wrapper over generateTestVideo for the
// existing call sites that only need a 128×128 MP4. Kept as a separate
// helper so the legacy tests don't churn.
func generateTestMP4(t *testing.T, duration int) []byte {
	t.Helper()
	return generateTestVideo(t, "mp4", duration, 128, 128)
}

// generateTestVideo produces a tiny valid clip in the requested container
// by shelling out to ffmpeg. Skips the test if ffmpeg isn't available.
//
// container is one of "mp4", "mov", "webm" (other extensions fall
// through to ffmpeg's container auto-detect from the file suffix). The
// codec is selected to match the container — libx264 for MP4/MOV,
// libvpx-vp9 for WebM — so the caller gets a representative real-world
// payload rather than the same H.264 stream relabelled.
//
// width/height are the source dimensions; libx264 requires them to be
// even, so callers must pass even numbers. The lavfi `testsrc` pattern
// gives a frame busy enough that thumbnail extraction succeeds across
// ffmpeg versions (a solid-color fill at <64×64 trips encoder
// thread-init quirks on some builds).
func generateTestVideo(t *testing.T, container string, duration, width, height int) []byte {
	t.Helper()
	if !hasFFmpeg() {
		t.Skip("ffmpeg not available")
	}

	tmp := filepath.Join(t.TempDir(), "test."+container)
	args := []string{
		"-y", "-loglevel", "error",
		"-f", "lavfi", "-i", fmt.Sprintf("testsrc=size=%dx%d:duration=%d:rate=25", width, height, duration),
		// silent stereo audio so the transcode tests can assert an AAC
		// audio stream came through; without it ffprobe reports no
		// audio stream and the codec assertion has nothing to bite on.
		"-f", "lavfi", "-i", "anullsrc=channel_layout=stereo:sample_rate=44100",
		"-shortest",
	}
	switch container {
	case "webm":
		args = append(args, "-c:v", "libvpx-vp9", "-c:a", "libopus", "-b:v", "200k")
	default:
		args = append(args, "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac")
	}
	args = append(args, tmp)

	cmd := exec.Command("ffmpeg", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg fixture generation failed (%s): %v\n%s", container, err, out)
	}
	data, err := os.ReadFile(tmp)
	require.NoError(t, err)
	return data
}

// ffprobeStream is the shape of one entry in `ffprobe -show_streams`'s
// JSON output. We only decode the fields the transcode tests assert on —
// any unrecognised JSON keys are ignored by encoding/json.
type ffprobeStream struct {
	CodecName string `json:"codec_name"`
	CodecType string `json:"codec_type"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	PixFmt    string `json:"pix_fmt"`
}

// mp4TopLevelBoxOrder returns the sequence of top-level box types in
// the supplied MP4 bytes, in the order they appear on disk. Used by the
// faststart test to assert `moov` lands before `mdat` (the whole point
// of `+faststart`). We stop after a small number of boxes to bound
// runtime — every MP4 we generate has the relevant boxes within the
// first few entries.
//
// MP4 box header is 4 bytes big-endian size + 4 bytes type. A size of
// 0 means "extends to end of file"; a size of 1 means "real size lives
// in the next 8 bytes (extended size)". We bail rather than handle
// those edge cases — neither shows up in libx264-encoded output at
// the small dimensions the test fixtures use.
func mp4TopLevelBoxOrder(t *testing.T, data []byte) []string {
	t.Helper()
	var boxes []string
	pos := 0
	for pos+8 <= len(data) && len(boxes) < 16 {
		size := int(binary.BigEndian.Uint32(data[pos : pos+4]))
		boxType := string(data[pos+4 : pos+8])
		boxes = append(boxes, boxType)
		if size < 8 {
			break
		}
		pos += size
	}
	return boxes
}

// ffprobeStreams runs ffprobe against `data` (piped to stdin) and
// returns the decoded streams. Used by the transcode tests to verify
// the output container actually carries h264/aac at the expected
// dimensions instead of trusting the file extension.
func ffprobeStreams(t *testing.T, data []byte) []ffprobeStream {
	t.Helper()
	cmd := exec.Command("ffprobe",
		"-v", "error",
		"-print_format", "json",
		"-show_streams",
		"pipe:0",
	)
	cmd.Stdin = bytes.NewReader(data)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Run(), "ffprobe failed: %s", stderr.String())

	var parsed struct {
		Streams []ffprobeStream `json:"streams"`
	}
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &parsed))
	return parsed.Streams
}

// findStream returns the first stream of the given codec_type
// ("video" or "audio"), or fails the test if none exists.
func findStream(t *testing.T, streams []ffprobeStream, codecType string) ffprobeStream {
	t.Helper()
	for _, s := range streams {
		if s.CodecType == codecType {
			return s
		}
	}
	t.Fatalf("no %s stream found in %d streams", codecType, len(streams))
	return ffprobeStream{}
}

func TestVideoStore_UploadFromReaderWithID_HappyPath(t *testing.T) {
	// duration ≥2s — thumbnail extraction seeks to the 1s mark and a
	// 1-second clip lands at end-of-stream, so the seek fails and the
	// thumbnail-success assertion below would be flaky.
	data := generateTestMP4(t, 2)

	store, err := storage.NewLocalStorage(t.TempDir())
	require.NoError(t, err)
	vs, err := NewLocalVideoStore(store, "/uploads", VideoStoreConfig{
		Category:          "intros",
		MaxSize:           10 * MB,
		MaxDuration:       60,
		GenerateThumbnail: true,
		ThumbnailWidth:    64,
	})
	require.NoError(t, err)

	id := uuid.New().String()
	result, err := vs.UploadFromReaderWithID(context.Background(), id, bytes.NewReader(data), int64(len(data)), false)
	require.NoError(t, err)

	// Caller-supplied id IS the result id — same contract as image.
	assert.Equal(t, id, result.ID)
	exists, err := store.Exists(context.Background(), "intros/"+id+"/video.mp4")
	require.NoError(t, err)
	assert.True(t, exists)
	// Thumbnail still generated unchanged in the WithID path. This
	// side-channel is the regression risk the test guards.
	assert.NotEmpty(t, result.ThumbnailPath, "thumbnail must still be generated when WithID is used")
	thumbExists, err := store.Exists(context.Background(), "intros/"+id+"/thumb.jpg")
	require.NoError(t, err)
	assert.True(t, thumbExists)
}

func TestVideoStore_UploadFromReaderWithID_ExistsRejection(t *testing.T) {
	// Two distinguishably-sized payloads — without this, the equality
	// assertion below would be vacuous (bytesBefore == bytesAfter
	// trivially when both writes carry identical bytes). Both ≥2s so
	// thumbnail extraction works in either case (see HappyPath note).
	first := generateTestMP4(t, 2)
	second := generateTestMP4(t, 3)
	require.NotEqual(t, first, second, "fixture sanity: 2s vs 3s clips must differ")

	store, err := storage.NewLocalStorage(t.TempDir())
	require.NoError(t, err)
	vs, err := NewLocalVideoStore(store, "/uploads", VideoStoreConfig{
		Category:    "intros",
		MaxSize:     10 * MB,
		MaxDuration: 60,
	})
	require.NoError(t, err)

	id := uuid.New().String()
	ctx := context.Background()
	storagePath := "intros/" + id + "/video.mp4"

	_, err = vs.UploadFromReaderWithID(ctx, id, bytes.NewReader(first), int64(len(first)), false)
	require.NoError(t, err)
	bytesBefore := readAll(t, store, ctx, storagePath)

	// Second call carries different bytes — if rejection happened AFTER
	// a clobber, the read-back below would diverge from bytesBefore.
	_, err = vs.UploadFromReaderWithID(ctx, id, bytes.NewReader(second), int64(len(second)), false)
	assert.ErrorIs(t, err, ErrIDExists)

	bytesAfter := readAll(t, store, ctx, storagePath)
	assert.Equal(t, bytesBefore, bytesAfter, "rejected video write must not have clobbered the original bytes")
}

// ---------------------------------------------------------------------------
// Transcode tests
// ---------------------------------------------------------------------------

// TestVideoStore_Transcode_HappyPath_WebM proves the round-trip works
// for a non-MP4 input container: a WebM upload (VP9 video + Opus audio)
// should land on disk as an H.264/AAC MP4. ffprobe parses the actual
// stream metadata rather than trusting the file extension.
//
// We don't have a sibling MOV-input test because Go's
// http.DetectContentType (used by detectMIME) doesn't recognise the
// `qt  ` major-brand QuickTime files that ffmpeg writes by default —
// it returns "application/octet-stream", and the upload is rejected
// before the transcode pass even runs. That's a pre-existing limitation
// of the MIME-sniff path, not a regression introduced here.
func TestVideoStore_Transcode_HappyPath_WebM(t *testing.T) {
	src := generateTestVideo(t, "webm", 2, 128, 128)

	store, err := storage.NewLocalStorage(t.TempDir())
	require.NoError(t, err)
	vs, err := NewLocalVideoStore(store, "/uploads", VideoStoreConfig{
		Category:    "intros",
		MaxSize:     10 * MB,
		MaxDuration: 60,
		// Bare-minimum transcode config — preset alone flips IsZero() to
		// false; validate() fills the rest.
		Transcode: VideoTranscodeOptions{Preset: "ultrafast"},
	})
	require.NoError(t, err)

	ctx := context.Background()
	id := uuid.New().String()
	result, err := vs.UploadFromReaderWithID(ctx, id, bytes.NewReader(src), int64(len(src)), false)
	require.NoError(t, err)

	stored := readAll(t, store, ctx, "intros/"+id+"/video.mp4")
	require.NotEmpty(t, stored)
	// FileSize must reflect what's actually on disk (post-transcode), not
	// the input size — the dev's view of "how much storage am I using"
	// is the on-disk number.
	assert.Equal(t, int64(len(stored)), result.FileSize)
	assert.NotEqual(t, src, stored, "transcoded bytes must differ from raw WebM input")
	// MimeType must reflect what's on disk: an MP4, not the source webm.
	// Callers persisting MimeType to a DB or plumbing it into Content-Type
	// when serving would otherwise mislabel the stored asset.
	assert.Equal(t, "video/mp4", result.MimeType)

	streams := ffprobeStreams(t, stored)
	video := findStream(t, streams, "video")
	assert.Equal(t, "h264", video.CodecName)
	// pix_fmt yuv420p is non-negotiable — Safari and many smart-TV
	// decoders refuse 4:2:2 H.264, which is what iPhone 4K footage
	// produces by default. A regression that drops the -pix_fmt pin
	// would still produce a parseable MP4 but break Safari silently;
	// this assertion is the canary.
	assert.Equal(t, "yuv420p", video.PixFmt)
	assert.Equal(t, "aac", findStream(t, streams, "audio").CodecName)
}

// TestVideoStore_NoTranscode_PreservesBytes pins the opt-in semantics
// of VideoTranscodeOptions: a zero-valued Transcode must NOT trigger
// any ffmpeg pass — bytes go to disk verbatim. This guards against a
// future refactor that accidentally always-on's the transcode branch.
func TestVideoStore_NoTranscode_PreservesBytes(t *testing.T) {
	src := generateTestVideo(t, "webm", 2, 128, 128)

	store, err := storage.NewLocalStorage(t.TempDir())
	require.NoError(t, err)
	vs, err := NewLocalVideoStore(store, "/uploads", VideoStoreConfig{
		Category:    "intros",
		MaxSize:     10 * MB,
		MaxDuration: 60,
		// No Transcode field — IsZero() is true, raw passthrough.
	})
	require.NoError(t, err)

	ctx := context.Background()
	id := uuid.New().String()
	result, err := vs.UploadFromReaderWithID(ctx, id, bytes.NewReader(src), int64(len(src)), false)
	require.NoError(t, err)

	stored := readAll(t, store, ctx, "intros/"+id+"/video.mp4")
	assert.Equal(t, src, stored, "raw passthrough must be byte-identical")
	assert.Equal(t, int64(len(src)), result.FileSize)
	// With Transcode off, MimeType reports the source MIME — the bytes
	// on disk really are the source. (Asserting against video/webm is
	// the load-bearing claim; the symmetric MimeType="video/mp4" check
	// for the transcode-on path lives in the WebM happy-path test.)
	assert.Equal(t, "video/webm", result.MimeType)
}

// TestVideoStore_Transcode_RespectsMaxHeight mirrors RespectsMaxWidth
// for the single-axis-height branch of buildScaleFilter. The two
// branches use different filter syntax, so a regression in the
// height-only branch would not be caught by the width-only test.
func TestVideoStore_Transcode_RespectsMaxHeight(t *testing.T) {
	src := generateTestVideo(t, "mp4", 2, 256, 256)

	store, err := storage.NewLocalStorage(t.TempDir())
	require.NoError(t, err)
	vs, err := NewLocalVideoStore(store, "/uploads", VideoStoreConfig{
		Category:    "intros",
		MaxSize:     10 * MB,
		MaxDuration: 60,
		Transcode: VideoTranscodeOptions{
			Preset:    "ultrafast",
			MaxHeight: 64,
		},
	})
	require.NoError(t, err)

	ctx := context.Background()
	id := uuid.New().String()
	_, err = vs.UploadFromReaderWithID(ctx, id, bytes.NewReader(src), int64(len(src)), false)
	require.NoError(t, err)

	stored := readAll(t, store, ctx, "intros/"+id+"/video.mp4")
	video := findStream(t, ffprobeStreams(t, stored), "video")
	assert.LessOrEqual(t, video.Height, 64, "MaxHeight=64 must clamp output height")
	assert.Greater(t, video.Height, 0)
	// Even-dim guard: libx264 rejects odd dimensions. The chained scale
	// filter normalises both axes; if a future refactor breaks that
	// chain, the encode would error before we got here, but this
	// assertion ensures the output dim is what libx264 accepted.
	assert.Equal(t, 0, video.Height%2, "output height must be divisible by 2")
	assert.Equal(t, 0, video.Width%2, "output width must be divisible by 2")
}

// TestVideoStore_Transcode_ThumbnailFromRawNotEncoded pins the ADR
// claim that thumbnails are extracted from the raw upload, not the
// transcoded output. The test uploads the same source twice — once
// with transcode off, once with transcode on at an aggressive preset
// that produces visibly different bytes — and asserts the thumbnail
// bytes are identical between the two runs. If a refactor swaps
// `raw` → `toSave` in the thumbnail call site, the transcode-on
// thumbnail would derive from the encoded MP4 and the bytes would
// diverge.
func TestVideoStore_Transcode_ThumbnailFromRawNotEncoded(t *testing.T) {
	src := generateTestVideo(t, "mp4", 2, 128, 128)
	ctx := context.Background()

	run := func(t *testing.T, transcode VideoTranscodeOptions) []byte {
		t.Helper()
		store, err := storage.NewLocalStorage(t.TempDir())
		require.NoError(t, err)
		vs, err := NewLocalVideoStore(store, "/uploads", VideoStoreConfig{
			Category:          "intros",
			MaxSize:           10 * MB,
			MaxDuration:       60,
			GenerateThumbnail: true,
			ThumbnailWidth:    64,
			Transcode:         transcode,
		})
		require.NoError(t, err)
		id := uuid.New().String()
		result, err := vs.UploadFromReaderWithID(ctx, id, bytes.NewReader(src), int64(len(src)), false)
		require.NoError(t, err)
		require.NotEmpty(t, result.ThumbnailPath)
		return readAll(t, store, ctx, result.ThumbnailPath)
	}

	rawThumb := run(t, VideoTranscodeOptions{})
	encodedThumb := run(t, VideoTranscodeOptions{Preset: "ultrafast", CRF: 40})

	assert.Equal(t, rawThumb, encodedThumb,
		"thumbnail bytes must be identical regardless of transcode — proves extraction runs against raw upload, not encoded output")
}

// TestVideoStore_Transcode_FaststartMoovBeforeMdat actually verifies
// faststart by walking the top-level MP4 box order and asserting the
// `moov` atom appears before `mdat`. The previous version of this test
// only proved ffprobe could parse the output, which a stdout-pipe
// regression would still satisfy (a non-faststart MP4 with moov at the
// end parses fine) — that's exactly the regression this canary needs
// to catch.
func TestVideoStore_Transcode_FaststartMoovBeforeMdat(t *testing.T) {
	src := generateTestVideo(t, "mp4", 2, 128, 128)

	store, err := storage.NewLocalStorage(t.TempDir())
	require.NoError(t, err)
	vs, err := NewLocalVideoStore(store, "/uploads", VideoStoreConfig{
		Category:    "intros",
		MaxSize:     10 * MB,
		MaxDuration: 60,
		Transcode:   VideoTranscodeOptions{Preset: "ultrafast"},
	})
	require.NoError(t, err)

	ctx := context.Background()
	id := uuid.New().String()
	_, err = vs.UploadFromReaderWithID(ctx, id, bytes.NewReader(src), int64(len(src)), false)
	require.NoError(t, err)

	stored := readAll(t, store, ctx, "intros/"+id+"/video.mp4")
	boxes := mp4TopLevelBoxOrder(t, stored)
	moovIdx := slices.Index(boxes, "moov")
	mdatIdx := slices.Index(boxes, "mdat")
	require.GreaterOrEqual(t, moovIdx, 0, "moov box must exist; got boxes=%v", boxes)
	require.GreaterOrEqual(t, mdatIdx, 0, "mdat box must exist; got boxes=%v", boxes)
	assert.Less(t, moovIdx, mdatIdx,
		"moov must appear before mdat for +faststart progressive playback; got boxes=%v", boxes)
}

// TestVideoStoreConfigValidation_FillsDefaults_VariousActivators covers
// the activator-permutation gap in the original defaults-fill test.
// Each subtest sets a DIFFERENT single field on Transcode (the
// "activator") and asserts every other field gets its documented
// default. A regression where defaults only get filled when Preset is
// the activator (e.g. someone refactors the IsZero check to inspect
// Preset specifically) would only be caught by exercising every
// activator.
func TestVideoStoreConfigValidation_FillsDefaults_VariousActivators(t *testing.T) {
	cases := []struct {
		name string
		in   VideoTranscodeOptions
	}{
		{"CRF only", VideoTranscodeOptions{CRF: 30}},
		{"AudioBitrate only", VideoTranscodeOptions{AudioBitrate: "192k"}},
		{"MaxWidth only", VideoTranscodeOptions{MaxWidth: 1920}},
		{"MaxHeight only", VideoTranscodeOptions{MaxHeight: 1080}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := VideoStoreConfig{
				Category: "clips", MaxSize: MB, MaxDuration: 60,
				Transcode: tc.in,
			}
			require.NoError(t, cfg.validate())
			// Preset always gets defaulted unless the caller set it.
			if tc.in.Preset == "" {
				assert.Equal(t, "medium", cfg.Transcode.Preset)
			}
			// CRF defaulted unless caller set it.
			if tc.in.CRF == 0 {
				assert.Equal(t, 23, cfg.Transcode.CRF)
			}
			// AudioBitrate defaulted unless caller set it.
			if tc.in.AudioBitrate == "" {
				assert.Equal(t, "128k", cfg.Transcode.AudioBitrate)
			}
		})
	}
}

// TestVideoStore_Transcode_RespectsMaxWidth proves the MaxWidth clamp
// reaches the encoded output. We feed in a 256-wide source and assert
// the stored MP4 reports width ≤ 64. Height is left unconstrained so
// the aspect-ratio-preserving scale filter has room to do its job.
func TestVideoStore_Transcode_RespectsMaxWidth(t *testing.T) {
	src := generateTestVideo(t, "mp4", 2, 256, 256)

	store, err := storage.NewLocalStorage(t.TempDir())
	require.NoError(t, err)
	vs, err := NewLocalVideoStore(store, "/uploads", VideoStoreConfig{
		Category:    "intros",
		MaxSize:     10 * MB,
		MaxDuration: 60,
		Transcode: VideoTranscodeOptions{
			Preset:   "ultrafast",
			MaxWidth: 64,
		},
	})
	require.NoError(t, err)

	ctx := context.Background()
	id := uuid.New().String()
	_, err = vs.UploadFromReaderWithID(ctx, id, bytes.NewReader(src), int64(len(src)), false)
	require.NoError(t, err)

	stored := readAll(t, store, ctx, "intros/"+id+"/video.mp4")
	video := findStream(t, ffprobeStreams(t, stored), "video")
	assert.LessOrEqual(t, video.Width, 64, "MaxWidth=64 must clamp output width")
	assert.Greater(t, video.Width, 0)
}

// TestProcessImage_PreservesSizeOrder locks the invariant that the
// existence-precheck and cleanup-on-failure paths in ImageStore.upload
// rely on: processImage must return its results in the same order as
// the input `sizes` slice. The probe uses `s.config.Sizes[0]` to find
// the first variant on disk, and cleanupPartial walks the same order
// the save loop wrote in. A future refactor that reorders silently
// would break both paths — this test is the cheap guard against that.
func TestProcessImage_PreservesSizeOrder(t *testing.T) {
	if !hasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	raw := testJPEG(t, 64, 64)
	processed, err := processImage(context.Background(), raw, SizesAvatar, FormatJPEG, 85)
	require.NoError(t, err)
	require.Len(t, processed, len(SizesAvatar), "one result per input size")
	for i, p := range processed {
		assert.Equal(t, SizesAvatar[i].Name, p.size.Name, "variant %d order mismatch", i)
	}
}

// Ensure unused import suppression.
var _ = io.Discard
