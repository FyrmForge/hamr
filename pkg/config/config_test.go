package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/FyrmForge/hamr/pkg/config"
)

func TestLoadEnvFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("HAMR_TEST_LOAD=hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := config.LoadEnvFile(path); err != nil {
		t.Fatalf("LoadEnvFile: %v", err)
	}
	if got := os.Getenv("HAMR_TEST_LOAD"); got != "hello" {
		t.Fatalf("HAMR_TEST_LOAD = %q, want %q", got, "hello")
	}
	// godotenv sets the env var directly; clean up via Unsetenv.
	t.Cleanup(func() { _ = os.Unsetenv("HAMR_TEST_LOAD") })
}

func TestLoadEnvFile_missing(t *testing.T) {
	if err := config.LoadEnvFile("/nonexistent/.env"); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestGetEnvOrDefault(t *testing.T) {
	const key = "HAMR_TEST_DEFAULT"

	if got := config.GetEnvOrDefault(key, "fb"); got != "fb" {
		t.Fatalf("unset: got %q, want %q", got, "fb")
	}

	t.Setenv(key, "val")
	if got := config.GetEnvOrDefault(key, "fb"); got != "val" {
		t.Fatalf("set: got %q, want %q", got, "val")
	}
}

func TestGetEnvOrPanic(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for unset variable")
		}
	}()
	config.GetEnvOrPanic("HAMR_TEST_PANIC")
}

func TestGetEnvOrPanic_set(t *testing.T) {
	const key = "HAMR_TEST_PANIC_SET"
	t.Setenv(key, "ok")

	if got := config.GetEnvOrPanic(key); got != "ok" {
		t.Fatalf("got %q, want %q", got, "ok")
	}
}

func TestGetEnvOrDefaultInt(t *testing.T) {
	const key = "HAMR_TEST_INT"

	if got := config.GetEnvOrDefaultInt(key, 10); got != 10 {
		t.Fatalf("unset: got %d, want 10", got)
	}

	t.Setenv(key, "42")
	if got := config.GetEnvOrDefaultInt(key, 10); got != 42 {
		t.Fatalf("set: got %d, want 42", got)
	}

	t.Setenv(key, "notanint")
	if got := config.GetEnvOrDefaultInt(key, 10); got != 10 {
		t.Fatalf("invalid: got %d, want 10", got)
	}
}

func TestGetEnvOrDefaultBool(t *testing.T) {
	const key = "HAMR_TEST_BOOL"

	if got := config.GetEnvOrDefaultBool(key, true); got != true {
		t.Fatalf("unset: got %v, want true", got)
	}

	t.Setenv(key, "false")
	if got := config.GetEnvOrDefaultBool(key, true); got != false {
		t.Fatalf("set: got %v, want false", got)
	}

	t.Setenv(key, "notabool")
	if got := config.GetEnvOrDefaultBool(key, true); got != true {
		t.Fatalf("invalid: got %v, want true", got)
	}
}

func TestGetEnvOrDefaultDuration(t *testing.T) {
	const key = "HAMR_TEST_DUR"

	def := 5 * time.Second
	if got := config.GetEnvOrDefaultDuration(key, def); got != def {
		t.Fatalf("unset: got %v, want %v", got, def)
	}

	t.Setenv(key, "30s")
	if got := config.GetEnvOrDefaultDuration(key, def); got != 30*time.Second {
		t.Fatalf("set: got %v, want 30s", got)
	}

	t.Setenv(key, "bad")
	if got := config.GetEnvOrDefaultDuration(key, def); got != def {
		t.Fatalf("invalid: got %v, want %v", got, def)
	}
}
