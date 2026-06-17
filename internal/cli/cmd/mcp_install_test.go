package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInstallClaudeMergesAndIsIdempotent(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".mcp.json")
	// Pre-existing config with an unrelated server that must survive.
	require.NoError(t, os.WriteFile(path, []byte(`{"mcpServers":{"other":{"command":"x"}}}`), 0o644))

	_, changed, err := installClaude(root, false)
	require.NoError(t, err)
	assert.True(t, changed)

	var cfg map[string]any
	data, _ := os.ReadFile(path)
	require.NoError(t, json.Unmarshal(data, &cfg))
	servers := cfg["mcpServers"].(map[string]any)
	assert.Contains(t, servers, "other", "existing server preserved")
	assert.Contains(t, servers, "hamr-dev", "hamr-dev added")

	// Second run: no change.
	_, changed, err = installClaude(root, false)
	require.NoError(t, err)
	assert.False(t, changed, "re-run should be idempotent")
}

func TestInstallOpencodeShape(t *testing.T) {
	root := t.TempDir()
	_, changed, err := installOpencode(root, false)
	require.NoError(t, err)
	assert.True(t, changed)

	var cfg map[string]any
	data, _ := os.ReadFile(filepath.Join(root, "opencode.json"))
	require.NoError(t, json.Unmarshal(data, &cfg))
	entry := cfg["mcp"].(map[string]any)["hamr-dev"].(map[string]any)
	assert.Equal(t, "local", entry["type"])
	assert.Equal(t, []any{"hamr", "mcp"}, entry["command"])
	assert.Equal(t, true, entry["enabled"])
}

func TestInstallDryRunWritesNothing(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".mcp.json")
	_, changed, err := installClaude(root, true)
	require.NoError(t, err)
	assert.True(t, changed, "dry-run reports it would change")
	assert.NoFileExists(t, path, "dry-run must not write")
}

func TestUpsertTOMLBlock(t *testing.T) {
	// Append into a file with unrelated content + comments (must be preserved).
	existing := "# my codex config\nmodel = \"gpt-5\"\n\n[mcp_servers.other]\ncommand = \"y\"\n"
	block := "[mcp_servers.hamr-dev]\ncommand = \"hamr\"\nargs = [\"mcp\"]\n"

	got := upsertTOMLBlock(existing, "[mcp_servers.hamr-dev]", block)
	assert.Contains(t, got, "# my codex config")
	assert.Contains(t, got, "[mcp_servers.other]")
	assert.Contains(t, got, "[mcp_servers.hamr-dev]")

	// Replacing is idempotent and doesn't duplicate the block.
	again := upsertTOMLBlock(got, "[mcp_servers.hamr-dev]", block)
	assert.Equal(t, 1, countOccurrences(again, "[mcp_servers.hamr-dev]"))
	assert.Equal(t, 1, countOccurrences(again, "[mcp_servers.other]"))
}

func countOccurrences(s, sub string) int {
	n := 0
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			n++
		}
	}
	return n
}
