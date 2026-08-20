package devserver

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestNewRunner(t *testing.T) {
	cfg := &Config{
		Dev: DevConfig{
			Watch: []WatchRule{
				{Name: "go", Watch: StringOrSlice{"**/*.go"}, Cmd: "echo build"},
			},
		},
	}

	r := NewRunner(cfg, WithVerbose(true), WithNoProxy(true))
	assert.NotNil(t, r)
	assert.True(t, r.verbose)
	assert.True(t, r.noProxy)
}

func TestNewRunner_Defaults(t *testing.T) {
	cfg := &Config{}

	r := NewRunner(cfg)
	assert.NotNil(t, r)
	assert.NotNil(t, r.logger, "logger should be auto-created")
	assert.False(t, r.verbose)
	assert.False(t, r.noProxy)
}

func TestNewRunner_WithLogger(t *testing.T) {
	cfg := &Config{}
	logger := discardLogger()

	r := NewRunner(cfg, WithLogger(logger))
	assert.Equal(t, logger, r.logger)
}

func TestRunner_FindRule(t *testing.T) {
	cfg := &Config{
		Dev: DevConfig{
			Watch: []WatchRule{
				{Name: "templ", Watch: StringOrSlice{"**/*.templ"}, Cmd: "templ generate"},
				{Name: "go", Watch: StringOrSlice{"**/*.go"}, Cmd: "go build"},
			},
		},
	}

	r := NewRunner(cfg, WithLogger(discardLogger()))

	t.Run("found", func(t *testing.T) {
		rule := r.findRule("go")
		require.NotNil(t, rule)
		assert.Equal(t, "go", rule.Name)
	})

	t.Run("first rule", func(t *testing.T) {
		rule := r.findRule("templ")
		require.NotNil(t, rule)
		assert.Equal(t, "templ", rule.Name)
	})

	t.Run("not found", func(t *testing.T) {
		rule := r.findRule("nonexistent")
		assert.Nil(t, rule)
	})
}

func TestRunner_HandleEvent(t *testing.T) {
	cfg := &Config{
		Dev: DevConfig{
			Watch: []WatchRule{
				{Name: "echo", Watch: StringOrSlice{"**/*.go"}, Cmd: "echo build-ran", Reload: ReloadFull},
			},
		},
	}

	r := NewRunner(cfg, WithLogger(discardLogger()), WithNoProxy(true))
	graph := NewGraph(cfg.Dev.Watch)
	pm := NewProcessManager(discardLogger())
	broker := NewSSEBroker(nil, nil, nil, false, false, false, false)

	rule := r.findRule("echo")
	evt := FileEvent{Rule: rule, Path: "main.go", Time: time.Now()}

	r.handleEvent(context.Background(), evt, graph, pm, broker, NewErrorState())

	// After handleEvent, the rule should be marked done (not blocking dependees).
	err := graph.WaitForDeps(context.Background(), "echo")
	require.NoError(t, err)
}

func TestRunner_HandleEvent_WithProcess(t *testing.T) {
	cfg := &Config{
		Dev: DevConfig{
			Watch: []WatchRule{
				{Name: "server", Watch: StringOrSlice{"**/*.go"}, Cmd: "echo build", Run: "sleep 60", Reload: ReloadFull},
			},
		},
	}

	r := NewRunner(cfg, WithLogger(discardLogger()), WithNoProxy(true))
	graph := NewGraph(cfg.Dev.Watch)
	pm := NewProcessManager(discardLogger())
	broker := NewSSEBroker(nil, nil, nil, false, false, false, false)

	rule := r.findRule("server")
	evt := FileEvent{Rule: rule, Path: "main.go", Time: time.Now()}

	r.handleEvent(context.Background(), evt, graph, pm, broker, NewErrorState())

	// Process should be running.
	time.Sleep(50 * time.Millisecond)
	pm.mu.Lock()
	_, running := pm.procs["server"]
	pm.mu.Unlock()
	assert.True(t, running)

	pm.StopAll()
}

