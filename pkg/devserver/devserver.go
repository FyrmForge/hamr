package devserver

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"
)

const schedulerBatchWindow = 20 * time.Millisecond

// Runner is the top-level dev server orchestrator.
type Runner struct {
	cfg     *Config
	logger  *slog.Logger
	verbose bool
	noProxy bool
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

// NewRunner creates a new Runner with the given config and options.
func NewRunner(cfg *Config, opts ...Option) *Runner {
	r := &Runner{cfg: cfg}
	for _, opt := range opts {
		opt(r)
	}
	if r.logger == nil {
		level := slog.LevelInfo
		if r.verbose {
			level = slog.LevelDebug
		}
		r.logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	}
	return r
}

// Run starts the dev server and blocks until ctx is cancelled.
func (r *Runner) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	graph := NewGraph(r.cfg.Dev.Watch)
	pm := NewProcessManager(r.logger)
	broker := NewSSEBroker()
	var schedulerWG sync.WaitGroup
	var watcher *Watcher

	// Start reverse proxy.
	var proxySrv *http.Server
	defer func() {
		r.logger.Info("shutting down")
		cancel()
		if watcher != nil {
			watcher.Stop()
		}
		schedulerWG.Wait()
		pm.StopAll()
		if proxySrv != nil {
			_ = proxySrv.Close()
		}
	}()

	if !r.noProxy {
		inject := r.cfg.Proxy.InjectReload != nil && *r.cfg.Proxy.InjectReload
		handler := NewProxyHandler(r.cfg.Proxy.Target, broker, inject)
		srv, _, err := ListenAndServeProxy(r.cfg.Proxy.Listen, handler)
		if err != nil {
			return fmt.Errorf("start proxy: %w", err)
		}
		proxySrv = srv
		r.logger.Info("proxy listening", "addr", r.cfg.Proxy.Listen, "target", r.cfg.Proxy.Target)
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
			if err := pm.RunCommand(runCtx, rule); err != nil {
				r.logger.Error("initial build failed", "rule", name, "err", err)
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

				r.handleEvent(runCtx, evt, graph, pm, broker)
			}
		}
	}()

	// Event loop: coalesce to latest event per rule and notify scheduler.
	for {
		select {
		case <-watcher.Done():
			return nil
		case <-runCtx.Done():
			return runCtx.Err()
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

func (r *Runner) handleEvent(ctx context.Context, evt FileEvent, graph *Graph, pm *ProcessManager, broker *SSEBroker) {
	rule := evt.Rule
	r.logger.Info("change detected", "rule", rule.Name, "path", evt.Path)

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
		if err := pm.RunCommand(ctx, rule); err != nil {
			r.logger.Error("build failed", "rule", rule.Name, "err", err)
			return
		}
	}

	// Restart the long-running process.
	if rule.Run != "" {
		if err := pm.StartProcess(ctx, rule); err != nil {
			r.logger.Error("restart failed", "rule", rule.Name, "err", err)
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

func (r *Runner) findRule(name string) *WatchRule {
	for i := range r.cfg.Dev.Watch {
		if r.cfg.Dev.Watch[i].Name == name {
			return &r.cfg.Dev.Watch[i]
		}
	}
	return nil
}
