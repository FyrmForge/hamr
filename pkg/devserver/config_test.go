package devserver

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "hamr.toml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func TestLoadConfig_Minimal(t *testing.T) {
	path := writeConfig(t, `
[[dev.watch]]
name = "go"
watch = "**/*.go"
cmd = "go build ./cmd/server"
`)

	cfg, err := LoadConfig(path)
	require.NoError(t, err)

	assert.Len(t, cfg.Dev.Watch, 1)
	assert.Equal(t, "go", cfg.Dev.Watch[0].Name)
	assert.Equal(t, StringOrSlice{"**/*.go"}, cfg.Dev.Watch[0].Watch)

	// No [proxy] section → proxy not configured, no defaults applied.
	assert.False(t, cfg.ProxyConfigured)
	assert.Equal(t, "", cfg.Proxy.Listen)
	assert.Equal(t, "", cfg.Proxy.Target)
	assert.Nil(t, cfg.Proxy.InjectReload)
	assert.Equal(t, 100*time.Millisecond, cfg.Dev.Watch[0].Debounce.Duration)
}

func TestLoadConfig_ProxyDefaults(t *testing.T) {
	path := writeConfig(t, `
[proxy]

[[dev.watch]]
name = "go"
watch = "**/*.go"
cmd = "go build ./cmd/server"
`)

	cfg, err := LoadConfig(path)
	require.NoError(t, err)

	// Empty [proxy] section → configured with defaults.
	assert.True(t, cfg.ProxyConfigured)
	assert.Equal(t, ":3000", cfg.Proxy.Listen)
	assert.Equal(t, ":8080", cfg.Proxy.Target)
	assert.True(t, *cfg.Proxy.InjectReload)
}

func TestLoadConfig_Full(t *testing.T) {
	path := writeConfig(t, `
[proxy]
listen = ":4000"
target = ":9090"
inject_reload = false

[[dev.watch]]
name = "templ"
watch = ["**/*.templ"]
ignore = ["*_templ.go"]
cmd = "templ generate"
debounce = 200
reload = "full"

[[dev.watch]]
name = "go"
watch = ["**/*.go"]
ignore = ["*_test.go"]
cmd = "go build -o ./tmp/app ./cmd/server"
run = "./tmp/app"
depends = ["templ"]
debounce = "300ms"
reload = true
env = ["PORT=8080"]
`)

	cfg, err := LoadConfig(path)
	require.NoError(t, err)

	assert.True(t, cfg.ProxyConfigured)
	assert.Equal(t, ":4000", cfg.Proxy.Listen)
	assert.Equal(t, ":9090", cfg.Proxy.Target)
	assert.False(t, *cfg.Proxy.InjectReload)

	require.Len(t, cfg.Dev.Watch, 2)

	templ := cfg.Dev.Watch[0]
	assert.Equal(t, "templ", templ.Name)
	assert.Equal(t, StringOrSlice{"**/*.templ"}, templ.Watch)
	assert.Equal(t, StringOrSlice{"*_templ.go"}, templ.Ignore)
	assert.Equal(t, 200*time.Millisecond, templ.Debounce.Duration)
	assert.Equal(t, ReloadFull, templ.Reload)

	goRule := cfg.Dev.Watch[1]
	assert.Equal(t, "go", goRule.Name)
	assert.Equal(t, StringOrSlice{"templ"}, goRule.Depends)
	assert.Equal(t, 300*time.Millisecond, goRule.Debounce.Duration)
	assert.Equal(t, ReloadFull, goRule.Reload)
	assert.Equal(t, []string{"PORT=8080"}, goRule.Env)
}

func TestLoadConfig_StringOrSlice(t *testing.T) {
	t.Run("string form", func(t *testing.T) {
		path := writeConfig(t, `
[[dev.watch]]
name = "test"
watch = "*.go"
cmd = "echo test"
`)
		cfg, err := LoadConfig(path)
		require.NoError(t, err)
		assert.Equal(t, StringOrSlice{"*.go"}, cfg.Dev.Watch[0].Watch)
	})

	t.Run("array form", func(t *testing.T) {
		path := writeConfig(t, `
[[dev.watch]]
name = "test"
watch = ["*.go", "*.templ"]
cmd = "echo test"
`)
		cfg, err := LoadConfig(path)
		require.NoError(t, err)
		assert.Equal(t, StringOrSlice{"*.go", "*.templ"}, cfg.Dev.Watch[0].Watch)
	})

	t.Run("empty array", func(t *testing.T) {
		path := writeConfig(t, `
[[dev.watch]]
name = "test"
watch = []
cmd = "echo test"
`)
		_, err := LoadConfig(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "at least one watch pattern")
	})
}

