package devserver

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runUntilReady boots a runner over cfg in a temp dir and blocks until it is
// ready. It returns the live DevActions, a cancel func that triggers an
// ordinary shutdown, and a wait func yielding the error Run eventually unwound
// with. Both restart and plain-shutdown tests boot the same way; only the
// trigger differs.
func runUntilReady(t *testing.T, cfg *Config) (actions *DevActions, cancel context.CancelFunc, wait func() error) {
	t.Helper()

	dir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)

	actionsCh := make(chan *DevActions, 1)
	r := NewRunner(cfg,
		WithLogger(discardLogger()),
		WithNoProxy(true),
		WithActionsHook(func(a *DevActions) { actionsCh <- a }),
	)

	errCh := make(chan error, 1)
	go func() { errCh <- r.Run(ctx) }()

	select {
	case actions = <-actionsCh:
	case <-time.After(5 * time.Second):
		t.Fatal("actions hook never fired")
	}

	// The select loops only read restartCh once startup finishes, so wait for
	// ready — otherwise a restart test asserts nothing about the loop it means
	// to exercise, and handleHotkey's gate would drop the request anyway.
	require.Eventually(t, r.ready.Load, 10*time.Second, 20*time.Millisecond,
		"runner never became ready")

	return actions, cancel, func() error {
		select {
		case err := <-errCh:
			return err
		case <-time.After(10 * time.Second):
			t.Fatal("runner.Run did not unwind")
			return nil
		}
	}
}

// idleDaemonConfig is the smallest config that reaches the daemons-only select
// loop: one long-running process, no watch rules.
func idleDaemonConfig() *Config {
	return &Config{Dev: DevConfig{
		Daemons: []Daemon{{Name: "idle", Cmd: "sleep 60"}},
	}}
}

// runUntilRestart boots cfg, runs beforeRestart (nil is fine) against the live
// DevActions, fires a restart, and returns the error Run unwound with.
func runUntilRestart(t *testing.T, cfg *Config, beforeRestart func(*DevActions)) error {
	t.Helper()

	actions, _, wait := runUntilReady(t, cfg)
	if beforeRestart != nil {
		beforeRestart(actions)
	}
	assert.True(t, actions.RestartServer(), "restart must be accepted by a live runner")
	return wait()
}

// The daemons-only path blocks in its own select loop, separate from the
// watcher loop below. A restart wired into only one of them silently quits
// instead of restarting, so both are covered.
func TestRunner_Restart_DaemonsOnlyLoop(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	assert.ErrorIs(t, runUntilRestart(t, idleDaemonConfig(), nil), ErrRestart)
}

func TestRunner_Restart_WatcherLoop(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	cfg := &Config{Dev: DevConfig{
		Watch: []WatchRule{{Name: "noop", Watch: StringOrSlice{"**/*.go"}, Cmd: "true"}},
	}}
	assert.ErrorIs(t, runUntilRestart(t, cfg, nil), ErrRestart)
}

// Restart is a no-op without a live runner rather than a nil-pointer panic:
// the MCP handler reports it as an error and the hotkey path ignores it.
func TestRestartServer_NoRunner(t *testing.T) {
	assert.False(t, (&DevActions{}).RestartServer())
	assert.False(t, (*DevActions)(nil).RestartServer())
}

// A restart request must never block its caller — an MCP handler queues the
// restart while still holding the HTTP response it has to write first.
func TestRestartServer_DoesNotBlock(t *testing.T) {
	ch := make(chan struct{}, 1)
	a := &DevActions{requestRestart: func() {
		select {
		case ch <- struct{}{}:
		default:
		}
	}}
	for range 5 {
		assert.True(t, a.RestartServer())
	}
	assert.Len(t, ch, 1, "repeated requests must coalesce, not queue up")
}

// collectSSE subscribes to the broker over its real handler and returns a
// func that yields every event text received so far.
func collectSSE(t *testing.T, broker *SSEBroker) func() string {
	t.Helper()

	srv := httptest.NewServer(broker.Handler())
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })

	var mu sync.Mutex
	var seen strings.Builder
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			mu.Lock()
			seen.WriteString(sc.Text())
			seen.WriteString("\n")
			mu.Unlock()
		}
	}()

	// The handler pushes a "connected" event on subscribe; waiting for it means
	// the client is registered before the caller triggers anything.
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return strings.Contains(seen.String(), "event: connected")
	}, 5*time.Second, 20*time.Millisecond, "SSE client never connected")

	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return seen.String()
	}
}

// A restart must NOT tell the browser the dev server is gone — it is coming
// back on the same URL, so the page should sit tight and reconnect rather than
// flash a disconnect banner. Paired with the context-cancel case below, which
// pins the opposite behaviour so this one cannot pass by simply never
// broadcasting anything.
func TestRunner_Restart_SuppressesShutdownBroadcast(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	var events func() string
	err := runUntilRestart(t, idleDaemonConfig(), func(a *DevActions) {
		events = collectSSE(t, a.broker)
	})
	require.ErrorIs(t, err, ErrRestart)

	assert.NotContains(t, events(), "shutdown",
		"restart must not broadcast shutdown — the browser should wait for the reload")
}

