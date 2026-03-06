package media

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
)

// processedImage holds the resized/converted image data for one size variant.
type processedImage struct {
	size ImageSize
	data []byte
}

// processImage reads raw image bytes and produces all configured size variants
// using ffmpeg. For "original" sizes (width/height == 0) the image is only
// format-converted without resizing.
func processImage(ctx context.Context, raw []byte, sizes []ImageSize, format string, quality int) ([]processedImage, error) {
	if err := checkFFmpeg(); err != nil {
		return nil, err
	}

	results := make([]processedImage, 0, len(sizes))
	for _, sz := range sizes {
		data, err := ffmpegConvert(ctx, raw, sz, format, quality)
		if err != nil {
			return nil, fmt.Errorf("media: process size %q: %w", sz.Name, err)
		}
		results = append(results, processedImage{size: sz, data: data})
	}
	return results, nil
}

// ffmpegConvert runs a single ffmpeg conversion from raw bytes to the target
// size and format. Input is piped via stdin, output read from stdout.
func ffmpegConvert(ctx context.Context, raw []byte, sz ImageSize, format string, quality int) ([]byte, error) {
	args := []string{
		"-i", "pipe:0",
		"-f", "image2",
	}

	// Resize filter — skip for "original" (0x0).
	if sz.Width > 0 && sz.Height > 0 {
		// Scale to fit within dimensions, maintaining aspect ratio.
		vf := fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease", sz.Width, sz.Height)
		args = append(args, "-vf", vf)
	}

	// Output codec / format options.
	switch format {
	case FormatWebP:
		args = append(args, "-c:v", "libwebp", "-quality", strconv.Itoa(quality))
	case FormatJPEG:
		args = append(args, "-c:v", "mjpeg", "-q:v", strconv.Itoa(jpegQScale(quality)))
	case FormatPNG:
		args = append(args, "-c:v", "png")
	}

	args = append(args, "-frames:v", "1", "pipe:1")

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Stdin = bytes.NewReader(raw)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg: %w: %s", err, stderr.String())
	}
	return stdout.Bytes(), nil
}

// jpegQScale converts a 1-100 quality value to ffmpeg's mjpeg q:v scale (1-31,
// lower is better).
func jpegQScale(quality int) int {
	if quality >= 95 {
		return 1
	}
	if quality <= 10 {
		return 31
	}
	// Linear interpolation: quality 95→1, quality 10→31
	return 31 - (quality-10)*30/85
}

// checkFFmpeg verifies that ffmpeg is available in PATH.
func checkFFmpeg() error {
	_, err := exec.LookPath("ffmpeg")
	if err != nil {
		return ErrFFmpegNotFound
	}
	return nil
}

// detectMIME reads the first 512 bytes from r to detect the MIME type and
// returns the full contents (header + rest) so the reader can still be used.
func detectMIME(r io.Reader) (mimeType string, buf []byte, err error) {
	header := make([]byte, 512)
	n, err := io.ReadAtLeast(r, header, 1)
	if err != nil {
		return "", nil, fmt.Errorf("media: read header: %w", err)
	}
	header = header[:n]

	rest, err := io.ReadAll(r)
	if err != nil {
		return "", nil, fmt.Errorf("media: read body: %w", err)
	}

	full := make([]byte, len(header)+len(rest))
	copy(full, header)
	copy(full[len(header):], rest)

	ct := normalizeMIME(header)
	return ct, full, nil
}

// normalizeMIME uses http.DetectContentType and strips any parameters
// (e.g. charset) to return a clean MIME type string.
func normalizeMIME(data []byte) string {
	raw := http.DetectContentType(data)
	ct, _, _ := strings.Cut(raw, ";")
	return strings.TrimSpace(ct)
}
