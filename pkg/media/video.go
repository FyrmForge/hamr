package media

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	// ffmpeg can exit 0 with no output when the seek point (-ss 1) is past the
	// end of a sub-second clip. Treat empty output as a failure so the caller
	// doesn't save a zero-byte thumbnail and report it as success.
	if stdout.Len() == 0 {
		return nil, fmt.Errorf("ffmpeg thumbnail: produced no output (clip shorter than the 1s seek point?)")
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

// transcodeVideoToMP4 re-encodes the input bytes to an H.264/AAC MP4
// container with the moov atom at the front (+faststart) so the result
// can be progressively streamed by browsers.
//
// The output is written to a temp file rather than stdout because
// +faststart requires a seekable destination to relocate the moov atom
// after writing — ffmpeg cannot do that on a non-seekable stdout pipe.
// The temp file is removed before this function returns.
func transcodeVideoToMP4(ctx context.Context, data []byte, opts VideoTranscodeOptions) ([]byte, error) {
	if err := checkFFmpeg(); err != nil {
		return nil, err
	}

	tmpDir, err := os.MkdirTemp("", "media-transcode-*")
	if err != nil {
		return nil, fmt.Errorf("media: transcode tempdir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()
	outPath := filepath.Join(tmpDir, "out.mp4")

	args := []string{
		"-loglevel", "error",
		"-i", "pipe:0",
		"-c:v", "libx264",
		"-preset", opts.Preset,
		"-crf", strconv.Itoa(opts.CRF),
		"-pix_fmt", "yuv420p",
		"-profile:v", "high",
	}
	if vf := buildScaleFilter(opts.MaxWidth, opts.MaxHeight); vf != "" {
		args = append(args, "-vf", vf)
	}
	args = append(args,
		"-c:a", "aac",
		"-b:a", opts.AudioBitrate,
		"-movflags", "+faststart",
		"-f", "mp4",
		"-y",
		outPath,
	)

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Stdin = bytes.NewReader(data)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg transcode: %w: %s", err, stderr.String())
	}

	// NOTE: the input `data`, the ffmpeg output file on disk, and this in-memory
	// `out` can all be resident at once — a memory amplification on top of the
	// input. Inputs are bounded upstream by MaxSize (io.LimitReader); the output
	// is not re-checked. Acceptable at the self-host/SMB scale this targets;
	// flagged for awareness if very large transcodes become a use case.
	out, err := os.ReadFile(outPath)
	if err != nil {
		return nil, fmt.Errorf("media: read transcoded output: %w", err)
	}
	return out, nil
}

// buildScaleFilter produces the -vf argument that clamps output
// dimensions while preserving aspect ratio and never upscales the
// source. Empty string means "do not pass -vf" (preserve source dims).
//
// libx264 requires even dimensions on both axes. We enforce that with a
// two-pass scale chain: the first scale applies the user's clamp (with
// `-2` for any unconstrained axis to maintain aspect), the second scale
// truncates both axes to even via `trunc(iw/2)*2`/`trunc(ih/2)*2`. The
// chained form works on every ffmpeg version that has the scale filter
// (i.e. all of them) — the alternative `force_divisible_by=2` argument
// only landed in libavfilter ~ffmpeg 4.4 and would silently fail on
// older distros (Debian buster, RHEL 8 base, Ubuntu 18.04 without
// backports).
//
// The second pass is also what fixes the single-axis-with-odd-source
// case: e.g. `MaxWidth=1920` against a 1281×720 source would otherwise
// pass through the odd width unchanged and trip libx264's "width not
// divisible by 2" reject.
func buildScaleFilter(maxW, maxH int) string {
	const evenize = ",scale=w='trunc(iw/2)*2':h='trunc(ih/2)*2'"
	switch {
	case maxW > 0 && maxH > 0:
		return fmt.Sprintf(
			"scale=w='min(iw,%d)':h='min(ih,%d)':force_original_aspect_ratio=decrease",
			maxW, maxH,
		) + evenize
	case maxW > 0:
		return fmt.Sprintf("scale=w='min(iw,%d)':h=-2", maxW) + evenize
	case maxH > 0:
		return fmt.Sprintf("scale=w=-2:h='min(ih,%d)'", maxH) + evenize
	default:
		return ""
	}
}