func TestLoadConfig_ReloadScope(t *testing.T) {
	tests := []struct {
		toml string
		want ReloadScope
	}{
		{`reload = true`, ReloadFull},
		{`reload = false`, ReloadNone},
		{`reload = "full"`, ReloadFull},
		{`reload = "css"`, ReloadCSS},
		{`reload = "none"`, ReloadNone},
		{`reload = "Full"`, ReloadFull},  // mixed case
		{`reload = "CSS"`, ReloadCSS},    // upper case
		{`reload = "true"`, ReloadFull},  // string "true"
		{`reload = "false"`, ReloadNone}, // string "false"
	}

	for _, tt := range tests {
		t.Run(tt.toml, func(t *testing.T) {
			path := writeConfig(t, `
[[dev.watch]]
name = "test"
watch = "*.go"
cmd = "echo test"
`+tt.toml+"\n")
			cfg, err := LoadConfig(path)
			require.NoError(t, err)
			assert.Equal(t, tt.want, cfg.Dev.Watch[0].Reload)
		})
	}
}

func TestLoadConfig_ReloadScope_Invalid(t *testing.T) {
	path := writeConfig(t, `
[[dev.watch]]
name = "test"
watch = "*.go"
cmd = "echo test"
reload = "invalid"
`)
	_, err := LoadConfig(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid reload scope")
}

func TestLoadConfig_Duration(t *testing.T) {
	t.Run("integer milliseconds", func(t *testing.T) {
		path := writeConfig(t, `
[[dev.watch]]
name = "test"
watch = "*.go"
cmd = "echo test"
debounce = 500
`)
		cfg, err := LoadConfig(path)
		require.NoError(t, err)
		assert.Equal(t, 500*time.Millisecond, cfg.Dev.Watch[0].Debounce.Duration)
	})

	t.Run("string duration", func(t *testing.T) {
		path := writeConfig(t, `
[[dev.watch]]
name = "test"
watch = "*.go"
cmd = "echo test"
debounce = "1s"
`)
		cfg, err := LoadConfig(path)
		require.NoError(t, err)
		assert.Equal(t, time.Second, cfg.Dev.Watch[0].Debounce.Duration)
	})

	t.Run("invalid string duration", func(t *testing.T) {
		path := writeConfig(t, `
[[dev.watch]]
name = "test"
watch = "*.go"
cmd = "echo test"
debounce = "not-a-duration"
`)
		_, err := LoadConfig(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid duration")
	})

	t.Run("zero defaults to 100ms", func(t *testing.T) {
		path := writeConfig(t, `
[[dev.watch]]
name = "test"
watch = "*.go"
cmd = "echo test"
`)
		cfg, err := LoadConfig(path)
		require.NoError(t, err)
		assert.Equal(t, 100*time.Millisecond, cfg.Dev.Watch[0].Debounce.Duration)
	})
}

func TestLoadConfig_DaemonOnly(t *testing.T) {
	path := writeConfig(t, `
[[dev.daemon]]
name = "tailwind"
cmd = "npx tailwindcss --watch"
env = ["NODE_ENV=development"]
`)
	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.Len(t, cfg.Dev.Daemons, 1)
	assert.Equal(t, "tailwind", cfg.Dev.Daemons[0].Name)
	assert.Equal(t, "npx tailwindcss --watch", cfg.Dev.Daemons[0].Cmd)
	assert.Equal(t, []string{"NODE_ENV=development"}, cfg.Dev.Daemons[0].Env)
	assert.Empty(t, cfg.Dev.Watch)
}

func TestLoadConfig_DaemonsWithWatchRules(t *testing.T) {
	path := writeConfig(t, `
[[dev.daemon]]
name = "tailwind"
cmd = "npx tailwindcss --watch"

[[dev.watch]]
name = "go"
watch = "**/*.go"
cmd = "go build ./cmd/server"
`)
	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	assert.Len(t, cfg.Dev.Daemons, 1)
	assert.Len(t, cfg.Dev.Watch, 1)
}

func TestLoadConfig_ValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		toml    string
		wantErr string
	}{
		{
			name:    "no watch rules or daemons",
			toml:    `[dev]`,
			wantErr: "no watch rules, daemons, or docker compose",
		},
		{
			name: "missing name",
			toml: `
[[dev.watch]]
watch = "*.go"
cmd = "echo"
`,
			wantErr: "name is required",
		},
		{
			name: "duplicate name",
			toml: `
[[dev.watch]]
name = "go"
watch = "*.go"
cmd = "echo"

[[dev.watch]]
name = "go"
watch = "*.go"
cmd = "echo"
`,
			wantErr: "duplicate name",
		},
		{
			name: "missing watch pattern",
			toml: `
[[dev.watch]]
name = "go"
cmd = "echo"
`,
			wantErr: "at least one watch pattern",
		},
		{
			name: "missing cmd and run",
			toml: `
[[dev.watch]]
name = "go"
watch = "*.go"
`,
			wantErr: "cmd or run is required",
		},
		{
			name: "unknown dependency",
			toml: `
[[dev.watch]]
name = "go"
watch = "*.go"
cmd = "echo"
depends = ["nonexistent"]
`,
			wantErr: "unknown dependency",
		},
		{
			name: "dependency cycle",
			toml: `
[[dev.watch]]
name = "a"
watch = "*.go"
cmd = "echo"
depends = ["b"]

[[dev.watch]]
name = "b"
watch = "*.go"
cmd = "echo"
depends = ["a"]
`,
			wantErr: "dependency cycle",
		},
		{
			name: "3-node dependency cycle",
			toml: `
[[dev.watch]]
name = "a"
watch = "*.go"
cmd = "echo"
depends = ["c"]

[[dev.watch]]
name = "b"
watch = "*.go"
cmd = "echo"
depends = ["a"]

[[dev.watch]]
name = "c"
watch = "*.go"
cmd = "echo"
depends = ["b"]
`,
			wantErr: "dependency cycle",
		},
		{
			name: "daemon missing name",
			toml: `
[[dev.daemon]]
cmd = "npx tailwindcss --watch"
`,
			wantErr: "name is required",
		},
		{
			name: "daemon missing cmd",
			toml: `
[[dev.daemon]]
name = "tailwind"
`,
			wantErr: "cmd is required",
		},
		{
			name: "duplicate daemon name",
			toml: `
[[dev.daemon]]
name = "tw"
cmd = "echo a"

[[dev.daemon]]
name = "tw"
cmd = "echo b"
`,
			wantErr: "duplicate daemon name",
		},
		{
			name: "daemon name collides with watch rule",
			toml: `
[[dev.daemon]]
name = "go"
cmd = "echo daemon"

[[dev.watch]]
name = "go"
watch = "*.go"
cmd = "echo watch"
`,
			wantErr: "duplicate name",
		},
		{
			name: "invalid proxy listen port",
			toml: `
[proxy]
listen = "noport"

[[dev.watch]]
name = "go"
watch = "*.go"
cmd = "echo"
`,
			wantErr: "proxy.listen",
		},
		{
			name: "invalid proxy target port",
			toml: `
[proxy]
target = ":99999"

[[dev.watch]]
name = "go"
watch = "*.go"
cmd = "echo"
`,
			wantErr: "proxy.target",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfig(t, tt.toml)
			_, err := LoadConfig(path)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestLoadConfig_FileNotFound(t *testing.T) {
	_, err := LoadConfig("/nonexistent/hamr.toml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read config")
}

func TestLoadConfig_MalformedTOML(t *testing.T) {
	path := writeConfig(t, `this is not valid toml {{{`)
	_, err := LoadConfig(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse config")
}

func TestValidateAddr(t *testing.T) {
	tests := []struct {
		addr    string
		wantErr bool
	}{
		{":8080", false},
		{":0", false},
		{":65535", false},
		{"localhost:3000", false},
		{"noport", true},
		{":99999", true},
		{":abc", true},
		{":-1", true},
	}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			err := validateAddr(tt.addr)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestDetectCycles(t *testing.T) {
	t.Run("no cycle", func(t *testing.T) {
		err := detectCycles([]WatchRule{
			{Name: "a"},
			{Name: "b", Depends: StringOrSlice{"a"}},
			{Name: "c", Depends: StringOrSlice{"b"}},
		})
		assert.NoError(t, err)
	})

	t.Run("self cycle", func(t *testing.T) {
		err := detectCycles([]WatchRule{
			{Name: "a", Depends: StringOrSlice{"a"}},
		})
		assert.Error(t, err)
	})

	t.Run("diamond no cycle", func(t *testing.T) {
		err := detectCycles([]WatchRule{
			{Name: "a"},
			{Name: "b", Depends: StringOrSlice{"a"}},
			{Name: "c", Depends: StringOrSlice{"a"}},
			{Name: "d", Depends: StringOrSlice{"b", "c"}},
		})
		assert.NoError(t, err)
	})
}

func TestLoadConfig_DockerCompose(t *testing.T) {
	path := writeConfig(t, `
[[dev.docker_compose]]
name = "infra"
file = "docker-compose.yml"
services = ["postgres", "redis"]
keep_running = true
env = ["COMPOSE_PROJECT_NAME=myapp"]

[[dev.watch]]
name = "go"
watch = "**/*.go"
cmd = "go build ./cmd/server"
`)
	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.Len(t, cfg.Dev.DockerCompose, 1)
	dc := cfg.Dev.DockerCompose[0]
	assert.Equal(t, "infra", dc.Name)
	assert.Equal(t, "docker-compose.yml", dc.File)
	assert.Equal(t, []string{"postgres", "redis"}, dc.Services)
	assert.True(t, dc.KeepRunning)
	assert.Equal(t, []string{"COMPOSE_PROJECT_NAME=myapp"}, dc.Env)
}

func TestLoadConfig_DockerComposeOnly(t *testing.T) {
	path := writeConfig(t, `
[[dev.docker_compose]]
name = "infra"
file = "docker-compose.yml"
`)
	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.Len(t, cfg.Dev.DockerCompose, 1)
	assert.Empty(t, cfg.Dev.Watch)
	assert.Empty(t, cfg.Dev.Daemons)
}

func TestLoadConfig_DockerCompose_ValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		toml    string
		wantErr string
	}{
		{
			name: "missing name",
			toml: `
[[dev.docker_compose]]
file = "docker-compose.yml"
`,
			wantErr: "name is required",
		},
		{
			name: "missing file",
			toml: `
[[dev.docker_compose]]
name = "infra"
`,
			wantErr: "file is required",
		},
		{
			name: "duplicate docker compose name",
			toml: `
[[dev.docker_compose]]
name = "infra"
file = "docker-compose.yml"

[[dev.docker_compose]]
name = "infra"
file = "docker-compose.dev.yml"
`,
			wantErr: "duplicate name",
		},
		{
			name: "docker compose name collides with daemon",
			toml: `
[[dev.docker_compose]]
name = "bg"
file = "docker-compose.yml"

[[dev.daemon]]
name = "bg"
cmd = "echo daemon"
`,
			wantErr: "duplicate",
		},
		{
			name: "docker compose name collides with watch rule",
			toml: `
[[dev.docker_compose]]
name = "go"
file = "docker-compose.yml"

[[dev.watch]]
name = "go"
watch = "*.go"
cmd = "echo"
`,
			wantErr: "duplicate name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfig(t, tt.toml)
			_, err := LoadConfig(path)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestLoadConfig_RunOnly(t *testing.T) {
	path := writeConfig(t, `
[[dev.watch]]
name = "server"
watch = "**/*.go"
run = "./tmp/app"
`)
	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	assert.Equal(t, "", cfg.Dev.Watch[0].Cmd)
	assert.Equal(t, "./tmp/app", cfg.Dev.Watch[0].Run)
}
