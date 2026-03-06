package media

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// probeVideoDuration returns the duration in seconds of the video data provided
// via stdin using ffprobe.
func probeVideoDuration(ctx context.Context, data []byte) (float64, error) {
	if err := checkFFprobe(); err != nil {
		return 0, err
	}

	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		"pipe:0",
	)
	cmd.Stdin = bytes.NewReader(data)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("ffprobe: %w: %s", err, stderr.String())
	}

	dur, err := strconv.ParseFloat(strings.TrimSpace(stdout.String()), 64)
	if err != nil {
		return 0, fmt.Errorf("media: parse duration %q: %w", stdout.String(), err)
	}
	return dur, nil
}

// generateThumbnail extracts a JPEG thumbnail from the video data at the 1-second
// mark (or the first frame if shorter) with the given width. Height is auto-scaled.
func generateThumbnail(ctx context.Context, data []byte, width int) ([]byte, error) {
	if err := checkFFmpeg(); err != nil {
		return nil, err
	}

	args := []string{
		"-i", "pipe:0",
		"-ss", "1",
		"-frames:v", "1",
		"-vf", fmt.Sprintf("scale=%d:-1", width),
		"-f", "image2",
		"-c:v", "mjpeg",
		"-q:v", "3",
		"pipe:1",
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Stdin = bytes.NewReader(data)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg thumbnail: %w: %s", err, stderr.String())
	}
	return stdout.Bytes(), nil
}

// checkFFprobe verifies that ffprobe is available in PATH.
func checkFFprobe() error {
	_, err := exec.LookPath("ffprobe")
	if err != nil {
		return ErrFFmpegNotFound
	}
	return nil
}
