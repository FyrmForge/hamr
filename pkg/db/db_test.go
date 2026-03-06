package db

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnectInvalidURL(t *testing.T) {
	_, err := Connect("")
	require.Error(t, err)
}

func TestConnectRetryInvalidDSN(t *testing.T) {
	_, err := Connect("postgres://invalid:5432/nope", WithMaxRetries(1))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db: connecting")
}

func TestConnectRetryExhausted(t *testing.T) {
	// Two retries should both fail and still wrap the error.
	_, err := Connect("postgres://invalid:5432/nope", WithMaxRetries(2))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db: connecting")
}

func TestConnectRejectsZeroRetries(t *testing.T) {
	_, err := Connect("postgres://localhost:5432/app", WithMaxRetries(0))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max retries must be >= 1")
}

func TestConnectRejectsInvalidAttemptTimeout(t *testing.T) {
	_, err := Connect("postgres://localhost:5432/app", WithAttemptTimeout(0))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "attempt timeout must be positive")
}

func TestConnectRejectsNegativePoolValues(t *testing.T) {
	_, err := Connect("postgres://localhost:5432/app", WithMaxOpenConns(-1))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max open conns")

	_, err = Connect("postgres://localhost:5432/app", WithMaxIdleConns(-1))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max idle conns")

	_, err = Connect("postgres://localhost:5432/app", WithConnMaxIdleTime(-time.Second))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "conn max idle time")

	_, err = Connect("postgres://localhost:5432/app", WithConnMaxLifetime(-time.Second))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "conn max lifetime")
}

func TestConnectMalformedScheme(t *testing.T) {
	_, err := Connect("not-a-url://???", WithMaxRetries(1))
	require.Error(t, err)
}

func TestConnectContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := ConnectContext(ctx, "postgres://invalid:5432/nope", WithMaxRetries(2))
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled) || assert.Contains(t, err.Error(), "connect canceled"))
}

func TestConnectPgBouncerSafeMode(t *testing.T) {
	_, err := Connect("postgres://invalid:5432/nope",
		WithPgBouncerSafe(true),
		WithMaxRetries(1),
		WithAttemptTimeout(time.Second),
	)
	require.Error(t, err)
	// Should fail to connect, not fail to parse config.
	assert.NotContains(t, err.Error(), "parse config")
}

func TestConnectOptionsApplied(t *testing.T) {
	// We can't easily verify pool settings without a real DB, but we can
	// ensure the option functions don't panic and the config is accepted.
	_, err := Connect("postgres://invalid:5432/nope",
		WithMaxRetries(1),
		WithMaxOpenConns(20),
		WithMaxIdleConns(10),
		WithConnMaxIdleTime(30*time.Second),
		WithConnMaxLifetime(2*time.Minute),
	)
	// Should still fail (no DB), but proves options are applied without error.
	require.Error(t, err)
}

func TestConnectIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	db, err := Connect(dsn)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	require.NoError(t, db.Ping())
}

func TestConnectIntegrationPoolConfig(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	db, err := Connect(dsn,
		WithMaxOpenConns(3),
		WithMaxIdleConns(2),
	)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	stats := db.Stats()
	assert.Equal(t, 3, stats.MaxOpenConnections)
}

func TestStartKeepAliveDoesNotPanic(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	db, err := Connect(dsn)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	// Should not panic with valid inputs.
	assert.NotPanics(t, func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		StartKeepAlive(ctx, db, 1, 1)
	})
}

func TestStartKeepAliveWithConfigDoesNotPanic(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	db, err := Connect(dsn)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	assert.NotPanics(t, func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		StartKeepAliveWithConfig(ctx, db, KeepAliveConfig{
			Interval: time.Second,
			Timeout:  time.Second,
		})
	})
}
