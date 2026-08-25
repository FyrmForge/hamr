package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/FyrmForge/hamr/internal/cli/generator"
	"github.com/FyrmForge/hamr/internal/devserver"
	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

// accessLevels are the [dev.mcp.access] values, ordered least → most
// permissive for the picker.
var accessLevels = []string{"deny", "read", "write"}

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Interactively configure AI agent integration for this project",
	Long: `Pick which AI agents get the hamr MCP bridge, what those agents are allowed to
do through it, and which agents get the hamr skill installed.

Writes [dev.mcp] and [dev.mcp.access] to hamr.toml and the per-agent MCP config
(the same files ` + "`hamr mcp install`" + ` writes). Existing agent config is merged,
never clobbered.

Examples:
  hamr setup
  hamr setup --dry-run`,
	Args: cobra.NoArgs,
	RunE: runSetup,
}

func init() {
	setupCmd.Flags().Bool("dry-run", false, "print what would change without writing")
	rootCmd.AddCommand(setupCmd)
}

// setupChoices is what the form collects.
type setupChoices struct {
	Agents  []string          // MCP bridge targets: claude, codex, opencode
	Enabled bool              // [dev.mcp].enabled
	Access  map[string]string // area → deny|read|write
	Skills  []string          // skill install targets

	// accessPtrs holds the *string each access picker writes through — huh
	// needs an addressable target and map elements aren't addressable.
	// collectAccess folds them back into Access once the form completes.
	accessPtrs map[string]*string
}

func runSetup(cmd *cobra.Command, _ []string) error {
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	root, err := resolveProjectRoot("")
	if err != nil {
		return err
	}
	// Run from the project root so the cwd-relative helpers this reuses
	// (skill destination, skill render data) resolve against the same project
	// resolveProjectRoot found when invoked from a subdirectory.
	if err := os.Chdir(root); err != nil {
		return fmt.Errorf("enter project root %s: %w", root, err)
	}

	choices := loadSetupDefaults(root)
	if err := runSetupForm(root, choices); err != nil {
		if strings.Contains(err.Error(), "could not open a new TTY") {
			return fmt.Errorf("hamr setup needs an interactive terminal; use `hamr mcp install` and edit [dev.mcp.access] in hamr.toml for scripted setups")
		}
		return err
	}

	return applySetup(root, choices, dryRun)
}

// loadSetupDefaults seeds the form from the project's current state: agents
// that are already installed (or at least present on this machine) start
// ticked, and access levels start at whatever hamr.toml already grants.
func loadSetupDefaults(root string) *setupChoices {
	c := &setupChoices{Access: map[string]string{}}

	all := installers()
	for _, name := range installOrder {
		if all[name].detect(root) {
			c.Agents = append(c.Agents, name)
		}
	}

	for _, area := range devserver.MCPAreaNames() {
		c.Access[area] = "deny"
	}
	// A partially-valid hamr.toml is not worth failing over — the picker just
	// falls back to the all-deny baseline. Loaded without the .pref.hamr.toml
	// merge on purpose: whatever the picker shows is written straight back to
	// hamr.toml, so a local preference must not ride along into the committed
	// file.
	if cfg, err := devserver.LoadConfigNoPrefs(filepath.Join(root, "hamr.toml")); err == nil {
		c.Enabled = cfg.Dev.MCP.Enabled
		for area, level := range cfg.Dev.MCP.Access {
			if _, ok := c.Access[area]; ok {
				c.Access[area] = level
			}
		}
	}

	if _, err := os.Stat(filepath.Join(root, skillToolDir("claude"), "skills", "hamr")); err == nil {
		c.Skills = append(c.Skills, "claude")
	}

	return c
}

func runSetupForm(root string, c *setupChoices) error {
	return newSetupForm(root, c).Run()
}

