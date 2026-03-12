package browsercapture

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

const (
	defaultWidth           = 1440
	defaultHeight          = 1024
	defaultScale           = 1.0
	defaultTimeout         = 15 * time.Second
	defaultWaitAfterLoad   = time.Second
	defaultWaitAfterScroll = 150 * time.Millisecond
	defaultCaptureRootDir  = ".hamr/ai/captures"
	defaultScreenshotName  = "screenshot.png"
	defaultMetaName        = "meta.json"

	// DefaultTileOverlap is the default pixel overlap between adjacent tiles.
	DefaultTileOverlap = 120

	// minTileStep is the minimum pixels advanced per tile to prevent abuse.
	minTileStep = 50
)

var invalidFilenameChars = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// braveCandidates lists Brave browser binary names and paths to try when no
// Chromium or Chrome binary is found by go-rod's default lookup.
var braveCandidates = []string{
	"brave-browser",
	"brave-browser-nightly",
	"brave",
	"/opt/brave.com/brave/brave-browser",
	"/opt/brave.com/brave-nightly/brave-browser-nightly",
	"/usr/bin/brave-browser",
	"/usr/bin/brave-browser-nightly",
}

// Options configures a browser capture run.
type Options struct {
	URL            string
	BrowserPath    string
	OutputPath     string
	OutputDir      string
	Selector       string
	CaptureHTML    bool
	CaptureText    bool
	FullPage       bool
	Tiles          bool
	Headless       bool
	NoSandbox      bool
	Width          int
	Height         int
	TileOverlap    int
	Scale          float64
	ScrollX        int
	ScrollY        int
	ScrollTo       string
	ScrollSelector string
	Timeout        time.Duration
	WaitAfterLoad  time.Duration
}

// Result describes the files written during a capture run.
type Result struct {
	RequestedURL   string    `json:"requested_url"`
	FinalURL       string    `json:"final_url"`
	Title          string    `json:"title,omitempty"`
	CapturedAt     time.Time `json:"captured_at"`
	CaptureDir     string    `json:"capture_dir"`
	ScreenshotPath string    `json:"screenshot_path"`
	HTMLPath       string    `json:"html_path,omitempty"`
	TextPath       string    `json:"text_path,omitempty"`
	MetaPath       string    `json:"meta_path,omitempty"`
	Selector       string    `json:"selector,omitempty"`
	FullPage       bool      `json:"full_page"`
	Width          int       `json:"width"`
	Height         int       `json:"height"`
	Scale          float64   `json:"scale"`
	ScrollX        int       `json:"scroll_x"`
	ScrollY        int       `json:"scroll_y"`
	ScrollSelector string    `json:"scroll_selector,omitempty"`
	TilePaths      []string  `json:"tile_paths,omitempty"`
	TileCount      int       `json:"tile_count,omitempty"`
	TileOverlap    int       `json:"tile_overlap,omitempty"`
	TotalHeight    int       `json:"total_height,omitempty"`
}

// PrepareOptions normalizes capture options and fills defaults.
func PrepareOptions(opts Options) (Options, error) {
	if strings.TrimSpace(opts.URL) == "" {
		return Options{}, fmt.Errorf("url is required")
	}

	opts.URL = normalizeURL(strings.TrimSpace(opts.URL))
	opts.BrowserPath = strings.TrimSpace(opts.BrowserPath)
	opts.OutputDir = strings.TrimSpace(opts.OutputDir)
	opts.ScrollTo = strings.ToLower(strings.TrimSpace(opts.ScrollTo))
	opts.ScrollSelector = strings.TrimSpace(opts.ScrollSelector)
	if err := validateURL(opts.URL); err != nil {
		return Options{}, err
	}

	if opts.Width == 0 {
		opts.Width = defaultWidth
	}
	if opts.Height == 0 {
		opts.Height = defaultHeight
	}
	if opts.Scale == 0 {
		opts.Scale = defaultScale
	}
	if opts.Timeout == 0 {
		opts.Timeout = defaultTimeout
	}
	if opts.WaitAfterLoad == 0 {
		opts.WaitAfterLoad = defaultWaitAfterLoad
	}

	if opts.Width <= 0 {
		return Options{}, fmt.Errorf("width must be greater than 0")
	}
	if opts.Height <= 0 {
		return Options{}, fmt.Errorf("height must be greater than 0")
	}
	if opts.Scale <= 0 {
		return Options{}, fmt.Errorf("scale must be greater than 0")
	}
	if opts.Timeout <= 0 {
		return Options{}, fmt.Errorf("timeout must be greater than 0")
	}
	if opts.WaitAfterLoad < 0 {
		return Options{}, fmt.Errorf("wait must be 0 or greater")
	}
	if opts.OutputPath != "" && opts.OutputDir != "" {
		return Options{}, fmt.Errorf("only one of output path or output dir may be set")
	}

	if err := validateScrollOptions(opts); err != nil {
		return Options{}, err
	}

	prepared, err := prepareTileOptions(opts)
	if err != nil {
		return Options{}, err
	}
	opts = prepared

	outputPath, err := normalizeOutputPath(opts.OutputPath, opts.OutputDir, opts.URL, time.Now().UTC())
	if err != nil {
		return Options{}, err
	}
	opts.OutputPath = outputPath

	return opts, nil
}

