package tui

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// TestSignalRunCancel_KillsChildTree runs a make target whose recipe
// spawns a background sleep holding the output pipe open. Cancelling must
// take the whole group down — signalling make alone leaves the sleep
// running and cmd.Wait blocks on the pipe forever.
func TestSignalRunCancel_KillsChildTree(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make not installed")
	}
	dir := t.TempDir()
	mk := "slow:\n\tsleep 60 & sleep 60\n"
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(mk), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	m := &Model{makeOut: io.Discard}
	cmd := m.dispatchRun("slow")
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()

	time.Sleep(300 * time.Millisecond)
	m.signalRunCancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cmd.Wait did not return after cancel — child tree survived")
	}
}
