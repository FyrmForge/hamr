package generator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func serviceTestProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hamr.toml"), []byte("[dev]\nproxy_target = \"$PORT\"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte("PORT=8080\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env.example"), []byte("PORT=8080\n"), 0o644))
	return dir
}

func readServiceFile(t *testing.T, dir string, parts ...string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(append([]string{dir}, parts...)...))
	require.NoError(t, err)
	s := string(content)
	assert.NotContains(t, s, "{{", "unrendered template directive in %s", filepath.Join(parts...))
	return s
}

func TestServiceConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     ServiceConfig
		wantErr string
	}{
		{
			name:    "empty name",
			cfg:     ServiceConfig{Type: "worker", Module: "github.com/t/p", GoVersion: "1.25.0"},
			wantErr: "required",
		},
		{
			name:    "invalid type",
			cfg:     ServiceConfig{Name: "w", Type: "cron", Module: "github.com/t/p", GoVersion: "1.25.0"},
			wantErr: "invalid service type",
		},
		{
			name:    "missing module",
			cfg:     ServiceConfig{Name: "w", Type: "worker", GoVersion: "1.25.0"},
			wantErr: "module path is required",
		},
		{
			name:    "auth on worker",
			cfg:     ServiceConfig{Name: "w", Type: "worker", Module: "github.com/t/p", GoVersion: "1.25.0", WithDB: true, WithAuth: true},
			wantErr: "auth is only supported",
		},
		{
			name:    "auth without db",
			cfg:     ServiceConfig{Name: "w", Type: "api", Module: "github.com/t/p", GoVersion: "1.25.0", WithAuth: true},
			wantErr: "auth requires the database",
		},
		{
			name:    "locale on api",
			cfg:     ServiceConfig{Name: "w", Type: "api", Module: "github.com/t/p", GoVersion: "1.25.0", WithLocale: true},
			wantErr: "locale is only supported",
		},
		{
			name: "valid worker",
			cfg:  ServiceConfig{Name: "w", Type: "worker", Module: "github.com/t/p", GoVersion: "1.25.0", WithDB: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestServiceConfig_Validate_defaults(t *testing.T) {
	cfg := ServiceConfig{Name: "billing", Type: "api", Module: "github.com/t/p", GoVersion: "1.25.0"}
	require.NoError(t, cfg.Validate())
	assert.Equal(t, 8081, cfg.Port)
	assert.Equal(t, "postgres", cfg.Database)
	assert.Equal(t, "sqlx", cfg.DBConnector)

	// empty type drops DB.
	cfg = ServiceConfig{Name: "x", Type: "empty", Module: "github.com/t/p", GoVersion: "1.25.0", WithDB: true}
	require.NoError(t, cfg.Validate())
	assert.False(t, cfg.WithDB)
	assert.Zero(t, cfg.Port)
}

func TestServiceConfig_nameDerivations(t *testing.T) {
	cfg := ServiceConfig{Name: "billing-svc"}
	assert.Equal(t, "billingsvc", cfg.PkgName())
	assert.Equal(t, "BILLING_SVC", cfg.EnvPrefix())

	cfg = ServiceConfig{Name: "Mailer"}
	assert.Equal(t, "mailer", cfg.PkgName())
	assert.Equal(t, "MAILER", cfg.EnvPrefix())
}

func TestGenerateService_empty(t *testing.T) {
	dir := serviceTestProject(t)
	cfg := &ServiceConfig{Name: "job", Type: "empty", Module: "github.com/t/p", GoVersion: "1.25.0"}
	require.NoError(t, cfg.Validate())
	require.NoError(t, GenerateService(dir, cfg))

	mainGo := readServiceFile(t, dir, "cmd", "job", "main.go")
	assert.Contains(t, mainGo, `job "github.com/t/p/internal/job"`)
	assert.Contains(t, mainGo, "job.Run(context.Background())")

	pkg := readServiceFile(t, dir, "internal", "job", "job.go")
	assert.Contains(t, pkg, "package job")

	dockerfile := readServiceFile(t, dir, "cmd", "job", "Dockerfile")
	assert.Contains(t, dockerfile, "./cmd/job")
	assert.NotContains(t, dockerfile, "EXPOSE")
	assert.NotContains(t, dockerfile, "/frontend")

	toml := readServiceFile(t, dir, "hamr.toml")
	assert.Contains(t, toml, `proxy_target = "$PORT"`) // original content preserved
	assert.Contains(t, toml, `name = "job"`)
	assert.Contains(t, toml, `cmd = "go build -o ./bin/job ./cmd/job"`)
	assert.Contains(t, toml, `run = "./bin/job"`)
	assert.NotContains(t, toml, "depends")

	// No port appended for non-HTTP services.
	env, err := os.ReadFile(filepath.Join(dir, ".env"))
	require.NoError(t, err)
	assert.NotContains(t, string(env), "_PORT")
}

func TestGenerateService_worker(t *testing.T) {
	dir := serviceTestProject(t)
	cfg := &ServiceConfig{Name: "mailer", Type: "worker", Module: "github.com/t/p", GoVersion: "1.25.0", WithDB: true, Database: "postgres", DBConnector: "sqlx"}
	require.NoError(t, cfg.Validate())
	require.NoError(t, GenerateService(dir, cfg))

	mainGo := readServiceFile(t, dir, "cmd", "mailer", "main.go")
	assert.Contains(t, mainGo, "signal.NotifyContext")
	assert.Contains(t, mainGo, "github.com/FyrmForge/hamr/pkg/db")
	assert.Contains(t, mainGo, "postgres.NewStore(database)")
	assert.Contains(t, mainGo, "Store: store")

	worker := readServiceFile(t, dir, "internal", "mailer", "worker.go")
	assert.Contains(t, worker, "package mailer")
	assert.Contains(t, worker, "Store repo.Store")
	assert.Contains(t, worker, "func Run(ctx context.Context, deps Deps) error")
}

func TestGenerateService_workerNoDB(t *testing.T) {
	dir := serviceTestProject(t)
	cfg := &ServiceConfig{Name: "ticker", Type: "worker", Module: "github.com/t/p", GoVersion: "1.25.0"}
	require.NoError(t, cfg.Validate())
	require.NoError(t, GenerateService(dir, cfg))

	mainGo := readServiceFile(t, dir, "cmd", "ticker", "main.go")
	assert.NotContains(t, mainGo, "NewStore")
	assert.NotContains(t, mainGo, "DATABASE_URL")

	worker := readServiceFile(t, dir, "internal", "ticker", "worker.go")
	assert.NotContains(t, worker, "repo.Store")
}

func TestGenerateService_api(t *testing.T) {
	dir := serviceTestProject(t)
	cfg := &ServiceConfig{Name: "billing", Type: "api", Module: "github.com/t/p", GoVersion: "1.25.0", WithDB: true, WithAuth: true, Port: 9090, Database: "postgres", DBConnector: "sqlx", ProjectSlug: "myproj"}
	require.NoError(t, cfg.Validate())
	require.NoError(t, GenerateService(dir, cfg))

	mainGo := readServiceFile(t, dir, "cmd", "billing", "main.go")
	assert.Contains(t, mainGo, `config.GetEnvOrDefaultInt("BILLING_PORT", 9090)`)
	assert.Contains(t, mainGo, "auth.NewSessionManager(store")
	assert.Contains(t, mainGo, "api.RegisterRoutes(srv, &api.Deps{")
	assert.Contains(t, mainGo, "localhost:5432/myproj")

	server := readServiceFile(t, dir, "internal", "billing", "api", "server.go")
	assert.Contains(t, server, "package api")
	assert.Contains(t, server, "hamrmw.NewBrowserAuth")
	assert.Contains(t, server, "deps.Store.Health")

	dockerfile := readServiceFile(t, dir, "cmd", "billing", "Dockerfile")
	assert.Contains(t, dockerfile, "EXPOSE 9090")
	assert.NotContains(t, dockerfile, "/frontend")

	env := readServiceFile(t, dir, ".env")
	assert.Contains(t, env, "BILLING_PORT=9090")
	envExample := readServiceFile(t, dir, ".env.example")
	assert.Contains(t, envExample, "BILLING_PORT=9090")
}

func TestGenerateService_apiNoDBNoAuth(t *testing.T) {
	dir := serviceTestProject(t)
	cfg := &ServiceConfig{Name: "ping", Type: "api", Module: "github.com/t/p", GoVersion: "1.25.0"}
	require.NoError(t, cfg.Validate())
	require.NoError(t, GenerateService(dir, cfg))

	mainGo := readServiceFile(t, dir, "cmd", "ping", "main.go")
	assert.NotContains(t, mainGo, "NewStore")
	assert.NotContains(t, mainGo, "SessionManager")

	server := readServiceFile(t, dir, "internal", "ping", "api", "server.go")
	assert.NotContains(t, server, "BrowserAuth")
	assert.NotContains(t, server, "repo.Store")
	assert.Contains(t, server, `"status": "healthy"`)
}

func TestGenerateService_html(t *testing.T) {
	dir := serviceTestProject(t)
	cfg := &ServiceConfig{Name: "admin", Type: "html", Module: "github.com/t/p", GoVersion: "1.25.0", WithDB: true, WithAuth: true, WithLocale: true, DefaultLocale: "de", Database: "sqlite", DBConnector: "sqlx", ProjectSlug: "myproj", HasTemplRule: true}
	require.NoError(t, cfg.Validate())
	require.NoError(t, GenerateService(dir, cfg))

	mainGo := readServiceFile(t, dir, "cmd", "admin", "main.go")
	assert.Contains(t, mainGo, `config.GetEnvOrDefaultInt("ADMIN_PORT", 8081)`)
	assert.Contains(t, mainGo, `db "github.com/FyrmForge/hamr/pkg/db/sqlite"`)
	assert.Contains(t, mainGo, "sqlite.NewStore(database)")
	assert.Contains(t, mainGo, `DefaultLocale:     "de"`)
	assert.Contains(t, mainGo, "adminweb.RegisterRoutes(srv, &adminweb.Deps{")
	assert.Contains(t, mainGo, "components.StaticBaseURL")
	assert.Contains(t, mainGo, "./data/myproj.db")

	server := readServiceFile(t, dir, "internal", "admin", "web", "server.go")
	assert.Contains(t, server, "package web")
	assert.Contains(t, server, "hamrmw.ErrorPages(components.ErrorPage)")
	assert.Contains(t, server, "hamrmw.LocaleFromPreference")
	assert.Contains(t, server, "hamrmw.NewBrowserAuth")

	templFile := readServiceFile(t, dir, "internal", "admin", "web", "home.templ")
	assert.Contains(t, templFile, "@components.Layout(c, \"admin\")")

	handler := readServiceFile(t, dir, "internal", "admin", "web", "home.go")
	assert.Contains(t, handler, "respond.HTML")

	dockerfile := readServiceFile(t, dir, "cmd", "admin", "Dockerfile")
	assert.Contains(t, dockerfile, "COPY --from=builder /app/frontend /frontend")
	assert.Contains(t, dockerfile, "ENV DATABASE_PATH=/data/myproj.db")

	toml := readServiceFile(t, dir, "hamr.toml")
	assert.Contains(t, toml, `depends = ["templ"]`)
	assert.Contains(t, toml, `reload = "full"`)
	assert.Contains(t, toml, "**/*.templ")
}

func TestGenerateService_htmlWithoutTemplRule(t *testing.T) {
	dir := serviceTestProject(t)
	cfg := &ServiceConfig{Name: "admin", Type: "html", Module: "github.com/t/p", GoVersion: "1.25.0"}
	require.NoError(t, cfg.Validate())
	require.NoError(t, GenerateService(dir, cfg))

	// No templ rule in the project: an unresolvable depends would hard-stop
	// hamr dev, so it must be omitted.
	toml := readServiceFile(t, dir, "hamr.toml")
	assert.NotContains(t, toml, "depends")
	assert.Contains(t, toml, `reload = "full"`)
}

func TestGenerateService_existingDirFails(t *testing.T) {
	dir := serviceTestProject(t)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "cmd", "job"), 0o755))

	cfg := &ServiceConfig{Name: "job", Type: "empty", Module: "github.com/t/p", GoVersion: "1.25.0"}
	require.NoError(t, cfg.Validate())
	err := GenerateService(dir, cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestGenerateService_missingEnvFilesSkipped(t *testing.T) {
	dir := serviceTestProject(t)
	require.NoError(t, os.Remove(filepath.Join(dir, ".env")))
	require.NoError(t, os.Remove(filepath.Join(dir, ".env.example")))

	cfg := &ServiceConfig{Name: "api2", Type: "api", Module: "github.com/t/p", GoVersion: "1.25.0"}
	require.NoError(t, cfg.Validate())
	require.NoError(t, GenerateService(dir, cfg))

	_, err := os.Stat(filepath.Join(dir, ".env"))
	assert.True(t, os.IsNotExist(err), "missing .env must not be created")
}

func TestGenerateService_hamrTomlStaysParseable(t *testing.T) {
	dir := serviceTestProject(t)
	cfg := &ServiceConfig{Name: "w1", Type: "worker", Module: "github.com/t/p", GoVersion: "1.25.0"}
	require.NoError(t, cfg.Validate())
	require.NoError(t, GenerateService(dir, cfg))

	// A second service appends alongside the first.
	cfg2 := &ServiceConfig{Name: "w2", Type: "api", Module: "github.com/t/p", GoVersion: "1.25.0"}
	require.NoError(t, cfg2.Validate())
	require.NoError(t, GenerateService(dir, cfg2))

	content := readServiceFile(t, dir, "hamr.toml")
	assert.Equal(t, 1, strings.Count(content, `name = "w1"`))
	assert.Equal(t, 1, strings.Count(content, `name = "w2"`))

	var parsed struct {
		Dev struct {
			Watch []struct {
				Name string `toml:"name"`
			} `toml:"watch"`
		} `toml:"dev"`
	}
	_, err := toml.Decode(content, &parsed)
	require.NoError(t, err, "appended hamr.toml must stay valid TOML")
	require.Len(t, parsed.Dev.Watch, 2)
	assert.Equal(t, "w1", parsed.Dev.Watch[0].Name)
	assert.Equal(t, "w2", parsed.Dev.Watch[1].Name)
}