// validateScrollOptions checks scroll-related option constraints.
func validateScrollOptions(opts Options) error {
	if opts.ScrollTo != "" && opts.ScrollTo != "top" && opts.ScrollTo != "middle" && opts.ScrollTo != "bottom" {
		return fmt.Errorf("scroll-to must be one of: top, middle, bottom")
	}
	if opts.ScrollTo != "" && (opts.ScrollX != 0 || opts.ScrollY != 0) {
		return fmt.Errorf("scroll-to cannot be combined with scroll-x or scroll-y")
	}
	if opts.ScrollSelector != "" && !hasScrollPositionRequest(opts) {
		return fmt.Errorf("scroll-selector requires scroll-to, scroll-x, or scroll-y")
	}
	if opts.FullPage && hasScrollRequest(opts) {
		return fmt.Errorf("scroll options cannot be used with full-page capture")
	}
	return nil
}

// prepareTileOptions checks tile-related option constraints and applies defaults.
func prepareTileOptions(opts Options) (Options, error) {
	if opts.Tiles && opts.FullPage {
		return opts, fmt.Errorf("tiles cannot be used with full-page capture")
	}
	if opts.Tiles && opts.Selector != "" {
		return opts, fmt.Errorf("tiles cannot be used with selector capture")
	}
	if opts.Tiles && hasScrollRequest(opts) {
		return opts, fmt.Errorf("scroll options cannot be used with tiled capture")
	}
	if opts.TileOverlap < 0 {
		return opts, fmt.Errorf("tile overlap must be 0 or greater")
	}
	if opts.TileOverlap > 0 && !opts.Tiles {
		return opts, fmt.Errorf("tile-overlap requires --tiles")
	}
	if opts.Tiles && opts.TileOverlap == 0 {
		opts.TileOverlap = DefaultTileOverlap
	}
	if opts.Tiles && opts.Height-opts.TileOverlap < minTileStep {
		return opts, fmt.Errorf("tile overlap must be less than viewport height minus %d", minTileStep)
	}
	return opts, nil
}

