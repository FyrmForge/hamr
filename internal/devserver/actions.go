package devserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// safeServiceName matches valid docker compose service names (alphanumeric, dash, underscore, dot).
var safeServiceName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// DevActions encapsulates API action handlers for the dev panel.
type DevActions struct {
	ctx        context.Context
	cfg        *Config
	pm         *ProcessManager
	broker     *SSEBroker
	errorState *ErrorState
	graph      *Graph
	logger     *slog.Logger
	// requestRun enqueues a rule onto the dev server's single scheduler
	// goroutine. Manual runs (POST /run, hotkey rebuild) must go through here so
	// they are serialized with file-watch builds and respect dependency order —
	// running a process directly would race the scheduler and could orphan a
	// process on the same port. Nil outside a live Run() (e.g. in unit tests).
	requestRun func(rule *WatchRule)
	// restartFn/wipeFn are test seams for the docker actions. When nil the real
	// docker compose commands run (production); tests set them to record the
	// dispatch without executing docker (which would otherwise run real compose
	// commands against the cwd in a detached goroutine that outlives the test).
	restartFn func(dc *DockerCompose, service string)
	wipeFn    func(dc *DockerCompose, service string)
}

// restart dispatches a docker restart, honoring the test seam if set.
func (a *DevActions) restart(dc *DockerCompose, service string) {
	if a.restartFn != nil {
		a.restartFn(dc, service)
		return
	}
	a.dockerRestart(dc, service)
}

// wipe dispatches a docker wipe, honoring the test seam if set.
func (a *DevActions) wipe(dc *DockerCompose, service string) {
	if a.wipeFn != nil {
		a.wipeFn(dc, service)
		return
	}
	a.dockerWipe(dc, service)
}

// RegisterRoutes registers the action API endpoints on the given mux.
func (a *DevActions) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/__hamr/rule/", guardUnsafe(a.handleRule))
	mux.HandleFunc("/__hamr/docker/", guardUnsafe(a.handleDocker))
}

// ErrorState returns the underlying error state so non-HTTP consumers (the
// TUI runtime) can subscribe to error changes.
func (a *DevActions) ErrorState() *ErrorState { return a.errorState }

// DockerComposes returns the configured docker compose entries the runner
// is managing.
func (a *DevActions) DockerComposes() []DockerCompose { return a.cfg.Dev.DockerCompose }

// DockerWipe triggers a "down -v + up -d" cycle for the given compose entry,
// removing volumes. When service is "" the whole entry is wiped; otherwise
// only that service. Runs synchronously on the calling goroutine — TUI
// callers should dispatch in a goroutine to keep the UI responsive.
func (a *DevActions) DockerWipe(dc *DockerCompose, service string) {
	a.dockerWipe(dc, service)
}

func (a *DevActions) handleRule(w http.ResponseWriter, r *http.Request) {
	// Path: /__hamr/rule/{name}/run
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/__hamr/rule/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 || parts[1] != "run" {
		jsonError(w, "invalid path", http.StatusBadRequest)
		return
	}
	name := parts[0]

	// Find the rule.
	var rule *WatchRule
	for i := range a.cfg.Dev.Watch {
		if a.cfg.Dev.Watch[i].Name == name {
			rule = &a.cfg.Dev.Watch[i]
			break
		}
	}
	if rule == nil {
		jsonError(w, fmt.Sprintf("unknown rule %q", name), http.StatusNotFound)
		return
	}

	// Enqueue onto the single scheduler goroutine rather than building here, so
	// the run is serialized with file-watch builds and respects dependency order.
	if a.requestRun == nil {
		jsonError(w, "dev server not ready", http.StatusServiceUnavailable)
		return
	}
	a.logger.Info("manual build triggered", "rule", rule.Name)
	a.requestRun(rule)

	jsonOK(w)
}

// RebuildAll enqueues every watch rule onto the scheduler, which resolves
// topological order and dependency gating itself. Used by the hotkey system to
// trigger a full rebuild.
func (a *DevActions) RebuildAll() {
	if a.requestRun == nil {
		return
	}
	a.logger.Info("full rebuild triggered")
	for i := range a.cfg.Dev.Watch {
		a.requestRun(&a.cfg.Dev.Watch[i])
	}
}

