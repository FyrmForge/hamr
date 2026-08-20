package devserver

import (
	"fmt"
	"slices"
	"time"
)

// MCPConfig holds the [dev.mcp] table — the gateway that lets an AI agent
// drive hamr dev through the `hamr mcp` stdio bridge. Off by default; opt-in.
//
// The bridge connects over the existing localhost /__hamr/* HTTP API,
// authenticated by a per-run token (see the gateway). Permissions are granted
// per functional area at read/write/deny granularity via Access; an area not
// granted exposes none of its tools.
type MCPConfig struct {
	// Enabled is the initial runtime state at launch. The TUI kill-switch can
	// flip the live gateway without rewriting this. Default false.
	Enabled bool `toml:"enabled"`

	// Access maps a functional area (dev, logs, docker, mail, sms, build, stripe) to
	// a level ("read", "write", or "deny"). "write" implies "read". Areas absent
	// from the map are denied. With no table at all, zero tools are exposed.
	Access map[string]string `toml:"access"`

	// MakeTargets constrains make.run to a named subset. Empty = every Makefile
	// target is allowed (the permissive default).
	MakeTargets []string `toml:"make_targets"`

	// MakeWait bounds how long make.run blocks before returning a "still
	// running, poll logs" result. Default 20s when unset.
	MakeWait Duration `toml:"make_wait"`

	// LogFile is the MCP audit log path. Default ".hamr/mcp_logs.txt"; set to
	// "none" to disable the audit log.
	LogFile string `toml:"log_file"`
}

// mcpArea describes the tools an area exposes at each access level. writeTools
// are additive over readTools ("write" implies "read").
type mcpArea struct {
	readTools  []string
	writeTools []string
}

// mcpAreas is the canonical area → tool-set registry, shared by the gateway
// (which exposes endpoints) and the bridge (which advertises tools). Keep in
// sync with the tool inventory in docs/proposed-features/mcp-dev-server.md.
var mcpAreas = map[string]mcpArea{
	"dev":    {readTools: []string{"dev.info"}},
	"logs":   {readTools: []string{"logs.read", "console.read", "http.read"}},
	"docker": {readTools: []string{"docker.logs", "docker.status"}, writeTools: []string{"docker.restart", "docker.wipe"}},
	"mail":   {readTools: []string{"mail.list", "mail.get"}, writeTools: []string{"mail.clear", "mail.ingest"}},
	"sms":    {readTools: []string{"sms.list", "sms.get"}, writeTools: []string{"sms.clear", "sms.ingest"}},
	"build":  {writeTools: []string{"rule.run", "rebuild.all", "make.run"}},
	"stripe": {readTools: []string{"stripe.list"}, writeTools: []string{"stripe.complete", "stripe.expire", "stripe.refund"}},
}

// MCPAreaNames returns every configurable [dev.mcp.access] area in a stable
// order. Exported for the `hamr setup` picker, which needs the canonical list
// without duplicating it.
func MCPAreaNames() []string {
	names := make([]string, 0, len(mcpAreas))
	for name := range mcpAreas {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// EnabledTools returns the set of tool names the Access map exposes. Unknown
// areas/levels are ignored here (validate() rejects them at load time).
func (c MCPConfig) EnabledTools() map[string]bool {
	out := make(map[string]bool)
	for area, level := range c.Access {
		a, ok := mcpAreas[area]
		if !ok {
			continue
		}
		switch level {
		case "read":
			for _, t := range a.readTools {
				out[t] = true
			}
		case "write":
			for _, t := range a.readTools {
				out[t] = true
			}
			for _, t := range a.writeTools {
				out[t] = true
			}
		}
	}
	return out
}

// ToolAllowed reports whether the named tool is exposed by the current Access
// map. The gateway uses this to enforce permissions per call.
func (c MCPConfig) ToolAllowed(tool string) bool {
	return c.EnabledTools()[tool]
}

// MakeTargetAllowed reports whether make.run may run the given target. Empty
// MakeTargets means every target is allowed.
func (c MCPConfig) MakeTargetAllowed(target string) bool {
	if len(c.MakeTargets) == 0 {
		return true
	}
	return slices.Contains(c.MakeTargets, target)
}

// ResolvedMakeWait returns the make.run bounded-wait duration, defaulting to
// 20s when unset or non-positive.
func (c MCPConfig) ResolvedMakeWait() time.Duration {
	if c.MakeWait.Duration <= 0 {
		return 20 * time.Second
	}
	return c.MakeWait.Duration
}

// ResolvedLogFile returns the audit-log path with the default applied, or ""
// when the audit log is disabled ("none").
func (c MCPConfig) ResolvedLogFile() string {
	switch c.LogFile {
	case "":
		return ".hamr/mcp_logs.txt"
	case "none":
		return ""
	default:
		return c.LogFile
	}
}

// validate checks the access map for unknown areas or levels. Runs regardless
// of Enabled so config typos surface early.
func (c MCPConfig) validate() error {
	for area, level := range c.Access {
		if _, ok := mcpAreas[area]; !ok {
			return fmt.Errorf("[dev.mcp.access] unknown area %q (want one of dev, logs, docker, mail, sms, build, stripe)", area)
		}
		switch level {
		case "read", "write", "deny":
		default:
			return fmt.Errorf("[dev.mcp.access] %q: invalid level %q (want \"read\", \"write\", or \"deny\")", area, level)
		}
	}
	return nil
}