// Capture launches a browser, captures a screenshot, and optionally saves HTML
// and visible text sidecars.
func Capture(opts Options) (Result, error) {
	opts, err := PrepareOptions(opts)
	if err != nil {
		return Result{}, err
	}

	result := Result{
		RequestedURL:   opts.URL,
		FinalURL:       opts.URL,
		CapturedAt:     time.Now().UTC(),
		CaptureDir:     filepath.Dir(opts.OutputPath),
		ScreenshotPath: opts.OutputPath,
		Selector:       opts.Selector,
		FullPage:       opts.FullPage,
		Width:          opts.Width,
		Height:         opts.Height,
		Scale:          opts.Scale,
		ScrollSelector: opts.ScrollSelector,
	}

	launchCtx, launchCancel := context.WithTimeout(context.Background(), opts.Timeout)
	defer launchCancel()

	l := launcher.New().
		Context(launchCtx).
		Headless(opts.Headless).
		NoSandbox(opts.NoSandbox).
		Set("disable-dev-shm-usage").
		Set("disable-gpu")

	browserPath, err := resolveBrowserPath(opts.BrowserPath)
	if err != nil {
		return Result{}, err
	}
	if browserPath != "" {
		l.Bin(browserPath)
	}

	controlURL, err := l.Launch()
	if err != nil {
		return Result{}, fmt.Errorf("launch browser: %w", err)
	}

	browser := rod.New().ControlURL(controlURL)
	if err := browser.Connect(); err != nil {
		return Result{}, fmt.Errorf("connect browser: %w", err)
	}
	defer func() { _ = browser.Close() }()

	page, err := browser.Page(proto.TargetCreateTarget{})
	if err != nil {
		return Result{}, fmt.Errorf("create browser page: %w", err)
	}
	defer func() { _ = page.Close() }()

	if err := page.Timeout(opts.Timeout).SetViewport(&proto.EmulationSetDeviceMetricsOverride{
		Width:             opts.Width,
		Height:            opts.Height,
		DeviceScaleFactor: opts.Scale,
		Mobile:            false,
	}); err != nil {
		return Result{}, fmt.Errorf("set viewport: %w", err)
	}

	if err := page.Timeout(opts.Timeout).Navigate(opts.URL); err != nil {
		return Result{}, fmt.Errorf("navigate to %s: %w", opts.URL, err)
	}
	if err := page.Timeout(opts.Timeout).WaitLoad(); err != nil {
		return Result{}, fmt.Errorf("wait for page load: %w", err)
	}
	if opts.WaitAfterLoad > 0 {
		time.Sleep(opts.WaitAfterLoad)
	}
	scrollX, scrollY, err := applyScroll(page, opts)
	if err != nil {
		return Result{}, err
	}
	result.ScrollX = scrollX
	result.ScrollY = scrollY

	if info, err := page.Timeout(opts.Timeout).Info(); err == nil {
		if info.URL != "" {
			result.FinalURL = info.URL
		}
		if info.Title != "" {
			result.Title = info.Title
		}
	}

	if opts.Tiles {
		tiles, totalHeight, err := captureTiles(page, opts)
		if err != nil {
			return Result{}, err
		}
		bundleDir := filepath.Dir(opts.OutputPath)
		for i, data := range tiles {
			tileName := fmt.Sprintf("tile_%03d.png", i+1)
			tilePath := filepath.Join(bundleDir, tileName)
			if err := writeFile(tilePath, data); err != nil {
				return Result{}, fmt.Errorf("write tile %d: %w", i+1, err)
			}
			result.TilePaths = append(result.TilePaths, tilePath)
		}
		result.TileCount = len(tiles)
		result.TileOverlap = opts.TileOverlap
		result.TotalHeight = totalHeight
		if len(result.TilePaths) > 0 {
			result.ScreenshotPath = result.TilePaths[0]
		}
	} else {
		screenshot, err := captureScreenshot(page, opts)
		if err != nil {
			return Result{}, err
		}
		if err := writeFile(opts.OutputPath, screenshot); err != nil {
			return Result{}, fmt.Errorf("write screenshot: %w", err)
		}
	}

	if opts.CaptureHTML {
		html, err := page.Timeout(opts.Timeout).HTML()
		if err != nil {
			return Result{}, fmt.Errorf("read page HTML: %w", err)
		}
		result.HTMLPath = sidecarPath(opts.OutputPath, ".html")
		if err := writeFile(result.HTMLPath, []byte(html)); err != nil {
			return Result{}, fmt.Errorf("write html: %w", err)
		}
	}

	if opts.CaptureText {
		text, err := visibleText(page, opts.Timeout)
		if err != nil {
			return Result{}, fmt.Errorf("read page text: %w", err)
		}
		result.TextPath = sidecarPath(opts.OutputPath, ".txt")
		if err := writeFile(result.TextPath, []byte(text)); err != nil {
			return Result{}, fmt.Errorf("write text: %w", err)
		}
	}

	result.MetaPath = metadataPath(opts.OutputPath)
	if err := writeJSON(result.MetaPath, result); err != nil {
		return Result{}, fmt.Errorf("write metadata: %w", err)
	}

	return result, nil
}

func captureTiles(page *rod.Page, opts Options) ([][]byte, int, error) {
	_, maxY, err := scrollBounds(page, opts.Timeout)
	if err != nil {
		return nil, 0, fmt.Errorf("read page scroll bounds: %w", err)
	}

	totalHeight := maxY + opts.Height
	step := opts.Height - opts.TileOverlap
	if step <= 0 {
		return nil, 0, fmt.Errorf("tile step must be positive (height=%d, overlap=%d)", opts.Height, opts.TileOverlap)
	}

	var tiles [][]byte

	for y := 0; y <= maxY; y += step {
		if _, err := page.Timeout(opts.Timeout).Eval(`(y) => { window.scrollTo(0, y); }`, y); err != nil {
			return nil, 0, fmt.Errorf("scroll to y=%d: %w", y, err)
		}
		time.Sleep(defaultWaitAfterScroll)

		data, err := page.Timeout(opts.Timeout).Screenshot(false, nil)
		if err != nil {
			return nil, 0, fmt.Errorf("capture tile at y=%d: %w", y, err)
		}
		tiles = append(tiles, data)
	}

	return tiles, totalHeight, nil
}

