package devserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
)

const schedulerBatchWindow = 20 * time.Millisecond

// ErrConfigReload is returned by Run when the config file changes.
// The caller should reload the config and call Run again.
var ErrConfigReload = errors.New("config changed, reloading")

// Runner is the top-level dev server orchestrator.
type Runner struct {
	cfg        *Config
	configPath string
	logger     *slog.Logger
	verbose    bool
	noProxy    bool
	hotkeys    *HotkeyReader
	statusBar  *StatusBar
}

// Option configures a Runner.
type Option func(*Runner)

// WithLogger sets the logger for the runner.
func WithLogger(l *slog.Logger) Option {
	return func(r *Runner) { r.logger = l }
}

// WithVerbose enables verbose logging.
func WithVerbose(v bool) Option {
	return func(r *Runner) { r.verbose = v }
}

// WithNoProxy disables the reverse proxy.
func WithNoProxy(v bool) Option {
	return func(r *Runner) { r.noProxy = v }
}

// WithConfigPath sets the config file path so the runner can watch it for changes.
func WithConfigPath(path string) Option {
	return func(r *Runner) { r.configPath = path }
}

// WithHotkeys provides an externally managed HotkeyReader.
// When set, Run() uses this reader instead of creating its own,
// and does not stop it on return — the caller owns its lifecycle.
func WithHotkeys(h *HotkeyReader) Option {
	return func(r *Runner) { r.hotkeys = h }
}

// WithStatusBar provides an externally managed StatusBar.
// When set, Run() uses this bar instead of creating its own,
// and does not stop it on return — the caller owns its lifecycle.
func WithStatusBar(sb *StatusBar) Option {
	return func(r *Runner) { r.statusBar = sb }
}

// NewRunner creates a new Runner with the given config and options.
func NewRunner(cfg *Config, opts ...Option) *Runner {
	r := &Runner{cfg: cfg}
	for _, opt := range opts {
		opt(r)
	}
	if r.logger == nil {
		r.logger = newDevLogger(os.Stderr, r.verbose)
	}
	return r
}

// buildHamrInjectedEnv returns env-var KEY=VALUE pairs that hamr dev sets
// on every spawned rule process so the scaffolded site (or any rule) can
// discover hamr-served URLs without hardcoding ports. The values are
// derived from `hamr.toml` so they automatically track `[proxy].listen`
// changes — no second source of truth in the scaffold.
//
// Currently emits:
//   - HAMR_DEV_URL: proxy origin, set when [dev.email] OR [dev.stripe] is
//     enabled (the email mock uses it as its ingest target; the scaffold's
//     emailmock.New(envHamrDevURL) reads it).
//   - HAMR_STRIPE_MOCK_URL: proxy origin, set only when [dev.stripe].enabled.
//     Scaffolded main.go points stripe-go at this URL when STRIPE_MOCK=true.
//
// godotenv.Load() in the spawned site honors pre-set env vars (doesn't
// overwrite), so these injected values win over any conflicting entry in
// the user's .env — by design. Hamr knows its own proxy URL; nothing the
// user puts in .env should be more authoritative than that.
func buildHamrInjectedEnv(cfg *Config) []string {
	if !cfg.ProxyConfigured {
		return nil
	}
	var injected []string
	proxyOrigin := stripeMockBaseURL(cfg) // same origin builder; "stripe" name is historical
	if cfg.Dev.Email.Enabled || cfg.Dev.Stripe.Enabled {
		injected = append(injected, "HAMR_DEV_URL="+proxyOrigin)
	}
	if cfg.Dev.Stripe.Enabled {
		injected = append(injected, "HAMR_STRIPE_MOCK_URL="+proxyOrigin)
	}
	return injected
}