// newSetupForm builds the picker. Split from runSetupForm so tests can drive it
// with scripted input via huh's WithInput/WithOutput instead of needing a TTY.
func newSetupForm(root string, c *setupChoices) *huh.Form {
	all := installers()
	agentOpts := make([]huh.Option[string], 0, len(installOrder))
	for _, name := range installOrder {
		label := name
		if all[name].detect(root) {
			label += " (detected)"
		}
		agentOpts = append(agentOpts, huh.NewOption(label, name))
	}

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("MCP bridge").
				Description("Which agents may drive `hamr dev` through the hamr MCP bridge.\nExisting agent config is merged, never overwritten.").
				Options(agentOpts...).
				Value(&c.Agents),
			huh.NewConfirm().
				Title("Enable the gateway at startup?").
				Description("Sets [dev.mcp].enabled. The dev TUI kill-switch can flip this live either way.").
				Value(&c.Enabled),
		),
		huh.NewGroup(accessSelects(c)...).
			Title("Tool permissions").
			Description("write implies read. Denied areas expose none of their tools."),
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Agent skills").
				Description("Installs the hamr framework skill (CLI, packages, templ/HTMX practices).\nOverwrites an existing hamr skill directory. Only claude is supported today.").
				Options(skillOptions()...).
				Value(&c.Skills),
		),
	)

	return form
}

// accessSelects builds one read/write/deny picker per MCP area, each bound to
// the choices map so the form writes straight into it.
func accessSelects(c *setupChoices) []huh.Field {
	areas := devserver.MCPAreaNames()
	fields := make([]huh.Field, 0, len(areas))
	c.accessPtrs = make(map[string]*string, len(areas))
	for _, area := range areas {
		level := c.Access[area]
		c.accessPtrs[area] = &level
		opts := make([]huh.Option[string], 0, len(accessLevels))
		for _, lvl := range accessLevels {
			opts = append(opts, huh.NewOption(lvl, lvl))
		}
		fields = append(fields, huh.NewSelect[string]().
			Title(area).
			Description(areaHelp[area]).
			Options(opts...).
			Value(c.accessPtrs[area]))
	}
	return fields
}

// areaHelp is a one-liner per area so the picker is readable without the docs.
var areaHelp = map[string]string{
	"dev":    "dev.info — rules, ports, versions",
	"logs":   "app, browser-console, and HTTP request logs",
	"docker": "compose status/logs; write adds restart + wipe",
	"mail":   "the mail mock inbox; write adds clear + ingest",
	"sms":    "the SMS mock inbox; write adds clear + ingest",
	"build":  "run watch rules, rebuild, run make targets (write-only area)",
	"stripe": "the Stripe mock; write adds complete/expire/refund",
}

func (c *setupChoices) collectAccess() {
	for area, p := range c.accessPtrs {
		c.Access[area] = *p
	}
}

func skillOptions() []huh.Option[string] {
	opts := make([]huh.Option[string], 0, len(generator.SupportedSkillTargets))
	for _, t := range generator.SupportedSkillTargets {
		opts = append(opts, huh.NewOption(t, t))
	}
	return opts
}

