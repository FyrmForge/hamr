package server_test

import (
	"context"
	"errors"
	"testing"

	"github.com/FyrmForge/hamr/pkg/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunBeforeMigrate_success(t *testing.T) {
	var order []int
	srv, err := server.New(
		server.WithOnBeforeMigrate(func(_ context.Context) error {
			order = append(order, 1)
			return nil
		}),
		server.WithOnBeforeMigrate(func(_ context.Context) error {
			order = append(order, 2)
			return nil
		}),
	)
	require.NoError(t, err)

	err = srv.RunBeforeMigrate(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2}, order)
}

func TestRunBeforeMigrate_error(t *testing.T) {
	secondRan := false
	srv, err := server.New(
		server.WithOnBeforeMigrate(func(_ context.Context) error {
			return errors.New("hook failed")
		}),
		server.WithOnBeforeMigrate(func(_ context.Context) error {
			secondRan = true
			return nil
		}),
	)
	require.NoError(t, err)

	err = srv.RunBeforeMigrate(context.Background())
	assert.EqualError(t, err, "hook failed")
	assert.False(t, secondRan)
}

func TestRunBeforeMigrate_noHooks(t *testing.T) {
	srv, err := server.New()
	require.NoError(t, err)

	err = srv.RunBeforeMigrate(context.Background())
	assert.NoError(t, err)
}

func TestRunAfterMigrate_success(t *testing.T) {
	var order []int
	srv, err := server.New(
		server.WithOnAfterMigrate(func(_ context.Context) error {
			order = append(order, 1)
			return nil
		}),
		server.WithOnAfterMigrate(func(_ context.Context) error {
			order = append(order, 2)
			return nil
		}),
	)
	require.NoError(t, err)

	err = srv.RunAfterMigrate(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2}, order)
}

func TestRunAfterMigrate_error(t *testing.T) {
	srv, err := server.New(
		server.WithOnAfterMigrate(func(_ context.Context) error {
			return errors.New("after failed")
		}),
	)
	require.NoError(t, err)

	err = srv.RunAfterMigrate(context.Background())
	assert.EqualError(t, err, "after failed")
}

func TestMultipleHooks_executionOrder(t *testing.T) {
	var order []int
	srv, err := server.New(
		server.WithOnBeforeMigrate(func(_ context.Context) error {
			order = append(order, 1)
			return nil
		}),
		server.WithOnBeforeMigrate(func(_ context.Context) error {
			order = append(order, 2)
			return nil
		}),
		server.WithOnBeforeMigrate(func(_ context.Context) error {
			order = append(order, 3)
			return nil
		}),
	)
	require.NoError(t, err)

	err = srv.RunBeforeMigrate(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2, 3}, order)
}