// Run starts the dev server and blocks until ctx is cancelled.
func (r *Runner) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)

	// Set up rolling file logger for LLM-readable dev logs.
	var fileLog *rollingFileWriter
	if r.cfg.Dev.LogFile != "" && r.cfg.Dev.LogFile != "none" {
		var err error
		fileLog, err = newRollingFileWriter(r.cfg.Dev.LogFile, r.cfg.Dev.LogFileMaxLines)
		if err != nil {
			r.logger.Error("failed to create dev log file", "path", r.cfg.Dev.LogFile, "err", err)
		}
	}
	if fileLog != nil {
		defer func() { _ = fileLog.Close() }()
		r.logger = newDevLogger(io.MultiWriter(os.Stderr, fileLog), r.verbose)
	}

	graph := NewGraph(r.cfg.Dev.Watch)
	pm := NewProcessManager(r.logger)
	broker := NewSSEBroker(r.cfg.Dev.Watch, r.cfg.Dev.Daemons, r.cfg.Dev.DockerCompose, r.cfg.Dev.Email.Enabled, r.cfg.Dev.Stripe.Enabled)
	errorState := NewErrorState()
	logBuf := NewLogBuffer(1000)
	pm.SetLogOutput(logBuf, broker)
	pm.SetInjectedEnv(buildHamrInjectedEnv(r.cfg))
	if fileLog != nil {
		pm.SetFileLog(fileLog)
	}
	pm.OnProcessExit = func(rule string, err error, output string) {
		errorState.Set(rule, output)
		broker.Broadcast(buildErrorEvent(rule, output))
	}
	var schedulerWG sync.WaitGroup
	var watcher *Watcher
	configReloadCh := make(chan struct{}, 1)
	var configReload bool

	// Start reverse proxy.
	var proxySrv *http.Server
	// Use externally provided status bar, or create a local one.
	statusBar := r.statusBar
	ownStatusBar := statusBar == nil
	if ownStatusBar {
		statusBar = &StatusBar{}
	}
	statusBar.SetErrorState(errorState)
	defer func() {
		if ownStatusBar {
			statusBar.Stop()
		}
		r.logger.Info("shutting down")
		if !configReload {
			broker.Broadcast(SSEEvent{Type: "shutdown"})
		}
		pm.ClearCallbacks()
		cancel()
		if watcher != nil {
			watcher.Stop()
		}
		schedulerWG.Wait()
		pm.StopAll()
		for i := range r.cfg.Dev.DockerCompose {
			dc := &r.cfg.Dev.DockerCompose[i]
			if !dc.KeepRunning {
				r.stopDockerCompose(dc)
			}
		}
		if proxySrv != nil {
			_ = proxySrv.Close()
		}
	}()

	actions := &DevActions{
		ctx: runCtx, cfg: r.cfg, pm: pm, broker: broker,
		errorState: errorState, graph: graph, logger: r.logger,
	}

	// Initialize hotkey reader early so quit works during startup
	// (docker compose, initial build). Without this, Ctrl+C in raw mode
	// has nowhere to go — SIGINT is disabled and the event loop hasn't started.
	hotkeyReader := r.hotkeyReader(runCtx)
	if ownStatusBar {
		statusBar.Start()
	}
	startupDone := make(chan struct{})
	startupExited := make(chan struct{})
	go func() {
		defer close(startupExited)
		for {
			select {
			case action := <-hotkeyReader.Actions():
				if r.handleHotkey(action, actions, statusBar, cancel) {
					return
				}
			case <-startupDone:
				return
			case <-runCtx.Done():
				return
			}
		}
	}()

	// Mail mock is gated on the proxy because its UI lives on the proxy mux.
	// Fail loudly if enabled without a proxy rather than silently disabling.
	var mailMock *MailMock
	if r.cfg.Dev.Email.Enabled {
		if !r.cfg.ProxyConfigured {
			return fmt.Errorf("dev.email.enabled = true requires a [proxy] section in hamr.toml (the mail UI lives on the proxy mux)")
		}
		if r.noProxy {
			return fmt.Errorf("dev.email.enabled = true cannot be used with --no-proxy (the mail UI lives on the proxy mux)")
		}
		opts := MailMockOptions{
			MaxMessages:     r.cfg.Dev.Email.MaxMessages,
			MaxMessageBytes: r.cfg.Dev.Email.MaxMessageBytes,
			OnPersistError: func(err error) {
				r.logger.Warn("mail mock persistence error", "err", err)
			},
		}
		if r.cfg.Dev.Email.PersistEnabled() {
			opts.PersistPath = r.cfg.Dev.Email.ResolvedPersistPath()
		}
		mailMock = NewMailMock(opts)
		r.logger.Info("mail mock enabled",
			"ui", "/__hamr/mail",
			"ingest", "/__hamr/mail/ingest",
			"max_messages", r.cfg.Dev.Email.MaxMessages,
			"persist", opts.PersistPath,
		)
	}

	// Stripe mock: API surface and dev UI both mount on the proxy mux. Apps
	// point stripe-go at the proxy URL via STRIPE_MOCK=true. Stripe-go's
	// path-prefix check (`req.URL.Path` must start with /v1) is satisfied
	// because we register /v1/* explicitly — http.ServeMux's longest-prefix
	// match keeps these from colliding with the / catch-all to the app.
	var stripeMock *StripeMock
	if r.cfg.Dev.Stripe.Enabled {
		if !r.cfg.ProxyConfigured {
			return fmt.Errorf("dev.stripe.enabled = true requires a [proxy] section in hamr.toml (the Stripe API + UI live on the proxy mux)")
		}
		if r.noProxy {
			return fmt.Errorf("dev.stripe.enabled = true cannot be used with --no-proxy (the Stripe API + UI live on the proxy mux)")
		}
		// Reuse one stripe-prefixed logger for both the mock's internal use
		// and the persist-error callback so all stripe-related output gets
		// the [hamr:stripe] tag (the dev handler interprets the "component"
		// attr as a tag override).
		stripeLogger := r.logger.With("component", "stripe")
		opts := StripeMockOptions{
			BaseURL: stripeMockBaseURL(r.cfg),
			Logger:  stripeLogger,
			OnPersistError: func(err error) {
				stripeLogger.Warn("persistence error", "err", err)
			},
		}
		if r.cfg.Dev.Stripe.PersistEnabled() {
			opts.PersistPath = r.cfg.Dev.Stripe.ResolvedPersistPath()
		}
		stripeMock = NewStripeMock(opts)
		stripeMock.SetWebhookEndpoint(WebhookEndpoint{
			URL:    r.cfg.Dev.Stripe.WebhookURL,
			Secret: r.cfg.Dev.Stripe.WebhookSecret,
		})
	}

	if !r.noProxy && r.cfg.ProxyConfigured {
		inject := r.cfg.Proxy.InjectReload != nil && *r.cfg.Proxy.InjectReload
		handler := NewProxyHandler(r.cfg.Proxy.Target, broker, errorState, logBuf, actions, mailMock, stripeMock, inject)
		srv, _, err := ListenAndServeProxy(r.cfg.Proxy.Listen, handler)
		if err != nil {
			return fmt.Errorf("start proxy: %w", err)
		}
		proxySrv = srv
		r.logger.Info("proxy listening", "addr", r.cfg.Proxy.Listen, "target", r.cfg.Proxy.Target)
		if stripeMock != nil {
			persistPath := ""
			if r.cfg.Dev.Stripe.PersistEnabled() {
				persistPath = r.cfg.Dev.Stripe.ResolvedPersistPath()
			}
			r.logger.With("component", "stripe").Info("mock enabled",
				"api", r.cfg.Proxy.Listen+"/v1/*",
				"ui", r.cfg.Proxy.Listen+"/__hamr/stripe/*",
				"webhook_url", r.cfg.Dev.Stripe.WebhookURL,
				"persist", persistPath,
			)
		}
	}

	// Ensure docker compose services are running before build.
	for i := range r.cfg.Dev.DockerCompose {
		if runCtx.Err() != nil {
			return nil
		}
		dc := &r.cfg.Dev.DockerCompose[i]
		r.logger.Info("ensuring docker compose", "name", dc.Name, "file", dc.File)
		if output, err := r.ensureDockerCompose(runCtx, dc); err != nil {
			r.logger.Error("docker compose failed", "name", dc.Name, "err", err)
			errorState.Set(dc.Name, output)
			broker.Broadcast(buildErrorEvent(dc.Name, output))
		} else {
			errorState.Clear(dc.Name)
		}
	}

	// Initial build: run all rules in topological order.
	// Track failures so dependents are skipped rather than started with stale artifacts.
	r.logger.Info("running initial build")
	order := graph.TopologicalOrder()
	failed := make(map[string]bool)
	for _, name := range order {
		if runCtx.Err() != nil {
			return nil
		}
		rule := r.findRule(name)
		if rule == nil {
			continue
		}

		// Skip if any dependency failed.
		depFailed := false
		for _, dep := range rule.Depends {
			if failed[dep] {
				depFailed = true
				break
			}
		}
		if depFailed {
			r.logger.Warn("skipping rule (dependency failed)", "rule", name)
			failed[name] = true
			graph.MarkDone(name)
			continue
		}

		if rule.Cmd != "" {
			if output, err := pm.RunCommand(runCtx, rule); err != nil {
				r.logger.Error("initial build failed", "rule", name, "err", err)
				errorState.Set(name, output)
				broker.Broadcast(buildErrorEvent(name, output))
				failed[name] = true
				graph.MarkDone(name)
				continue
			}
		}
		if rule.Run != "" {
			if err := pm.StartProcess(runCtx, rule); err != nil {
				r.logger.Error("failed to start process", "rule", name, "err", err)
			}
		}
		graph.MarkDone(name)
	}

	// Start daemons.
	for i := range r.cfg.Dev.Daemons {
		d := &r.cfg.Dev.Daemons[i]
		r.logger.Info("starting daemon", "name", d.Name)
		rule := &WatchRule{Name: d.Name, Run: d.Cmd, Env: d.Env}
		if err := pm.StartProcess(runCtx, rule); err != nil {
			r.logger.Error("failed to start daemon", "daemon", d.Name, "err", err)
		}
	}

	// If no watch rules, just block until shutdown or config reload.
	if len(r.cfg.Dev.Watch) == 0 {
		r.logger.Info("no watch rules, running daemons only")
		r.logger.Info("ready")
		close(startupDone)
		<-startupExited
		if r.configPath != "" {
			go r.watchConfigFile(runCtx, configReloadCh)
		}
		for {
			select {
			case <-runCtx.Done():
				return nil
			case <-configReloadCh:
				configReload = true
				return ErrConfigReload
			case action := <-hotkeyReader.Actions():
				if r.handleHotkey(action, actions, statusBar, cancel) {
					return nil
				}
			}
		}
	}

	// Start file watcher.
	var err error
	watcher, err = NewWatcher(".", r.cfg.Dev.Watch, r.logger)
	if err != nil {
		return fmt.Errorf("create watcher: %w", err)
	}
	if err := watcher.Start(runCtx); err != nil {
		return fmt.Errorf("start watcher: %w", err)
	}

	r.logger.Info("watching for changes")
	r.logger.Info("ready")
	close(startupDone)
	<-startupExited

	// Watch the config file for changes.
	if r.configPath != "" {
		go r.watchConfigFile(runCtx, configReloadCh)
	}

	dirty := make(map[string]FileEvent, len(r.cfg.Dev.Watch))
	var dirtyMu sync.Mutex
	scheduleCh := make(chan struct{}, 1)

	// Single scheduler goroutine coalesces bursts and executes in topological
	// order to preserve dependency semantics.
	schedulerWG.Add(1)
	go func() {
		defer schedulerWG.Done()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-scheduleCh:
			}

			// Small quiet window to batch near-simultaneous events so dependency
			// ordering can be resolved from a fuller dirty set.
			timer := time.NewTimer(schedulerBatchWindow)
		batchLoop:
			for {
				select {
				case <-runCtx.Done():
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					return
				case <-scheduleCh:
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					timer.Reset(schedulerBatchWindow)
				case <-timer.C:
					break batchLoop
				}
			}

			for {
				var evt FileEvent
				found := false

				dirtyMu.Lock()
				for _, name := range order {
					candidate, ok := dirty[name]
					if !ok {
						continue
					}

					rule := r.findRule(name)
					if rule == nil {
						delete(dirty, name)
						continue
					}

					// If any dependency is also dirty, schedule that first.
					blocked := false
					for _, dep := range rule.Depends {
						if _, depDirty := dirty[dep]; depDirty {
							blocked = true
							break
						}
					}
					if blocked {
						continue
					}

					evt = candidate
					delete(dirty, name)
					found = true
					break
				}
				dirtyMu.Unlock()

				if !found {
					break
				}

				r.handleEvent(runCtx, evt, graph, pm, broker, errorState)
			}
		}
	}()

	// Event loop: coalesce to latest event per rule and notify scheduler.
	for {
		select {
		case <-watcher.Done():
			return nil
		case <-runCtx.Done():
			return nil
		case <-configReloadCh:
			r.logger.Info("config changed, reloading")
			configReload = true
			return ErrConfigReload
		case action := <-hotkeyReader.Actions():
			if r.handleHotkey(action, actions, statusBar, cancel) {
				return nil
			}
		case evt, ok := <-watcher.Events():
			if !ok {
				return nil
			}
			dirtyMu.Lock()
			dirty[evt.Rule.Name] = evt
			dirtyMu.Unlock()
			select {
			case scheduleCh <- struct{}{}:
			default:
			}
		}
	}
}