// applySetup writes hamr.toml and the per-agent configs, printing one line per
// change. Nothing is written when dryRun is set.
func applySetup(root string, c *setupChoices, dryRun bool) error {
	c.collectAccess()

	tomlPath := filepath.Join(root, "hamr.toml")
	changed, err := writeMCPConfig(tomlPath, c.Enabled, c.Access, dryRun)
	if err != nil {
		return err
	}
	if changed {
		fmt.Printf("  %s hamr.toml [dev.mcp]\n", verb(dryRun))
	} else {
		fmt.Println("  hamr.toml already up to date")
	}

	for _, name := range agentInstructionFiles {
		path := filepath.Join(root, name)
		changed, err := writeAgentMCPSection(path, c.Enabled, c.Access, dryRun)
		if err != nil {
			return err
		}
		if changed {
			fmt.Printf("  %s %s (## %s)\n", verb(dryRun), name, agentMCPHeading)
		}
	}

	all := installers()
	for _, name := range installOrder {
		if !slices.Contains(c.Agents, name) {
			continue
		}
		path, ok, err := all[name].install(root, dryRun)
		if err != nil {
			return fmt.Errorf("configure %s: %w", name, err)
		}
		if ok {
			fmt.Printf("  %s %s (%s)\n", verb(dryRun), path, name)
		} else {
			fmt.Printf("  %s already registered (%s)\n", path, name)
		}
	}

	for _, target := range c.Skills {
		dest, err := resolveSkillDest(target, false)
		if err != nil {
			return err
		}
		if dryRun {
			fmt.Printf("  would install %s skill → %s\n", target, dest)
			continue
		}
		if err := generator.InstallSkill(target, dest, true, loadSkillData(false)); err != nil {
			return fmt.Errorf("install %s skill: %w", target, err)
		}
		fmt.Printf("  wrote %s skill → %s\n", target, dest)
	}

	if dryRun {
		fmt.Println("\nDry run — nothing written.")
	} else {
		fmt.Println("\nDone. Run `hamr dev` to start the gateway.")
	}
	return nil
}

func verb(dryRun bool) string {
	if dryRun {
		return "would write"
	}
	return "wrote"
}

// writeMCPConfig upserts [dev.mcp] and [dev.mcp.access] in hamr.toml, leaving
// the rest of the file (comments, ordering) untouched. Reports whether the file
// content actually changed.
func writeMCPConfig(path string, enabled bool, access map[string]string, dryRun bool) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	existing := string(data)

	updated := upsertTOMLBlock(existing, "[dev.mcp]",
		fmt.Sprintf("[dev.mcp]\nenabled = %t\n", enabled))
	updated = upsertTOMLBlock(updated, "[dev.mcp.access]", accessBlock(access))

	if updated == existing {
		return false, nil
	}
	if dryRun {
		return true, nil
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, nil
}

// accessBlock renders [dev.mcp.access] with areas in a stable order. Denied
// areas are written explicitly rather than omitted: an absent area already
// means deny, but spelling it out keeps the file self-documenting and makes a
// later flip a one-word edit.
func accessBlock(access map[string]string) string {
	areas := make([]string, 0, len(access))
	for area := range access {
		areas = append(areas, area)
	}
	sort.Strings(areas)

	var b strings.Builder
	b.WriteString("[dev.mcp.access]\n")
	for _, area := range areas {
		fmt.Fprintf(&b, "%s = %q\n", area, access[area])
	}
	return b.String()
}

// agentInstructionFiles are the always-in-context instruction files the MCP
// section is mirrored into. CLAUDE.md delegates conventions to AGENTS.md, but
// Claude Code auto-loads CLAUDE.md and only reads AGENTS.md when told to — so
// the habit-forming rules have to be in both.
var agentInstructionFiles = []string{"AGENTS.md", "CLAUDE.md"}

const agentMCPHeading = "hamr MCP"

// mcpHabit is the "reach for this, not that" line per area. read fires at
// read-or-write, write only at write. Written as instructions about what NOT to
// do by hand, because the failure mode is the agent defaulting to the manual
// path, not ignorance of the tool list.
var mcpHabits = []struct{ area, read, write string }{
	{area: "logs",
		read: "Never ask the developer to paste logs, and never tail a log file — `logs.read` (app + build output), `console.read` (browser console, uncaught errors, CSP violations), `http.read` (request log)."},
	{area: "build",
		write: "The dev server is already running. Never run `make build`, `go build`, or start a second server — `rule.run` rebuilds one watch rule, `rebuild.all` rebuilds everything, `make.run` runs a Makefile target."},
	{area: "docker",
		read:  "Check dependency containers with `docker.status` / `docker.logs` before assuming a connection error is app-side.",
		write: "`docker.restart` restarts a service; `docker.wipe` resets its volumes."},
	{area: "mail",
		read:  "Never ask what an email said — `mail.list` and `mail.get` read the dev inbox.",
		write: "`mail.clear` empties it; `mail.ingest` injects a message."},
	{area: "sms",
		read:  "Never ask what an SMS said — `sms.list` and `sms.get` read the dev inbox.",
		write: "`sms.clear` empties it; `sms.ingest` injects a message."},
	{area: "stripe",
		read:  "Never guess at payment state — `stripe.list` reads the mock's objects.",
		write: "`stripe.complete` / `stripe.expire` / `stripe.refund` drive a payment to an outcome."},
	{area: "dev",
		read: "`dev.info` reports the running rules, ports (including walked ones), and versions — read it before assuming a port."},
}