func TestRunner_Cancel_BroadcastsShutdown(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	actions, cancel, wait := runUntilReady(t, idleDaemonConfig())

	events := collectSSE(t, actions.broker)
	cancel()
	if err := wait(); err != nil {
		require.ErrorIs(t, err, context.Canceled)
	}

	assert.Eventually(t, func() bool { return strings.Contains(events(), "shutdown") },
		5*time.Second, 20*time.Millisecond,
		"a real shutdown must broadcast, or the restart assertion proves nothing")
}

// dev.restart must not request the restart until its HTTP response is written:
// the restart closes the proxy the call arrived on, so firing it inline could
// hand the agent a connection error for a restart that actually succeeded.
func TestMCPDevRestart_FiresOnlyAfterResponse(t *testing.T) {
	g := newTestGateway(t, map[string]string{"dev": "write"})
	g.enabled.Store(true)

	// Record the response body as it is seen at the moment the restart is
	// requested — proves the reply was already written, not merely that both
	// eventually happened.
	rec := httptest.NewRecorder()
	var bodyWhenFired string
	var fired int
	g.actions.requestRestart = func() {
		fired++
		bodyWhenFired = rec.Body.String()
	}

	req := httptest.NewRequest(http.MethodPost, "/__hamr/mcp/dev.restart", strings.NewReader(""))
	req.Header.Set("Authorization", "Bearer "+g.token)
	g.handle(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"ok":true}`, rec.Body.String())
	assert.Equal(t, 1, fired, "restart must be requested exactly once")
	assert.JSONEq(t, `{"ok":true}`, bodyWhenFired,
		"restart fired before the response was written")
}

// Without a live runner the tool is a normal error result, and nothing is
// queued — the gateway must not report ok for a restart that cannot happen.
func TestMCPDevRestart_NoRunner(t *testing.T) {
	g := newTestGateway(t, map[string]string{"dev": "write"})
	g.enabled.Store(true)

	rec := doMCP(g, "dev.restart", g.token, "")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "restart unavailable")
}

// dev.restart is a mutation, so it must be attributed in the main log like
// every other write tool (reads are skipped to keep the log quiet).
func TestMCPDevRestart_IsMutating(t *testing.T) {
	assert.True(t, mutatingMCPTool("dev.restart"))
	assert.False(t, mutatingMCPTool("dev.info"))
}

// --- restart cooldown ---

func TestRestartRefusal_BeforeReady(t *testing.T) {
	r := &Runner{}
	assert.ErrorContains(t, r.restartRefusal(), "still starting up")
}

func TestRestartRefusal_WithinCooldown(t *testing.T) {
	r := &Runner{}
	r.markReady()
	assert.ErrorContains(t, r.restartRefusal(), "retry in")
}

func TestRestartRefusal_AfterCooldown(t *testing.T) {
	r := &Runner{}
	r.readyAt.Store(time.Now().Add(-restartCooldown - time.Second).UnixNano())
	assert.NoError(t, r.restartRefusal())
}

// CheckRestart with no gate wired (unit tests, any caller outside a live Run)
// must not invent a refusal — only a missing runner blocks it.
func TestCheckRestart_NoGate(t *testing.T) {
	assert.ErrorContains(t, (&DevActions{}).CheckRestart(), "restart unavailable")
	assert.NoError(t, (&DevActions{requestRestart: func() {}}).CheckRestart())
}

// The cooldown is what stops an agent that has decided the server is wedged
// from spending a full teardown-and-rebuild per call. A refused call must
// report the refusal AND leave the runner alone.
func TestMCPDevRestart_RefusedWithinCooldown(t *testing.T) {
	g := newTestGateway(t, map[string]string{"dev": "write"})
	g.enabled.Store(true)

	var fired int
	g.actions.requestRestart = func() { fired++ }
	r := &Runner{}
	r.markReady()
	g.actions.restartRefusal = r.restartRefusal

	rec := doMCP(g, "dev.restart", g.token, "")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "retry in")
	assert.Zero(t, fired, "a refused restart must not reach the runner")

	// Past the cooldown the same call goes through.
	r.readyAt.Store(time.Now().Add(-restartCooldown - time.Second).UnixNano())
	rec = doMCP(g, "dev.restart", g.token, "")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 1, fired)
}

// The R hotkey is gated by the same cooldown, so a double-press costs one
// restart rather than two.
func TestHandleHotkey_RestartRespectsCooldown(t *testing.T) {
	r := &Runner{logger: discardLogger()}
	r.markReady()

	var fired int
	actions := &DevActions{requestRestart: func() { fired++ }, restartRefusal: r.restartRefusal}

	assert.False(t, r.handleHotkey(HotkeyRestart, actions, func() {}))
	assert.Zero(t, fired, "restart within the cooldown must be refused")

	r.readyAt.Store(time.Now().Add(-restartCooldown - time.Second).UnixNano())
	assert.False(t, r.handleHotkey(HotkeyRestart, actions, func() {}))
	assert.Equal(t, 1, fired)
}
