// Package config provides typed accessors for environment variables with
// sensible defaults and panic-on-missing semantics.
//
// For .env file loading, use the godotenv/autoload blank import in your main
// package:
//
//	import _ "github.com/joho/godotenv/autoload"
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// ParseBaseURL validates a raw base URL string and returns the origin
// (scheme://host, including port if present) and the hostname (without port).
// An empty input is valid and returns zero values (optional in dev).
func ParseBaseURL(raw string) (origin, hostname string, err error) {
	if raw == "" {
		return "", "", nil
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("config: invalid BASE_URL: %w", err)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return "", "", fmt.Errorf("config: BASE_URL scheme must be http or https, got %q", u.Scheme)
	}

	if u.Hostname() == "" {
		return "", "", fmt.Errorf("config: BASE_URL must include a host")
	}

	origin = u.Scheme + "://" + u.Host // Host includes port if present
	hostname = u.Hostname()            // without port
	return origin, hostname, nil
}

// GetEnvOrDefault returns the value of the environment variable named by key,
// or def if the variable is unset or empty.
func GetEnvOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// GetEnvCSV returns the environment variable named by key parsed as a
// comma-separated list, with surrounding whitespace trimmed and empty entries
// dropped. Returns nil when the variable is unset/empty or contains no
// non-empty entries.
func GetEnvCSV(key string) []string {
	raw := os.Getenv(key)
	if raw == "" {
		return nil
	}
	var out []string
	for part := range strings.SplitSeq(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// GetEnvOrPanic returns the value of the environment variable named by key.
// It panics if the variable is unset or empty.
func GetEnvOrPanic(key string) string {
	v := os.Getenv(key)
	if v == "" {
		panic(key + " must be defined in env")
	}
	return v
}

// GetEnvOrDefaultInt returns the environment variable as an int, falling back
// to def if unset, empty, or not a valid integer.
// Note: invalid values silently fall back to the default with no warning.
func GetEnvOrDefaultInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return i
}

// GetEnvOrDefaultBool returns the environment variable as a bool, falling back
// to def if unset, empty, or not a valid boolean.
// Note: invalid values silently fall back to the default with no warning.
func GetEnvOrDefaultBool(key string, def bool) bool {
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

// GetEnvOrDefaultDuration returns the environment variable as a time.Duration,
// falling back to def if unset, empty, or not a valid duration string.
// Note: invalid values silently fall back to the default with no warning.
func GetEnvOrDefaultDuration(key string, def time.Duration) time.Duration {
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
