package devserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
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

// Run starts the dev server and blocks until ctx is cancelled.
func (r *Runner) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	graph := NewGraph(r.cfg.Dev.Watch)
	pm := NewProcessManager(r.logger)
	broker := NewSSEBroker(r.cfg.Dev.Watch, r.cfg.Dev.Daemons, r.cfg.Dev.DockerCompose)
	errorState := NewErrorState()
	logBuf := NewLogBuffer(1000)
	pm.SetLogOutput(logBuf, broker)
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
	var statusBar StatusBar
	defer func() {
		statusBar.Stop()
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

	if !r.noProxy && r.cfg.ProxyConfigured {
		inject := r.cfg.Proxy.InjectReload != nil && *r.cfg.Proxy.InjectReload
		handler := NewProxyHandler(r.cfg.Proxy.Target, broker, errorState, logBuf, actions, inject)
		srv, _, err := ListenAndServeProxy(r.cfg.Proxy.Listen, handler)
		if err != nil {
			return fmt.Errorf("start proxy: %w", err)
		}
		proxySrv = srv
		r.logger.Info("proxy listening", "addr", r.cfg.Proxy.Listen, "target", r.cfg.Proxy.Target)
	}

	// Ensure docker compose services are running before build.
	for i := range r.cfg.Dev.DockerCompose {
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
	r.logger.Info("running initial build")
	order := graph.TopologicalOrder()
	for _, name := range order {
		rule := r.findRule(name)
		if rule == nil {
			continue
		}
		if rule.Cmd != "" {
			if output, err := pm.RunCommand(runCtx, rule); err != nil {
				r.logger.Error("initial build failed", "rule", name, "err", err)
				errorState.Set(name, output)
				broker.Broadcast(buildErrorEvent(name, output))
				// Continue — don't abort the whole dev server for a build error.
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
		var hotkeyReader HotkeyReader
		hotkeyReader.Start(runCtx)
		defer hotkeyReader.Stop()
		statusBar.Start()
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
				if r.handleHotkey(action, actions, &statusBar, cancel) {
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
	var hotkeyReader HotkeyReader
	hotkeyReader.Start(runCtx)
	defer hotkeyReader.Stop()
	statusBar.Start()

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
			if r.handleHotkey(action, actions, &statusBar, cancel) {
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
	cmd.Env = buildEnv(dc.Env)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		r.logger.Error("docker compose down failed", "name", dc.Name, "err", err, "output", buf.String())
	}
}
