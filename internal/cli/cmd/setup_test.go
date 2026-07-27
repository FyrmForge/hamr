package cmd

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FyrmForge/hamr/internal/devserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const setupBaseTOML = `[hamr]
version = "0.24.0"

[static]
dir = "frontend/static"

[dev]
log_file = ".hamr/dev_logs.txt"

[[dev.watch]]
name = "site"
watch = "**/*.go"
cmd = "go build ./..."

[lint.templ]
img-alt = "warning"
`

func writeSetupProject(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hamr.toml"), []byte(content), 0o644))
	return dir
}

func TestWriteMCPConfig_InsertsBlocksAndKeepsRest(t *testing.T) {
	dir := writeSetupProject(t, setupBaseTOML)
	path := filepath.Join(dir, "hamr.toml")

	access := map[string]string{"dev": "read", "logs": "read", "build": "write", "stripe": "deny"}
	changed, err := writeMCPConfig(path, true, access, false)
	require.NoError(t, err)
	assert.True(t, changed)

	// The rest of the file survives untouched.
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), `dir = "frontend/static"`)
	assert.Contains(t, string(data), `img-alt = "warning"`)

	// And the result is a config the dev server accepts, with the levels set.
	cfg, err := devserver.LoadConfig(path)
	require.NoError(t, err)
	assert.True(t, cfg.Dev.MCP.Enabled)
	assert.Equal(t, access, cfg.Dev.MCP.Access)
}

func TestWriteMCPConfig_RewritesExistingBlocks(t *testing.T) {
	dir := writeSetupProject(t, setupBaseTOML+`
[dev.mcp]
enabled = true

[dev.mcp.access]
dev = "read"
docker = "write"
`)
	path := filepath.Join(dir, "hamr.toml")

	changed, err := writeMCPConfig(path, false, map[string]string{"dev": "read"}, false)
	require.NoError(t, err)
	assert.True(t, changed)

	cfg, err := devserver.LoadConfig(path)
	require.NoError(t, err)
	assert.False(t, cfg.Dev.MCP.Enabled)
	assert.Equal(t, map[string]string{"dev": "read"}, cfg.Dev.MCP.Access,
		"stale areas from the previous block must not survive")
}

func TestWriteMCPConfig_NoChangeAndDryRun(t *testing.T) {
	access := map[string]string{"dev": "read"}

	dir := writeSetupProject(t, setupBaseTOML)
	path := filepath.Join(dir, "hamr.toml")
	_, err := writeMCPConfig(path, true, access, false)
	require.NoError(t, err)

	changed, err := writeMCPConfig(path, true, access, false)
	require.NoError(t, err)
	assert.False(t, changed, "writing the same values twice is a no-op")

	// Dry run reports the change without touching the file.
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	changed, err = writeMCPConfig(path, false, access, true)
	require.NoError(t, err)
	assert.True(t, changed)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after))
}

func TestCollectAccess_ReadsFormBindings(t *testing.T) {
	c := &setupChoices{Access: map[string]string{}}
	for _, area := range devserver.MCPAreaNames() {
		c.Access[area] = "deny"
	}

	accessSelects(c) // builds the bindings the pickers write through
	*c.accessPtrs["docker"] = "write"
	c.collectAccess()

	assert.Equal(t, "write", c.Access["docker"])
	assert.Equal(t, "deny", c.Access["mail"])
}

func TestApplySetup_WritesConfigAndAgentFiles(t *testing.T) {
	dir := writeSetupProject(t, setupBaseTOML)

	c := &setupChoices{
		Agents:  []string{"claude"},
		Enabled: true,
		Access:  map[string]string{"dev": "read", "build": "write"},
	}
	require.NoError(t, applySetup(dir, c, false))

	cfg, err := devserver.LoadConfig(filepath.Join(dir, "hamr.toml"))
	require.NoError(t, err)
	assert.True(t, cfg.Dev.MCP.Enabled)
	assert.Equal(t, "write", cfg.Dev.MCP.Access["build"])

	mcpJSON, err := os.ReadFile(filepath.Join(dir, ".mcp.json"))
	require.NoError(t, err)
	assert.Contains(t, string(mcpJSON), "hamr-dev")

	// Unselected agents are left alone.
	_, err = os.Stat(filepath.Join(dir, "opencode.json"))
	assert.True(t, os.IsNotExist(err))
}

func TestApplySetup_DryRunWritesNothing(t *testing.T) {
	dir := writeSetupProject(t, setupBaseTOML)

	c := &setupChoices{Agents: []string{"claude"}, Enabled: true, Access: map[string]string{"dev": "read"}}
	require.NoError(t, applySetup(dir, c, true))

	data, err := os.ReadFile(filepath.Join(dir, "hamr.toml"))
	require.NoError(t, err)
	assert.Equal(t, setupBaseTOML, string(data))
	_, err = os.Stat(filepath.Join(dir, ".mcp.json"))
	assert.True(t, os.IsNotExist(err))
}

const agentsDoc = `# myapp — Project Conventions

## Build & Test

make test

## CSS

Tailwind.
`