func captureScreenshot(page *rod.Page, opts Options) ([]byte, error) {
	if opts.Selector != "" {
		el, err := page.Timeout(opts.Timeout).Element(opts.Selector)
		if err != nil {
			return nil, fmt.Errorf("find selector %q: %w", opts.Selector, err)
		}
		data, err := el.Screenshot(proto.PageCaptureScreenshotFormatPng, 0)
		if err != nil {
			return nil, fmt.Errorf("capture selector %q: %w", opts.Selector, err)
		}
		return data, nil
	}

	data, err := page.Timeout(opts.Timeout).Screenshot(opts.FullPage, nil)
	if err != nil {
		return nil, fmt.Errorf("capture screenshot: %w", err)
	}
	return data, nil
}

func visibleText(page *rod.Page, timeout time.Duration) (string, error) {
	result, err := page.Timeout(timeout).Eval(`() => {
		if (!document.body) return "";
		return document.body.innerText || "";
	}`)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Value.Str()), nil
}

func validateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse url %q: %w", raw, err)
	}
	if u.Scheme == "" {
		return fmt.Errorf("url %q must include a scheme", raw)
	}
	if (u.Scheme == "http" || u.Scheme == "https") && u.Host == "" {
		return fmt.Errorf("url %q is missing a host", raw)
	}
	if u.Scheme == "file" && u.Path == "" {
		return fmt.Errorf("file url %q is missing a path", raw)
	}
	return nil
}

func normalizeURL(raw string) string {
	if strings.HasPrefix(raw, "//") {
		return "http:" + raw
	}
	if strings.Contains(raw, "://") || strings.HasPrefix(raw, "file:") {
		return raw
	}
	return "http://" + raw
}

func normalizeOutputPath(outPath, outDir, targetURL string, now time.Time) (string, error) {
	outPath = strings.TrimSpace(outPath)
	outDir = strings.TrimSpace(outDir)

	if outPath == "" {
		if outDir == "" {
			outDir = defaultCaptureRootDir
		}
		outPath = filepath.Join(outDir, defaultCaptureBaseName(targetURL, now), defaultScreenshotName)
	} else if strings.HasSuffix(outPath, string(os.PathSeparator)) {
		outPath = filepath.Join(outPath, defaultScreenshotName)
	} else if info, err := os.Stat(outPath); err == nil && info.IsDir() {
		outPath = filepath.Join(outPath, defaultScreenshotName)
	}

	ext := filepath.Ext(outPath)
	switch {
	case ext == "":
		outPath += ".png"
	case !strings.EqualFold(ext, ".png"):
		return "", fmt.Errorf("output path must use a .png extension")
	}

	absPath, err := filepath.Abs(outPath)
	if err != nil {
		return "", fmt.Errorf("resolve output path: %w", err)
	}

	return absPath, nil
}

