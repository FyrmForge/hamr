package devserver

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
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
	output, err := pm.RunCommand(context.Background(), rule)
	require.NoError(t, err)
	assert.Contains(t, output, "hello", "captured output is returned on success too")
}

func TestProcessManager_RunCommand_Failure(t *testing.T) {
	pm := NewProcessManager(testLogger())

	rule := &WatchRule{Name: "test", Cmd: "echo boom && exit 1"}
	output, err := pm.RunCommand(context.Background(), rule)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "rule \"test\" cmd failed")
	assert.Contains(t, output, "boom")
}

func TestProcessManager_RunCommand_Cancelled(t *testing.T) {
	pm := NewProcessManager(testLogger())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rule := &WatchRule{Name: "test", Cmd: "sleep 60"}
	_, err := pm.RunCommand(ctx, rule)
	assert.Error(t, err)
}

func TestProcessManager_RunCommand_WithEnv(t *testing.T) {
	pm := NewProcessManager(testLogger())

	rule := &WatchRule{Name: "test", Cmd: "echo $MY_TEST_VAR", Env: []string{"MY_TEST_VAR=hello_from_env"}}
	_, err := pm.RunCommand(context.Background(), rule)
	require.NoError(t, err)
}

func TestProcessManager_RunCommand_Stderr(t *testing.T) {
	pm := NewProcessManager(testLogger())

	rule := &WatchRule{Name: "test", Cmd: "echo error >&2"}
	_, err := pm.RunCommand(context.Background(), rule)
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

// safeBuf is a thread-safe bytes.Buffer for collecting subprocess output
// from goroutines spawned by exec.Command.
type safeBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestProcessManager_SetOutputSinks_RedirectsStdout(t *testing.T) {
	pm := NewProcessManager(testLogger())
	var stdout, stderr safeBuf
	pm.SetOutputSinks(&stdout, &stderr)

	rule := &WatchRule{Name: "redir", Cmd: "echo hello-from-redir"}
	_, err := pm.RunCommand(context.Background(), rule)
	require.NoError(t, err)

	// prefixWriter prepends "[name] " and emits "\r\n" terminators.
	if !strings.Contains(stdout.String(), "hello-from-redir") {
		t.Fatalf("stdout sink should have captured subprocess output, got %q", stdout.String())
	}
	if stderr.String() != "" {
		t.Fatalf("stderr sink should be empty for echo, got %q", stderr.String())
	}
}

func TestProcessManager_SetOutputSinks_FileLogStillFansIn(t *testing.T) {
	pm := NewProcessManager(testLogger())
	var sink, file safeBuf
	pm.SetOutputSinks(&sink, &sink)
	pm.SetFileLog(&file)

	rule := &WatchRule{Name: "tee", Cmd: "echo line"}
	_, err := pm.RunCommand(context.Background(), rule)
	require.NoError(t, err)

	if !strings.Contains(sink.String(), "line") {
		t.Fatalf("sink should still receive output when file log is also configured, got %q", sink.String())
	}
	if !strings.Contains(file.String(), "line") {
		t.Fatalf("file log fan-out must remain active, got %q", file.String())
	}
}

func TestProcessManager_SetOutputSinks_RoutesStderrSeparately(t *testing.T) {
	pm := NewProcessManager(testLogger())
	var stdout, stderr safeBuf
	pm.SetOutputSinks(&stdout, &stderr)

	rule := &WatchRule{Name: "split", Cmd: "echo go-out; echo go-err >&2"}
	_, err := pm.RunCommand(context.Background(), rule)
	require.NoError(t, err)

	if !strings.Contains(stdout.String(), "go-out") {
		t.Fatalf("stdout sink missing stdout payload, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "go-err") {
		t.Fatalf("stderr sink missing stderr payload, got %q", stderr.String())
	}
	if strings.Contains(stdout.String(), "go-err") {
		t.Fatalf("stderr payload must not bleed into stdout sink, got %q", stdout.String())
	}
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
	assert.NotEqual(t, firstProc.proc.Pid, secondProc.proc.Pid)

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

func TestTailBuffer(t *testing.T) {
	t.Run("small input", func(t *testing.T) {
		tb := newTailBuffer()
		_, _ = tb.Write([]byte("hello"))
		assert.Equal(t, "hello", tb.String())
	})

	t.Run("exact fit", func(t *testing.T) {
		tb := newTailBuffer()
		data := bytes.Repeat([]byte("x"), tailBufSize)
		_, _ = tb.Write(data)
		assert.Equal(t, string(data), tb.String())
	})

	t.Run("overflow single write", func(t *testing.T) {
		tb := newTailBuffer()
		data := bytes.Repeat([]byte("a"), tailBufSize+100)
		_, _ = tb.Write(data)
		assert.Equal(t, string(data[100:]), tb.String())
	})

	t.Run("multi-write wrap", func(t *testing.T) {
		tb := newTailBuffer()
		chunk := bytes.Repeat([]byte("b"), tailBufSize-10)
		_, _ = tb.Write(chunk)
		_, _ = tb.Write([]byte("0123456789extra"))

		got := tb.String()
		assert.Len(t, got, tailBufSize)
		assert.True(t, strings.HasSuffix(got, "extra"), "should end with last written data")
	})

	t.Run("larger than buffer", func(t *testing.T) {
		tb := newTailBuffer()
		data := bytes.Repeat([]byte("c"), tailBufSize*3)
		_, _ = tb.Write(data)
		assert.Equal(t, string(data[len(data)-tailBufSize:]), tb.String())
	})

	t.Run("reset", func(t *testing.T) {
		tb := newTailBuffer()
		_, _ = tb.Write([]byte("data"))
		tb.Reset()
		assert.Equal(t, "", tb.String())
	})
}

func TestProcessManager_OnProcessExit(t *testing.T) {
	pm := NewProcessManager(testLogger())

	var mu sync.Mutex
	var gotRule string
	var gotErr error
	var gotOutput string

	pm.OnProcessExit = func(rule string, err error, output string) {
		mu.Lock()
		gotRule = rule
		gotErr = err
		gotOutput = output
		mu.Unlock()
	}

	rule := &WatchRule{Name: "crasher", Run: "echo crash-output && exit 42"}
	err := pm.StartProcess(context.Background(), rule)
	require.NoError(t, err)

	// Wait for the process to exit and callback to fire.
	assert.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return gotRule != ""
	}, 3*time.Second, 50*time.Millisecond)

	mu.Lock()
	assert.Equal(t, "crasher", gotRule)
	assert.Error(t, gotErr)
	assert.Contains(t, gotOutput, "crash-output")
	mu.Unlock()
}

func TestProcessManager_LogOutput(t *testing.T) {
	logBuf := NewLogBuffer(100)
	broker := NewSSEBroker(nil, nil, nil, false, false, false)

	pm := NewProcessManager(testLogger())
	pm.SetLogOutput(logBuf, broker)

	rule := &WatchRule{Name: "test", Cmd: "echo hello && echo world"}
	_, err := pm.RunCommand(context.Background(), rule)
	require.NoError(t, err)

	lines := logBuf.Lines()
	require.GreaterOrEqual(t, len(lines), 2)

	var texts []string
	for _, l := range lines {
		assert.Equal(t, "test", l.Rule)
		texts = append(texts, l.Text)
	}
	assert.Contains(t, texts, "hello")
	assert.Contains(t, texts, "world")
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

		found := slices.Contains(env, "FOO=bar")
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

		found := slices.Contains(env, "HAMR_TEST_MARKER=present")
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
		assert.Contains(t, buf.String(), "[test] hello world\r\n")
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
		assert.Contains(t, buf.String(), "[test] partial complete\r\n")
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
		assert.Contains(t, output, "[test] line1\r\n")
		assert.Contains(t, output, "[test] line2\r\n")
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

// TestProcessManager_StopProcess_EscalatesToKill exercises the stop path with a
// process that ignores SIGINT: stopProcess must hit the grace timeout, escalate
// to SIGKILL, and wait for the single Wait owner to reap — without a second
// Wait racing the reap.
func TestProcessManager_StopProcess_EscalatesToKill(t *testing.T) {
	pm := NewProcessManager(testLogger())

	rule := &WatchRule{Name: "stubborn", Run: "trap '' INT; sleep 60"}
	require.NoError(t, pm.StartProcess(context.Background(), rule))
	time.Sleep(100 * time.Millisecond)

	start := time.Now()
	pm.StopAll()
	elapsed := time.Since(start)

	assert.GreaterOrEqual(t, elapsed, shutdownTimeout, "SIGINT is ignored, so the grace timeout must elapse before SIGKILL")
	assert.Less(t, elapsed, shutdownTimeout+3*time.Second, "stop must not hang well past the timeout")

	pm.mu.Lock()
	_, running := pm.procs["stubborn"]
	pm.mu.Unlock()
	assert.False(t, running, "process entry must be removed after stop")
}

// TestProcessManager_StopProcess_DoesNotHangOnEscapedPipeHolder guards the quit
// hang: a long-running process can spawn a grandchild that escapes the process
// group (setsid → new session) and keeps the stdout pipe open. After the group
// is SIGKILLed that grandchild survives, so cmd.Wait() would block forever
// copying its pipe — hanging stopProcess (and quit) — unless StartProcess sets
// cmd.WaitDelay to bound the post-exit I/O wait.
func TestProcessManager_StopProcess_DoesNotHangOnEscapedPipeHolder(t *testing.T) {
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("setsid not available")
	}
	pm := NewProcessManager(testLogger())

	rule := &WatchRule{Name: "leaky", Run: "setsid sh -c 'sleep 10' & exec sleep 10"}
	require.NoError(t, pm.StartProcess(context.Background(), rule))
	time.Sleep(100 * time.Millisecond)

	done := make(chan struct{})
	go func() { pm.StopAll(); close(done) }()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("stopProcess hung: cmd.Wait() blocked on an escaped pipe-holder (StartProcess missing WaitDelay)")
	}
}