func TestRunner_HandleEvent_BuildFailure(t *testing.T) {
	cfg := &Config{
		Dev: DevConfig{
			Watch: []WatchRule{
				{Name: "fail", Watch: StringOrSlice{"**/*.go"}, Cmd: "exit 1", Run: "sleep 60"},
			},
		},
	}

	r := NewRunner(cfg, WithLogger(discardLogger()), WithNoProxy(true))
	graph := NewGraph(cfg.Dev.Watch)
	pm := NewProcessManager(discardLogger())
	broker := NewSSEBroker(nil, nil, nil, false, false, false, false)

	rule := r.findRule("fail")
	evt := FileEvent{Rule: rule, Path: "main.go", Time: time.Now()}

	r.handleEvent(context.Background(), evt, graph, pm, broker, NewErrorState())

	// Build failed — process should NOT have started.
	pm.mu.Lock()
	_, running := pm.procs["fail"]
	pm.mu.Unlock()
	assert.False(t, running, "process should not start after build failure")

	// But the rule should still be marked done to unblock dependees.
	err := graph.WaitForDeps(context.Background(), "fail")
	require.NoError(t, err)
}

func TestRunner_HandleEvent_NoReload(t *testing.T) {
	cfg := &Config{
		Dev: DevConfig{
			Watch: []WatchRule{
				{Name: "quiet", Watch: StringOrSlice{"**/*.go"}, Cmd: "echo ok", Reload: ReloadNone},
			},
		},
	}

	r := NewRunner(cfg, WithLogger(discardLogger()), WithNoProxy(true))
	graph := NewGraph(cfg.Dev.Watch)
	pm := NewProcessManager(discardLogger())
	broker := NewSSEBroker(nil, nil, nil, false, false, false, false)

	rule := r.findRule("quiet")
	evt := FileEvent{Rule: rule, Path: "main.go", Time: time.Now()}

	r.handleEvent(context.Background(), evt, graph, pm, broker, NewErrorState())
	// No assertion on broadcast — just verifying no panic and flow completes.
}

func TestRunner_HandleEvent_CSSReload(t *testing.T) {
	cfg := &Config{
		Dev: DevConfig{
			Watch: []WatchRule{
				{Name: "css", Watch: StringOrSlice{"**/*.css"}, Cmd: "echo css", Reload: ReloadCSS},
			},
		},
	}

	r := NewRunner(cfg, WithLogger(discardLogger()), WithNoProxy(true))
	graph := NewGraph(cfg.Dev.Watch)
	pm := NewProcessManager(discardLogger())
	broker := NewSSEBroker(nil, nil, nil, false, false, false, false)

	rule := r.findRule("css")
	evt := FileEvent{Rule: rule, Path: "style.css", Time: time.Now()}

	r.handleEvent(context.Background(), evt, graph, pm, broker, NewErrorState())
	// Completes without error.
}

func TestRunner_HandleEvent_Cancelled(t *testing.T) {
	cfg := &Config{
		Dev: DevConfig{
			Watch: []WatchRule{
				{Name: "templ", Watch: StringOrSlice{"**/*.templ"}, Cmd: "echo templ"},
				{Name: "go", Watch: StringOrSlice{"**/*.go"}, Cmd: "echo go", Depends: StringOrSlice{"templ"}},
			},
		},
	}

	r := NewRunner(cfg, WithLogger(discardLogger()), WithNoProxy(true))
	graph := NewGraph(cfg.Dev.Watch)
	pm := NewProcessManager(discardLogger())
	broker := NewSSEBroker(nil, nil, nil, false, false, false, false)

	// Mark templ as running so go will block.
	graph.MarkRunning("templ")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	rule := r.findRule("go")
	evt := FileEvent{Rule: rule, Path: "main.go", Time: time.Now()}

	// Should return without hanging.
	r.handleEvent(ctx, evt, graph, pm, broker, NewErrorState())
}

func TestRunner_Run_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "pkg"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pkg", "main.go"), []byte("package main"), 0o644))

	injectReload := true
	cfg := &Config{
		Proxy: ProxyConfig{
			Listen:       ":0",
			Target:       ":19876",
			InjectReload: &injectReload,
		},
		Dev: DevConfig{
			Watch: []WatchRule{
				{
					Name:     "go",
					Watch:    StringOrSlice{"**/*.go"},
					Cmd:      "echo built",
					Debounce: Duration{50 * time.Millisecond},
					Reload:   ReloadFull,
				},
			},
		},
	}

	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	defer func() { _ = os.Chdir(origDir) }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	r := NewRunner(cfg, WithLogger(discardLogger()), WithNoProxy(true))

	errCh := make(chan error, 1)
	go func() {
		errCh <- r.Run(ctx)
	}()

	// Give it time to start.
	time.Sleep(500 * time.Millisecond)

	// Trigger a file change.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pkg", "new.go"), []byte("package main"), 0o644))

	// Wait a bit for the event to process.
	time.Sleep(500 * time.Millisecond)

	cancel()

	select {
	case err := <-errCh:
		// May be nil or context.Canceled — both are fine.
		if err != nil {
			assert.ErrorIs(t, err, context.Canceled)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runner.Run did not shut down")
	}
}

