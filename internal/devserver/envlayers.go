package devserver

import (
	"context"
	"log/slog"
	"sync"
)

// envLayers composes the env hamr injects into spawned processes from layers
// owned by separate runtime features, so one feature switching its vars can't
// wipe another's:
//
//	injected = base (port-walk rewrites + hamr vars) + stripe + tunnel
//
// Later layers win on key conflict (buildEnv is last-wins).
type envLayers struct {
	ctx    context.Context
	cfg    *Config
	pm     *ProcessManager
	logger *slog.Logger

	mu     sync.Mutex
	base   []string
	stripe []string
	tunnel []string
}

// setBase sets the base layer. Startup only: nothing is running yet, so no restart.
func (e *envLayers) setBase(env []string) { e.set(&e.base, env, false) }

// setStripe sets the Stripe layer, restarting running apps when restart is true.
func (e *envLayers) setStripe(env []string, restart bool) { e.set(&e.stripe, env, restart) }

// setTunnel sets the tunnel layer and restarts running apps.
func (e *envLayers) setTunnel(env []string) { e.set(&e.tunnel, env, true) }

func (e *envLayers) set(layer *[]string, env []string, restart bool) {
	e.mu.Lock()
	*layer = env
	merged := make([]string, 0, len(e.base)+len(e.stripe)+len(e.tunnel))
	merged = append(append(append(merged, e.base...), e.stripe...), e.tunnel...)
	e.pm.SetInjectedEnv(merged)
	e.mu.Unlock()
	if restart {
		e.restartRunning()
	}
}

// restartRunning restarts the running run-rules and daemons so they pick up
// the injected env. Builds are not re-run. Only what is running restarts: a
// rule whose build failed stays down. StartProcess is serialized, so a
// scheduler rebuild of the same rule racing this just restarts it twice.
func (e *envLayers) restartRunning() {
	for i := range e.cfg.Dev.Watch {
		rule := &e.cfg.Dev.Watch[i]
		if rule.Run != "" && e.pm.isRunning(rule.Name) {
			if err := e.pm.StartProcess(e.ctx, rule); err != nil {
				e.logger.Error("restart failed", "rule", rule.Name, "err", err)
			}
		}
	}
	for i := range e.cfg.Dev.Daemons {
		d := &e.cfg.Dev.Daemons[i]
		if e.pm.isRunning(d.Name) {
			if err := e.pm.StartProcess(e.ctx, &WatchRule{Name: d.Name, Run: d.Cmd, Dir: d.Dir, Env: d.Env}); err != nil {
				e.logger.Error("restart failed", "daemon", d.Name, "err", err)
			}
		}
	}
}
