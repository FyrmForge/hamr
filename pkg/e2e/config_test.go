package e2e

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// Default config values
// ---------------------------------------------------------------------------

func TestDefaultConfig(t *testing.T) {
	cfg := defaultConfig()

	assert.True(t, cfg.Headless, "Headless default")
	assert.True(t, cfg.NoSandbox, "NoSandbox default")
	assert.Equal(t, time.Duration(0), cfg.SlowMotion, "SlowMotion default")
	assert.Equal(t, 10*time.Second, cfg.Timeout, "Timeout default")
	assert.Equal(t, "testdata/e2e-artifacts", cfg.ArtifactDir, "ArtifactDir default")
	assert.True(t, cfg.ScreenshotOnFailure, "ScreenshotOnFailure default")
	assert.True(t, cfg.HTMLDumpOnFailure, "HTMLDumpOnFailure default")
	assert.False(t, cfg.GPU, "GPU default")
	assert.Empty(t, cfg.BrowserPath, "BrowserPath default")
	assert.Zero(t, cfg.WindowWidth, "WindowWidth default")
	assert.Zero(t, cfg.WindowHeight, "WindowHeight default")
}

// ---------------------------------------------------------------------------
// With* options
// ---------------------------------------------------------------------------

func TestWithHeadless(t *testing.T) {
	cfg := buildConfig([]Option{WithHeadless(false)})
	assert.False(t, cfg.Headless)
}

func TestWithSlowMotion(t *testing.T) {
	cfg := buildConfig([]Option{WithSlowMotion(500 * time.Millisecond)})
	assert.Equal(t, 500*time.Millisecond, cfg.SlowMotion)
}

func TestWithTimeout(t *testing.T) {
	cfg := buildConfig([]Option{WithTimeout(30 * time.Second)})
	assert.Equal(t, 30*time.Second, cfg.Timeout)
}

func TestWithArtifactDir(t *testing.T) {
	cfg := buildConfig([]Option{WithArtifactDir("/tmp/arts")})
	assert.Equal(t, "/tmp/arts", cfg.ArtifactDir)
}

func TestWithScreenshotOnFailure(t *testing.T) {
	cfg := buildConfig([]Option{WithScreenshotOnFailure(false)})
	assert.False(t, cfg.ScreenshotOnFailure)
}

func TestWithHTMLDumpOnFailure(t *testing.T) {
	cfg := buildConfig([]Option{WithHTMLDumpOnFailure(false)})
	assert.False(t, cfg.HTMLDumpOnFailure)
}

func TestWithNoSandbox(t *testing.T) {
	cfg := buildConfig([]Option{WithNoSandbox(false)})
	assert.False(t, cfg.NoSandbox)
}

func TestWithGPU(t *testing.T) {
	cfg := buildConfig([]Option{WithGPU(true)})
	assert.True(t, cfg.GPU)
}

func TestWithBrowserPath(t *testing.T) {
	cfg := buildConfig([]Option{WithBrowserPath("/usr/bin/chromium")})
	assert.Equal(t, "/usr/bin/chromium", cfg.BrowserPath)
}

func TestWithWindowSize(t *testing.T) {
	cfg := buildConfig([]Option{WithWindowSize(1280, 800)})
	assert.Equal(t, 1280, cfg.WindowWidth)
	assert.Equal(t, 800, cfg.WindowHeight)
}

// ---------------------------------------------------------------------------
// Env var overrides
// ---------------------------------------------------------------------------

func TestEnvOverride_Headless(t *testing.T) {
	t.Setenv("E2E_HEADLESS", "false")
	cfg := buildConfig(nil)
	assert.False(t, cfg.Headless)
}

func TestEnvOverride_SlowMotion(t *testing.T) {
	t.Setenv("E2E_SLOW_MOTION", "200ms")
	cfg := buildConfig(nil)
	assert.Equal(t, 200*time.Millisecond, cfg.SlowMotion)
}

func TestEnvOverride_Timeout(t *testing.T) {
	t.Setenv("E2E_TIMEOUT", "30s")
	cfg := buildConfig(nil)
	assert.Equal(t, 30*time.Second, cfg.Timeout)
}

func TestEnvOverride_ArtifactDir(t *testing.T) {
	t.Setenv("E2E_ARTIFACT_DIR", "/tmp/e2e")
	cfg := buildConfig(nil)
	assert.Equal(t, "/tmp/e2e", cfg.ArtifactDir)
}

func TestEnvOverride_ScreenshotOnFailure(t *testing.T) {
	t.Setenv("E2E_SCREENSHOT_ON_FAIL", "false")
	cfg := buildConfig(nil)
	assert.False(t, cfg.ScreenshotOnFailure)
}