func TestRunner_Run_DaemonsOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	dir := t.TempDir()
	markerPath := filepath.Join(dir, "daemon.started")

	cfg := &Config{
		Dev: DevConfig{
			Daemons: []Daemon{
				{
					Name: "marker",
					Cmd:  "touch " + markerPath + " && sleep 60",
				},
			},
		},
	}

	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	defer func() { _ = os.Chdir(origDir) }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	r := NewRunner(cfg, WithLogger(discardLogger()), WithNoProxy(true))

	errCh := make(chan error, 1)
	go func() {
		errCh <- r.Run(ctx)
	}()

	// Wait for the daemon to create the marker file.
	assert.Eventually(t, func() bool {
		_, err := os.Stat(markerPath)
		return err == nil
	}, 3*time.Second, 50*time.Millisecond, "daemon should have started and created marker file")

	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			assert.ErrorIs(t, err, context.Canceled)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runner.Run did not shut down")
	}
}

func TestRunner_Run_DaemonsWithWatchRules(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	dir := t.TempDir()
	markerPath := filepath.Join(dir, "daemon.started")
	triggerPath := filepath.Join(dir, "trigger.go")
	require.NoError(t, os.WriteFile(triggerPath, []byte("package main"), 0o644))

	cfg := &Config{
		Dev: DevConfig{
			Daemons: []Daemon{
				{
					Name: "bg",
					Cmd:  "touch " + markerPath + " && sleep 60",
				},
			},
			Watch: []WatchRule{
				{
					Name:     "go",
					Watch:    StringOrSlice{"**/*.go"},
					Cmd:      "echo built",
					Debounce: Duration{50 * time.Millisecond},
				},
			},
		},
	}

	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	defer func() { _ = os.Chdir(origDir) }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	r := NewRunner(cfg, WithLogger(discardLogger()), WithNoProxy(true))

	errCh := make(chan error, 1)
	go func() {
		errCh <- r.Run(ctx)
	}()

	// Daemon should start.
	assert.Eventually(t, func() bool {
		_, err := os.Stat(markerPath)
		return err == nil
	}, 3*time.Second, 50*time.Millisecond, "daemon should have started")

	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			assert.ErrorIs(t, err, context.Canceled)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runner.Run did not shut down")
	}
}

func TestRunner_Run_DependencyOrderForCoalescedEvents(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	dir := t.TempDir()
	orderPath := filepath.Join(dir, "order.log")
	triggerPath := filepath.Join(dir, "trigger.txt")
	require.NoError(t, os.WriteFile(triggerPath, []byte("initial"), 0o644))

	cfg := &Config{
		Dev: DevConfig{
			Watch: []WatchRule{
				{
					Name:     "templ",
					Watch:    StringOrSlice{"**/*.txt"},
					Cmd:      "printf 'templ\\n' >> ./order.log",
					Debounce: Duration{50 * time.Millisecond},
				},
				{
					Name:     "go",
					Watch:    StringOrSlice{"**/*.txt"},
					Cmd:      "printf 'go\\n' >> ./order.log",
					Depends:  StringOrSlice{"templ"},
					Debounce: Duration{50 * time.Millisecond},
				},
			},
		},
	}

	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	defer func() { _ = os.Chdir(origDir) }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	r := NewRunner(cfg, WithLogger(discardLogger()), WithNoProxy(true))

	errCh := make(chan error, 1)
	go func() {
		errCh <- r.Run(ctx)
	}()

	time.Sleep(300 * time.Millisecond)
	require.NoError(t, os.WriteFile(triggerPath, []byte("changed"), 0o644))
	time.Sleep(500 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			assert.ErrorIs(t, err, context.Canceled)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runner.Run did not shut down")
	}

	data, err := os.ReadFile(orderPath)
	require.NoError(t, err)
	lines := strings.Fields(string(data))
	require.NotEmpty(t, lines, "expected at least one build invocation")

	templCount := 0
	goCount := 0
	for _, line := range lines {
		switch line {
		case "templ":
			templCount++
		case "go":
			if templCount <= goCount {
				t.Fatalf("go ran before templ in order log: %v", lines)
			}
			goCount++
		}
	}
	require.GreaterOrEqual(t, goCount, 1)
}