func (a *DevActions) handleDocker(w http.ResponseWriter, r *http.Request) {
	// Paths: /__hamr/docker/{name}/restart, /wipe, /logs
	path := strings.TrimPrefix(r.URL.Path, "/__hamr/docker/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 {
		jsonError(w, "invalid path", http.StatusBadRequest)
		return
	}
	name := parts[0]
	action := parts[1]

	// Find the docker compose entry.
	var dc *DockerCompose
	for i := range a.cfg.Dev.DockerCompose {
		if a.cfg.Dev.DockerCompose[i].Name == name {
			dc = &a.cfg.Dev.DockerCompose[i]
			break
		}
	}
	if dc == nil {
		jsonError(w, fmt.Sprintf("unknown docker compose %q", name), http.StatusNotFound)
		return
	}

	switch action {
	case "restart":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		service := r.URL.Query().Get("service")
		if service != "" && !safeServiceName.MatchString(service) {
			jsonError(w, "invalid service name", http.StatusBadRequest)
			return
		}
		go a.restart(dc, service)
		jsonOK(w)

	case "wipe":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		service := r.URL.Query().Get("service")
		if service != "" && !safeServiceName.MatchString(service) {
			jsonError(w, "invalid service name", http.StatusBadRequest)
			return
		}
		go a.wipe(dc, service)
		jsonOK(w)

	case "logs":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		output, err := a.dockerLogs(dc)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"output": output}) //nolint:errcheck

	case "status":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		statuses, err := a.dockerStatus(dc)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(statuses) //nolint:errcheck

	default:
		jsonError(w, "unknown action", http.StatusBadRequest)
	}
}

