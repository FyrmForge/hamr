// Package config provides typed accessors for environment variables with
// sensible defaults and panic-on-missing semantics.
//
// For .env file loading, use the godotenv/autoload blank import in your main
// package:
//
//	import _ "github.com/joho/godotenv/autoload"
package config

import (
	"os"
	"strconv"
	"time"
)

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
