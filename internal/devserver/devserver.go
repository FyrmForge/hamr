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
	"sync/atomic"
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
	cfg            *Config
	configPath     string
	logger         *slog.Logger
	logWriter      io.Writer            // base writer for the slog handler; defaults to os.Stderr
	procStdout     io.Writer            // override for child stdout (TUI mode); nil = os.Stdout
	procStderr     io.Writer            // override for child stderr (TUI mode); nil = os.Stderr
	dockerLogSinks map[string]io.Writer // per-compose-entry sinks for `compose logs -f`
	verbose        bool
	noProxy        bool
	hotkeys        HotkeySource
	actionsHook    func(*DevActions)
	proxyURLHook   func(string)
	mcpStatusHook  func(enabled bool, tools int) // pushes MCP gateway state to the TUI indicator
	mcpLogHook     func(line string)             // pushes each MCP request line to the TUI MCP tab

	// mcpGateway is set by Run() when the proxy is up; the M hotkey toggles it.
	mcpGateway *mcpGateway

	// proxyURL is set by Run() after the proxy listener has bound to its
	// (possibly walked) port. Read by the o-open hotkey so it always points
	// at the actual listening URL, not the value originally written in
	// hamr.toml. Empty when the proxy isn't running.
	//
	// It is written before `ready` is set and only read once `ready` is
	// observed true, so the atomic store/load on `ready` provides the
	// happens-before that makes the cross-goroutine access (startup hotkey
	// drain vs Run) race-free without a separate mutex.
	proxyURL string

	// ready flips true once startup has finished and proxyURL / cfg.Proxy.*
	// are stable. Until then the startup hotkey-drain goroutine ignores
	// system-dependent actions (open browser, rebuild) — only quit works.
	ready atomic.Bool
}

// Option configures a Runner.
type Option func(*Runner)

// WithLogger sets the logger for the runner.
func WithLogger(l *slog.Logger) Option {
	return func(r *Runner) { r.logger = l }
}

// WithLogWriter overrides the base writer used by the runner's default slog
// handler (defaults to os.Stderr). The file logger fan-out, when enabled,
// remains on top. TUI mode wires this to a viewport-backed sink so the
// runner's own log lines render inside the TUI instead of corrupting the
// frame.
func WithLogWriter(w io.Writer) Option {
	return func(r *Runner) { r.logWriter = w }
}

// WithProcessOutput redirects child-process stdout/stderr away from the
// terminal into the given writers. Internally calls SetOutputSinks on the
// ProcessManager once it's constructed inside Run. Used by the TUI runtime.
func WithProcessOutput(stdout, stderr io.Writer) Option {
	return func(r *Runner) {
		r.procStdout = stdout
		r.procStderr = stderr
	}
}