// hotkeyReader returns the externally provided HotkeyReader if set,
// otherwise creates and starts a local one tied to the given context.
func (r *Runner) hotkeyReader(ctx context.Context) *HotkeyReader {
	if r.hotkeys != nil {
		return r.hotkeys
	}
	h := &HotkeyReader{}
	h.Start(ctx)
	return h
}

// handleHotkey processes a hotkey action. Returns true if the server should exit.
func (r *Runner) handleHotkey(action HotkeyAction, actions *DevActions, sb *StatusBar, cancel context.CancelFunc) bool {
	switch action {
	case HotkeyRebuild:
		r.logger.Info("rebuilding all rules")
		go actions.RebuildAll()
	case HotkeyOpenBrowser:
		if r.cfg.ProxyConfigured {
			url := "http://" + normalizeHost(r.cfg.Proxy.Listen)
			r.logger.Info("opening browser", "url", url)
			openBrowser(url)
		} else {
			r.logger.Warn("no proxy configured, cannot open browser")
		}
	case HotkeyClearTerminal:
		clearTerminal()
		sb.Redraw()
	case HotkeyQuit:
		r.logger.Info("quit requested")
		cancel()
		return true
	}
	return false
}

func (r *Runner) handleEvent(ctx context.Context, evt FileEvent, graph *Graph, pm *ProcessManager, broker *SSEBroker, errorState *ErrorState) {
	rule := evt.Rule
	r.logger.Info("change detected", "rule", rule.Name, "path", evt.Path)

	// Notify the browser that a build is starting.
	broker.Broadcast(SSEEvent{Type: "building", Data: rule.Name})

	// Mark this rule as running so dependees block.
	graph.MarkRunning(rule.Name)
	defer graph.MarkDone(rule.Name)

	// Wait for our dependencies to finish.
	if err := graph.WaitForDeps(ctx, rule.Name); err != nil {
		r.logger.Error("wait for deps cancelled", "rule", rule.Name, "err", err)
		return
	}

	// Run the build command.
	if rule.Cmd != "" {
		if output, err := pm.RunCommand(ctx, rule); err != nil {
			r.logger.Error("build failed", "rule", rule.Name, "err", err)
			errorState.Set(rule.Name, output)
			broker.Broadcast(buildErrorEvent(rule.Name, output))
			return
		}
		errorState.Clear(rule.Name)
		broker.Broadcast(SSEEvent{Type: "build_ok", Data: rule.Name})
	}

	// Restart the long-running process.
	if rule.Run != "" {
		if err := pm.StartProcess(ctx, rule); err != nil {
			r.logger.Error("restart failed", "rule", rule.Name, "err", err)
		}
	}

	// Wait for the target server to be ready before reloading the browser.
	if rule.Run != "" && !r.noProxy && r.cfg.ProxyConfigured && rule.Reload != "" && rule.Reload != ReloadNone {
		target := normalizeHost(r.cfg.Proxy.Target)
		if !waitForTarget(ctx, target, 5*time.Second) {
			r.logger.Warn("target not ready, broadcasting reload anyway", "rule", rule.Name)
		}
	}

	// Broadcast reload event.
	if rule.Reload != "" && rule.Reload != ReloadNone {
		broker.Broadcast(SSEEvent{
			Type: "reload",
			Data: string(rule.Reload),
		})
	}
}

