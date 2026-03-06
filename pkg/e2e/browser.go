// Package e2e provides reusable go-rod browser helpers for E2E testing.
//
// Projects import this package in their e2e-go/ test files. The package itself
// carries no //go:build tag — build-tag isolation happens in the consuming
// project.
package e2e

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Config
// ---------------------------------------------------------------------------

// Config controls browser lifecycle, timeouts, and artifact capture.
type Config struct {
	Headless            bool          // default: true,  env: E2E_HEADLESS
	NoSandbox           bool          // default: true,  env: E2E_NO_SANDBOX
	SlowMotion          time.Duration // default: 0,     env: E2E_SLOW_MOTION
	Timeout             time.Duration // default: 10s,   env: E2E_TIMEOUT
	ArtifactDir         string        // default: "testdata/e2e-artifacts", env: E2E_ARTIFACT_DIR
	ScreenshotOnFailure bool          // default: true,  env: E2E_SCREENSHOT_ON_FAIL
	HTMLDumpOnFailure   bool          // default: true,  env: E2E_HTML_DUMP_ON_FAIL
}

func defaultConfig() Config {
	return Config{
		Headless:            true,
		NoSandbox:           true,
		SlowMotion:          0,
		Timeout:             10 * time.Second,
		ArtifactDir:         "testdata/e2e-artifacts",
		ScreenshotOnFailure: true,
		HTMLDumpOnFailure:   true,
	}
}

// ---------------------------------------------------------------------------
// Functional options
// ---------------------------------------------------------------------------

// Option configures a [Config] value.
type Option func(*Config)

// WithHeadless sets whether the browser runs in headless mode.
func WithHeadless(b bool) Option { return func(c *Config) { c.Headless = b } }

// WithSlowMotion adds a delay between browser actions for debugging.
func WithSlowMotion(d time.Duration) Option { return func(c *Config) { c.SlowMotion = d } }

// WithTimeout sets the default timeout for browser operations.
func WithTimeout(d time.Duration) Option { return func(c *Config) { c.Timeout = d } }

// WithArtifactDir sets the directory for screenshots and HTML dumps.
func WithArtifactDir(path string) Option { return func(c *Config) { c.ArtifactDir = path } }

// WithScreenshotOnFailure enables or disables automatic screenshots on test failure.
func WithScreenshotOnFailure(b bool) Option { return func(c *Config) { c.ScreenshotOnFailure = b } }

// WithHTMLDumpOnFailure enables or disables automatic HTML dumps on test failure.
func WithHTMLDumpOnFailure(b bool) Option { return func(c *Config) { c.HTMLDumpOnFailure = b } }

// WithNoSandbox controls the Chrome --no-sandbox flag. Defaults to true for CI
// environments. Set to false when running with a proper sandbox available.
func WithNoSandbox(b bool) Option { return func(c *Config) { c.NoSandbox = b } }

// ---------------------------------------------------------------------------
// Unexported env helpers (no dependency on pkg/config)
// ---------------------------------------------------------------------------

func getEnvBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func getEnvDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