// WithDockerLogSinks subscribes one writer per `[[dev.docker_compose]]`
// entry to that stack's `docker compose logs -f` output. Keys in the map
// are the same `name` field hamr.toml uses; entries without a writer are
// skipped (no follower spawned).
//
// The runner manages follower lifetime: started once an entry has been
// brought up, restarted automatically if the follower exits early
// (typically because `docker compose down -v` from a wipe killed it),
// stopped on shutdown via the runner ctx.
func WithDockerLogSinks(sinks map[string]io.Writer) Option {
	return func(r *Runner) { r.dockerLogSinks = sinks }
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

// WithHotkeys wires the bubbletea-backed HotkeySource that feeds q / r / o
// into Run's event loop. The TUI runtime owns the source's lifecycle.
func WithHotkeys(h HotkeySource) Option {
	return func(r *Runner) { r.hotkeys = h }
}

// WithActionsHook registers a callback that fires once Run has constructed
// its DevActions object. The TUI uses this to capture a reference for
// dispatching actions (e.g. docker wipe) that aren't expressible through the
// scalar HotkeyAction enum. The hook fires on the runner goroutine; copy the
// pointer and return — do not block.
func WithActionsHook(fn func(*DevActions)) Option {
	return func(r *Runner) { r.actionsHook = fn }
}

// WithProxyURLHook registers a callback that fires once the reverse proxy
// has bound (after any +1-on-busy port walking) so the caller can publish
// the actual reachable URL to its UI surface. The TUI runtime uses this to
// push the URL into a bubbletea message. The hook fires on the runner
// goroutine; copy the string and return — do not block.
func WithProxyURLHook(fn func(string)) Option {
	return func(r *Runner) { r.proxyURLHook = fn }
}

// WithMCPStatusHook registers a callback that receives the MCP gateway's state
// (enabled, exposed-tool count) at startup and on every M-toggle, so the TUI
// can render its indicator. Fires on the runner goroutine; do not block.
func WithMCPStatusHook(fn func(enabled bool, tools int)) Option {
	return func(r *Runner) { r.mcpStatusHook = fn }
}

// WithMCPLogHook registers a callback that receives a one-line summary of every
// MCP request the gateway handles, so the TUI can render a dedicated MCP tab.
// Fires on the gateway's request goroutine; do not block.
func WithMCPLogHook(fn func(line string)) Option {
	return func(r *Runner) { r.mcpLogHook = fn }
}

// NewRunner creates a new Runner with the given config and options.
func NewRunner(cfg *Config, opts ...Option) *Runner {
	r := &Runner{cfg: cfg}
	for _, opt := range opts {
		opt(r)
	}
	if r.logger == nil {
		r.logger = newDevLogger(r.baseLogWriter(), r.verbose)
	}
	if r.hotkeys == nil {
		r.hotkeys = noopHotkeySource{}
	}
	return r
}

// baseLogWriter returns the writer the slog handler should use.
func (r *Runner) baseLogWriter() io.Writer {
	if r.logWriter != nil {
		return r.logWriter
	}
	return os.Stderr
}

// buildHamrInjectedEnv returns env-var KEY=VALUE pairs that hamr dev sets
// on every spawned rule process so the scaffolded site (or any rule) can
// discover hamr-served URLs (and the chosen app port) without hardcoding
// values. The proxyOrigin and appPort arguments are the *actual* values
// the runner has bound or probed — they may differ from `hamr.toml` when
// [dev].port_walk has shifted a busy port +1.
//
// Currently emits:
//   - PORT: spawned app's listen port. Always set when the proxy is
//     configured so the app and the proxy.target stay aligned even after a
//     +1 walk. Apps read PORT to know where to listen; this is hamr taking
//     ownership of that value when it controls the proxy.
//   - HAMR_DEV_URL: proxy origin, set when [dev.email], [dev.sms], OR
//     [dev.stripe] is enabled (the email/SMS mocks use it as their ingest
//     target; the scaffold's emailmock.New(envHamrDevURL) reads it, as does
//     smsmock.New).
//   - HAMR_STRIPE_MOCK_URL: proxy origin, set only when [dev.stripe].enabled.
//     Scaffolded main.go points stripe-go at this URL when STRIPE_MOCK=true.
//
// godotenv.Load() in the spawned site honors pre-set env vars (doesn't
// overwrite), so these injected values win over any conflicting entry in
// the user's .env — by design. Hamr knows its own proxy URL and the app
// port it has chosen; nothing in .env should be more authoritative.
func buildHamrInjectedEnv(cfg *Config, proxyOrigin string, appPort int) []string {
	if !cfg.ProxyConfigured {
		return nil
	}
	var injected []string
	if appPort > 0 {
		injected = append(injected, fmt.Sprintf("PORT=%d", appPort))
	}
	if proxyOrigin != "" {
		if cfg.Dev.Email.Enabled || cfg.Dev.SMS.Enabled || cfg.Dev.Stripe.Enabled {
			injected = append(injected, "HAMR_DEV_URL="+proxyOrigin)
		}
		if cfg.Dev.Stripe.Enabled {
			injected = append(injected, "HAMR_STRIPE_MOCK_URL="+proxyOrigin)
		}
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
	// devOut is the writer the slog handler emits through (terminal or
	// TUI sink) plus the rolling file fan-out when enabled. The browser-
	// console sink shares it so [site:console] lines interleave with
	// backend events in arrival order, in both TUI tab 0 and dev_logs.txt.
	devOut := io.Writer(r.baseLogWriter())
	if fileLog != nil {
		defer func() { _ = fileLog.Close() }()
		devOut = io.MultiWriter(r.baseLogWriter(), fileLog)
		r.logger = newDevLogger(devOut, r.verbose)
	}
	// Browser-console transport is opt-out via [dev].hamr_console_capture.
	// When disabled, we leave consoleSink nil so NewProxyHandler skips the
	// /__hamr/console route — the JS side reads the same flag from the
	// SSE config payload and won't attempt to connect.
	consoleCapture := r.cfg.Dev.HamrConsoleCaptureEnabled()
	var consoleSink *ConsoleSink
	if consoleCapture {
		consoleSink = NewConsoleSink(devOut, r.cfg.Dev.HamrConsoleFilter)
	}

	graph := NewGraph(r.cfg.Dev.Watch)
	pm := NewProcessManager(r.logger)
	if r.procStdout != nil || r.procStderr != nil {
		pm.SetOutputSinks(r.procStdout, r.procStderr)
	}
	broker := NewSSEBroker(r.cfg.Dev.Watch, r.cfg.Dev.Daemons, r.cfg.Dev.DockerCompose, r.cfg.Dev.Email.Enabled, r.cfg.Dev.SMS.Enabled, r.cfg.Dev.Stripe.Enabled, consoleCapture)
	errorState := NewErrorState()
	logBuf := NewLogBuffer(1000)
	requestLog := NewRequestLog(1000)
	pm.SetLogOutput(logBuf, broker)
	// Injected env (PORT, HAMR_DEV_URL, HAMR_STRIPE_MOCK_URL) is set further
	// down once the proxy listener has bound and we know the actual ports —
	// hamr's port_walk may have shifted them above the configured defaults.
	if fileLog != nil {
		pm.SetFileLog(fileLog)
	}
	pm.OnProcessExit = func(rule string, err error, output string) {
		errorState.Set(rule, output)
		broker.Broadcast(buildErrorEvent(rule, output))
	}

	// Clear any stale walks.json from a previous run before doing work
	// that can fail. The canonical writeWalks below will overwrite this
	// on success; if startup fails before reaching that point, consumers
	// (`hamr env`, `hamr sync`, scaffold Makefile) see "no walks
	// recorded" and fall through to literal .env values rather than
	// replaying yesterday's rewrites against today's broken state.
	if err := writeWalks(".", nil); err != nil {
		r.logger.Warn("failed to clear stale walks file", "err", err)
	}

	var schedulerWG sync.WaitGroup
	var followersWG sync.WaitGroup
	var watcher *Watcher
	configReloadCh := make(chan struct{}, 1)
	var configReload bool

	// Start reverse proxy.
	var proxySrv *http.Server
	defer func() {
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
		// Followers are tied to runCtx (exec.CommandContext), so cancel
		// above already triggered SIGKILL — just drain.
		followersWG.Wait()
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
		// Close the MCP audit log only after the proxy has stopped serving, so a
		// late in-flight tool call can't write to an already-closed audit file.
		if r.mcpGateway != nil {
			r.mcpGateway.closeAudit()
		}
	}()

	// Build scheduler state up front so manual runs (POST /run, hotkey rebuild)
	// can enqueue through the single scheduler goroutine rather than starting
	// processes directly. requestRun mirrors the file-watcher event path below.
	dirty := make(map[string]FileEvent, len(r.cfg.Dev.Watch))
	var dirtyMu sync.Mutex
	scheduleCh := make(chan struct{}, 1)

	actions := &DevActions{
		ctx: runCtx, cfg: r.cfg, pm: pm, broker: broker,
		errorState: errorState, graph: graph, logger: r.logger,
		requestRun: func(rule *WatchRule) {
			dirtyMu.Lock()
			dirty[rule.Name] = FileEvent{Rule: rule}
			dirtyMu.Unlock()
			select {
			case scheduleCh <- struct{}{}:
			default:
			}
		},
	}
	if r.actionsHook != nil {
		r.actionsHook(actions)
	}

	// Hotkey actions flow in from the TUI's bubbletea-backed source. We
	// drain them during startup (docker compose, initial build) so q
	// quits even before the main event loop is up.
	hotkeys := r.hotkeys
	startupDone := make(chan struct{})
	startupExited := make(chan struct{})
	go func() {
		defer close(startupExited)
		for {
			select {
			case action := <-hotkeys.Actions():
				if r.handleHotkey(action, actions, cancel) {
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

	// SMS mock: same proxy gating as the mail mock.
	var smsMock *SMSMock
	if r.cfg.Dev.SMS.Enabled {
		if !r.cfg.ProxyConfigured {
			return fmt.Errorf("dev.sms.enabled = true requires a [proxy] section in hamr.toml (the SMS UI lives on the proxy mux)")
		}
		if r.noProxy {
			return fmt.Errorf("dev.sms.enabled = true cannot be used with --no-proxy (the SMS UI lives on the proxy mux)")
		}
		opts := SMSMockOptions{
			MaxMessages: r.cfg.Dev.SMS.MaxMessages,
			OnPersistError: func(err error) {
				r.logger.Warn("sms mock persistence error", "err", err)
			},
		}
		if r.cfg.Dev.SMS.PersistEnabled() {
			opts.PersistPath = r.cfg.Dev.SMS.ResolvedPersistPath()
		}
		smsMock = NewSMSMock(opts)
		r.logger.Info("sms mock enabled",
			"ui", "/__hamr/sms",
			"ingest", "/__hamr/sms/ingest",
			"max_messages", r.cfg.Dev.SMS.MaxMessages,
			"persist", opts.PersistPath,
		)
	}

	// Stripe mock validation. The mock is constructed below — once we know
	// the actual proxy port — so its BaseURL reflects any +1-on-busy walk
	// rather than the originally-configured listen value.
	if r.cfg.Dev.Stripe.Enabled {
		if !r.cfg.ProxyConfigured {
			return fmt.Errorf("dev.stripe.enabled = true requires a [proxy] section in hamr.toml (the Stripe API + UI live on the proxy mux)")
		}
		if r.noProxy {
			return fmt.Errorf("dev.stripe.enabled = true cannot be used with --no-proxy (the Stripe API + UI live on the proxy mux)")
		}
	}

	// Resolve actual ports and stand up the proxy. listenWalk binds the
	// proxy listener (walking +1 on EADDRINUSE up to a small cap when
	// [dev].port_walk is on); probeFreePort picks a free app port the same
	// way. Both walks log a WARN per shift so two `hamr dev` instances on
	// the same machine surface the conflict instead of silently colliding.
	//
	// The cfg is mutated in memory so downstream code (mock URL derivation,
	// waitForTarget, status bar) reads the actual values rather than the
	// originally-configured ones. Persisting these to hamr.toml is out of
	// scope — the next run probes again.
	var (
		stripeMock        *StripeMock
		proxyOrigin       string
		actualAppPort     int
		originalAppPort   int
		actualProxyPort   int
		originalProxyPort int
	)
	if r.cfg.Dev.MCP.Enabled && (r.noProxy || !r.cfg.ProxyConfigured) {
		r.logger.Warn("[dev.mcp] enabled but no reverse proxy will run (needs [proxy] and not --no-proxy); the MCP gateway is unavailable")
	}
	if !r.noProxy && r.cfg.ProxyConfigured {
		if _, p, perr := splitListenAddr(r.cfg.Proxy.Listen); perr == nil {
			originalProxyPort = p
		}
		ln, proxyPort, err := listenWalk(r.cfg.Proxy.Listen, portWalkAttempts(r.cfg), r.logger)
		if err != nil {
			return fmt.Errorf("start proxy: %w", err)
		}
		actualProxyPort = proxyPort

		targetHost, targetStartPort, parseErr := splitListenAddr(r.cfg.Proxy.Target)
		if parseErr != nil {
			_ = ln.Close()
			return fmt.Errorf("parse proxy.target %q: %w", r.cfg.Proxy.Target, parseErr)
		}
		probeHost := targetHost
		if probeHost == "" {
			probeHost = "127.0.0.1"
		}
		actualAppPort, err = probeFreePort(probeHost, targetStartPort, portWalkAttempts(r.cfg), nil, r.logger)
		if err != nil {
			_ = ln.Close()
			return fmt.Errorf("probe app port: %w", err)
		}
		originalAppPort = targetStartPort

		// Mutate cfg in memory so the rest of Run sees the actual port pair.
		// The original listen host (":3000" / "0.0.0.0:3000" / etc.) is
		// preserved — only the port shifts.
		listenHost := ""
		if h, _, lerr := splitListenAddr(r.cfg.Proxy.Listen); lerr == nil {
			listenHost = h
		}
		r.cfg.Proxy.Listen = joinHostPort(listenHost, proxyPort)
		r.cfg.Proxy.Target = joinHostPort(targetHost, actualAppPort)

		proxyOrigin = proxyClientBaseURLFromPort(proxyPort)
		r.proxyURL = proxyOrigin

		// Stripe mock construction is gated on Enabled — when disabled the
		// pointer stays nil and NewProxyHandler skips its routes.
		if r.cfg.Dev.Stripe.Enabled {
			stripeLogger := r.logger.With("component", "stripe")
			opts := StripeMockOptions{
				BaseURL: proxyOrigin,
				Logger:  stripeLogger,
				OnPersistError: func(err error) {
					stripeLogger.Warn("persistence error", "err", err)
				},
			}
			if r.cfg.Dev.Stripe.PersistEnabled() {
				opts.PersistPath = r.cfg.Dev.Stripe.ResolvedPersistPath()
			}
			stripeMock = NewStripeMock(opts)
			webhookURL := rewriteWebhookURLForAppPort(r.cfg.Dev.Stripe.WebhookURL, originalAppPort, actualAppPort)
			stripeMock.SetWebhookEndpoint(WebhookEndpoint{
				URL:    webhookURL,
				Secret: r.cfg.Dev.Stripe.WebhookSecret,
			})
			persistPath := ""
			if r.cfg.Dev.Stripe.PersistEnabled() {
				persistPath = r.cfg.Dev.Stripe.ResolvedPersistPath()
			}
			stripeLogger.Info("mock enabled",
				"api", r.cfg.Proxy.Listen+"/v1/*",
				"ui", r.cfg.Proxy.Listen+"/__hamr/stripe/*",
				"webhook_url", webhookURL,
				"persist", persistPath,
			)
		}

		// MCP gateway: the token-gated /__hamr/mcp/ namespace the `hamr mcp`
		// bridge drives. Always constructed when the proxy is up so the TUI
		// kill-switch (M) can flip it on at runtime; the handshake file (.hamr/
		// dev.json) and audit log are only activated when enabled (initially
		// from [dev.mcp].enabled).
		// Resolve the project root from the config path so the handshake file
		// lands next to hamr.toml — matching where the bridge looks for it,
		// regardless of the dev server's working directory.
		mcpProjectRoot := "."
		if r.configPath != "" {
			if abs, aerr := filepath.Abs(r.configPath); aerr == nil {
				mcpProjectRoot = filepath.Dir(abs)
			}
		}
		mcpGw, gwErr := newMCPGateway(mcpGatewayDeps{
			cfg:         r.cfg,
			ctx:         runCtx,
			projectRoot: mcpProjectRoot,
			actions:     actions,
			logBuf:      logBuf,
			mailMock:    mailMock,
			smsMock:     smsMock,
			stripeMock:  stripeMock,
			errorState:  errorState,
			auditPath:   r.cfg.Dev.MCP.ResolvedLogFile(),
			logSink:     r.mcpLogHook,
			proxyURL:    proxyOrigin,
			appPort:     actualAppPort,
			makefile:    "Makefile",
			consoleSink: consoleSink,
			requestLog:  requestLog,
			logger:      r.logger,
		})
		if gwErr != nil {
			_ = ln.Close()
			return fmt.Errorf("start mcp gateway: %w", gwErr)
		}
		r.mcpGateway = mcpGw
		// Remove the handshake on shutdown. The audit log is closed by the proxy
		// shutdown defer instead (after the proxy stops serving) so an in-flight
		// tool call can't write to a closed audit file.
		defer mcpGw.removeHandshake()
		if r.cfg.Dev.MCP.Enabled {
			if err := mcpGw.SetActive(true); err != nil {
				r.logger.Error("failed to activate mcp gateway", "err", err)
			} else {
				r.logger.Info("mcp gateway enabled", "tools", mcpGw.EnabledToolCount(), "audit", r.cfg.Dev.MCP.ResolvedLogFile())
			}
		}
		// Publish initial MCP state to the TUI indicator.
		if r.mcpStatusHook != nil {
			r.mcpStatusHook(mcpGw.IsEnabled(), mcpGw.EnabledToolCount())
		}

		inject := r.cfg.Proxy.InjectReload != nil && *r.cfg.Proxy.InjectReload
		handler := NewProxyHandler(r.cfg.Proxy.Target, broker, errorState, logBuf, actions, mailMock, smsMock, stripeMock, consoleSink, mcpGw, requestLog, inject)
		proxySrv = serveProxy(ln, handler)

		// Single-line banner that surfaces the actual reachable URL +
		// app port. With port_walk on, this is the one place the user
		// sees what hamr picked when something was busy.
		r.logger.Info("hamr dev URL", "url", proxyOrigin, "app", normalizeHost(r.cfg.Proxy.Target))

		// Publish the URL to the TUI runtime (when wired).
		if r.proxyURLHook != nil {
			r.proxyURLHook(proxyOrigin)
		}
	}

	// Ensure docker compose services are running before build. Walks from
	// each entry are accumulated so we can persist a single walks.json and
	// build the merged dotenv injection once all walks are known.
	var composeShifts []portShift
	composeShiftToEntry := make(map[int]string)
	for i := range r.cfg.Dev.DockerCompose {
		if runCtx.Err() != nil {
			return nil
		}
		dc := &r.cfg.Dev.DockerCompose[i]
		r.logger.Info("ensuring docker compose", "name", dc.Name, "file", dc.File)
		output, shifts, err := r.ensureDockerCompose(runCtx, dc)
		for _, s := range shifts {
			composeShifts = append(composeShifts, s)
			composeShiftToEntry[len(composeShifts)-1] = dc.Name
		}
		if err != nil {
			r.logger.Error("docker compose failed", "name", dc.Name, "err", err)
			errorState.Set(dc.Name, output)
			broker.Broadcast(buildErrorEvent(dc.Name, output))
		} else {
			errorState.Clear(dc.Name)
		}
		// Spawn the per-entry `compose logs -f` follower after the up
		// has been issued. The follower self-restarts on early exit
		// (typically because dockerWipe ran `down -v`) until runCtx
		// closes. Entries the TUI didn't register a sink for are
		// skipped silently.
		if sink, ok := r.dockerLogSinks[dc.Name]; ok && sink != nil {
			followersWG.Add(1)
			go r.followDockerLogs(runCtx, dc, sink, &followersWG)
		}
	}

	// Persist walk record + build the merged injected env. walks.json is
	// the source of truth for `hamr env` / `hamr sync` invoked outside the
	// dev process; the in-process injection below feeds spawned children
	// (site daemon, sync-static daemon, etc.). Both paths share the same
	// rewrite engine — driven by the same shifts list — so the values
	// they emit are guaranteed identical.
	walkRecords := buildWalkRecords(originalProxyPort, actualProxyPort, originalAppPort, actualAppPort, composeShifts, composeShiftToEntry)
	if err := writeWalks(".", walkRecords); err != nil {
		r.logger.Warn("failed to persist walks file", "err", err)
	}
	envRewrites, rewriteErr := resolveDotenvInjection(".env", shiftsToMap(walkRecords))
	if rewriteErr != nil {
		r.logger.Warn("failed to resolve .env rewrites", "err", rewriteErr)
	}
	for _, rewrite := range envRewrites {
		r.logger.Info("walked .env value", "rewrite", rewrite)
	}
	// SetInjectedEnv must precede the first StartProcess — initial-build
	// is below so this placement is safe. Compose ensure above doesn't
	// consume pm.injectedEnv (it builds its own env from dc.Env), so
	// moving the call from the old pre-loop position to here doesn't
	// regress anything.
	//
	// Order matters: envRewrites first, buildHamrInjectedEnv last. buildEnv
	// is last-wins on key conflict, so framework-owned vars (PORT,
	// HAMR_DEV_URL, HAMR_STRIPE_MOCK_URL) authoritatively beat any .env
	// rewrite that happens to share a key — important when a user has e.g.
	// PORT=:8080 in .env and the whole-value rule would otherwise emit
	// PORT=:8081, breaking int-parsing in the scaffolded main.go.
	merged := append([]string(nil), envRewrites...)
	merged = append(merged, buildHamrInjectedEnv(r.cfg, proxyOrigin, actualAppPort)...)
	pm.SetInjectedEnv(merged)

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
		rule := &WatchRule{Name: d.Name, Run: d.Cmd, Dir: d.Dir, Env: d.Env}
		if err := pm.StartProcess(runCtx, rule); err != nil {
			r.logger.Error("failed to start daemon", "daemon", d.Name, "err", err)
		}
	}

	// If no watch rules, just block until shutdown or config reload.
	if len(r.cfg.Dev.Watch) == 0 {
		r.logger.Info("no watch rules, running daemons only")
		r.logger.Info("ready")
		r.ready.Store(true)
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
			case action := <-hotkeys.Actions():
				if r.handleHotkey(action, actions, cancel) {
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
	r.ready.Store(true)
	close(startupDone)
	<-startupExited

	// Watch the config file for changes.
	if r.configPath != "" {
		go r.watchConfigFile(runCtx, configReloadCh)
	}

	// Single scheduler goroutine coalesces bursts and executes in topological
	// order to preserve dependency semantics.
	schedulerWG.Go(func() {
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
				// Abandon the queue the instant shutdown begins — don't launch
				// further builds. Without this, a runaway watch loop (e.g. a rule
				// whose output lands in its own watched dir) keeps re-filling
				// `dirty` and the drain never converges, so schedulerWG.Wait()
				// blocks shutdown and `q`/Ctrl-C appear to hang.
				select {
				case <-runCtx.Done():
					return
				default:
				}

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
	})

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
		case action := <-hotkeys.Actions():
			if r.handleHotkey(action, actions, cancel) {
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
func (r *Runner) handleHotkey(action HotkeyAction, actions *DevActions, cancel context.CancelFunc) bool {
	// Until the dev server is ready, only quit is honored. The startup
	// hotkey-drain goroutine runs concurrently with Run() binding the proxy, so
	// gating here both (a) avoids opening a not-yet-listening URL / rebuilding
	// before the scheduler is up, and (b) keeps the drain goroutine from reading
	// proxyURL / cfg.Proxy.* while Run() is still writing them.
	if action != HotkeyQuit && !r.ready.Load() {
		return false
	}
	switch action {
	case HotkeyRebuild:
		r.logger.Info("rebuilding all rules")
		go actions.RebuildAll()
	case HotkeyOpenBrowser:
		// Prefer the URL the proxy actually bound to (post +1 walk) so the
		// browser opens at the right place even when [dev].port_walk
		// shifted the port off the configured default. Falls back to the
		// configured listen value when the proxy isn't running yet.
		switch {
		case r.proxyURL != "":
			r.logger.Info("opening browser", "url", r.proxyURL)
			openBrowser(r.proxyURL)
		case r.cfg.ProxyConfigured:
			url := "http://" + normalizeHost(r.cfg.Proxy.Listen)
			r.logger.Info("opening browser", "url", url)
			openBrowser(url)
		default:
			r.logger.Warn("no proxy configured, cannot open browser")
		}
	case HotkeyMCPToggle:
		if r.mcpGateway == nil {
			r.logger.Warn("MCP unavailable (no reverse proxy running)")
			return false
		}
		on, err := r.mcpGateway.Toggle()
		if err != nil {
			r.logger.Error("MCP toggle failed", "err", err)
			return false
		}
		r.logger.Info("MCP gateway toggled", "enabled", on, "tools", r.mcpGateway.EnabledToolCount())
		if r.mcpStatusHook != nil {
			r.mcpStatusHook(on, r.mcpGateway.EnabledToolCount())
		}
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
			// The per-developer override lives beside the config and is
			// merged into it, so a change there is a config change.
			if b := filepath.Base(event.Name); b != base && b != PrefsFileName {
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

func composeArgs(dc *DockerCompose) []string {
	args := composeArgsForInspect(dc)
	override := composeOverridePath(dc.Name)
	if info, err := os.Stat(override); err == nil && !info.IsDir() {
		args = append(args, "-f", override)
	}
	return args
}

// composeArgsForInspect returns base-only compose args (no generated
// override). Used for the preflight `compose ps` so a stale override
// referencing services that have since been renamed or removed in the
// base file can't poison config-merge before we get a chance to
// reconcile it. Project identity comes from --project-directory, which
// is what compose uses to label and find the project's containers.
func composeArgsForInspect(dc *DockerCompose) []string {
	projectDir := filepath.Dir(dc.File)
	if projectDir == "" {
		projectDir = "."
	}
	return []string{"compose", "--project-directory", projectDir, "-f", dc.File}
}

// ensureDockerCompose ensures a compose entry's services are running,
// adopting the existing stack when it's already up + ready and only
// running `compose up -d` when something is missing or unhealthy.
// Returns combined output (for error display) and the list of port
// shifts (walk-driven for missing services + drift-derived for adopted
// peers) — callers thread them into walks.json + dotenv injection so
// spawned processes and `hamr env` consumers see the rewritten URLs.
//
// Decision tree:
//
//   - inspect via `docker compose ps --format json` (hard-fail on docker
//     errors so a missing daemon aborts hamr dev with a surfaced error
//     instead of silently rebuilding state)
//   - if every expected service is running + ready → adopt: derive
//     shifts from actual published ports vs. base compose; do NOT touch
//     the override file; do NOT run `up -d`
//   - otherwise → apply: walk only the non-adopted services (peers'
//     ports stay unchanged), pass the project's owned ports into the
//     walk so probes against our own ports return as-is, run `up -d`
//
// Override file management is state-aware: an existing override is only
// removed when the project has zero running containers AND walk
// produced no shifts. With anything running we leave it alone — auto-
// removing risks recreating peers that are already up on walked ports.
func (r *Runner) ensureDockerCompose(ctx context.Context, dc *DockerCompose) (string, []portShift, error) {
	state, err := r.inspectRunningCompose(ctx, dc)
	if err != nil {
		return "", nil, err
	}

	services, err := parseComposePorts(dc.File)
	if err != nil {
		return "", nil, err
	}
	expected := expectedServiceNames(dc, services)

	if len(expected) > 0 && allServicesAdopted(expected, state.Adopted) {
		// Adopt path: stack is up + ready. Compose ps is the source of
		// truth for actual published ports; derive shifts vs. base for
		// env injection.
		shifts := stateShiftsForServices(services, state.Publishers, nil)
		r.logger.Info("docker compose adopted (services already running)",
			"name", dc.Name, "services", expected)
		// Each shift here is a divergence between the running container
		// and the current base compose declaration. It can be a leftover
		// walk from a prior session (benign, env injection rewrites
		// consumers) OR a config edit the user made since the stack was
		// started (their change won't take effect until they wipe). Warn
		// so a config-edit case isn't silently swallowed.
		for _, s := range shifts {
			r.logger.Warn("docker compose adopted on non-base port (declared port unused; wipe stack to apply)",
				"name", dc.Name, "service", s.Service, "declared", s.Old, "running", s.New)
		}
		// Reconcile the override file even though we won't run `up -d`:
		// downstream TUI/CLI actions (logs, restart, wipe, down) call
		// composeArgs(dc) which includes the override. Without
		// reconciliation here:
		//   - a stale override referencing services dropped from the
		//     base file would break those follow-up commands' config
		//     merge (compose ps doesn't see the override but everything
		//     else does);
		//   - an adopted stack running on walked ports with the
		//     override missing (e.g. cleaned .hamr/, switched worktrees)
		//     would have wipe/restart fall back to base ports and clash.
		override := composeOverridePath(dc.Name)
		if err := manageComposeOverride(override, services, state, shifts, nil); err != nil {
			return "", nil, err
		}
		return "", shifts, nil
	}

	// Apply path: at least one expected service is missing or unhealthy.
	override := composeOverridePath(dc.Name)
	var walkShifts []portShift
	walkedByService := make(map[string]composeService)
	running := runningServiceSet(state)
	if r.cfg.Dev.PortWalkEnabled() {
		toWalk := servicesNeedingWalk(services, running)
		walked, shifts := walkComposeServices(toWalk, state.Owned, portWalkAttempts(r.cfg), r.logger)
		walkShifts = shifts
		for _, svc := range walked {
			walkedByService[svc.Name] = svc
		}
		for _, shift := range walkShifts {
			r.logger.Warn("docker compose port walked",
				"name", dc.Name,
				"service", shift.Service,
				"from", shift.Old,
				"to", shift.New,
			)
		}
	}

	// Combine shifts: walk-driven (for non-adopted services) + drift-
	// derived (for adopted peers, so env injection reflects ports they
	// were walked to in a prior session).
	combined := combinedComposeShifts(services, state, walkShifts)

	if err := manageComposeOverride(override, services, state, combined, walkedByService); err != nil {
		return "", nil, err
	}

	args := append(composeArgs(dc), "up", "-d")
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
		return buf.String(), combined, fmt.Errorf("docker compose up failed: %w", err)
	}
	return "", combined, nil
}

// combinedComposeShifts merges walk-derived shifts (for services we
// just walked because they weren't running) with drift-derived shifts
// (for any running peer — adopted, unhealthy, or starting — whose
// actual published port differs from base compose). The result is what
// env injection consumes; every service whose host port differs from
// its base compose declaration appears once.
//
// Drift derivation keys off "service is running" (has a publisher),
// not "is adopted". A running-unhealthy peer holds a real port and the
// app env must reflect it — the alternative (filtering to Adopted only)
// would silently drop its shift and leave consumers pointed at the
// base port while the actual container sits elsewhere.
func combinedComposeShifts(services []composeService, state composeStackState, walkShifts []portShift) []portShift {
	if len(state.Publishers) == 0 {
		return walkShifts
	}
	out := append([]portShift(nil), walkShifts...)
	out = append(out, stateShiftsForServices(services, state.Publishers, runningServiceSet(state))...)
	return out
}

// runningServiceSet collapses state.Publishers into the set of service
// names that have at least one publisher. Used as the "only" filter for
// stateShiftsForServices so drift derivation considers every running
// peer, not just the adopted subset.
func runningServiceSet(state composeStackState) map[string]bool {
	out := make(map[string]bool, len(state.Publishers))
	for _, p := range state.Publishers {
		out[p.Service] = true
	}
	return out
}

// servicesNeedingWalk returns the subset of services whose ports the
// apply path should attempt to walk. Running services (adopted, unhealthy,
// or starting) are excluded — their ports are held by the container
// regardless of health, so a walk would diff against base and emit a
// shift for the same service that combinedComposeShifts already covers
// from compose ps. Two shifts for one service would land in walks.json
// and the rewrite map would resolve last-write-wins.
func servicesNeedingWalk(services []composeService, running map[string]bool) []composeService {
	out := make([]composeService, 0, len(services))
	for _, svc := range services {
		if running[svc.Name] {
			continue
		}
		out = append(out, svc)
	}
	return out
}

// manageComposeOverride decides whether to write or remove the per-
// entry override file based on the apply-path's combined shift set.
//
//   - If anything drifts from base (walk shifts OR running peers on
//     non-base ports), write an override capturing every drifted
//     service. This includes running peers so `compose up -d` doesn't
//     see drift and recreate them.
//   - If nothing drifts, remove any stale override. Running peers on
//     base ports don't need an override; running peers on non-base
//     ports always produce a state-derived shift (non-empty combined),
//     so the empty-combined branch can never strand a real running
//     mapping. A stale override left on disk would otherwise force
//     `compose up -d` to recreate a stopped service on the previously-
//     walked port instead of returning it to base.
func manageComposeOverride(override string, services []composeService, state composeStackState, combined []portShift, walkedByService map[string]composeService) error {
	if len(combined) > 0 {
		updated := overrideServices(services, state, walkedByService)
		affected := make(map[string]bool, len(combined))
		for _, s := range combined {
			affected[s.Service] = true
		}
		return writeComposeOverride(override, updated, affected)
	}
	_ = os.Remove(override)
	return nil
}

// overrideServices builds the services list that the override file
// emits. Per service, the rendered HostPort comes from the first
// applicable source:
//
//  1. **Running** (has a publisher in state.Publishers — adopted,
//     unhealthy, or starting): use the actual published port. This
//     preserves the running container's binding so `compose up -d`
//     doesn't see drift and recreate it. Running-unhealthy stays put;
//     the user wipes to force a rebuild.
//  2. **Walked**: use the walk result. Walk only operates on non-
//     running services (adopted services are excluded from toWalk;
//     non-adopted-but-running services have their ports in the owned
//     set so the walk wouldn't shift them anyway).
//  3. **Base compose**: fallback when neither running nor walked.
//
// Per-binding port resolution for case (1) goes through
// resolvedPortsForService so services with ambiguous container-port
// mappings (or older compose ps output) still get sensible per-binding
// ports rather than dropping to base.
//
// Services without published ports (workers, etc.) round-trip via case
// (3) but never appear in the override emission — writeComposeOverride
// filters by `affected`, which is keyed off shift records that only
// exist for ported services.
func overrideServices(services []composeService, state composeStackState, walkedByService map[string]composeService) []composeService {
	running := runningServiceSet(state)

	out := make([]composeService, len(services))
	for i, svc := range services {
		if running[svc.Name] {
			ports := append([]composePortBinding(nil), svc.Ports...)
			resolved := resolvedPortsForService(svc.Name, svc.Ports, state.Publishers)
			for j, p := range ports {
				if p.HostPort == 0 {
					continue
				}
				if got, ok := resolved[j]; ok {
					ports[j].HostPort = got
				}
			}
			out[i] = composeService{Name: svc.Name, Ports: ports}
			continue
		}
		if walked, ok := walkedByService[svc.Name]; ok {
			out[i] = composeService{Name: svc.Name, Ports: append([]composePortBinding(nil), walked.Ports...)}
			continue
		}
		out[i] = composeService{Name: svc.Name, Ports: append([]composePortBinding(nil), svc.Ports...)}
	}
	return out
}

// followDockerLogs streams `docker compose logs -f` for one entry into
// the supplied sink for as long as runCtx is alive. It auto-restarts on
// unexpected exit so a `down -v` from a wipe doesn't permanently sever
// the stream — once `up -d` brings containers back, the next iteration
// of the loop re-attaches.
//
// `--ansi=always` is the top-level compose flag (must precede the
// subcommand) that forces ANSI colour codes through even though we're
// piping into a Go writer rather than attaching a TTY — without it,
// compose auto-detects no-TTY and strips colour, leaving the docker
// tab a wall of grey. The bubbles viewport renders ANSI content
// correctly, so the per-service colour prefixes and any container ANSI
// pass through to the screen unchanged. `--tail=50` gives a useful
// backlog without flooding the buffer on attach.
func (r *Runner) followDockerLogs(runCtx context.Context, dc *DockerCompose, sink io.Writer, wg *sync.WaitGroup) {
	defer wg.Done()

	args := append([]string{}, composeArgs(dc)...)
	args = append(args[:1], append([]string{"--ansi=always"}, args[1:]...)...)
	args = append(args, "logs", "-f", "--tail=50")
	if len(dc.Services) > 0 {
		args = append(args, dc.Services...)
	}

	const restartBackoff = 2 * time.Second
	for runCtx.Err() == nil {
		cmd := exec.CommandContext(runCtx, "docker", args...)
		cmd.Stdout = sink
		cmd.Stderr = sink
		cmd.Env = buildEnv(dc.Env)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.WaitDelay = 2 * time.Second

		if err := cmd.Start(); err != nil {
			r.logger.Warn("docker logs follower start failed", "name", dc.Name, "err", err)
		} else {
			_ = cmd.Wait()
		}
		if runCtx.Err() != nil {
			return
		}
		// The follower exited but we're still alive — back off briefly
		// then retry. Common cause: a wipe just brought the project
		// down; up -d is in flight, the next attempt will pick up the
		// new containers.
		select {
		case <-runCtx.Done():
			return
		case <-time.After(restartBackoff):
		}
	}
}

// stopDockerCompose runs "docker compose down" for a compose entry during shutdown.
func (r *Runner) stopDockerCompose(dc *DockerCompose) {
	r.logger.Info("stopping docker compose", "name", dc.Name)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := append(composeArgs(dc), "down")
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
	_ = os.Remove(composeOverridePath(dc.Name))
}

// ComposeArgs returns the `docker` arguments hamr itself uses for a compose
// entry — project directory, base file, and the generated port-walk override
// when one exists. Exported so `hamr compose` can hand external callers the
// same merged config the dev server is running, instead of them merging the
// base file alone and reconciling the stack back onto un-walked ports.
//
// Paths are relative to the project root, so the caller must run docker from
// there.
func ComposeArgs(dc *DockerCompose) []string {
	return composeArgs(dc)
}