// watchConfigFile watches the config file for changes and signals on ch.
func (r *Runner) watchConfigFile(ctx context.Context, ch chan<- struct{}) {
	absPath, err := filepath.Abs(r.configPath)
	if err != nil {
		r.logger.Error("cannot resolve config path", "path", r.configPath, "err", err)
		return
	}

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		r.logger.Error("cannot watch config file", "err", err)
		return
	}
	defer func() { _ = fsw.Close() }()

	// Watch the directory (editors often write to a temp file and rename).
	dir := filepath.Dir(absPath)
	if err := fsw.Add(dir); err != nil {
		r.logger.Error("cannot watch config directory", "dir", dir, "err", err)
		return
	}

	base := filepath.Base(absPath)
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-fsw.Events:
			if !ok {
				return
			}
			if filepath.Base(event.Name) != base {
				continue
			}
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
				select {
				case ch <- struct{}{}:
				default:
				}
				return
			}
		case _, ok := <-fsw.Errors:
			if !ok {
				return
			}
		}
	}
}

// waitForTarget polls the given TCP address until a connection succeeds or the
// timeout expires. Returns true if the target became reachable.
func waitForTarget(ctx context.Context, addr string, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		select {
		case <-ctx.Done():
			return false
		case <-deadline:
			return false
		default:
		}
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// buildErrorEvent creates an SSE event for a build/process error.
func buildErrorEvent(rule, output string) SSEEvent {
	payload, _ := json.Marshal(struct {
		Rule   string `json:"rule"`
		Output string `json:"output"`
	}{Rule: rule, Output: output})
	return SSEEvent{Type: "build_error", Data: string(payload)}
}

func (r *Runner) findRule(name string) *WatchRule {
	for i := range r.cfg.Dev.Watch {
		if r.cfg.Dev.Watch[i].Name == name {
			return &r.cfg.Dev.Watch[i]
		}
	}
	return nil
}

// ensureDockerCompose runs "docker compose up -d" for a compose entry.
func (r *Runner) ensureDockerCompose(ctx context.Context, dc *DockerCompose) (string, error) {
	args := []string{"compose", "-f", dc.File, "up", "-d"}
	if dc.WaitReady {
		args = append(args, "--wait")
	}
	args = append(args, dc.Services...)
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.WaitDelay = 2 * time.Second
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = buildEnv(dc.Env)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return buf.String(), fmt.Errorf("docker compose up failed: %w", err)
	}
	return "", nil
}

// stopDockerCompose runs "docker compose down" for a compose entry during shutdown.
func (r *Runner) stopDockerCompose(dc *DockerCompose) {
	r.logger.Info("stopping docker compose", "name", dc.Name)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := []string{"compose", "-f", dc.File, "down"}
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.WaitDelay = 2 * time.Second
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = buildEnv(dc.Env)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		r.logger.Error("docker compose down failed", "name", dc.Name, "err", err, "output", buf.String())
	}
}
