package devserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// waitResult runs WaitForConfigChangeOrQuit in the background and returns a
// func that blocks for its result.
func waitResult(t *testing.T, ctx context.Context, path string, hotkeys <-chan HotkeyAction) func() error {
	t.Helper()

	errCh := make(chan error, 1)
	go func() { errCh <- WaitForConfigChangeOrQuit(ctx, path, hotkeys) }()

	return func() error {
		select {
		case err := <-errCh:
			return err
		case <-time.After(5 * time.Second):
			t.Fatal("WaitForConfigChangeOrQuit never returned")
			return nil
		}
	}
}

// R while parked on a config error means "retry now". Without it the only way
// out is editing hamr.toml — useless when the config is fine and startup failed
// on a clashing port or a bad .env, neither of which touches that file.
func TestWaitForConfigChange_RestartRetries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hamr.toml")
	require.NoError(t, os.WriteFile(path, []byte("broken"), 0o600))

	hotkeys := make(chan HotkeyAction, 1)
	wait := waitResult(t, t.Context(), path, hotkeys)
	hotkeys <- HotkeyRestart

	assert.ErrorIs(t, wait(), ErrRestart)
}

func TestWaitForConfigChange_QuitCancels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hamr.toml")
	require.NoError(t, os.WriteFile(path, []byte("broken"), 0o600))

	hotkeys := make(chan HotkeyAction, 1)
	wait := waitResult(t, t.Context(), path, hotkeys)
	hotkeys <- HotkeyQuit

	assert.ErrorIs(t, wait(), context.Canceled)
}

// Every other hotkey stays consumed: rebuilding or opening a browser is
// meaningless with no running server, so they must not end the wait.
func TestWaitForConfigChange_OtherHotkeysConsumed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hamr.toml")
	require.NoError(t, os.WriteFile(path, []byte("broken"), 0o600))

	hotkeys := make(chan HotkeyAction, 3)
	wait := waitResult(t, t.Context(), path, hotkeys)
	hotkeys <- HotkeyRebuild
	hotkeys <- HotkeyOpenBrowser
	hotkeys <- HotkeyMCPToggle

	// Still waiting: only a file write ends it.
	time.Sleep(100 * time.Millisecond)
	require.NoError(t, os.WriteFile(path, []byte("fixed"), 0o600))
	assert.NoError(t, wait())
}

// A write to the config file is the ordinary exit and reports no error.
func TestWaitForConfigChange_FileWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hamr.toml")
	require.NoError(t, os.WriteFile(path, []byte("broken"), 0o600))

	wait := waitResult(t, t.Context(), path, nil)
	time.Sleep(100 * time.Millisecond)
	require.NoError(t, os.WriteFile(path, []byte("fixed"), 0o600))

	assert.NoError(t, wait())
}