func resolveBrowserPath(configured string) (string, error) {
	if configured != "" {
		path, err := exec.LookPath(configured)
		if err == nil {
			return path, nil
		}
		if _, statErr := os.Stat(configured); statErr == nil {
			return configured, nil
		}
		return "", fmt.Errorf("browser binary %q not found", configured)
	}

	if path, ok := launcher.LookPath(); ok {
		return path, nil
	}

	for _, candidate := range braveCandidates {
		path, err := exec.LookPath(candidate)
		if err == nil {
			return path, nil
		}
		if _, statErr := os.Stat(candidate); statErr == nil {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("no supported browser binary found; install Chromium/Chrome/Brave or pass --browser")
}

func defaultCaptureBaseName(rawURL string, now time.Time) string {
	host := "page"
	pathParts := []string{}

	if u, err := url.Parse(rawURL); err == nil {
		switch {
		case u.Host != "":
			host = u.Hostname()
			if port := u.Port(); port != "" {
				host += "_" + port
			}
		case u.Scheme == "file":
			host = "file"
		}

		for _, part := range strings.Split(strings.Trim(u.Path, "/"), "/") {
			if part != "" {
				pathParts = append(pathParts, part)
			}
		}
	}

	base := sanitizeFilename(strings.Join(append([]string{host}, pathParts...), "_"))
	if base == "" {
		base = "page"
	}

	return fmt.Sprintf("%s_%s", base, now.UTC().Format("20060102T150405Z"))
}

func sidecarPath(path, ext string) string {
	return strings.TrimSuffix(path, filepath.Ext(path)) + ext
}

func metadataPath(screenshotPath string) string {
	if filepath.Base(screenshotPath) == defaultScreenshotName {
		return filepath.Join(filepath.Dir(screenshotPath), defaultMetaName)
	}
	return sidecarPath(screenshotPath, ".meta.json")
}

func hasScrollRequest(opts Options) bool {
	return hasScrollPositionRequest(opts) || opts.ScrollSelector != ""
}

func hasScrollPositionRequest(opts Options) bool {
	return opts.ScrollTo != "" || opts.ScrollX != 0 || opts.ScrollY != 0
}

func applyScroll(page *rod.Page, opts Options) (int, int, error) {
	if !hasScrollPositionRequest(opts) {
		return 0, 0, nil
	}

	if opts.ScrollSelector != "" {
		return applyElementScroll(page, opts)
	}

	return applyWindowScroll(page, opts)
}

func applyWindowScroll(page *rod.Page, opts Options) (int, int, error) {
	targetX := opts.ScrollX
	targetY := opts.ScrollY

	if opts.ScrollTo != "" {
		maxX, maxY, err := scrollBounds(page, opts.Timeout)
		if err != nil {
			return 0, 0, fmt.Errorf("read page scroll bounds: %w", err)
		}
		switch opts.ScrollTo {
		case "top":
			targetY = 0
		case "middle":
			targetY = maxY / 2
		case "bottom":
			targetY = maxY
		}
		if targetX > maxX {
			targetX = maxX
		}
	}

	result, err := page.Timeout(opts.Timeout).Eval(`(x, y) => {
		window.scrollTo(x, y);
		return {
			x: Math.round(window.scrollX || 0),
			y: Math.round(window.scrollY || 0)
		};
	}`, targetX, targetY)
	if err != nil {
		return 0, 0, fmt.Errorf("scroll page: %w", err)
	}

	time.Sleep(defaultWaitAfterScroll)

	return result.Value.Get("x").Int(), result.Value.Get("y").Int(), nil
}

func applyElementScroll(page *rod.Page, opts Options) (int, int, error) {
	result, err := page.Timeout(opts.Timeout).Eval(`(selector, preset, x, y) => {
		const el = document.querySelector(selector);
		if (!el) {
			return { error: "selector not found: " + selector };
		}

		const maxX = Math.max(0, (el.scrollWidth || 0) - (el.clientWidth || 0));
		const maxY = Math.max(0, (el.scrollHeight || 0) - (el.clientHeight || 0));

		if (preset === "top") y = 0;
		if (preset === "middle") y = Math.round(maxY / 2);
		if (preset === "bottom") y = maxY;

		if (x < 0) x = 0;
		if (y < 0) y = 0;
		if (x > maxX) x = maxX;
		if (y > maxY) y = maxY;

		el.scrollTo(x, y);

		return {
			x: Math.round(el.scrollLeft || 0),
			y: Math.round(el.scrollTop || 0)
		};
	}`, opts.ScrollSelector, opts.ScrollTo, opts.ScrollX, opts.ScrollY)
	if err != nil {
		return 0, 0, fmt.Errorf("scroll element %q: %w", opts.ScrollSelector, err)
	}

	if result.Value.Has("error") && !result.Value.Get("error").Nil() {
		msg := result.Value.Get("error").Str()
		return 0, 0, fmt.Errorf("scroll element %q: %s", opts.ScrollSelector, msg)
	}

	time.Sleep(defaultWaitAfterScroll)

	return result.Value.Get("x").Int(), result.Value.Get("y").Int(), nil
}

func scrollBounds(page *rod.Page, timeout time.Duration) (int, int, error) {
	result, err := page.Timeout(timeout).Eval(`() => {
		const doc = document.documentElement || {};
		const body = document.body || {};
		const scrollWidth = Math.max(doc.scrollWidth || 0, body.scrollWidth || 0);
		const scrollHeight = Math.max(doc.scrollHeight || 0, body.scrollHeight || 0);
		return {
			maxX: Math.max(0, scrollWidth - window.innerWidth),
			maxY: Math.max(0, scrollHeight - window.innerHeight)
		};
	}`)
	if err != nil {
		return 0, 0, err
	}

	return result.Value.Get("maxX").Int(), result.Value.Get("maxY").Int(), nil
}

func sanitizeFilename(s string) string {
	s = invalidFilenameChars.ReplaceAllString(strings.TrimSpace(s), "_")
	s = strings.Trim(s, "._-")
	return s
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFile(path, data)
}