func (a *DevActions) dockerRestart(dc *DockerCompose, service string) {
	a.logger.Info("docker compose restart", "name", dc.Name, "service", service)
	args := append(composeArgs(dc), "restart")
	if service != "" {
		args = append(args, service)
	} else {
		args = append(args, dc.Services...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	rule := &WatchRule{Name: "docker:" + dc.Name, Cmd: dockerCmd(args), Env: dc.Env}
	if output, err := a.pm.RunCommand(ctx, rule); err != nil {
		a.logger.Error("docker compose restart failed", "name", dc.Name, "err", err)
		a.errorState.Set(dc.Name, output)
		a.broker.Broadcast(buildErrorEvent(dc.Name, output))
	} else {
		a.errorState.Clear(dc.Name)
	}
}

func (a *DevActions) dockerWipe(dc *DockerCompose, service string) {
	a.logger.Info("docker compose wipe", "name", dc.Name, "service", service)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	if service != "" {
		// Single service: stop, rm -v, then up.
		stopArgs := append(composeArgs(dc), "rm", "-fsv", service)
		stopRule := &WatchRule{Name: "docker:" + dc.Name, Cmd: dockerCmd(stopArgs), Env: dc.Env}
		if output, err := a.pm.RunCommand(ctx, stopRule); err != nil {
			a.logger.Error("docker compose rm failed", "name", dc.Name, "err", err)
			a.errorState.Set(dc.Name, output)
			a.broker.Broadcast(buildErrorEvent(dc.Name, output))
			return
		}
		upArgs := append(composeArgs(dc), "up", "-d", service)
		upRule := &WatchRule{Name: "docker:" + dc.Name, Cmd: dockerCmd(upArgs), Env: dc.Env}
		if output, err := a.pm.RunCommand(ctx, upRule); err != nil {
			a.logger.Error("docker compose up failed after wipe", "name", dc.Name, "err", err)
			a.errorState.Set(dc.Name, output)
			a.broker.Broadcast(buildErrorEvent(dc.Name, output))
		} else {
			a.errorState.Clear(dc.Name)
		}
		return
	}

	// All services: down -v then up -d.
	downArgs := append(composeArgs(dc), "down", "-v")
	downRule := &WatchRule{Name: "docker:" + dc.Name, Cmd: dockerCmd(downArgs), Env: dc.Env}
	if output, err := a.pm.RunCommand(ctx, downRule); err != nil {
		a.logger.Error("docker compose down -v failed", "name", dc.Name, "err", err)
		a.errorState.Set(dc.Name, output)
		a.broker.Broadcast(buildErrorEvent(dc.Name, output))
		return
	}

	upArgs := append(composeArgs(dc), "up", "-d")
	upArgs = append(upArgs, dc.Services...)
	upRule := &WatchRule{Name: "docker:" + dc.Name, Cmd: dockerCmd(upArgs), Env: dc.Env}
	if output, err := a.pm.RunCommand(ctx, upRule); err != nil {
		a.logger.Error("docker compose up failed after wipe", "name", dc.Name, "err", err)
		a.errorState.Set(dc.Name, output)
		a.broker.Broadcast(buildErrorEvent(dc.Name, output))
	} else {
		a.errorState.Clear(dc.Name)
	}
}

// containerStatus is the JSON response for a single container's status.
type containerStatus struct {
	Service string `json:"service"`
	State   string `json:"state"`
	Health  string `json:"health,omitempty"`
	Status  string `json:"status"`
}

// dockerPSEntry matches the JSON output of "docker compose ps --format json".
type dockerPSEntry struct {
	Service string `json:"Service"`
	State   string `json:"State"`
	Health  string `json:"Health"`
	Status  string `json:"Status"`
}

func (a *DevActions) dockerStatus(dc *DockerCompose) ([]containerStatus, error) {
	args := append(composeArgs(dc), "ps", "--format", "json")
	args = append(args, dc.Services...)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Env = buildEnv(dc.Env)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("docker compose ps failed: %w", err)
	}

	// docker compose ps --format json outputs one JSON object per line (NDJSON)
	// or a JSON array depending on the docker compose version.
	raw := bytes.TrimSpace(buf.Bytes())
	var entries []dockerPSEntry
	if len(raw) > 0 && raw[0] == '[' {
		// JSON array format.
		if err := json.Unmarshal(raw, &entries); err != nil {
			return nil, fmt.Errorf("parse docker ps output: %w", err)
		}
	} else {
		// NDJSON format (one object per line).
		for line := range bytes.SplitSeq(raw, []byte("\n")) {
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				continue
			}
			var entry dockerPSEntry
			if err := json.Unmarshal(line, &entry); err != nil {
				continue
			}
			entries = append(entries, entry)
		}
	}

	result := make([]containerStatus, len(entries))
	for i, e := range entries {
		result[i] = containerStatus{
			Service: e.Service,
			State:   strings.ToLower(e.State),
			Health:  strings.ToLower(e.Health),
			Status:  e.Status,
		}
	}
	return result, nil
}

func (a *DevActions) dockerLogs(dc *DockerCompose) (string, error) {
	return a.dockerLogsOpts(dc, "", 100, "")
}

// dockerLogsOpts fetches compose logs with optional filters. service ""
// follows all configured services; tail <= 0 defaults to 100; since "" omits
// the --since flag. service/since are passed as exec args (no shell), so they
// can't inject; callers should still validate service against safeServiceName.
func (a *DevActions) dockerLogsOpts(dc *DockerCompose, service string, tail int, since string) (string, error) {
	if tail <= 0 {
		tail = 100
	}
	args := append(composeArgs(dc), "logs", fmt.Sprintf("--tail=%d", tail))
	if since != "" {
		args = append(args, "--since", since)
	}
	if service != "" {
		args = append(args, service)
	} else {
		args = append(args, dc.Services...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Env = buildEnv(dc.Env)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return buf.String(), fmt.Errorf("docker compose logs failed: %w", err)
	}
	return buf.String(), nil
}

func dockerCmd(args []string) string {
	// The result is run via `sh -c`, so each arg must be quoted — a compose
	// file path with a space (or a shell metacharacter) would otherwise split
	// into multiple words or be interpreted by the shell.
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellQuote(a)
	}
	return "docker " + strings.Join(quoted, " ")
}

// shellQuote wraps s in single quotes for safe use in `sh -c`. POSIX single
// quotes preserve every byte literally except the single quote itself, which is
// emitted as the standard '\” sequence.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func jsonOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`)) //nolint:errcheck
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg}) //nolint:errcheck
}
