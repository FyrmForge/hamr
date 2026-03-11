package config_test

import (
	"testing"
	"time"

	"github.com/FyrmForge/hamr/pkg/config"
)

func TestParseBaseURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		origin   string
		hostname string
		wantErr  bool
	}{
		{"empty", "", "", "", false},
		{"https", "https://example.com", "https://example.com", "example.com", false},
		{"http", "http://localhost", "http://localhost", "localhost", false},
		{"with port", "https://example.com:8443", "https://example.com:8443", "example.com", false},
		{"with path stripped", "https://example.com/app", "https://example.com", "example.com", false},
		{"bad scheme", "ftp://example.com", "", "", true},
		{"no host", "https://", "", "", true},
		{"no scheme", "example.com", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origin, hostname, err := config.ParseBaseURL(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for input %q", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if origin != tt.origin {
				t.Fatalf("origin: got %q, want %q", origin, tt.origin)
			}
			if hostname != tt.hostname {
				t.Fatalf("hostname: got %q, want %q", hostname, tt.hostname)
			}
		})
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