func TestEnvOverride_HTMLDumpOnFailure(t *testing.T) {
	t.Setenv("E2E_HTML_DUMP_ON_FAIL", "false")
	cfg := buildConfig(nil)
	assert.False(t, cfg.HTMLDumpOnFailure)
}

func TestEnvOverride_NoSandbox(t *testing.T) {
	t.Setenv("E2E_NO_SANDBOX", "false")
	cfg := buildConfig(nil)
	assert.False(t, cfg.NoSandbox)
}

func TestEnvOverride_GPU(t *testing.T) {
	t.Setenv("E2E_GPU", "true")
	cfg := buildConfig(nil)
	assert.True(t, cfg.GPU)
}

func TestEnvOverride_BrowserPath(t *testing.T) {
	t.Setenv("E2E_BROWSER_PATH", "/opt/chrome")
	cfg := buildConfig([]Option{WithBrowserPath("/code/chrome")})
	assert.Equal(t, "/opt/chrome", cfg.BrowserPath, "env should override code option")
}

func TestEnvOverride_WindowSize(t *testing.T) {
	t.Setenv("E2E_WINDOW_SIZE", "1920X1080")
	cfg := buildConfig([]Option{WithWindowSize(1, 1)})
	assert.Equal(t, 1920, cfg.WindowWidth)
	assert.Equal(t, 1080, cfg.WindowHeight)
}

// ---------------------------------------------------------------------------
// Env vars take precedence over code options
// ---------------------------------------------------------------------------

func TestEnvPrecedence_Headless(t *testing.T) {
	t.Setenv("E2E_HEADLESS", "true")
	cfg := buildConfig([]Option{WithHeadless(false)})
	assert.True(t, cfg.Headless, "env should override code option")
}

func TestEnvPrecedence_Timeout(t *testing.T) {
	t.Setenv("E2E_TIMEOUT", "5s")
	cfg := buildConfig([]Option{WithTimeout(30 * time.Second)})
	assert.Equal(t, 5*time.Second, cfg.Timeout, "env should override code option")
}

func TestEnvPrecedence_ArtifactDir(t *testing.T) {
	t.Setenv("E2E_ARTIFACT_DIR", "/env/path")
	cfg := buildConfig([]Option{WithArtifactDir("/code/path")})
	assert.Equal(t, "/env/path", cfg.ArtifactDir, "env should override code option")
}

func TestEnvPrecedence_NoSandbox(t *testing.T) {
	t.Setenv("E2E_NO_SANDBOX", "true")
	cfg := buildConfig([]Option{WithNoSandbox(false)})
	assert.True(t, cfg.NoSandbox, "env should override code option")
}

// ---------------------------------------------------------------------------
// Invalid env values fall back to code option
// ---------------------------------------------------------------------------

func TestEnvInvalid_Headless(t *testing.T) {
	t.Setenv("E2E_HEADLESS", "notabool")
	cfg := buildConfig([]Option{WithHeadless(false)})
	assert.False(t, cfg.Headless, "invalid env should fall back to code option")
}

func TestEnvInvalid_SlowMotion(t *testing.T) {
	t.Setenv("E2E_SLOW_MOTION", "bad")
	cfg := buildConfig([]Option{WithSlowMotion(100 * time.Millisecond)})
	assert.Equal(t, 100*time.Millisecond, cfg.SlowMotion, "invalid env should fall back to code option")
}

func TestEnvInvalid_WindowSize(t *testing.T) {
	for _, bad := range []string{"1280", "1280x", "ax800", "0x800", "-1x5"} {
		t.Setenv("E2E_WINDOW_SIZE", bad)
		cfg := buildConfig([]Option{WithWindowSize(1280, 800)})
		assert.Equal(t, 1280, cfg.WindowWidth, "invalid %q should fall back to code option", bad)
		assert.Equal(t, 800, cfg.WindowHeight, "invalid %q should fall back to code option", bad)
	}
}

func TestEnvInvalid_Timeout(t *testing.T) {
	t.Setenv("E2E_TIMEOUT", "bad")
	cfg := buildConfig([]Option{WithTimeout(20 * time.Second)})
	assert.Equal(t, 20*time.Second, cfg.Timeout, "invalid env should fall back to code option")
}

// ---------------------------------------------------------------------------
// sanitizeName
// ---------------------------------------------------------------------------

func TestSanitizeName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"simple", "simple"},
		{"TestLogin/happy_path", "TestLogin_happy_path"},
		{"test with spaces", "test_with_spaces"},
		{"a/b/c.d:e", "a_b_c_d_e"},
		{"keep-dashes_and_underscores", "keep-dashes_and_underscores"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeName(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

// ---------------------------------------------------------------------------
// configForTest fallback
// ---------------------------------------------------------------------------

func TestConfigForTest_fallback(t *testing.T) {
	cfg := configForTest(t)
	assert.Equal(t, defaultConfig(), cfg, "should return defaults when no config stored")
}
