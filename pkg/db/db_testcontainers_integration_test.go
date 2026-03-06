//go:build integration

package db

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestConnectContext_ReconnectsAfterBackendTermination(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()

	pgContainer, err := tcpostgres.Run(ctx,
		"postgres:18-alpine",
		tcpostgres.WithDatabase("hamr_db_test"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		tcpostgres.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		if dockerUnavailable(err) {
			t.Skipf("docker not available for integration test: %v", err)
		}
		require.NoError(t, err)
	}
	t.Cleanup(func() {
		_ = pgContainer.Terminate(context.Background())
	})

	dsn, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	appDB, err := ConnectContext(ctx, dsn,
		WithMaxOpenConns(1),
		WithMaxIdleConns(1),
		WithConnMaxIdleTime(10*time.Minute),
		WithConnMaxLifetime(30*time.Minute),
		WithMaxRetries(10),
		WithAttemptTimeout(2*time.Second),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = appDB.Close()
	})

	adminDB, err := ConnectContext(ctx, dsn,
		WithMaxOpenConns(1),
		WithMaxIdleConns(1),
		WithMaxRetries(10),
		WithAttemptTimeout(2*time.Second),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = adminDB.Close()
	})

	var appPID int
	require.NoError(t, appDB.Get(&appPID, "SELECT pg_backend_pid()"))

	var adminPID int
	require.NoError(t, adminDB.Get(&adminPID, "SELECT pg_backend_pid()"))
	assert.NotEqual(t, appPID, adminPID, "app and admin handles should use distinct backend sessions")

	// Simulate managed-cloud connection drop by terminating the app session.
	var terminated bool
	require.NoError(t, adminDB.Get(&terminated, "SELECT pg_terminate_backend($1)", appPID))
	require.True(t, terminated, "expected postgres to terminate backend")

	var reconnectedPID int
	require.Eventually(t, func() bool {
		queryCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		err := appDB.GetContext(queryCtx, &reconnectedPID, "SELECT pg_backend_pid()")
		return err == nil
	}, 10*time.Second, 200*time.Millisecond, "expected app DB handle to recover after backend termination")

	assert.NotEqual(t, appPID, reconnectedPID, "expected reconnection to use a new backend session")
}

func dockerUnavailable(err error) bool {
	if err == nil {
		return false
	}

	msg := strings.ToLower(err.Error())
	signatures := []string{
		"cannot connect to the docker daemon",
		"docker daemon is not running",
		"is the docker daemon running",
		"error during connect",
		"connect: permission denied",
		"operation not permitted",
		"no such host",
	}

	for _, sig := range signatures {
		if strings.Contains(msg, sig) {
			return true
		}
	}

	return false
}
