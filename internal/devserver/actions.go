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
}

// RegisterRoutes registers the action API endpoints on the given mux.
func (a *DevActions) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/__hamr/rule/", a.handleRule)
	mux.HandleFunc("/__hamr/docker/", a.handleDocker)
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

	// Run in background goroutine so HTTP returns immediately.
	go a.runRule(rule)

	jsonOK(w)
}

func (a *DevActions) runRule(rule *WatchRule) {
	ctx := a.ctx
	a.logger.Info("manual build triggered", "rule", rule.Name)
	a.broker.Broadcast(SSEEvent{Type: "building", Data: rule.Name})

	if rule.Cmd != "" {
		if output, err := a.pm.RunCommand(ctx, rule); err != nil {
			a.logger.Error("manual build failed", "rule", rule.Name, "err", err)
			a.errorState.Set(rule.Name, output)
			a.broker.Broadcast(buildErrorEvent(rule.Name, output))
			return
		}
		a.errorState.Clear(rule.Name)
		a.broker.Broadcast(SSEEvent{Type: "build_ok", Data: rule.Name})
	}

	if rule.Run != "" {
		if err := a.pm.StartProcess(ctx, rule); err != nil {
			a.logger.Error("manual restart failed", "rule", rule.Name, "err", err)
		}
	}
}

// RebuildAll runs every watch rule in topological order and broadcasts a
// final reload event. It is used by the hotkey system to trigger a full rebuild.
func (a *DevActions) RebuildAll() {
	a.logger.Info("full rebuild triggered")
	order := a.graph.TopologicalOrder()
	for _, name := range order {
		var rule *WatchRule
		for i := range a.cfg.Dev.Watch {
			if a.cfg.Dev.Watch[i].Name == name {
				rule = &a.cfg.Dev.Watch[i]
				break
			}
		}
		if rule == nil {
			continue
		}
		a.runRule(rule)
	}
	a.broker.Broadcast(SSEEvent{Type: "reload", Data: "full"})
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
		go a.dockerRestart(dc, service)
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
		go a.dockerWipe(dc, service)
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
	args := []string{"compose", "-f", dc.File, "restart"}
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
		stopArgs := []string{"compose", "-f", dc.File, "rm", "-fsv", service}
		stopRule := &WatchRule{Name: "docker:" + dc.Name, Cmd: dockerCmd(stopArgs), Env: dc.Env}
		if output, err := a.pm.RunCommand(ctx, stopRule); err != nil {
			a.logger.Error("docker compose rm failed", "name", dc.Name, "err", err)
			a.errorState.Set(dc.Name, output)
			a.broker.Broadcast(buildErrorEvent(dc.Name, output))
			return
		}
		upArgs := []string{"compose", "-f", dc.File, "up", "-d", service}
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
	downArgs := []string{"compose", "-f", dc.File, "down", "-v"}
	downRule := &WatchRule{Name: "docker:" + dc.Name, Cmd: dockerCmd(downArgs), Env: dc.Env}
	if output, err := a.pm.RunCommand(ctx, downRule); err != nil {
		a.logger.Error("docker compose down -v failed", "name", dc.Name, "err", err)
		a.errorState.Set(dc.Name, output)
		a.broker.Broadcast(buildErrorEvent(dc.Name, output))
		return
	}

	upArgs := []string{"compose", "-f", dc.File, "up", "-d"}
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
	args := []string{"compose", "-f", dc.File, "ps", "--format", "json"}
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
		for _, line := range bytes.Split(raw, []byte("\n")) {
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
	args := []string{"compose", "-f", dc.File, "logs", "--tail=100"}
	args = append(args, dc.Services...)
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
	return "docker " + strings.Join(args, " ")
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
