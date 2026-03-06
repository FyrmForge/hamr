package media

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"net/textproto"
	"os/exec"
	"strings"
	"testing"

	"github.com/FyrmForge/hamr/pkg/storage"
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
	mime, buf, err := detectMIME(bytes.NewReader(data))
	require.NoError(t, err)
	assert.Equal(t, "image/jpeg", mime)
	assert.Equal(t, data, buf)
}

func TestDetectMIME_PNG(t *testing.T) {
	data := testPNG(t, 10, 10)
	mime, buf, err := detectMIME(bytes.NewReader(data))
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

// Ensure unused import suppression.
var _ = io.Discard
