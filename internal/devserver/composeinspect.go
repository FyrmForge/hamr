package devserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// composePSEntry is the subset of `docker compose ps --format json` we
// read. Compose v2 emits either a JSON array (newer) or NDJSON (older);
// decodeComposePS handles both.
type composePSEntry struct {
	Name       string               `json:"Name"`
	Service    string               `json:"Service"`
	State      string               `json:"State"`  // "running", "exited", "restarting", ...
	Health     string               `json:"Health"` // "healthy", "unhealthy", "starting", "" (no healthcheck)
	Publishers []composePSPublisher `json:"Publishers"`
}

type composePSPublisher struct {
	URL           string `json:"URL"`           // host bind address ("", "0.0.0.0", "127.0.0.1", ...)
	TargetPort    int    `json:"TargetPort"`    // container-internal port
	PublishedPort int    `json:"PublishedPort"` // host-side published port (0 = unpublished)
	Protocol      string `json:"Protocol"`      // "tcp"/"udp"
}

// composeStackState captures the result of inspecting a project's
// running containers via `docker compose ps`. Returned by
// inspectRunningCompose.
type composeStackState struct {
	// Adopted is the set of service names whose containers are running
	// AND ready (healthy or no-healthcheck). Running-but-unhealthy or
	// running-but-starting services are excluded — they fail readiness
	// and the apply path should take over.
	Adopted map[string]bool

	// Owned is the set of normalised "host:port" keys (see hostPortKey)
	// for ports currently published by this project's containers. Used
	// by walkComposeServices to skip probing ports we already own.
	Owned map[string]bool

	// Publishers carries actual published-port info per service so the
	// adopt path can derive port shifts vs. base compose without re-
	// parsing the override file. One entry per publisher row in compose
	// ps output.
	Publishers []composeStackPublisher
}

// composeStackPublisher mirrors one row of a service's running
// publishers, keyed for matching against base compose port bindings.
type composeStackPublisher struct {
	Service       string
	HostIP        string // canonicalised: loopback variants → ""
	Container     int
	PublishedPort int
	Protocol      string
}

// hostPortKey normalises (host, port) into the canonical "owned" lookup
// key. Empty / 0.0.0.0 / [::] / ::/127.0.0.1 / localhost all collapse to
// "localhost" — on a dev machine they collide for binding purposes, and
// the publisher URL reported by compose ps may use any of these forms
// regardless of how the compose file declared the binding.
func hostPortKey(host string, port int) string {
	switch host {
	case "", "0.0.0.0", "[::]", "::", "127.0.0.1", "localhost":
		host = "localhost"
	}
	return host + ":" + strconv.Itoa(port)
}

// canonComposeHost is the inverse-ish of hostPortKey for shift records:
// loopback variants collapse to "" so the value matches what base
// compose's HostIP carries when no host_ip is explicit.
func canonComposeHost(host string) string {
	switch host {
	case "0.0.0.0", "[::]", "::", "127.0.0.1", "localhost":
		return ""
	}
	return host
}

// inspectRunningCompose queries `docker compose ps --format json` for a
// compose entry and returns a snapshot of which services are adopted
// (running + ready) plus the host ports they currently publish. Hard-
// fails on any docker error: a missing daemon, missing compose
// subcommand, or unparseable output should abort hamr dev with a
// surfaced error rather than silently fall through to a cold-start path
// that may bounce running containers.
//
// `compose ps` without `--all` returns only running containers, which
// is exactly what every downstream consumer cares about — Adopted,
// Owned, and Publishers are all running-only concepts. Including exited
// containers would pollute Owned with ports nobody actually holds and
// derive false shifts from last-known publisher state.
//
// Empty output (project never started) is not an error — the helper
// returns an empty state in that case.
func (r *Runner) inspectRunningCompose(ctx context.Context, dc *DockerCompose) (composeStackState, error) {
	args := append(composeArgsForInspect(dc), "ps", "--format", "json")
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.WaitDelay = 2 * time.Second
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = buildEnv(dc.Env)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return composeStackState{}, fmt.Errorf("docker compose ps failed: %w (output: %s)", err, strings.TrimSpace(stderr.String()))
	}

	entries, err := decodeComposePS(stdout.Bytes())
	if err != nil {
		return composeStackState{}, fmt.Errorf("parse docker compose ps output: %w", err)
	}
	return interpretComposePS(entries), nil
}

