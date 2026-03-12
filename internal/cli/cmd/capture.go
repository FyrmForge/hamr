package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/FyrmForge/hamr/internal/browsercapture"
	"github.com/FyrmForge/hamr/internal/scaffold"
	"github.com/spf13/cobra"
)

var captureCmd = &cobra.Command{
	Use:   "capture <url>",
	Short: "Capture a browser screenshot of a page for debugging or LLM use",
	Long: `Launch a browser, open the target URL, and save a PNG screenshot.

Optional sidecars can also be written:
  - visible page text (.txt) for LLM ingestion
  - page HTML (.html) for DOM inspection

Examples:
  hamr ai capture http://localhost:3000
  hamr ai capture localhost:3000/login --text --html --json
  hamr ai capture https://example.com --selector '#app' --dir .hamr/captures
  hamr ai capture https://example.com/pricing --scroll-to middle --width 1280 --height 720
  hamr ai capture https://example.com/dashboard --scroll-selector '.results-pane' --scroll-to bottom`,
	Args: cobra.ExactArgs(1),
	RunE: runCapture,
}

func init() {
	captureCmd.Flags().String("out", "", "output PNG path; if omitted a per-capture folder is created under .hamr/ai/captures")
	captureCmd.Flags().String("dir", "", "root directory for per-capture folders (defaults to .hamr/ai/captures)")
	captureCmd.Flags().String("browser", "", "browser binary path to launch (auto-detects Chromium/Chrome/Brave by default)")
	captureCmd.Flags().Bool("text", false, "also save visible page text alongside the screenshot")
	captureCmd.Flags().Bool("html", false, "also save page HTML alongside the screenshot")
	captureCmd.Flags().Bool("json", false, "print capture metadata as JSON")
	captureCmd.Flags().String("selector", "", "capture only the first element matching this CSS selector")
	captureCmd.Flags().Bool("full-page", false, "capture the full scrollable page instead of only the viewport")
	captureCmd.Flags().Bool("headless", true, "run the browser in headless mode")
	captureCmd.Flags().Bool("no-sandbox", true, "launch Chromium with --no-sandbox")
	captureCmd.Flags().Int("width", 1440, "viewport width in pixels")
	captureCmd.Flags().Int("height", 1024, "viewport height in pixels")
	captureCmd.Flags().Float64("scale", 1, "device scale factor for the browser viewport")
	captureCmd.Flags().String("scroll-to", "", "scroll the page before capture: top, middle, or bottom")
	captureCmd.Flags().Int("scroll-x", 0, "horizontal scroll offset in pixels before capture")
	captureCmd.Flags().Int("scroll-y", 0, "vertical scroll offset in pixels before capture")
	captureCmd.Flags().String("scroll-selector", "", "CSS selector for a scroll container; defaults to the window")
	captureCmd.Flags().Duration("timeout", 15*time.Second, "timeout for page operations")
	captureCmd.Flags().Duration("wait", time.Second, "extra delay after page load before capture")
}

func runCapture(cmd *cobra.Command, args []string) error {
	opts, jsonOutput, err := captureOptionsFromFlags(cmd, args[0])
	if err != nil {
		return err
	}

	result, err := browsercapture.Capture(opts)
	if err != nil {
		return err
	}

	if jsonOutput {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	out := cmd.OutOrStdout()
	if err := writeCaptureLine(out, "capture saved to %s\n", result.CaptureDir); err != nil {
		return err
	}
	if err := writeCaptureLine(out, "screenshot saved to %s\n", result.ScreenshotPath); err != nil {
		return err
	}
	if result.TextPath != "" {
		if err := writeCaptureLine(out, "text saved to %s\n", result.TextPath); err != nil {
			return err
		}
	}
	if result.HTMLPath != "" {
		if err := writeCaptureLine(out, "html saved to %s\n", result.HTMLPath); err != nil {
			return err
		}
	}
	if result.MetaPath != "" {
		if err := writeCaptureLine(out, "meta saved to %s\n", result.MetaPath); err != nil {
			return err
		}
	}
	if result.Title != "" {
		if err := writeCaptureLine(out, "title: %s\n", result.Title); err != nil {
			return err
		}
	}
	if result.FinalURL != "" && result.FinalURL != result.RequestedURL {
		if err := writeCaptureLine(out, "final url: %s\n", result.FinalURL); err != nil {
			return err
		}
	}
	if result.ScrollX != 0 || result.ScrollY != 0 {
		if err := writeCaptureLine(out, "scroll position: x=%d y=%d\n", result.ScrollX, result.ScrollY); err != nil {
			return err
		}
	}
	if result.ScrollSelector != "" {
		if err := writeCaptureLine(out, "scroll target: %s\n", result.ScrollSelector); err != nil {
			return err
		}
	}

	return nil
}

func writeCaptureLine(w io.Writer, format string, args ...any) error {
	_, err := fmt.Fprintf(w, format, args...)
	return err
}

func captureOptionsFromFlags(cmd *cobra.Command, rawURL string) (browsercapture.Options, bool, error) {
	outPath, _ := cmd.Flags().GetString("out")
	outDir, _ := cmd.Flags().GetString("dir")
	if outPath == "" && outDir == "" {
		aiDir := scaffold.ResolveAIDir("hamr.toml")
		outDir = filepath.Join(aiDir, "captures")
	}
	browserPath, _ := cmd.Flags().GetString("browser")
	captureText, _ := cmd.Flags().GetBool("text")
	captureHTML, _ := cmd.Flags().GetBool("html")
	jsonOutput, _ := cmd.Flags().GetBool("json")
	selector, _ := cmd.Flags().GetString("selector")
	fullPage, _ := cmd.Flags().GetBool("full-page")
	headless, _ := cmd.Flags().GetBool("headless")
	noSandbox, _ := cmd.Flags().GetBool("no-sandbox")
	width, _ := cmd.Flags().GetInt("width")
	height, _ := cmd.Flags().GetInt("height")
	scale, _ := cmd.Flags().GetFloat64("scale")
	scrollTo, _ := cmd.Flags().GetString("scroll-to")
	scrollX, _ := cmd.Flags().GetInt("scroll-x")
	scrollY, _ := cmd.Flags().GetInt("scroll-y")
	scrollSelector, _ := cmd.Flags().GetString("scroll-selector")
	timeout, _ := cmd.Flags().GetDuration("timeout")
	waitAfterLoad, _ := cmd.Flags().GetDuration("wait")

	return browsercapture.Options{
		URL:            rawURL,
		BrowserPath:    browserPath,
		OutputPath:     outPath,
		OutputDir:      outDir,
		Selector:       selector,
		CaptureHTML:    captureHTML,
		CaptureText:    captureText,
		FullPage:       fullPage,
		Headless:       headless,
		NoSandbox:      noSandbox,
		Width:          width,
		Height:         height,
		Scale:          scale,
		ScrollTo:       scrollTo,
		ScrollX:        scrollX,
		ScrollY:        scrollY,
		ScrollSelector: scrollSelector,
		Timeout:        timeout,
		WaitAfterLoad:  waitAfterLoad,
	}, jsonOutput, nil
}