func getEnvString(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// ---------------------------------------------------------------------------
// Config registry (per-test)
// ---------------------------------------------------------------------------

var configs sync.Map // testing.TB → Config

// buildConfig applies defaults → code options → env var overrides.
func buildConfig(opts []Option) Config {
	cfg := defaultConfig()

	// Code options.
	for _, o := range opts {
		o(&cfg)
	}

	// Env overrides always win.
	cfg.Headless = getEnvBool("E2E_HEADLESS", cfg.Headless)
	cfg.SlowMotion = getEnvDuration("E2E_SLOW_MOTION", cfg.SlowMotion)
	cfg.Timeout = getEnvDuration("E2E_TIMEOUT", cfg.Timeout)
	cfg.ArtifactDir = getEnvString("E2E_ARTIFACT_DIR", cfg.ArtifactDir)
	cfg.NoSandbox = getEnvBool("E2E_NO_SANDBOX", cfg.NoSandbox)
	cfg.ScreenshotOnFailure = getEnvBool("E2E_SCREENSHOT_ON_FAIL", cfg.ScreenshotOnFailure)
	cfg.HTMLDumpOnFailure = getEnvBool("E2E_HTML_DUMP_ON_FAIL", cfg.HTMLDumpOnFailure)

	return cfg
}

// configForTest returns the Config associated with t, falling back to defaults.
func configForTest(t testing.TB) Config {
	if v, ok := configs.Load(t); ok {
		return v.(Config)
	}
	return defaultConfig()
}

// ---------------------------------------------------------------------------
// Browser lifecycle
// ---------------------------------------------------------------------------

// SetupBrowser launches a Chromium instance configured via opts and env vars.
// It registers a t.Cleanup to close the browser when the test finishes.
func SetupBrowser(t *testing.T, opts ...Option) *rod.Browser {
	t.Helper()

	cfg := buildConfig(opts)
	configs.Store(t, cfg)
	t.Cleanup(func() { configs.Delete(t) })

	l := launcher.New().
		Headless(cfg.Headless).
		NoSandbox(cfg.NoSandbox).
		Set("disable-dev-shm-usage").
		Set("disable-gpu")

	u, err := l.Launch()
	require.NoError(t, err, "e2e: failed to launch browser")

	browser := rod.New().ControlURL(u)
	if cfg.SlowMotion > 0 {
		browser = browser.SlowMotion(cfg.SlowMotion)
	}

	err = browser.Connect()
	require.NoError(t, err, "e2e: failed to connect to browser")

	t.Cleanup(func() {
		if cerr := browser.Close(); cerr != nil {
			t.Logf("e2e: warning: browser close: %v", cerr)
		}
	})

	return browser
}

// NewPage creates a new browser tab navigated to url and waits for load.
// On test failure the page's screenshot and HTML are captured automatically
// (controlled by Config). The page is closed via t.Cleanup.
func NewPage(t *testing.T, browser *rod.Browser, url string) *rod.Page {
	t.Helper()

	cfg := configForTest(t)

	page, err := browser.Page(proto.TargetCreateTarget{URL: url})
	require.NoError(t, err, "e2e: failed to create page for %s", url)

	err = page.Timeout(cfg.Timeout).WaitLoad()
	require.NoError(t, err, "e2e: page load timed out for %s", url)

	t.Cleanup(func() {
		if t.Failed() {
			if cfg.ScreenshotOnFailure {
				SaveScreenshot(t, page, t.Name())
			}
			if cfg.HTMLDumpOnFailure {
				SavePageHTML(t, page, t.Name())
			}
		}
		if cerr := page.Close(); cerr != nil {
			t.Logf("e2e: warning: page close: %v", cerr)
		}
	})

	return page
}

// ---------------------------------------------------------------------------
// Interaction helpers
// ---------------------------------------------------------------------------

// WaitForElement polls for selector to appear within timeout, failing the
// test if not found.
func WaitForElement(t *testing.T, page *rod.Page, selector string, timeout time.Duration) *rod.Element {
	t.Helper()

	el, err := page.Timeout(timeout).Element(selector)
	require.NoError(t, err, "e2e: element %q did not appear within %v", selector, timeout)

	return el
}

// WaitForURLChange waits until the page URL differs from currentURL or timeout
// elapses.
func WaitForURLChange(t *testing.T, page *rod.Page, currentURL string, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		info, err := page.Info()
		if err == nil && info.URL != "" && info.URL != currentURL {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.Fail(t, "e2e: URL did not change from %s within %v", currentURL, timeout)
}

// Input clears and types value into the element matching selector.
func Input(t *testing.T, page *rod.Page, selector, value string) {
	t.Helper()

	cfg := configForTest(t)

	el, err := page.Timeout(cfg.Timeout).Element(selector)
	require.NoError(t, err, "e2e: element %q not found for input", selector)

	err = el.SelectAllText()
	require.NoError(t, err, "e2e: failed to select text in %q", selector)

	err = el.Input(value)
	require.NoError(t, err, "e2e: failed to input into %q", selector)
}

// Click clicks the element matching selector.
func Click(t *testing.T, page *rod.Page, selector string) {
	t.Helper()

	cfg := configForTest(t)

	el, err := page.Timeout(cfg.Timeout).Element(selector)
	require.NoError(t, err, "e2e: element %q not found for click", selector)

	err = el.Click(proto.InputMouseButtonLeft, 1)
	require.NoError(t, err, "e2e: failed to click %q", selector)
}

// SelectOption selects an <option> by value inside a <select> matching selector.
func SelectOption(t *testing.T, page *rod.Page, selector, value string) {
	t.Helper()

	cfg := configForTest(t)

	el, err := page.Timeout(cfg.Timeout).Element(selector)
	require.NoError(t, err, "e2e: select element %q not found", selector)

	err = el.Select([]string{value}, true, rod.SelectorTypeCSSSector)
	require.NoError(t, err, "e2e: failed to select option %q in %q", value, selector)
}

// ElementExists returns true if selector matches an element on the page.
func ElementExists(t *testing.T, page *rod.Page, selector string) bool {
	t.Helper()

	cfg := configForTest(t)

	_, err := page.Timeout(cfg.Timeout).Element(selector)
	return err == nil
}

// ElementNotExists returns true if selector does not match any element on the
// page. Uses a short timeout to avoid waiting the full config timeout for an
// element that is expected to be absent.
func ElementNotExists(t *testing.T, page *rod.Page, selector string) bool {
	t.Helper()

	_, err := page.Timeout(500 * time.Millisecond).Element(selector)
	return err != nil
}

// ElementText returns the text content of the element matching selector.
// Fatals if the element is not found.
func ElementText(t *testing.T, page *rod.Page, selector string) string {
	t.Helper()

	cfg := configForTest(t)

	el, err := page.Timeout(cfg.Timeout).Element(selector)
	require.NoError(t, err, "e2e: element %q not found", selector)

	text, err := el.Text()
	require.NoError(t, err, "e2e: failed to get text of %q", selector)

	return text
}

// ElementAttribute returns the value of attr on the element matching selector.
// Returns an empty string if the attribute is not set. Fatals if the element is
// not found.
func ElementAttribute(t *testing.T, page *rod.Page, selector, attr string) string {
	t.Helper()

	cfg := configForTest(t)

	el, err := page.Timeout(cfg.Timeout).Element(selector)
	require.NoError(t, err, "e2e: element %q not found", selector)

	val, err := el.Attribute(attr)
	require.NoError(t, err, "e2e: failed to get attribute %q of %q", attr, selector)

	if val == nil {
		return ""
	}
	return *val
}

// ElementCount returns the number of elements matching selector on the page.
func ElementCount(t *testing.T, page *rod.Page, selector string) int {
	t.Helper()

	cfg := configForTest(t)

	els, err := page.Timeout(cfg.Timeout).Elements(selector)
	if err != nil {
		return 0
	}
	return len(els)
}

// WaitForElementRemoved waits until no element matches selector, or timeout
// elapses. Useful for waiting for loading spinners or deleted rows to disappear.
func WaitForElementRemoved(t *testing.T, page *rod.Page, selector string, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, err := page.Timeout(200 * time.Millisecond).Element(selector)
		if err != nil {
			return // element is gone
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.Fail(t, fmt.Sprintf("e2e: element %q still present after %v", selector, timeout))
}

// ---------------------------------------------------------------------------
// Artifact capture (never fatal)
// ---------------------------------------------------------------------------

var unsafeChars = regexp.MustCompile(`[^a-zA-Z0-9_\-]`)

func sanitizeName(name string) string {
	return unsafeChars.ReplaceAllString(name, "_")
}

// SaveScreenshot saves a PNG screenshot of the page. It logs warnings on
// failure but never fails the test.
func SaveScreenshot(t *testing.T, page *rod.Page, name string) {
	t.Helper()

	cfg := configForTest(t)

	if err := os.MkdirAll(cfg.ArtifactDir, 0o755); err != nil {
		t.Logf("e2e: warning: mkdir %s: %v", cfg.ArtifactDir, err)
		return
	}

	fname := fmt.Sprintf("%s_%d.png", sanitizeName(name), time.Now().UnixMilli())
	path := cfg.ArtifactDir + "/" + fname

	data, err := page.Screenshot(true, nil)
	if err != nil {
		t.Logf("e2e: warning: screenshot: %v", err)
		return
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Logf("e2e: warning: write screenshot %s: %v", path, err)
		return
	}

	t.Logf("e2e: screenshot saved to %s", path)
}

// SavePageHTML saves the page's HTML source to a file. It logs warnings on
// failure but never fails the test.
func SavePageHTML(t *testing.T, page *rod.Page, name string) {
	t.Helper()

	cfg := configForTest(t)

	if err := os.MkdirAll(cfg.ArtifactDir, 0o755); err != nil {
		t.Logf("e2e: warning: mkdir %s: %v", cfg.ArtifactDir, err)
		return
	}

	fname := fmt.Sprintf("%s_%d.html", sanitizeName(name), time.Now().UnixMilli())
	path := cfg.ArtifactDir + "/" + fname

	html, err := page.HTML()
	if err != nil {
		t.Logf("e2e: warning: page HTML: %v", err)
		return
	}

	if err := os.WriteFile(path, []byte(html), 0o644); err != nil {
		t.Logf("e2e: warning: write HTML %s: %v", path, err)
		return
	}

	t.Logf("e2e: HTML dump saved to %s", path)
}
