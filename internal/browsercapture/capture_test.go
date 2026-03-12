package browsercapture

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrepareOptionsNormalizesURLAndOutputPath(t *testing.T) {
	out := filepath.Join(t.TempDir(), "capture")

	opts, err := PrepareOptions(Options{
		URL:        "localhost:3000/login",
		OutputPath: out,
		Headless:   true,
		NoSandbox:  true,
	})
	require.NoError(t, err)

	assert.Equal(t, "http://localhost:3000/login", opts.URL)
	assert.Equal(t, out+".png", opts.OutputPath)
	assert.Equal(t, defaultWidth, opts.Width)
	assert.Equal(t, defaultHeight, opts.Height)
	assert.Equal(t, defaultScale, opts.Scale)
	assert.Equal(t, defaultTimeout, opts.Timeout)
	assert.Equal(t, defaultWaitAfterLoad, opts.WaitAfterLoad)
}

func TestPrepareOptionsUsesBundleDirectoryOutput(t *testing.T) {
	dir := t.TempDir()

	opts, err := PrepareOptions(Options{
		URL:       "https://example.com/settings/profile",
		OutputDir: dir,
		Headless:  true,
		NoSandbox: true,
	})
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(opts.OutputPath, dir+string(filepath.Separator)))
	assert.Equal(t, defaultScreenshotName, filepath.Base(opts.OutputPath))
	assert.Contains(t, filepath.Base(filepath.Dir(opts.OutputPath)), "example.com_settings_profile_")
}

func TestPrepareOptionsRejectsNonPNGOutput(t *testing.T) {
	_, err := PrepareOptions(Options{
		URL:        "https://example.com",
		OutputPath: filepath.Join(t.TempDir(), "capture.jpg"),
		Headless:   true,
		NoSandbox:  true,
	})

	require.Error(t, err)
	assert.ErrorContains(t, err, ".png extension")
}

func TestPrepareOptionsRejectsInvalidDimensions(t *testing.T) {
	_, err := PrepareOptions(Options{
		URL:       "https://example.com",
		Width:     -1,
		Headless:  true,
		NoSandbox: true,
	})

	require.Error(t, err)
	assert.ErrorContains(t, err, "width must be greater than 0")
}

func TestPrepareOptionsRejectsOutAndDirTogether(t *testing.T) {
	_, err := PrepareOptions(Options{
		URL:        "https://example.com",
		OutputPath: filepath.Join(t.TempDir(), "capture.png"),
		OutputDir:  t.TempDir(),
		Headless:   true,
		NoSandbox:  true,
	})

	require.Error(t, err)
	assert.ErrorContains(t, err, "only one of output path or output dir")
}

func TestPrepareOptionsRejectsScrollWithFullPage(t *testing.T) {
	_, err := PrepareOptions(Options{
		URL:       "https://example.com",
		FullPage:  true,
		ScrollY:   400,
		Headless:  true,
		NoSandbox: true,
	})

	require.Error(t, err)
	assert.ErrorContains(t, err, "scroll options cannot be used with full-page")
}

func TestPrepareOptionsRejectsScrollPresetAndPixelsTogether(t *testing.T) {
	_, err := PrepareOptions(Options{
		URL:       "https://example.com",
		ScrollTo:  "middle",
		ScrollY:   400,
		Headless:  true,
		NoSandbox: true,
	})

	require.Error(t, err)
	assert.ErrorContains(t, err, "scroll-to cannot be combined")
}

func TestPrepareOptionsRejectsScrollSelectorWithoutPosition(t *testing.T) {
	_, err := PrepareOptions(Options{
		URL:            "https://example.com",
		ScrollSelector: ".results",
		Headless:       true,
		NoSandbox:      true,
	})

	require.Error(t, err)
	assert.ErrorContains(t, err, "scroll-selector requires")
}

func TestNormalizeURL(t *testing.T) {
	assert.Equal(t, "http://example.com/app", normalizeURL("example.com/app"))
	assert.Equal(t, "https://example.com/app", normalizeURL("https://example.com/app"))
	assert.Equal(t, "http://example.com/app", normalizeURL("//example.com/app"))
	assert.Equal(t, "file:///tmp/example.html", normalizeURL("file:///tmp/example.html"))
}

func TestDefaultCaptureBaseName(t *testing.T) {
	now := time.Date(2026, time.March, 11, 12, 34, 56, 0, time.UTC)

	name := defaultCaptureBaseName("https://example.com/team/42", now)

	assert.Equal(t, "example.com_team_42_20260311T123456Z", name)
}

func TestSidecarPath(t *testing.T) {
	assert.Equal(t, "/tmp/capture.html", sidecarPath("/tmp/capture.png", ".html"))
	assert.Equal(t, "/tmp/capture.txt", sidecarPath("/tmp/capture.png", ".txt"))
}

func TestMetadataPath(t *testing.T) {
	assert.Equal(t, "/tmp/run/meta.json", metadataPath("/tmp/run/screenshot.png"))
	assert.Equal(t, "/tmp/run/custom.meta.json", metadataPath("/tmp/run/custom.png"))
}

func TestNormalizeOutputPathDefaultsToCaptureRoot(t *testing.T) {
	now := time.Date(2026, time.March, 11, 12, 34, 56, 0, time.UTC)

	path, err := normalizeOutputPath("", "", "https://example.com/team/42", now)
	require.NoError(t, err)

	assert.True(t, strings.HasSuffix(path, filepath.Join(".hamr", "ai", "captures", "example.com_team_42_20260311T123456Z", defaultScreenshotName)))
}