func TestRunner_HandleEvent_BuildFailure_BroadcastsError(t *testing.T) {
	cfg := &Config{
		Dev: DevConfig{
			Watch: []WatchRule{
				{Name: "fail", Watch: StringOrSlice{"**/*.go"}, Cmd: "echo build-error-output && exit 1"},
			},
		},
	}

	r := NewRunner(cfg, WithLogger(discardLogger()), WithNoProxy(true))
	graph := NewGraph(cfg.Dev.Watch)
	pm := NewProcessManager(discardLogger())
	broker := NewSSEBroker(nil, nil, nil, false, false, false, false)
	srv := httptest.NewServer(broker.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	time.Sleep(50 * time.Millisecond)

	rule := r.findRule("fail")
	evt := FileEvent{Rule: rule, Path: "main.go", Time: time.Now()}
	r.handleEvent(context.Background(), evt, graph, pm, broker, NewErrorState())

	// Read events: connected + config + building + build_error.
	scanner := bufio.NewScanner(resp.Body)
	events := readSSEEvents(scanner, 4)

	require.Len(t, events, 4)
	assert.Equal(t, "connected", events[0].typ)
	assert.Equal(t, "config", events[1].typ)
	assert.Equal(t, "building", events[2].typ)
	assert.Equal(t, "build_error", events[3].typ)
	assert.Contains(t, events[3].data, "build-error-output")
	assert.Contains(t, events[3].data, `"rule":"fail"`)
}

func TestRunner_HandleEvent_BuildSuccess_BroadcastsOk(t *testing.T) {
	cfg := &Config{
		Dev: DevConfig{
			Watch: []WatchRule{
				{Name: "ok", Watch: StringOrSlice{"**/*.go"}, Cmd: "echo fine", Reload: ReloadFull},
			},
		},
	}

	r := NewRunner(cfg, WithLogger(discardLogger()), WithNoProxy(true))
	graph := NewGraph(cfg.Dev.Watch)
	pm := NewProcessManager(discardLogger())
	broker := NewSSEBroker(nil, nil, nil, false, false, false, false)
	srv := httptest.NewServer(broker.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	time.Sleep(50 * time.Millisecond)

	rule := r.findRule("ok")
	evt := FileEvent{Rule: rule, Path: "main.go", Time: time.Now()}
	r.handleEvent(context.Background(), evt, graph, pm, broker, NewErrorState())

	// Read events: connected + config + building + build_ok + reload.
	scanner := bufio.NewScanner(resp.Body)
	events := readSSEEvents(scanner, 5)

	require.Len(t, events, 5)
	assert.Equal(t, "building", events[2].typ)
	assert.Equal(t, "build_ok", events[3].typ)
	assert.Equal(t, "ok", events[3].data)
	assert.Equal(t, "reload", events[4].typ)
}

func TestWaitForTarget_ListenerUp(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()

	ok := waitForTarget(context.Background(), ln.Addr().String(), 2*time.Second)
	assert.True(t, ok, "should return true when target is listening")
}

func TestWaitForTarget_NothingListening(t *testing.T) {
	// Bind and immediately close to get a port that nothing listens on.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	_ = ln.Close()

	start := time.Now()
	ok := waitForTarget(context.Background(), addr, 300*time.Millisecond)
	elapsed := time.Since(start)

	assert.False(t, ok, "should return false when nothing is listening")
	assert.GreaterOrEqual(t, elapsed, 250*time.Millisecond, "should wait close to timeout")
}

func TestWaitForTarget_ContextCancelled(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	_ = ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ok := waitForTarget(ctx, addr, 5*time.Second)
	assert.False(t, ok, "should return false when context is cancelled")
}