// decodeComposePS parses compose ps JSON output. Compose v2 emits an
// array form ("[ {...}, {...} ]") on newer versions and NDJSON
// (one object per line) on some configurations; we accept both so an
// upstream tweak doesn't break us. Empty input → empty entries.
func decodeComposePS(raw []byte) ([]composePSEntry, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, nil
	}
	if raw[0] == '[' {
		var entries []composePSEntry
		if err := json.Unmarshal(raw, &entries); err != nil {
			return nil, err
		}
		return entries, nil
	}
	var entries []composePSEntry
	for _, line := range bytes.Split(raw, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var e composePSEntry
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// interpretComposePS folds entries into the (Adopted, Owned, Publishers)
// triple. Pure compute — no docker shell-out — so it's directly unit-
// testable with hand-crafted entries.
//
// Defence in depth: only entries in the running state contribute to
// Owned and Publishers. Without `--all` compose ps already filters
// non-running containers out, but if a future compose version starts
// reporting more we don't want exited containers' last-known publisher
// state polluting our owned-port set or producing phantom shifts.
func interpretComposePS(entries []composePSEntry) composeStackState {
	state := composeStackState{
		Adopted: make(map[string]bool, len(entries)),
		Owned:   make(map[string]bool),
	}
	for _, e := range entries {
		if e.Service == "" {
			continue
		}
		if isAdopted(e) {
			state.Adopted[e.Service] = true
		}
		if !strings.EqualFold(e.State, "running") {
			continue
		}
		for _, p := range e.Publishers {
			if p.PublishedPort == 0 {
				continue
			}
			state.Owned[hostPortKey(p.URL, p.PublishedPort)] = true
			state.Publishers = append(state.Publishers, composeStackPublisher{
				Service:       e.Service,
				HostIP:        canonComposeHost(p.URL),
				Container:     p.TargetPort,
				PublishedPort: p.PublishedPort,
				Protocol:      p.Protocol,
			})
		}
	}
	return state
}

// isAdopted reports whether a compose ps entry counts as "running and
// ready for adoption". Container must be in the running state, and (when
// docker tracks a healthcheck) Health must be "healthy". No healthcheck
// → empty Health → docker has no opinion → trust running.
func isAdopted(e composePSEntry) bool {
	if !strings.EqualFold(e.State, "running") {
		return false
	}
	switch strings.ToLower(e.Health) {
	case "", "healthy":
		return true
	default: // "starting", "unhealthy", anything else
		return false
	}
}

// allServicesAdopted reports whether every service in expected is in
// state.Adopted. Used to choose between adopt and apply paths.
func allServicesAdopted(expected []string, adopted map[string]bool) bool {
	for _, name := range expected {
		if !adopted[name] {
			return false
		}
	}
	return true
}

// expectedServiceNames returns the list of service names hamr expects
// to be running for a compose entry. Explicit dc.Services wins (the
// user opted in by name; profile gates don't apply to that path —
// `compose up -d <name>` starts a named service regardless of its
// profile). Otherwise every service declared in the compose file
// EXCEPT those gated by a `profiles:` attribute, since hamr doesn't
// pass --profile / COMPOSE_PROFILES so compose won't start them by
// default. Including them here would make allServicesAdopted always
// return false for any compose file that declares profiles.
func expectedServiceNames(dc *DockerCompose, services []composeService) []string {
	if len(dc.Services) > 0 {
		out := make([]string, len(dc.Services))
		copy(out, dc.Services)
		return out
	}
	out := make([]string, 0, len(services))
	for _, s := range services {
		if len(s.Profiles) > 0 {
			continue
		}
		out = append(out, s.Name)
	}
	return out
}

// stateShiftsForServices derives portShifts by diffing the actual
// published ports (from compose ps) against base compose declarations,
// scoped to the given services. Used by:
//
//   - the adopt path, where every service is adopted and shifts come
//     entirely from running state;
//   - the apply path, to capture drift on already-running peers that
//     the walk skipped.
//
// Pairing of base bindings to publisher rows is done by
// resolvedPortsForService, which prefers a (service, container_port)
// match but falls back to sort-order pairing when container ports
// collide or the publisher reports an unreliable TargetPort.
//
// Bindings with HostPort=0 (random) are skipped because "actual vs
// declared" isn't meaningful — declared is unspecified.
func stateShiftsForServices(services []composeService, publishers []composeStackPublisher, only map[string]bool) []portShift {
	var shifts []portShift
	for _, svc := range services {
		if only != nil && !only[svc.Name] {
			continue
		}
		resolved := resolvedPortsForService(svc.Name, svc.Ports, publishers)
		for i, b := range svc.Ports {
			if b.HostPort == 0 {
				continue
			}
			actual, ok := resolved[i]
			if !ok || actual == b.HostPort {
				continue
			}
			shifts = append(shifts, portShift{
				Service:  svc.Name,
				Old:      b.HostPort,
				New:      actual,
				Protocol: b.Protocol,
				HostIP:   b.HostIP,
			})
		}
	}
	return shifts
}

// resolvedPortsForService returns a map from base-binding index to the
// matching publisher's PublishedPort for the given service.
//
// Pairing strategy, in order:
//
//  1. Match by container port — for every container port that has
//     exactly one binding and exactly one publisher, pair them. This is
//     the common case.
//  2. Fallback for the remainder: pair leftover bindings to leftover
//     publishers by sort order (bindings ascending HostPort, publishers
//     ascending PublishedPort), iff the leftover counts match. This
//     handles two edge cases without bloating the call site:
//     - exotic compose files publishing the same container port to
//       multiple host ports (container-collision ambiguity);
//     - older compose ps output where TargetPort may be unreliable or
//       0 (the only-binding-only-publisher service degrades into a
//       1-vs-1 sort pairing).
//
// Bindings whose HostPort is 0 (random/dynamic) are excluded from
// pairing entirely — there's no declared baseline to diff against.
// Unpaired bindings are absent from the result; the caller falls back
// to the base port for those.
func resolvedPortsForService(svcName string, bindings []composePortBinding, publishers []composeStackPublisher) map[int]int {
	out := make(map[int]int)
	if len(bindings) == 0 {
		return out
	}

	// Filter publishers to this service; preserve compose ps order.
	own := make([]composeStackPublisher, 0, len(publishers))
	for _, p := range publishers {
		if p.Service == svcName {
			own = append(own, p)
		}
	}
	if len(own) == 0 {
		return out
	}

	// Step 1: container-port match for unambiguous pairs.
	bindBins := make(map[int][]int, len(bindings))
	for i, b := range bindings {
		if b.HostPort == 0 {
			continue
		}
		bindBins[b.Container] = append(bindBins[b.Container], i)
	}
	pubBins := make(map[int][]int, len(own))
	for i, p := range own {
		pubBins[p.Container] = append(pubBins[p.Container], i)
	}
	matchedBinds := make(map[int]bool)
	matchedPubs := make(map[int]bool)
	for container, bIdxs := range bindBins {
		pIdxs := pubBins[container]
		if len(bIdxs) == 1 && len(pIdxs) == 1 {
			out[bIdxs[0]] = own[pIdxs[0]].PublishedPort
			matchedBinds[bIdxs[0]] = true
			matchedPubs[pIdxs[0]] = true
		}
	}

	// Step 2: sort-order fallback for the remainder, but only when
	// leftover counts agree — otherwise we'd be guessing.
	var unmatchedBinds []int
	for i, b := range bindings {
		if b.HostPort == 0 || matchedBinds[i] {
			continue
		}
		unmatchedBinds = append(unmatchedBinds, i)
	}
	var unmatchedPubs []int
	for i := range own {
		if matchedPubs[i] {
			continue
		}
		unmatchedPubs = append(unmatchedPubs, i)
	}
	if len(unmatchedBinds) == 0 || len(unmatchedBinds) != len(unmatchedPubs) {
		return out
	}
	sort.SliceStable(unmatchedBinds, func(i, j int) bool {
		return bindings[unmatchedBinds[i]].HostPort < bindings[unmatchedBinds[j]].HostPort
	})
	sort.SliceStable(unmatchedPubs, func(i, j int) bool {
		return own[unmatchedPubs[i]].PublishedPort < own[unmatchedPubs[j]].PublishedPort
	})
	for k, bIdx := range unmatchedBinds {
		out[bIdx] = own[unmatchedPubs[k]].PublishedPort
	}
	return out
}
