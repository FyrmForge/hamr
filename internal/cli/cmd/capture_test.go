package cmd

import (
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newCaptureTestCmd() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Flags().String("out", "", "output PNG path")
	cmd.Flags().String("dir", "", "output directory")
	cmd.Flags().String("browser", "", "browser binary")
	cmd.Flags().Bool("text", false, "save visible text")
	cmd.Flags().Bool("html", false, "save HTML")
	cmd.Flags().Bool("json", false, "print json")
	cmd.Flags().String("selector", "", "capture selector")
	cmd.Flags().Bool("full-page", false, "full page")
	cmd.Flags().Bool("headless", true, "headless")
	cmd.Flags().Bool("no-sandbox", true, "no sandbox")
	cmd.Flags().Int("width", 1440, "width")
	cmd.Flags().Int("height", 1024, "height")
	cmd.Flags().Float64("scale", 1, "scale")
	cmd.Flags().String("scroll-to", "", "scroll preset")
	cmd.Flags().Int("scroll-x", 0, "scroll x")
	cmd.Flags().Int("scroll-y", 0, "scroll y")
	cmd.Flags().String("scroll-selector", "", "scroll selector")
	cmd.Flags().Bool("tiles", false, "tiles")
	cmd.Flags().Int("tile-overlap", 0, "tile overlap")
	cmd.Flags().Duration("timeout", 15*time.Second, "timeout")
	cmd.Flags().Duration("wait", time.Second, "wait")
	return cmd
}

func TestCaptureOptionsFromFlags(t *testing.T) {
	cmd := newCaptureTestCmd()
	require.NoError(t, cmd.Flags().Set("out", "artifacts/page"))
	require.NoError(t, cmd.Flags().Set("browser", "/usr/bin/chromium"))
	require.NoError(t, cmd.Flags().Set("text", "true"))
	require.NoError(t, cmd.Flags().Set("html", "true"))
	require.NoError(t, cmd.Flags().Set("json", "true"))
	require.NoError(t, cmd.Flags().Set("selector", "#app"))
	require.NoError(t, cmd.Flags().Set("full-page", "true"))
	require.NoError(t, cmd.Flags().Set("headless", "false"))
	require.NoError(t, cmd.Flags().Set("no-sandbox", "false"))
	require.NoError(t, cmd.Flags().Set("width", "1280"))
	require.NoError(t, cmd.Flags().Set("height", "720"))
	require.NoError(t, cmd.Flags().Set("scale", "2"))
	require.NoError(t, cmd.Flags().Set("scroll-x", "25"))
	require.NoError(t, cmd.Flags().Set("scroll-y", "400"))
	require.NoError(t, cmd.Flags().Set("scroll-selector", ".results"))
	require.NoError(t, cmd.Flags().Set("timeout", "30s"))
	require.NoError(t, cmd.Flags().Set("wait", "250ms"))

	opts, jsonOutput, err := captureOptionsFromFlags(cmd, "localhost:3000")
	require.NoError(t, err)

	assert.True(t, jsonOutput)
	assert.Equal(t, "localhost:3000", opts.URL)
	assert.Equal(t, "/usr/bin/chromium", opts.BrowserPath)
	assert.Equal(t, "artifacts/page", opts.OutputPath)
	assert.True(t, opts.CaptureText)
	assert.True(t, opts.CaptureHTML)
	assert.Equal(t, "#app", opts.Selector)
	assert.True(t, opts.FullPage)
	assert.False(t, opts.Headless)
	assert.False(t, opts.NoSandbox)
	assert.Equal(t, 1280, opts.Width)
	assert.Equal(t, 720, opts.Height)
	assert.Equal(t, 2.0, opts.Scale)
	assert.Equal(t, 25, opts.ScrollX)
	assert.Equal(t, 400, opts.ScrollY)
	assert.Equal(t, ".results", opts.ScrollSelector)
	assert.Equal(t, 30*time.Second, opts.Timeout)
	assert.Equal(t, 250*time.Millisecond, opts.WaitAfterLoad)
}

func TestCaptureOptionsFromFlagsWithOutputDirAndScrollPreset(t *testing.T) {
	cmd := newCaptureTestCmd()
	require.NoError(t, cmd.Flags().Set("dir", ".hamr/captures"))
	require.NoError(t, cmd.Flags().Set("scroll-to", "bottom"))

	opts, jsonOutput, err := captureOptionsFromFlags(cmd, "localhost:3000")
	require.NoError(t, err)

	assert.False(t, jsonOutput)
	assert.Equal(t, ".hamr/captures", opts.OutputDir)
	assert.Equal(t, "bottom", opts.ScrollTo)
}

func TestCaptureOptionsFromFlagsWithTiles(t *testing.T) {
	cmd := newCaptureTestCmd()
	require.NoError(t, cmd.Flags().Set("tiles", "true"))
	require.NoError(t, cmd.Flags().Set("tile-overlap", "200"))

	opts, _, err := captureOptionsFromFlags(cmd, "localhost:3000")
	require.NoError(t, err)

	assert.True(t, opts.Tiles)
	assert.Equal(t, 200, opts.TileOverlap)
}
