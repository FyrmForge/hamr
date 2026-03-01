// Package config provides environment-based configuration helpers.
//
// It wraps godotenv for .env file loading and offers typed accessors for
// environment variables with sensible defaults and panic-on-missing semantics.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

// LoadEnvFile loads one or more .env files into the process environment.
// If no paths are given it defaults to ".env". Files that do not exist are
// silently skipped; other errors are returned.
func LoadEnvFile(paths ...string) error {
	if len(paths) == 0 {
		paths = []string{".env"}
	}
	if err := godotenv.Load(paths...); err != nil {
		return fmt.Errorf("config: loading env file: %w", err)
	}
	return nil
}

// GetEnvOrDefault returns the value of the environment variable named by key,
// or def if the variable is unset or empty.
func GetEnvOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// GetEnvOrPanic returns the value of the environment variable named by key.
// It panics if the variable is unset or empty.
func GetEnvOrPanic(key string) string {
	v := os.Getenv(key)
	if v == "" {
		panic(fmt.Sprintf("config: %s is required", key))
	}
	return v
}

// GetEnvOrDefaultInt returns the environment variable as an int, falling back
// to def if unset, empty, or not a valid integer.
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
