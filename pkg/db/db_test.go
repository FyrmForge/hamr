package db

import (
	"context"
	"os"
	"testing"

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

func TestConnectMalformedScheme(t *testing.T) {
	_, err := Connect("not-a-url://???", WithMaxRetries(1))
	require.Error(t, err)
}

func TestConnectOptionsApplied(t *testing.T) {
	// We can't easily verify pool settings without a real DB, but we can
	// ensure the option functions don't panic and the config is accepted.
	_, err := Connect("postgres://invalid:5432/nope",
		WithMaxRetries(1),
		WithMaxOpenConns(20),
		WithMaxIdleConns(10),
		WithConnMaxIdleTime(30),
		WithConnMaxLifetime(120),
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
