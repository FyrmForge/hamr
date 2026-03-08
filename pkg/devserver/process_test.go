package devserver

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestProcessManager_RunCommand(t *testing.T) {
	pm := NewProcessManager(testLogger())

	rule := &WatchRule{Name: "test", Cmd: "echo hello"}
	err := pm.RunCommand(context.Background(), rule)
	require.NoError(t, err)
}

func TestProcessManager_RunCommand_Failure(t *testing.T) {
	pm := NewProcessManager(testLogger())

	rule := &WatchRule{Name: "test", Cmd: "exit 1"}
	err := pm.RunCommand(context.Background(), rule)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "rule \"test\" cmd failed")
}

func TestProcessManager_RunCommand_Cancelled(t *testing.T) {
	pm := NewProcessManager(testLogger())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rule := &WatchRule{Name: "test", Cmd: "sleep 60"}
	err := pm.RunCommand(ctx, rule)
	assert.Error(t, err)
}

func TestProcessManager_RunCommand_WithEnv(t *testing.T) {
	pm := NewProcessManager(testLogger())

	rule := &WatchRule{Name: "test", Cmd: "echo $MY_TEST_VAR", Env: []string{"MY_TEST_VAR=hello_from_env"}}
	err := pm.RunCommand(context.Background(), rule)
	require.NoError(t, err)
}

func TestProcessManager_RunCommand_Stderr(t *testing.T) {
	pm := NewProcessManager(testLogger())

	rule := &WatchRule{Name: "test", Cmd: "echo error >&2"}
	err := pm.RunCommand(context.Background(), rule)
	require.NoError(t, err) // cmd exits 0 even with stderr
}

func TestProcessManager_StartProcess_StopAll(t *testing.T) {
	pm := NewProcessManager(testLogger())

	rule := &WatchRule{Name: "sleeper", Run: "sleep 60"}
	err := pm.StartProcess(context.Background(), rule)
	require.NoError(t, err)

	// Give the process time to start.
	time.Sleep(50 * time.Millisecond)

	pm.mu.Lock()
	_, running := pm.procs["sleeper"]
	pm.mu.Unlock()
	assert.True(t, running)

	pm.StopAll()

	// After StopAll, procs should be empty.
	time.Sleep(100 * time.Millisecond)
	pm.mu.Lock()
	_, running = pm.procs["sleeper"]
	pm.mu.Unlock()
	assert.False(t, running)
}

func TestProcessManager_StopAll_Empty(t *testing.T) {
	pm := NewProcessManager(testLogger())
	pm.StopAll() // should not panic
}

func TestProcessManager_StartProcess_Restart(t *testing.T) {
	pm := NewProcessManager(testLogger())

	rule := &WatchRule{Name: "sleeper", Run: "sleep 60"}
	err := pm.StartProcess(context.Background(), rule)
	require.NoError(t, err)
	time.Sleep(50 * time.Millisecond)

	pm.mu.Lock()
	firstProc := pm.procs["sleeper"]
	pm.mu.Unlock()
	require.NotNil(t, firstProc)

	// Starting again should kill the first.
	err = pm.StartProcess(context.Background(), rule)
	require.NoError(t, err)
	time.Sleep(50 * time.Millisecond)

	pm.mu.Lock()
	secondProc := pm.procs["sleeper"]
	pm.mu.Unlock()
	require.NotNil(t, secondProc)

	// PIDs should be different.
	assert.NotEqual(t, firstProc.Pid, secondProc.Pid)

	pm.StopAll()
}

func TestProcessManager_StartProcess_MultipleRules(t *testing.T) {
	pm := NewProcessManager(testLogger())

	rule1 := &WatchRule{Name: "server", Run: "sleep 60"}
	rule2 := &WatchRule{Name: "worker", Run: "sleep 60"}

	require.NoError(t, pm.StartProcess(context.Background(), rule1))
	require.NoError(t, pm.StartProcess(context.Background(), rule2))
	time.Sleep(50 * time.Millisecond)

	pm.mu.Lock()
	count := len(pm.procs)
	pm.mu.Unlock()
	assert.Equal(t, 2, count)

	pm.StopAll()
	time.Sleep(100 * time.Millisecond)

	pm.mu.Lock()
	count = len(pm.procs)
	pm.mu.Unlock()
	assert.Equal(t, 0, count)
}

func TestBuildEnv(t *testing.T) {
	t.Run("nil returns nil", func(t *testing.T) {
		env := buildEnv(nil)
		assert.Nil(t, env, "nil input should return nil (inherit parent)")
	})

	t.Run("empty returns nil", func(t *testing.T) {
		env := buildEnv([]string{})
		assert.Nil(t, env, "empty input should return nil")
	})

	t.Run("adds custom var", func(t *testing.T) {
		env := buildEnv([]string{"FOO=bar"})
		assert.NotNil(t, env)

		found := false
		for _, e := range env {
			if e == "FOO=bar" {
				found = true
				break
			}
		}
		assert.True(t, found, "FOO=bar should be in env")
	})

	t.Run("override existing var", func(t *testing.T) {
		// PATH exists in every env — override it.
		env := buildEnv([]string{"PATH=/custom/path"})

		var pathVal string
		for _, e := range env {
			if len(e) > 5 && e[:5] == "PATH=" {
				pathVal = e
			}
		}
		assert.Equal(t, "PATH=/custom/path", pathVal, "rule env should override existing")
	})

	t.Run("includes system env", func(t *testing.T) {
		t.Setenv("HAMR_TEST_MARKER", "present")

		env := buildEnv([]string{"EXTRA=val"})

		found := false
		for _, e := range env {
			if e == "HAMR_TEST_MARKER=present" {
				found = true
				break
			}
		}
		assert.True(t, found, "system env should be included")
	})
}

func TestPrefixWriter(t *testing.T) {
	t.Run("writes complete lines with prefix", func(t *testing.T) {
		r, w, _ := os.Pipe()
		pw := &prefixWriter{dest: w, tag: []byte("[test] ")}

		_, err := pw.Write([]byte("hello world\n"))
		require.NoError(t, err)
		_ = w.Close()

		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		assert.Contains(t, buf.String(), "[test] hello world\n")
	})

	t.Run("buffers partial lines", func(t *testing.T) {
		r, w, _ := os.Pipe()
		pw := &prefixWriter{dest: w, tag: []byte("[test] ")}

		// Write without newline — should buffer.
		_, err := pw.Write([]byte("partial"))
		require.NoError(t, err)

		// Complete the line.
		_, err = pw.Write([]byte(" complete\n"))
		require.NoError(t, err)
		_ = w.Close()

		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		assert.Contains(t, buf.String(), "[test] partial complete\n")
	})

	t.Run("multiple lines in one write", func(t *testing.T) {
		r, w, _ := os.Pipe()
		pw := &prefixWriter{dest: w, tag: []byte("[test] ")}

		_, err := pw.Write([]byte("line1\nline2\n"))
		require.NoError(t, err)
		_ = w.Close()

		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		output := buf.String()
		assert.Contains(t, output, "[test] line1\n")
		assert.Contains(t, output, "[test] line2\n")
	})

	t.Run("preserves ANSI colors", func(t *testing.T) {
		r, w, _ := os.Pipe()
		pw := &prefixWriter{dest: w, tag: []byte("[test] ")}

		ansi := "\033[31mred text\033[0m\n"
		_, err := pw.Write([]byte(ansi))
		require.NoError(t, err)
		_ = w.Close()

		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		assert.Contains(t, buf.String(), "\033[31mred text\033[0m")
	})
}