// buildAgentMCPSection renders the instruction block for the granted areas, or
// "" when the gateway is off or nothing is granted (in which case the section
// is removed — a standing instruction to call tools that don't exist trains the
// agent to ignore the file).
func buildAgentMCPSection(enabled bool, access map[string]string) string {
	if !enabled {
		return ""
	}

	var lines []string
	for _, h := range mcpHabits {
		level := access[h.area]
		if level != "read" && level != "write" {
			continue
		}
		if h.read != "" {
			lines = append(lines, "- "+h.read)
		}
		if level == "write" && h.write != "" {
			lines = append(lines, "- "+h.write)
		}
	}
	if len(lines) == 0 {
		return ""
	}

	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n\n", agentMCPHeading)
	b.WriteString("`hamr dev` exposes these tools over MCP. Prefer them over doing the same\n")
	b.WriteString("thing by hand — they read the live dev server, so their answers are current\n")
	b.WriteString("and cost the developer nothing.\n\n")
	b.WriteString(strings.Join(lines, "\n"))
	b.WriteString("\n\nIf a call fails with \"dev not running / gateway off\", say so instead of\nfalling back to manual steps — the developer needs to start `hamr dev`.\n")
	return b.String()
}

// writeAgentMCPSection upserts (or removes) the MCP section in an instruction
// file. Missing files are skipped rather than created: a project that deleted
// its AGENTS.md doesn't want it back.
func writeAgentMCPSection(path string, enabled bool, access map[string]string, dryRun bool) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read %s: %w", path, err)
	}

	existing := string(data)
	updated := upsertMarkdownSection(existing, agentMCPHeading, buildAgentMCPSection(enabled, access))
	if updated == existing {
		return false, nil
	}
	if dryRun {
		return true, nil
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, nil
}

// upsertMarkdownSection replaces the `## <heading>` section of doc with body,
// appending it when absent and removing it when body is empty. The section runs
// until the next heading at the same level or above, so nested `###` content
// belongs to it. Markdown twin of upsertTOMLBlock.
func upsertMarkdownSection(doc, heading, body string) string {
	want := "## " + heading
	lines := strings.Split(doc, "\n")

	start := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == want {
			start = i
			break
		}
	}

	if start == -1 {
		if body == "" {
			return doc
		}
		base := strings.TrimRight(doc, "\n")
		if base == "" {
			return body
		}
		return base + "\n\n" + strings.TrimRight(body, "\n") + "\n"
	}

	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "## ") || strings.HasPrefix(trimmed, "# ") {
			end = i
			break
		}
	}
	// Keep the blank lines separating this section from the next so repeated
	// upserts don't eat a line each run.
	for end-1 > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}

	out := append([]string{}, lines[:start]...)
	if body != "" {
		out = append(out, strings.Split(strings.TrimRight(body, "\n"), "\n")...)
	} else if end < len(lines) {
		// Removing the section: drop the blank line that preceded it too.
		for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
			out = out[:len(out)-1]
		}
		out = append(out, "")
	}
	out = append(out, lines[end:]...)
	return strings.Join(out, "\n")
}
