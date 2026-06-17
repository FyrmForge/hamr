package devserver

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMCPEnabledTools(t *testing.T) {
	tests := []struct {
		name   string
		access map[string]string
		want   []string
		absent []string
	}{
		{
			name:   "no access table = zero tools",
			access: nil,
			absent: []string{"dev.info", "logs.read", "make.run"},
		},
		{
			name:   "docker read exposes logs/status but not restart/wipe",
			access: map[string]string{"docker": "read"},
			want:   []string{"docker.logs", "docker.status"},
			absent: []string{"docker.restart", "docker.wipe"},
		},
		{
			name:   "docker write implies read plus destructive",
			access: map[string]string{"docker": "write"},
			want:   []string{"docker.logs", "docker.status", "docker.restart", "docker.wipe"},
		},
		{
			name:   "build is write-only",
			access: map[string]string{"build": "write"},
			want:   []string{"rule.run", "rebuild.all", "make.run"},
		},
		{
			name:   "build read exposes nothing",
			access: map[string]string{"build": "read"},
			absent: []string{"rule.run", "rebuild.all", "make.run"},
		},
		{
			name:   "logs read exposes logs.read and console.read",
			access: map[string]string{"logs": "read"},
			want:   []string{"logs.read", "console.read"},
		},
		{
			name:   "stripe write exposes list + lifecycle",
			access: map[string]string{"stripe": "write"},
			want:   []string{"stripe.list", "stripe.complete", "stripe.expire", "stripe.refund"},
		},
		{
			name:   "deny level exposes nothing",
			access: map[string]string{"mail": "deny"},
			absent: []string{"mail.list", "mail.clear"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MCPConfig{Access: tt.access}.EnabledTools()
			for _, tool := range tt.want {
				assert.Truef(t, got[tool], "expected tool %q exposed", tool)
			}
			for _, tool := range tt.absent {
				assert.Falsef(t, got[tool], "expected tool %q NOT exposed", tool)
			}
		})
	}
}

func TestMCPValidate(t *testing.T) {
	require.NoError(t, MCPConfig{Access: map[string]string{"docker": "read", "build": "write"}}.validate())
	assert.Error(t, MCPConfig{Access: map[string]string{"bogus": "read"}}.validate(), "unknown area")
	assert.Error(t, MCPConfig{Access: map[string]string{"docker": "execute"}}.validate(), "invalid level")
}

func TestMCPMakeTargetAllowed(t *testing.T) {
	assert.True(t, MCPConfig{}.MakeTargetAllowed("anything"), "empty allowlist allows any")

	limited := MCPConfig{MakeTargets: []string{"ai", "test"}}
	assert.True(t, limited.MakeTargetAllowed("test"))
	assert.False(t, limited.MakeTargetAllowed("deploy"))
}

func TestMCPResolvedDefaults(t *testing.T) {
	assert.Equal(t, 20*time.Second, MCPConfig{}.ResolvedMakeWait())
	assert.Equal(t, ".hamr/mcp_logs.txt", MCPConfig{}.ResolvedLogFile())
	assert.Empty(t, MCPConfig{LogFile: "none"}.ResolvedLogFile(), "none disables")
}
