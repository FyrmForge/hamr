package tui

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMCPTabRouting(t *testing.T) {
	m := NewModel(NewHotkeySource())

	// No MCP gateway known yet: no MCP tab.
	assert.Equal(t, -1, m.mcpTabIndex())
	assert.Equal(t, 1, m.tabCount())

	// Gateway reported → MCP tab appears as the last tab.
	m.mcpKnown = true
	assert.Equal(t, 1, m.mcpTabIndex())
	assert.Equal(t, 2, m.tabCount())

	// Lines route into the MCP buffer and currentLogs reflects it when active.
	m.appendMCPLog("12:00 [mcp] dev.info → ok")
	m.viewMode = m.mcpTabIndex()
	require.Len(t, m.currentLogs(), 1)
	assert.Contains(t, m.currentLogs()[0], "dev.info → ok") // styled (ANSI), so substring
	assert.Equal(t, "mcp", m.activeTabTitle())

	// Clearing empties only the MCP buffer.
	m.clearActiveLog()
	assert.Empty(t, m.mcpLogs)
}

func TestMCPTabIndexShiftsWithDockerTabs(t *testing.T) {
	m := NewModel(NewHotkeySource())
	m.mcpKnown = true

	// Activate the MCP tab (index 1 with no docker stacks).
	m.viewMode = m.mcpTabIndex()
	assert.Equal(t, 1, m.viewMode)

	// Registering docker stacks pushes the MCP tab to the end; the active tab
	// must follow it, not silently become a docker tab.
	m.setDockerTabs([]string{"infra", "cache"})
	assert.Equal(t, 3, m.mcpTabIndex())
	assert.Equal(t, 3, m.viewMode, "active MCP tab followed its shifted index")
	assert.Equal(t, "mcp", m.activeTabTitle())
}

func TestMCPTabCycle(t *testing.T) {
	m := NewModel(NewHotkeySource())
	m.mcpKnown = true
	m.setDockerTabs([]string{"infra"})
	// Tabs: 0 hamr, 1 infra(docker), 2 mcp.
	assert.Equal(t, 3, m.tabCount())

	m.cycleTab(true)
	assert.Equal(t, 1, m.viewMode)
	m.cycleTab(true)
	assert.Equal(t, 2, m.viewMode) // mcp
	m.cycleTab(true)
	assert.Equal(t, 0, m.viewMode) // wraps back to hamr
}
