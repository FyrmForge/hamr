package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var mcpInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Register the hamr MCP bridge with an AI agent (claude, codex, opencode)",
	Long: `Writes the per-client config that tells an agent to spawn ` + "`hamr mcp`" + `.

With no --client, auto-detects which of the supported agents are present and
configures each. Existing config is merged, never clobbered; re-running is
idempotent. Use --dry-run to preview.

Claude and opencode use project-scoped config (rely on the working directory to
locate the project); Codex's config is global, so its entry is pinned to this
project with --project.`,
	Args: cobra.NoArgs,
	RunE: runMCPInstall,
}

func init() {
	mcpInstallCmd.Flags().String("client", "", "claude | codex | opencode (default: auto-detect installed agents)")
	mcpInstallCmd.Flags().Bool("dry-run", false, "print what would change without writing")
	mcpCmd.AddCommand(mcpInstallCmd)
}

// clientInstaller knows how to detect and configure one agent.
type clientInstaller struct {
	detect  func(projectRoot string) bool
	install func(projectRoot string, dryRun bool) (path string, changed bool, err error)
}

// installOrder fixes the iteration order for auto-detect output.
var installOrder = []string{"claude", "codex", "opencode"}

func installers() map[string]clientInstaller {
	return map[string]clientInstaller{
		"claude":   {detect: detectClaude, install: installClaude},
		"codex":    {detect: detectCodex, install: installCodex},
		"opencode": {detect: detectOpencode, install: installOpencode},
	}
}

func runMCPInstall(cmd *cobra.Command, _ []string) error {
	client, _ := cmd.Flags().GetString("client")
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	root, err := resolveProjectRoot("")
	if err != nil {
		return err
	}

	all := installers()
	var targets []string
	if client != "" {
		if _, ok := all[client]; !ok {
			return fmt.Errorf("unknown client %q (want claude, codex, or opencode)", client)
		}
		targets = []string{client}
	} else {
		for _, name := range installOrder {
			if all[name].detect(root) {
				targets = append(targets, name)
			}
		}
		if len(targets) == 0 {
			return fmt.Errorf("no supported agent detected; pass --client claude|codex|opencode")
		}
	}

	for _, name := range targets {
		path, changed, err := all[name].install(root, dryRun)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		switch {
		case dryRun:
			fmt.Printf("[dry-run] %s → would write %s\n", name, path)
		case changed:
			fmt.Printf("%s → configured %s\n", name, path)
		default:
			fmt.Printf("%s → already configured (%s)\n", name, path)
		}
	}
	return nil
}

// --- detection ---

func detectClaude(root string) bool {
	return fileExists(filepath.Join(root, ".mcp.json")) || onPath("claude")
}

func detectCodex(_ string) bool {
	home, err := os.UserHomeDir()
	return (err == nil && dirExists(filepath.Join(home, ".codex"))) || onPath("codex")
}

func detectOpencode(root string) bool {
	if fileExists(filepath.Join(root, "opencode.json")) {
		return true
	}
	if home, err := os.UserHomeDir(); err == nil && dirExists(filepath.Join(home, ".config", "opencode")) {
		return true
	}
	return onPath("opencode")
}

func onPath(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// --- installers ---

// installClaude writes .mcp.json (project): {"mcpServers":{"hamr-dev":{...}}}.
func installClaude(root string, dryRun bool) (string, bool, error) {
	path := filepath.Join(root, ".mcp.json")
	return mergeJSONConfig(path, dryRun, func(cfg map[string]any) {
		servers := childMap(cfg, "mcpServers")
		servers["hamr-dev"] = map[string]any{"command": "hamr", "args": []any{"mcp"}}
	})
}

// installOpencode writes opencode.json (project): {"mcp":{"hamr-dev":{...}}}.
func installOpencode(root string, dryRun bool) (string, bool, error) {
	path := filepath.Join(root, "opencode.json")
	return mergeJSONConfig(path, dryRun, func(cfg map[string]any) {
		servers := childMap(cfg, "mcp")
		servers["hamr-dev"] = map[string]any{
			"type":    "local",
			"command": []any{"hamr", "mcp"},
			"enabled": true,
		}
	})
}

// installCodex upserts the [mcp_servers.hamr-dev] block in ~/.codex/config.toml.
// Codex config is global, so the entry is pinned to this project with --project.
func installCodex(root string, dryRun bool) (string, bool, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false, err
	}
	path := filepath.Join(home, ".codex", "config.toml")
	block := fmt.Sprintf("[mcp_servers.hamr-dev]\ncommand = \"hamr\"\nargs = [\"mcp\", \"--project\", %q]\n", root)

	existing := ""
	if data, err := os.ReadFile(path); err == nil {
		existing = string(data)
	} else if !os.IsNotExist(err) {
		return "", false, err
	}
	updated := upsertTOMLBlock(existing, "[mcp_servers.hamr-dev]", block)
	if updated == existing {
		return path, false, nil
	}
	if dryRun {
		return path, true, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", false, err
	}
	return path, true, os.WriteFile(path, []byte(updated), 0o644)
}

// --- merge helpers ---

// mergeJSONConfig loads path (or {} if absent), applies mutate, and writes the
// result when it changed. Returns whether a write was needed.
func mergeJSONConfig(path string, dryRun bool, mutate func(cfg map[string]any)) (string, bool, error) {
	cfg := map[string]any{}
	var before []byte
	if data, err := os.ReadFile(path); err == nil {
		before = data
		if len(bytes.TrimSpace(data)) > 0 {
			if err := json.Unmarshal(data, &cfg); err != nil {
				return "", false, fmt.Errorf("parse %s: %w", path, err)
			}
		}
	} else if !os.IsNotExist(err) {
		return "", false, err
	}

	mutate(cfg)
	after, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", false, err
	}
	after = append(after, '\n')

	if bytes.Equal(before, after) {
		return path, false, nil
	}
	if dryRun {
		return path, true, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", false, err
	}
	return path, true, os.WriteFile(path, after, 0o644)
}

// childMap returns cfg[key] as a map, creating it (and storing it back) when
// absent or the wrong type.
func childMap(cfg map[string]any, key string) map[string]any {
	if existing, ok := cfg[key].(map[string]any); ok {
		return existing
	}
	m := map[string]any{}
	cfg[key] = m
	return m
}

// upsertTOMLBlock replaces the existing header block (header line through the
// line before the next "[section]" or EOF) with block, or appends block when
// the header is absent. Everything outside the block is preserved byte-for-byte
// so a user's hand-maintained config (comments, ordering) survives.
func upsertTOMLBlock(existing, header, block string) string {
	block = strings.TrimRight(block, "\n")
	lines := strings.Split(existing, "\n")

	start := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == header {
			start = i
			break
		}
	}

	if start == -1 {
		base := strings.TrimRight(existing, "\n")
		if base == "" {
			return block + "\n"
		}
		return base + "\n\n" + block + "\n"
	}

	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "[") {
			end = i
			break
		}
	}
	out := append([]string{}, lines[:start]...)
	out = append(out, strings.Split(block, "\n")...)
	out = append(out, lines[end:]...)
	return strings.Join(out, "\n")
}