func TestBuildAgentMCPSection_ScopedToGrantedAreas(t *testing.T) {
	section := buildAgentMCPSection(true, map[string]string{
		"logs": "read", "build": "write", "mail": "read", "stripe": "deny",
	})

	assert.Contains(t, section, "logs.read")
	assert.Contains(t, section, "rebuild.all", "build is write, so the rebuild habit applies")
	assert.Contains(t, section, "mail.get")
	assert.NotContains(t, section, "mail.clear", "mail is read-only here")
	assert.NotContains(t, section, "stripe", "denied areas produce no instructions")

	assert.Empty(t, buildAgentMCPSection(false, map[string]string{"logs": "read"}),
		"gateway off means no instructions")
	assert.Empty(t, buildAgentMCPSection(true, map[string]string{"logs": "deny"}),
		"nothing granted means no instructions")
}

func TestUpsertMarkdownSection(t *testing.T) {
	body := "## hamr MCP\n\nUse the tools.\n"

	added := upsertMarkdownSection(agentsDoc, "hamr MCP", body)
	assert.Contains(t, added, "Use the tools.")
	assert.Contains(t, added, "## CSS", "other sections survive")

	assert.Equal(t, added, upsertMarkdownSection(added, "hamr MCP", body),
		"re-upserting identical content is a no-op")

	replaced := upsertMarkdownSection(added, "hamr MCP", "## hamr MCP\n\nDifferent.\n")
	assert.Contains(t, replaced, "Different.")
	assert.NotContains(t, replaced, "Use the tools.")

	removed := upsertMarkdownSection(added, "hamr MCP", "")
	assert.NotContains(t, removed, "hamr MCP")
	assert.Contains(t, removed, "## CSS")
	assert.Equal(t, removed, upsertMarkdownSection(removed, "hamr MCP", ""))
}

func TestUpsertMarkdownSection_MidDocumentSectionKeepsNeighbours(t *testing.T) {
	doc := "# Title\n\n## hamr MCP\n\nold\n\n## Next\n\nkeep me\n"

	out := upsertMarkdownSection(doc, "hamr MCP", "## hamr MCP\n\nnew\n")
	assert.Contains(t, out, "new")
	assert.NotContains(t, out, "old")
	assert.Contains(t, out, "## Next\n\nkeep me")
}

func TestApplySetup_WritesAgentInstructionFiles(t *testing.T) {
	dir := writeSetupProject(t, setupBaseTOML)
	for _, f := range agentInstructionFiles {
		require.NoError(t, os.WriteFile(filepath.Join(dir, f), []byte(agentsDoc), 0o644))
	}

	c := &setupChoices{Enabled: true, Access: map[string]string{"logs": "read", "build": "write"}}
	require.NoError(t, applySetup(dir, c, false))

	for _, f := range agentInstructionFiles {
		data, err := os.ReadFile(filepath.Join(dir, f))
		require.NoError(t, err)
		assert.Contains(t, string(data), "## hamr MCP", f)
		assert.Contains(t, string(data), "console.read", f)
		assert.Contains(t, string(data), "## CSS", f)
	}

	// Turning the gateway off removes the section again.
	off := &setupChoices{Enabled: false, Access: map[string]string{"logs": "read"}}
	require.NoError(t, applySetup(dir, off, false))
	data, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	require.NoError(t, err)
	assert.NotContains(t, string(data), "hamr MCP")
}

func TestWriteAgentMCPSection_SkipsMissingFile(t *testing.T) {
	dir := t.TempDir()
	changed, err := writeAgentMCPSection(filepath.Join(dir, "AGENTS.md"), true,
		map[string]string{"logs": "read"}, false)
	require.NoError(t, err)
	assert.False(t, changed)
}

// Drives the real picker headlessly — huh reads scripted keystrokes and renders
// into a discard writer — so the form itself is exercised without a TTY.
// Keys: toggle the first agent, tab to the confirm and accept it, then move one
// option down in the first access select (deny → read) and accept the rest.
// The trailing input EOF ends the form, so the last group (skills) is left at
// its default rather than driven.
func TestSetupForm_DrivenHeadlessly(t *testing.T) {
	c := &setupChoices{Access: map[string]string{}}
	for _, area := range devserver.MCPAreaNames() {
		c.Access[area] = "deny"
	}

	keys := "x\r\ty\r" + "j\r\r\r\r\r\r" + "x\r" + strings.Repeat("\r", 10)
	form := newSetupForm(t.TempDir(), c).
		WithInput(strings.NewReader(keys)).
		WithOutput(io.Discard)

	require.NoError(t, form.Run())
	c.collectAccess()

	assert.Equal(t, []string{"claude"}, c.Agents, "x toggled the first agent")
	assert.True(t, c.Enabled, "y accepted the gateway confirm")
	assert.Equal(t, "read", c.Access["build"], "one step down from deny is read")
	assert.Equal(t, "deny", c.Access["stripe"], "untouched selects keep their seeded value")
}
