package generator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProjectConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     ProjectConfig
		wantErr string
	}{
		{
			name:    "empty name",
			cfg:     ProjectConfig{Module: "github.com/test/proj"},
			wantErr: "project name is required",
		},
		{
			name:    "invalid name",
			cfg:     ProjectConfig{Name: "proj name", Module: "github.com/test/proj"},
			wantErr: ProjectNameFormatMessage,
		},
		{
			name:    "empty module",
			cfg:     ProjectConfig{Name: "proj"},
			wantErr: "module path is required",
		},
		{
			name:    "invalid css",
			cfg:     ProjectConfig{Name: "proj", Module: "github.com/test/proj", CSS: "bootstrap"},
			wantErr: "invalid --css value",
		},
		{
			name: "valid minimal",
			cfg:  ProjectConfig{Name: "proj", Module: "github.com/test/proj"},
		},
		{
			name: "valid dotted name",
			cfg:  ProjectConfig{Name: "topleveluk.com", Module: "github.com/test/topleveluk.com"},
		},
		{
			name: "valid with defaults filled",
			cfg:  ProjectConfig{Name: "proj", Module: "github.com/test/proj", CSS: "plain"},
		},
		{
			name: "valid tailwind",
			cfg:  ProjectConfig{Name: "proj", Module: "github.com/test/proj", CSS: "tailwind"},
		},
		{
			name: "auth implies sessions",
			cfg:  ProjectConfig{Name: "proj", Module: "github.com/test/proj", IncludeAuth: true},
		},
		{
			name:    "invalid storage backend",
			cfg:     ProjectConfig{Name: "proj", Module: "github.com/test/proj", StorageBackend: "none"},
			wantErr: "invalid --storage value",
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

func TestProjectConfig_Validate_authImpliesSessions(t *testing.T) {
	cfg := ProjectConfig{Name: "proj", Module: "github.com/test/proj", IncludeAuth: true}
	require.NoError(t, cfg.Validate())
	assert.True(t, cfg.IncludeSessions, "auth should imply sessions")
}

func TestProjectConfig_Validate_defaultValues(t *testing.T) {
	cfg := ProjectConfig{Name: "proj", Module: "github.com/test/proj"}
	require.NoError(t, cfg.Validate())
	assert.Equal(t, "plain", cfg.CSS)
	assert.Equal(t, "postgres", cfg.Database)
	assert.NotEmpty(t, cfg.GoVersion, "GoVersion should be detected from local Go installation")
}

func TestProjectSlug(t *testing.T) {
	assert.Equal(t, "topleveluk-com", ProjectSlug("TopLevelUK.com"))
	assert.Equal(t, "my-app-v2", ProjectSlug("my_app.v2"))
}

func TestBuildProjectFileList_coreFiles(t *testing.T) {
	cfg := &ProjectConfig{
		Name:   "proj",
		Module: "github.com/test/proj",
		CSS:    "plain",
	}

	files := buildProjectFileList(cfg)

	// Check that some core files are always present.
	dests := make(map[string]bool)
	for _, f := range files {
		dests[f.dest] = true
	}

	coreFiles := []string{
		"cmd/site/main.go",
		"cmd/site/Dockerfile",
		"internal/db/db.go",
		"internal/middleware/logging.go",
		"internal/repo/repo.go",
		"internal/repo/postgres/store.go",
		"internal/web/server.go",
		"internal/web/handler/home/handler.go",
		"internal/web/components/layout.templ",
		".gitignore",
		"Makefile",
		"scripts/db-shell.sh",
		"go.mod",
		"README.md",
	}

	for _, f := range coreFiles {
		assert.True(t, dests[f], "expected core file: %s", f)
	}
}

func TestBuildProjectFileList_plainCSS(t *testing.T) {
	cfg := &ProjectConfig{Name: "proj", Module: "github.com/test/proj", CSS: "plain"}
	files := buildProjectFileList(cfg)

	dests := make(map[string]bool)
	for _, f := range files {
		dests[f.dest] = true
	}

	assert.True(t, dests["static/css/base/variables.css"])
	assert.True(t, dests["static/css/components/buttons.css"])
	assert.False(t, dests["tailwind.config.js"])
	assert.False(t, dests["package.json"])
}

func TestBuildProjectFileList_tailwind(t *testing.T) {
	cfg := &ProjectConfig{Name: "proj", Module: "github.com/test/proj", CSS: "tailwind"}
	files := buildProjectFileList(cfg)

	dests := make(map[string]bool)
	for _, f := range files {
		dests[f.dest] = true
	}

	assert.True(t, dests["tailwind.config.js"])
	assert.True(t, dests["package.json"])
	assert.False(t, dests["static/css/base/variables.css"])
}

func TestBuildProjectFileList_auth(t *testing.T) {
	cfg := &ProjectConfig{
		Name:        "proj",
		Module:      "github.com/test/proj",
		CSS:         "plain",
		IncludeAuth: true,
	}
	files := buildProjectFileList(cfg)

	dests := make(map[string]bool)
	for _, f := range files {
		dests[f.dest] = true
	}

	assert.True(t, dests["internal/repo/user.go"])
	assert.True(t, dests["internal/repo/postgres/users.go"])
	assert.True(t, dests["internal/service/auth.go"])
	assert.True(t, dests["internal/auth/cookies.go"])
	assert.True(t, dests["internal/web/handler/auth/login/handler.go"])
	assert.True(t, dests["internal/web/handler/auth/login/login.templ"])
	assert.True(t, dests["internal/web/handler/auth/register/handler.go"])
	assert.True(t, dests["internal/web/handler/auth/register/register.templ"])
}

func TestBuildProjectFileList_noAuth(t *testing.T) {
	cfg := &ProjectConfig{Name: "proj", Module: "github.com/test/proj", CSS: "plain"}
	files := buildProjectFileList(cfg)

	dests := make(map[string]bool)
	for _, f := range files {
		dests[f.dest] = true
	}

	assert.False(t, dests["internal/repo/user.go"])
	assert.False(t, dests["internal/service/auth.go"])
}

func TestBuildProjectFileList_storageLocal(t *testing.T) {
	cfg := &ProjectConfig{
		Name:           "proj",
		Module:         "github.com/test/proj",
		CSS:            "plain",
		IncludeStorage: true,
		StorageBackend: "local",
	}
	files := buildProjectFileList(cfg)

	dests := make(map[string]bool)
	for _, f := range files {
		dests[f.dest] = true
	}

	// Core files should be present, no syncstatic (sync is now hamr CLI subcommand).
	assert.True(t, dests["cmd/site/main.go"])
}

func TestBuildProjectFileList_storageS3(t *testing.T) {
	cfg := &ProjectConfig{
		Name:           "proj",
		Module:         "github.com/test/proj",
		CSS:            "plain",
		IncludeStorage: true,
		StorageBackend: "s3",
	}
	files := buildProjectFileList(cfg)

	dests := make(map[string]bool)
	for _, f := range files {
		dests[f.dest] = true
	}

	// S3 storage should not generate syncstatic (sync is now hamr CLI subcommand).
	assert.True(t, dests["cmd/site/main.go"])
}

func TestBuildProjectFileList_e2e(t *testing.T) {
	cfg := &ProjectConfig{
		Name:       "proj",
		Module:     "github.com/test/proj",
		CSS:        "plain",
		IncludeE2E: true,
	}
	files := buildProjectFileList(cfg)

	dests := make(map[string]bool)
	for _, f := range files {
		dests[f.dest] = true
	}

	assert.True(t, dests["e2e/main_test.go"])
	assert.True(t, dests["e2e/helpers.go"])
	assert.True(t, dests["e2e/home_test.go"])
	assert.True(t, dests["e2e/testdata/seed_e2e.sql"])
	assert.True(t, dests["e2e/README.md"])
}

func TestBuildProjectFileList_noE2E(t *testing.T) {
	cfg := &ProjectConfig{Name: "proj", Module: "github.com/test/proj", CSS: "plain"}
	files := buildProjectFileList(cfg)

	dests := make(map[string]bool)
	for _, f := range files {
		dests[f.dest] = true
	}

	assert.False(t, dests["e2e/main_test.go"])
}

func TestGenerateProject_createsFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "testproj")

	cfg := &ProjectConfig{
		Name:      "testproj",
		Module:    "github.com/test/testproj",
		CSS:       "plain",
		Database:  "postgres",
		GoVersion: "1.25.0",
	}

	require.NoError(t, GenerateProject(dir, cfg))

	// Spot-check key files exist and contain expected content.
	assertFileExists(t, dir, "cmd/site/main.go")
	assertFileExists(t, dir, "internal/middleware/logging.go")
	assertFileExists(t, dir, "internal/web/server.go")
	assertFileExists(t, dir, "go.mod")
	assertFileExists(t, dir, ".gitignore")
	assertFileExists(t, dir, "scripts/db-shell.sh")

	// Logging middleware contains expected function.
	loggingGo := readFile(t, dir, "internal/middleware/logging.go")
	assert.Contains(t, loggingGo, "func Logging()")

	// Framework reference docs (raw-copied, not templated).
	assertFileExists(t, dir, "docs/llms.txt")
	assertFileExists(t, dir, "docs/llms-full.txt")

	// Check module substitution.
	gomod := readFile(t, dir, "go.mod")
	assert.Contains(t, gomod, "module github.com/test/testproj")

	// Check name substitution.
	readme := readFile(t, dir, "README.md")
	assert.Contains(t, readme, "testproj")
	assert.Contains(t, readme, "make migrate")

	claude := readFile(t, dir, "CLAUDE.md")
	assert.Contains(t, claude, "make migrate")
	assert.NotContains(t, claude, "Migrations run automatically")

	agents := readFile(t, dir, "AGENTS.md")
	assert.Contains(t, agents, "make migrate")
	assert.Contains(t, agents, "Configure rules via `[lint.templ]` in `hamr.toml`")
	assert.NotContains(t, agents, ".templint.yml")

	// Check main.go has correct imports and env config pattern.
	mainGo := readFile(t, dir, "cmd/site/main.go")
	assert.Contains(t, mainGo, "github.com/FyrmForge/hamr/pkg/config")
	assert.Contains(t, mainGo, "github.com/FyrmForge/hamr/pkg/server")
	assert.Contains(t, mainGo, "envPort")
	assert.Contains(t, mainGo, "envDatabaseURL")
	assert.NotContains(t, mainGo, "db.Migrate(")
}

func TestGenerateProject_withAuth(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "authproj")

	cfg := &ProjectConfig{
		Name:            "authproj",
		Module:          "github.com/test/authproj",
		CSS:             "plain",
		Database:        "postgres",
		GoVersion:       "1.25.0",
		IncludeSessions: true,
		IncludeAuth:     true,
		AuthWithTables:  true,
	}

	require.NoError(t, GenerateProject(dir, cfg))

	assertFileExists(t, dir, "internal/repo/user.go")
	assertFileExists(t, dir, "internal/repo/postgres/users.go")
	assertFileExists(t, dir, "internal/service/auth.go")
	assertFileExists(t, dir, "internal/auth/cookies.go")
	assertFileExists(t, dir, "internal/web/handler/auth/login/handler.go")
	assertFileExists(t, dir, "internal/web/handler/auth/login/login.templ")
	assertFileExists(t, dir, "internal/web/handler/auth/register/handler.go")
	assertFileExists(t, dir, "internal/web/handler/auth/register/register.templ")

	// Check migrations include users table and are wrapped in a transaction.
	upSQL := readFile(t, dir, "internal/db/migrations/001_initial.up.sql")
	assert.Contains(t, upSQL, "BEGIN;")
	assert.Contains(t, upSQL, "COMMIT;")
	assert.Contains(t, upSQL, "CREATE TABLE sessions")
	assert.Contains(t, upSQL, "CREATE TABLE users")

	// Check main.go includes auth imports.
	mainGo := readFile(t, dir, "cmd/site/main.go")
	assert.Contains(t, mainGo, "github.com/FyrmForge/hamr/pkg/auth")
	assert.Contains(t, mainGo, "github.com/test/authproj/internal/service")

	// Check server.go includes auth routes.
	serverGo := readFile(t, dir, "internal/web/server.go")
	assert.Contains(t, serverGo, "/internal/web/handler/auth/login")
	assert.Contains(t, serverGo, "/internal/web/handler/auth/register")
	assert.Contains(t, serverGo, "loginHandler")
	assert.Contains(t, serverGo, "registerHandler")
	assert.Contains(t, serverGo, "auth.RequireNotAuth()")

	agents := readFile(t, dir, "AGENTS.md")
	assert.Contains(t, agents, "h.sessionManager.CreateSession")
	assert.Contains(t, agents, `return respond.Redirect(c, "/")`)
	assert.NotContains(t, agents, "h.sessions.CreateSession")
	assert.NotContains(t, agents, `return respond.Redirect(c, "/dashboard")`)
}

func TestGenerateProject_noSessions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nosess")

	cfg := &ProjectConfig{
		Name:      "nosess",
		Module:    "github.com/test/nosess",
		CSS:       "plain",
		Database:  "postgres",
		GoVersion: "1.25.0",
	}

	require.NoError(t, GenerateProject(dir, cfg))

	// main.go should not have auth/session imports.
	mainGo := readFile(t, dir, "cmd/site/main.go")
	assert.NotContains(t, mainGo, "pkg/auth")
	assert.NotContains(t, mainGo, "sessionManager")

	// server.go should not have session middleware.
	serverGo := readFile(t, dir, "internal/web/server.go")
	assert.NotContains(t, serverGo, "Flash()")
	assert.NotContains(t, serverGo, "CSRF()")

	// Store should not embed SessionStore.
	repoGo := readFile(t, dir, "internal/repo/repo.go")
	assert.NotContains(t, repoGo, "SessionStore")
}

func TestGenerateProject_directoryAlreadyExists(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "existing")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	cfg := &ProjectConfig{
		Name:      "existing",
		Module:    "github.com/test/existing",
		CSS:       "plain",
		Database:  "postgres",
		GoVersion: "1.25.0",
	}

	err := GenerateProject(dir, cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestGenerateProject_tailwindCSS(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "twproj")

	cfg := &ProjectConfig{
		Name:      "twproj",
		Module:    "github.com/test/twproj",
		CSS:       "tailwind",
		Database:  "postgres",
		GoVersion: "1.25.0",
	}

	require.NoError(t, GenerateProject(dir, cfg))

	assertFileExists(t, dir, "tailwind.config.js")
	assertFileExists(t, dir, "package.json")

	// Plain CSS files should NOT exist.
	assertFileNotExists(t, dir, "static/css/base/variables.css")
}

func TestGenerateProject_e2eFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "e2eproj")

	cfg := &ProjectConfig{
		Name:       "e2eproj",
		Module:     "github.com/test/e2eproj",
		CSS:        "plain",
		Database:   "postgres",
		GoVersion:  "1.25.0",
		IncludeE2E: true,
	}

	require.NoError(t, GenerateProject(dir, cfg))

	assertFileExists(t, dir, "e2e/main_test.go")
	assertFileExists(t, dir, "e2e/helpers.go")
	assertFileExists(t, dir, "e2e/home_test.go")

	// Makefile should have e2e targets.
	makefile := readFile(t, dir, "Makefile")
	assert.Contains(t, makefile, "e2e:")
	assert.Contains(t, makefile, "e2e-local:")
}

func TestGenerateProject_dockerCompose(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dcproj")

	cfg := &ProjectConfig{
		Name:      "dcproj",
		Module:    "github.com/test/dcproj",
		CSS:       "plain",
		Database:  "postgres",
		GoVersion: "1.25.0",
	}

	require.NoError(t, GenerateProject(dir, cfg))

	compose := readFile(t, dir, "docker/docker-compose.yaml")
	assert.Contains(t, compose, "postgres:")
	assert.Contains(t, compose, "POSTGRES_DB: dcproj")
	assert.Contains(t, compose, "pg_data:")
}

func TestGenerateProject_configHasCorrectDBURL(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cfgproj")

	cfg := &ProjectConfig{
		Name:      "cfgproj",
		Module:    "github.com/test/cfgproj",
		CSS:       "plain",
		Database:  "postgres",
		GoVersion: "1.25.0",
	}

	require.NoError(t, GenerateProject(dir, cfg))

	mainGo := readFile(t, dir, "cmd/site/main.go")
	assert.Contains(t, mainGo, "cfgproj?sslmode=disable")
}

func TestGenerateProject_hamrConfigRebuildsGoOnTemplChanges(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "devcfgproj")

	cfg := &ProjectConfig{
		Name:      "devcfgproj",
		Module:    "github.com/test/devcfgproj",
		CSS:       "plain",
		Database:  "postgres",
		GoVersion: "1.25.0",
	}

	require.NoError(t, GenerateProject(dir, cfg))

	hamrToml := readFile(t, dir, "hamr.toml")
	assert.Contains(t, hamrToml, `watch = ["**/*.go", "**/*.templ", ".env"]`)
	assert.Contains(t, hamrToml, `depends = ["templ"]`)
}

func TestGenerateProject_inPlace(t *testing.T) {
	dir := t.TempDir() // already exists

	cfg := &ProjectConfig{
		Name:      "inplaceproj",
		Module:    "github.com/test/inplaceproj",
		CSS:       "plain",
		Database:  "postgres",
		GoVersion: "1.25.0",
		InPlace:   true,
	}

	require.NoError(t, GenerateProject(dir, cfg))

	assertFileExists(t, dir, "cmd/site/main.go")
	assertFileExists(t, dir, "go.mod")
	assertFileExists(t, dir, "internal/web/server.go")
}

func TestGenerateProject_inPlace_skipsExistingGoMod(t *testing.T) {
	dir := t.TempDir()

	// Write a pre-existing go.mod.
	existingGoMod := "module github.com/existing/module\n\ngo 1.25.0\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte(existingGoMod), 0o644))

	cfg := &ProjectConfig{
		Name:      "existing",
		Module:    "github.com/existing/module",
		CSS:       "plain",
		Database:  "postgres",
		GoVersion: "1.25.0",
		InPlace:   true,
	}

	require.NoError(t, GenerateProject(dir, cfg))

	// go.mod should be the original, not overwritten.
	gomod := readFile(t, dir, "go.mod")
	assert.Equal(t, strings.TrimSpace(existingGoMod), gomod)

	// Other files should still be created.
	assertFileExists(t, dir, "cmd/site/main.go")
	assertFileExists(t, dir, "internal/web/server.go")
}

func TestReadExistingGoMod(t *testing.T) {
	dir := t.TempDir()

	// No go.mod → empty strings, no error.
	mod, ver, err := ReadExistingGoMod(dir)
	require.NoError(t, err)
	assert.Equal(t, "", mod)
	assert.Equal(t, "", ver)

	// Write a go.mod.
	gomod := "module github.com/test/proj\n\ngo 1.25.0\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644))

	mod, ver, err = ReadExistingGoMod(dir)
	require.NoError(t, err)
	assert.Equal(t, "github.com/test/proj", mod)
	assert.Equal(t, "1.25.0", ver)
}

func TestProjectConfig_Validate_storageBackendSetsIncludeStorage(t *testing.T) {
	cfg := ProjectConfig{Name: "proj", Module: "github.com/test/proj", StorageBackend: "s3"}
	require.NoError(t, cfg.Validate())
	assert.True(t, cfg.IncludeStorage, "StorageBackend should imply IncludeStorage")
}

func TestProjectConfig_Validate_includeStorageDefaultsToLocal(t *testing.T) {
	cfg := ProjectConfig{Name: "proj", Module: "github.com/test/proj", IncludeStorage: true}
	require.NoError(t, cfg.Validate())
	assert.True(t, cfg.IncludeStorage)
	assert.Equal(t, "local", cfg.StorageBackend)
}

func TestGenerateProject_s3Storage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "s3proj")

	cfg := &ProjectConfig{
		Name:           "s3proj",
		Module:         "github.com/test/s3proj",
		CSS:            "plain",
		Database:       "postgres",
		GoVersion:      "1.25.0",
		IncludeStorage: true,
		StorageBackend: "s3",
	}

	require.NoError(t, GenerateProject(dir, cfg))

	// docker-compose should have RustFS.
	compose := readFile(t, dir, "docker/docker-compose.yaml")
	assert.Contains(t, compose, "rustfs:")
	assert.Contains(t, compose, "rustfs_data:")

	// .env should have S3 vars.
	envFile := readFile(t, dir, ".env.example")
	assert.Contains(t, envFile, "S3_ENDPOINT")
	assert.Contains(t, envFile, "S3_BUCKET")
	assert.NotContains(t, envFile, "STORAGE_PATH")

	// main.go should have S3 env vars and use S3 storage init.
	mainGo := readFile(t, dir, "cmd/site/main.go")
	assert.Contains(t, mainGo, "envS3Endpoint")
	assert.Contains(t, mainGo, "envS3Bucket")
	assert.Contains(t, mainGo, "envStaticBaseURL")
	assert.Contains(t, mainGo, "NewS3Storage")
	assert.NotContains(t, mainGo, "NewLocalStorage")

	// server.go should have FileStorage in Deps.
	serverGo := readFile(t, dir, "internal/web/server.go")
	assert.Contains(t, serverGo, "FileStorage")

	// layout.templ should use StaticURL.
	layout := readFile(t, dir, "internal/web/components/layout.templ")
	assert.Contains(t, layout, "StaticURL(")
	assert.NotContains(t, layout, `href="/static/`)

	// go.mod should exist with correct module.
	gomod := readFile(t, dir, "go.mod")
	assert.Contains(t, gomod, "module github.com/test/s3proj")

	agents := readFile(t, dir, "AGENTS.md")
	assert.Contains(t, agents, "`S3_ACCESS_KEY`")
	assert.Contains(t, agents, "`S3_SECRET_KEY`")
	assert.NotContains(t, agents, "S3_ACCESS_KEY_ID")
	assert.NotContains(t, agents, "S3_SECRET_ACCESS_KEY")
}

func TestGenerateProject_localStorage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "localproj")

	cfg := &ProjectConfig{
		Name:           "localproj",
		Module:         "github.com/test/localproj",
		CSS:            "plain",
		Database:       "postgres",
		GoVersion:      "1.25.0",
		IncludeStorage: true,
		StorageBackend: "local",
	}

	require.NoError(t, GenerateProject(dir, cfg))

	// docker-compose should NOT have RustFS.
	compose := readFile(t, dir, "docker/docker-compose.yaml")
	assert.NotContains(t, compose, "rustfs:")

	// .env should have STORAGE_PATH.
	envFile := readFile(t, dir, ".env.example")
	assert.Contains(t, envFile, "STORAGE_PATH")
	assert.NotContains(t, envFile, "S3_ENDPOINT")

	// main.go should have local storage env vars.
	mainGo := readFile(t, dir, "cmd/site/main.go")
	assert.Contains(t, mainGo, "envStoragePath")
	assert.NotContains(t, mainGo, "envS3Endpoint")
	assert.Contains(t, mainGo, "NewLocalStorage")
	assert.NotContains(t, mainGo, "NewS3Storage")

	// Makefile should NOT have sync-static target for local storage.
	localMakefile := readFile(t, dir, "Makefile")
	assert.NotContains(t, localMakefile, "sync-static:")
}

func TestGenerateProject_dbShellUsesProjectRootEnv(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dbshellproj")

	cfg := &ProjectConfig{
		Name:      "dbshellproj",
		Module:    "github.com/test/dbshellproj",
		CSS:       "plain",
		Database:  "postgres",
		GoVersion: "1.25.0",
	}

	require.NoError(t, GenerateProject(dir, cfg))

	script := readFile(t, dir, "scripts/db-shell.sh")
	assert.Contains(t, script, `PROJECT_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd)"`)
	assert.Contains(t, script, `source "$PROJECT_ROOT/.env"`)
	assert.NotContains(t, script, `source "$SCRIPT_DIR/.env"`)
}

func TestGenerateProject_ciWorkflow(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ciproj")

	cfg := &ProjectConfig{
		Name:      "ciproj",
		Module:    "github.com/test/ciproj",
		CSS:       "plain",
		Database:  "postgres",
		GoVersion: "1.25.0",
	}

	require.NoError(t, GenerateProject(dir, cfg))

	assertFileExists(t, dir, ".github/workflows/ci.yml")
	assertFileExists(t, dir, ".github/workflows/deploy.yml")

	ci := readFile(t, dir, ".github/workflows/ci.yml")
	assert.Contains(t, ci, `go-version: "1.25.0"`)
	assert.Contains(t, ci, "make build")
	assert.Contains(t, ci, "make test")
	assert.Contains(t, ci, "make templint")
	assert.Contains(t, ci, "templ files are out of date")
	assert.Contains(t, ci, "golangci-lint-action")
	assert.Contains(t, ci, "# - name: Run migrations")
	assert.Contains(t, ci, "#   run: make migrate")
	assert.NotContains(t, ci, "setup-node")

	makefile := readFile(t, dir, "Makefile")
	assert.Contains(t, makefile, "## migrate: Run all pending migrations")
	assert.Contains(t, makefile, "migrate:\n\t$(ENV_LOAD) go run ./cmd/migrate up")
	assert.Contains(t, makefile, "ENV_LOAD := eval", "scaffold must define ENV_LOAD so migrate / db-sh / generate pick up hamr-dev port walks")

	deploy := readFile(t, dir, ".github/workflows/deploy.yml")
	// Every non-empty line should be a comment.
	for _, line := range strings.Split(deploy, "\n") {
		if strings.TrimSpace(line) != "" {
			assert.True(t, strings.HasPrefix(line, "#"), "deploy.yml has uncommented line: %q", line)
		}
	}
	assert.Contains(t, deploy, "secrets.RAILWAY_TOKEN")
	assert.Contains(t, deploy, "secrets.RAILWAY_SERVICE")
}

func TestGenerateProject_ciWorkflowTailwind(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "twciproj")

	cfg := &ProjectConfig{
		Name:      "twciproj",
		Module:    "github.com/test/twciproj",
		CSS:       "tailwind",
		Database:  "postgres",
		GoVersion: "1.24.0",
	}

	require.NoError(t, GenerateProject(dir, cfg))

	ci := readFile(t, dir, ".github/workflows/ci.yml")
	assert.Contains(t, ci, `go-version: "1.24.0"`)
	assert.Contains(t, ci, "setup-node")
	assert.Contains(t, ci, "make build")
	assert.Contains(t, ci, "make test")
	assert.Contains(t, ci, "make templint")
}

func TestGenerateProject_staticS3(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "s3staticproj")

	cfg := &ProjectConfig{
		Name:           "s3staticproj",
		Module:         "github.com/test/s3staticproj",
		CSS:            "plain",
		Database:       "postgres",
		GoVersion:      "1.25.0",
		IncludeStorage: true,
		StorageBackend: "s3",
		StaticS3:       true,
	}

	require.NoError(t, GenerateProject(dir, cfg))

	// hamr.toml should have sync-static daemon.
	hamrToml := readFile(t, dir, "hamr.toml")
	assert.Contains(t, hamrToml, `name = "sync-static"`)
	assert.Contains(t, hamrToml, "hamr sync --watch --bucket s3staticproj-static")

	// Makefile should have sync-static target.
	makefile := readFile(t, dir, "Makefile")
	assert.Contains(t, makefile, "sync-static:")
	assert.Contains(t, makefile, "hamr sync --bucket $(S3_STATIC_BUCKET)")

	// .env.example should have S3_STATIC_BUCKET.
	envFile := readFile(t, dir, ".env.example")
	assert.Contains(t, envFile, "S3_STATIC_BUCKET=s3staticproj-static")
}

func TestGenerateProject_s3WithoutStaticS3(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "s3nostaticproj")

	cfg := &ProjectConfig{
		Name:           "s3nostaticproj",
		Module:         "github.com/test/s3nostaticproj",
		CSS:            "plain",
		Database:       "postgres",
		GoVersion:      "1.25.0",
		IncludeStorage: true,
		StorageBackend: "s3",
		StaticS3:       false,
	}

	require.NoError(t, GenerateProject(dir, cfg))

	// hamr.toml should NOT have sync-static daemon.
	hamrToml := readFile(t, dir, "hamr.toml")
	assert.NotContains(t, hamrToml, "sync-static")

	// Makefile should NOT have sync-static target.
	makefile := readFile(t, dir, "Makefile")
	assert.NotContains(t, makefile, "sync-static:")

	// .env.example should NOT have S3_STATIC_BUCKET.
	envFile := readFile(t, dir, ".env.example")
	assert.NotContains(t, envFile, "S3_STATIC_BUCKET")
}

func TestGenerateProject_dottedProjectName(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "topleveluk.com")

	cfg := &ProjectConfig{
		Name:           "topleveluk.com",
		Module:         "github.com/test/topleveluk.com",
		CSS:            "plain",
		Database:       "postgres",
		GoVersion:      "1.25.0",
		IncludeStorage: true,
		StorageBackend: "s3",
		StaticS3:       true,
	}

	require.NoError(t, GenerateProject(dir, cfg))

	compose := readFile(t, dir, "docker/docker-compose.yaml")
	assert.Contains(t, compose, "name: topleveluk-com-deps")
	assert.Contains(t, compose, "POSTGRES_DB: topleveluk-com")
	assert.NotContains(t, compose, "name: topleveluk.com-deps")

	envFile := readFile(t, dir, ".env.example")
	assert.Contains(t, envFile, "DATABASE_URL=postgres://postgres:postgres@localhost:5432/topleveluk-com?sslmode=disable")
	assert.Contains(t, envFile, "S3_BUCKET=topleveluk-com-uploads")
	assert.Contains(t, envFile, "S3_STATIC_BUCKET=topleveluk-com-static")

	mainGo := readFile(t, dir, "cmd/site/main.go")
	assert.Contains(t, mainGo, `config.GetEnvOrDefault("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/topleveluk-com?sslmode=disable")`)
	assert.Contains(t, mainGo, `config.GetEnvOrDefault("S3_BUCKET", "topleveluk-com-uploads")`)

	hamrToml := readFile(t, dir, "hamr.toml")
	assert.Contains(t, hamrToml, "hamr sync --watch --bucket topleveluk-com-static")

	readme := readFile(t, dir, "README.md")
	assert.Contains(t, readme, "# topleveluk.com")

	gomod := readFile(t, dir, "go.mod")
	assert.Contains(t, gomod, "module github.com/test/topleveluk.com")
}

func TestBuildProjectFileList_stripe(t *testing.T) {
	cfg := &ProjectConfig{
		Name:          "proj",
		Module:        "github.com/test/proj",
		CSS:           "plain",
		IncludeStripe: true,
	}
	files := buildProjectFileList(cfg)

	dests := make(map[string]bool)
	for _, f := range files {
		dests[f.dest] = true
	}

	assert.True(t, dests["internal/api/handler/stripe/handler.go"])
}

func TestBuildProjectFileList_noStripe(t *testing.T) {
	cfg := &ProjectConfig{Name: "proj", Module: "github.com/test/proj", CSS: "plain"}
	files := buildProjectFileList(cfg)

	dests := make(map[string]bool)
	for _, f := range files {
		dests[f.dest] = true
	}

	assert.False(t, dests["internal/api/handler/stripe/handler.go"])
}

func TestGenerateProject_stripe(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "stripeproj")

	cfg := &ProjectConfig{
		Name:          "stripeproj",
		Module:        "github.com/test/stripeproj",
		CSS:           "plain",
		Database:      "postgres",
		GoVersion:     "1.25.0",
		IncludeStripe: true,
	}

	require.NoError(t, GenerateProject(dir, cfg))

	// Handler file exists.
	assertFileExists(t, dir, "internal/api/handler/stripe/handler.go")

	// Handler file has expected content.
	handlerGo := readFile(t, dir, "internal/api/handler/stripe/handler.go")
	assert.Contains(t, handlerGo, "HandleWebhook")
	assert.Contains(t, handlerGo, "webhook.ConstructEvent")

	// Handler stubs every event the hamr Stripe mock fires so dev-time
	// "unhandled event" surprises don't slip past the developer. If we add
	// or rename an event in internal/devserver/stripemock_*.go, this test
	// fires and forces the scaffold template to keep up.
	for _, evt := range []string{
		"checkout.session.completed",
		"checkout.session.expired",
		"checkout.session.async_payment_failed",
		"account.updated",
		"payment_intent.succeeded",
		"payment_intent.payment_failed",
		"charge.succeeded",
		"charge.refunded",
		"transfer.created",
		"payout.paid",
		"payout.failed",
	} {
		assert.Contains(t, handlerGo, evt, "scaffolded webhook handler must stub %q (mock fires it)", evt)
	}

	// API server.go imports stripe handler.
	serverGo := readFile(t, dir, "internal/api/server.go")
	assert.Contains(t, serverGo, "stripehandler")
	assert.Contains(t, serverGo, "WebhookSecret")
	assert.Contains(t, serverGo, "/webhooks/stripe")

	// main.go uses real stripe-go via SetBackend (no pkg/stripemock anymore).
	mainGo := readFile(t, dir, "cmd/site/main.go")
	assert.Contains(t, mainGo, "envStripeWebhookSecret")
	assert.Contains(t, mainGo, "envStripeMock")
	assert.Contains(t, mainGo, "envStripeKey")
	assert.Contains(t, mainGo, "envHamrStripeMockURL", "main.go must read HAMR_STRIPE_MOCK_URL (hamr-injected, no hardcoded port)")
	assert.Contains(t, mainGo, "STRIPE_WEBHOOK_SECRET")
	assert.Contains(t, mainGo, "STRIPE_MOCK")
	assert.Contains(t, mainGo, "HAMR_STRIPE_MOCK_URL")
	assert.Contains(t, mainGo, "stripe.SetBackend")
	// Production safety guard: STRIPE_MOCK=true with DEV_MODE=false must
	// refuse to start so a leftover dev flag in prod doesn't silently
	// route real payment calls to a non-existent localhost mock.
	assert.Contains(t, mainGo, "STRIPE_MOCK=true requires DEV_MODE=true",
		"main.go must guard against STRIPE_MOCK in prod")
	assert.NotContains(t, mainGo, "pkg/stripemock", "scaffold must no longer import the removed pkg/stripemock")
	assert.NotContains(t, mainGo, "STRIPE_API_URL", "STRIPE_API_URL was retired in favor of STRIPE_MOCK + hamr-injected URL")
	assert.NotContains(t, mainGo, "STRIPE_CURRENCY", "STRIPE_CURRENCY was retired (currency is now per-line-item via stripe-go)")
	assert.NotContains(t, mainGo, `const hamrStripeMockURL`,
		"hardcoded mock URL constant retired — HAMR_STRIPE_MOCK_URL is hamr-injected so it tracks proxy port changes")

	// .env has the stripe trio.
	envFile := readFile(t, dir, ".env.example")
	assert.Contains(t, envFile, "STRIPE_KEY=")
	assert.Contains(t, envFile, "STRIPE_MOCK=true")
	assert.Contains(t, envFile, "STRIPE_WEBHOOK_SECRET=whsec_dev_")
	assert.NotContains(t, envFile, "STRIPE_API_URL=")
	assert.NotContains(t, envFile, "STRIPE_CURRENCY=")

	// hamr.toml has [dev.stripe] block with the same generated webhook_secret
	// as .env (the scaffold writes one value to both).
	hamrToml := readFile(t, dir, "hamr.toml")
	assert.Contains(t, hamrToml, "[dev.stripe]")
	assert.Contains(t, hamrToml, "enabled = true")
	assert.Contains(t, hamrToml, "webhook_url = \"http://localhost:8080/api/webhooks/stripe\"")
	// Extract the secret from .env and assert hamr.toml contains the same value.
	const envPrefix = "STRIPE_WEBHOOK_SECRET="
	idx := strings.Index(envFile, envPrefix)
	require.GreaterOrEqual(t, idx, 0)
	envSecret := envFile[idx+len(envPrefix):]
	if nl := strings.IndexAny(envSecret, "\n\r"); nl >= 0 {
		envSecret = envSecret[:nl]
	}
	require.True(t, strings.HasPrefix(envSecret, "whsec_dev_"), "got %q", envSecret)
	assert.Contains(t, hamrToml, "webhook_secret = \""+envSecret+"\"",
		"hamr.toml webhook_secret must match the value generated into .env")

	// web/server.go.tmpl no longer references the StripeMock dep.
	webServerGo := readFile(t, dir, "internal/web/server.go")
	assert.NotContains(t, webServerGo, "StripeMock")
	assert.NotContains(t, webServerGo, "pkg/stripemock")
}

func TestBuildProjectFileList_emailMock(t *testing.T) {
	cfg := &ProjectConfig{
		Name:             "proj",
		Module:           "github.com/test/proj",
		CSS:              "plain",
		IncludeEmailMock: true,
	}
	files := buildProjectFileList(cfg)

	dests := make(map[string]bool)
	for _, f := range files {
		dests[f.dest] = true
	}
	assert.True(t, dests["internal/web/handler/devemail/handler.go"])
}

func TestGenerateProject_emailMock(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "emailproj")

	cfg := &ProjectConfig{
		Name:             "emailproj",
		Module:           "github.com/test/emailproj",
		CSS:              "plain",
		Database:         "postgres",
		GoVersion:        "1.25.0",
		IncludeEmailMock: true,
	}

	require.NoError(t, GenerateProject(dir, cfg))

	assertFileExists(t, dir, "internal/web/handler/devemail/handler.go")

	// main.go wires the emailmock client behind EMAIL_MOCK.
	mainGo := readFile(t, dir, "cmd/site/main.go")
	assert.Contains(t, mainGo, `"github.com/FyrmForge/hamr/pkg/email"`)
	assert.Contains(t, mainGo, `"github.com/FyrmForge/hamr/pkg/emailmock"`)
	assert.Contains(t, mainGo, "envEmailMock")
	assert.Contains(t, mainGo, "emailmock.New(envHamrDevURL)")
	assert.Contains(t, mainGo, "EmailSender: emailSender")

	// web/server.go has EmailSender field + route registration.
	serverGo := readFile(t, dir, "internal/web/server.go")
	assert.Contains(t, serverGo, "EmailSender email.Sender")
	assert.Contains(t, serverGo, "/dev/send-test-email")
	assert.Contains(t, serverGo, "devemail.NewHandler")

	// .env.example has the mock env vars. HAMR_DEV_URL was retired from
	// the example file — `hamr dev` now injects it derived from
	// [proxy].listen so .env doesn't need to track the proxy port.
	envFile := readFile(t, dir, ".env.example")
	assert.Contains(t, envFile, "EMAIL_MOCK=true")
	assert.NotContains(t, envFile, "HAMR_DEV_URL=",
		"HAMR_DEV_URL was retired from .env.example — hamr dev injects it now")

	// hamr.toml has [dev.email] enabled.
	hamrToml := readFile(t, dir, "hamr.toml")
	assert.Contains(t, hamrToml, "[dev.email]")
	assert.Contains(t, hamrToml, "enabled = true")
}

func TestGenerateProject_noEmailMock(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "noemailproj")

	cfg := &ProjectConfig{
		Name:      "noemailproj",
		Module:    "github.com/test/noemailproj",
		CSS:       "plain",
		Database:  "postgres",
		GoVersion: "1.25.0",
	}

	require.NoError(t, GenerateProject(dir, cfg))

	assertFileNotExists(t, dir, "internal/web/handler/devemail/handler.go")

	mainGo := readFile(t, dir, "cmd/site/main.go")
	assert.NotContains(t, mainGo, "emailmock.New")
	assert.NotContains(t, mainGo, "envEmailMock")

	envFile := readFile(t, dir, ".env.example")
	assert.NotContains(t, envFile, "EMAIL_MOCK=")

	hamrToml := readFile(t, dir, "hamr.toml")
	// No [dev.email] block at all when the project wasn't scaffolded with the
	// mock — users opt in by following the schema reference linked at the top.
	assert.NotContains(t, hamrToml, "[dev.email]")
}

func TestGenerateProject_noStripe(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nostripeproj")

	cfg := &ProjectConfig{
		Name:      "nostripeproj",
		Module:    "github.com/test/nostripeproj",
		CSS:       "plain",
		Database:  "postgres",
		GoVersion: "1.25.0",
	}

	require.NoError(t, GenerateProject(dir, cfg))

	assertFileNotExists(t, dir, "internal/api/handler/stripe/handler.go")

	serverGo := readFile(t, dir, "internal/api/server.go")
	assert.NotContains(t, serverGo, "stripehandler")
	assert.NotContains(t, serverGo, "WebhookSecret")

	mainGo := readFile(t, dir, "cmd/site/main.go")
	assert.NotContains(t, mainGo, "envStripeWebhookSecret")

	envFile := readFile(t, dir, ".env.example")
	assert.NotContains(t, envFile, "STRIPE_WEBHOOK_SECRET")
}

func TestGenerateProject_alpine(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "alpineproj")

	cfg := &ProjectConfig{
		Name:          "alpineproj",
		Module:        "github.com/test/alpineproj",
		CSS:           "plain",
		Database:      "postgres",
		GoVersion:     "1.25.0",
		IncludeAlpine: true,
	}

	require.NoError(t, GenerateProject(dir, cfg))

	layout := readFile(t, dir, "internal/web/components/layout.templ")
	assert.Contains(t, layout, "js/alpine.min.js")

	hamrToml := readFile(t, dir, "hamr.toml")
	assert.Contains(t, hamrToml, "alpine = true")

	readme := readFile(t, dir, "README.md")
	assert.Contains(t, readme, "Alpine.js")

	adr := readFile(t, dir, "docs/adr/000-base-framework.md")
	assert.Contains(t, adr, "Alpine.js")
}

func TestGenerateProject_noAlpine(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "noalpineproj")

	cfg := &ProjectConfig{
		Name:      "noalpineproj",
		Module:    "github.com/test/noalpineproj",
		CSS:       "plain",
		Database:  "postgres",
		GoVersion: "1.25.0",
	}

	require.NoError(t, GenerateProject(dir, cfg))

	layout := readFile(t, dir, "internal/web/components/layout.templ")
	assert.NotContains(t, layout, "js/alpine.min.js")

	hamrToml := readFile(t, dir, "hamr.toml")
	assert.Contains(t, hamrToml, "alpine = false")

	readme := readFile(t, dir, "README.md")
	assert.NotContains(t, readme, "Alpine.js")

	adr := readFile(t, dir, "docs/adr/000-base-framework.md")
	assert.NotContains(t, adr, "Alpine.js")
}

func TestBuildProjectFileList_gorm(t *testing.T) {
	cfg := &ProjectConfig{
		Name:        "proj",
		Module:      "github.com/test/proj",
		CSS:         "plain",
		DBConnector: "gorm",
	}
	files := buildProjectFileList(cfg)

	dests := make(map[string]bool)
	for _, f := range files {
		dests[f.dest] = true
	}

	// GORM files present.
	assert.True(t, dests["internal/db/db.go"])
	assert.True(t, dests["internal/db/models.go"])

	// sqlx migration files absent.
	assert.False(t, dests["internal/db/migrations/001_initial.up.sql"])
	assert.False(t, dests["internal/db/migrations/001_initial.down.sql"])

	// cmd/migrate present (not MigrateAtStartup).
	assert.True(t, dests["cmd/migrate/main.go"])
}

func TestBuildProjectFileList_migrateAtStartup(t *testing.T) {
	cfg := &ProjectConfig{
		Name:             "proj",
		Module:           "github.com/test/proj",
		CSS:              "plain",
		MigrateAtStartup: true,
	}
	files := buildProjectFileList(cfg)

	dests := make(map[string]bool)
	for _, f := range files {
		dests[f.dest] = true
	}

	// cmd/migrate should NOT be present.
	assert.False(t, dests["cmd/migrate/main.go"])

	// DB files should still exist (sqlx default).
	assert.True(t, dests["internal/db/db.go"])
}

func TestBuildProjectFileList_gormNoMigrateAtStartup(t *testing.T) {
	cfg := &ProjectConfig{
		Name:             "proj",
		Module:           "github.com/test/proj",
		CSS:              "plain",
		DBConnector:      "gorm",
		MigrateAtStartup: false,
	}
	files := buildProjectFileList(cfg)

	dests := make(map[string]bool)
	for _, f := range files {
		dests[f.dest] = true
	}

	assert.True(t, dests["internal/db/db.go"])
	assert.True(t, dests["internal/db/models.go"])
	assert.True(t, dests["cmd/migrate/main.go"])
	assert.False(t, dests["internal/db/migrations/001_initial.up.sql"])
}

func TestGenerateProject_gorm(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "gormproj")

	cfg := &ProjectConfig{
		Name:            "gormproj",
		Module:          "github.com/test/gormproj",
		CSS:             "plain",
		Database:        "postgres",
		DBConnector:     "gorm",
		GoVersion:       "1.25.0",
		IncludeSessions: true,
		IncludeAuth:     true,
		AuthWithTables:  true,
	}

	require.NoError(t, GenerateProject(dir, cfg))

	// GORM model file exists.
	assertFileExists(t, dir, "internal/db/models.go")
	models := readFile(t, dir, "internal/db/models.go")
	assert.Contains(t, models, "Session")
	assert.Contains(t, models, "User")
	assert.Contains(t, models, "models()")

	// db.go uses GORM.
	dbGo := readFile(t, dir, "internal/db/db.go")
	assert.Contains(t, dbGo, "gorm.io/gorm")
	assert.Contains(t, dbGo, "AutoMigrate")

	// No SQL migration files.
	assertFileNotExists(t, dir, "internal/db/migrations/001_initial.up.sql")
	assertFileNotExists(t, dir, "internal/db/migrations/001_initial.down.sql")

	// Store uses GORM.
	storeGo := readFile(t, dir, "internal/repo/postgres/store.go")
	assert.Contains(t, storeGo, "*gorm.DB")
	assert.NotContains(t, storeGo, "*sqlx.DB")

	// Users use GORM.
	usersGo := readFile(t, dir, "internal/repo/postgres/users.go")
	assert.Contains(t, usersGo, "gorm.ErrRecordNotFound")
	assert.NotContains(t, usersGo, "sql.ErrNoRows")

	// go.mod has GORM deps.
	gomod := readFile(t, dir, "go.mod")
	assert.Contains(t, gomod, "gorm.io/gorm")
	assert.Contains(t, gomod, "gorm.io/driver/postgres")

	// cmd/migrate exists (MigrateAtStartup is false).
	assertFileExists(t, dir, "cmd/migrate/main.go")
	migrateGo := readFile(t, dir, "cmd/migrate/main.go")
	assert.Contains(t, migrateGo, "appdb.AutoMigrate")
	assert.NotContains(t, migrateGo, "db.Migrate(")

	// Server main.go uses GORM connection with retry.
	mainGo := readFile(t, dir, "cmd/site/main.go")
	assert.Contains(t, mainGo, "appdb.ConnectContext")
	assert.NotContains(t, mainGo, "appdb.AutoMigrate") // Not MigrateAtStartup

	// README mentions GORM.
	readme := readFile(t, dir, "README.md")
	assert.Contains(t, readme, "GORM")

	agents := readFile(t, dir, "AGENTS.md")
	assert.Contains(t, agents, "internal/db/            Database connection + GORM models")
	assert.Contains(t, agents, "cmd/migrate/            Standalone migration runner")
	assert.Contains(t, agents, "Models live in `internal/db/models.go`")
	assert.Contains(t, agents, "Repo implementations use `gorm.DB`")
	assert.Contains(t, agents, "Use `cmd/migrate` / `make migrate` to run `appdb.AutoMigrate(...)`")
	assert.NotContains(t, agents, "Migrations in `internal/db/migrations/`")
	assert.NotContains(t, agents, "Use `sqlx` for queries in repo implementations")
}

func TestGenerateProject_sqlxMigrateCommandUsesSQLXDB(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sqlxmigproj")

	cfg := &ProjectConfig{
		Name:        "sqlxmigproj",
		Module:      "github.com/test/sqlxmigproj",
		CSS:         "plain",
		Database:    "postgres",
		DBConnector: "sqlx",
		GoVersion:   "1.25.0",
	}

	require.NoError(t, GenerateProject(dir, cfg))

	migrateGo := readFile(t, dir, "cmd/migrate/main.go")
	assert.Contains(t, migrateGo, `"github.com/jmoiron/sqlx"`)
	assert.Contains(t, migrateGo, "func connect() *sqlx.DB")
	assert.NotContains(t, migrateGo, "func connect() *db.DB")
}

func TestGenerateProject_migrateAtStartup(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "migstartproj")

	cfg := &ProjectConfig{
		Name:             "migstartproj",
		Module:           "github.com/test/migstartproj",
		CSS:              "plain",
		Database:         "postgres",
		DBConnector:      "sqlx",
		MigrateAtStartup: true,
		GoVersion:        "1.25.0",
	}

	require.NoError(t, GenerateProject(dir, cfg))

	// cmd/migrate should NOT exist.
	assertFileNotExists(t, dir, "cmd/migrate/main.go")

	// Server main.go should run migrations at startup.
	mainGo := readFile(t, dir, "cmd/site/main.go")
	assert.Contains(t, mainGo, "db.Migrate(database, appdb.MigrateConfig())")
	assert.Contains(t, mainGo, "migrations completed")

	// Makefile should NOT have migrate targets.
	makefile := readFile(t, dir, "Makefile")
	assert.NotContains(t, makefile, "## migrate:")
	assert.NotContains(t, makefile, "migrate-down")
	assert.NotContains(t, makefile, "migrate-status")

	// README should NOT mention make migrate.
	readme := readFile(t, dir, "README.md")
	assert.NotContains(t, readme, "make migrate")
	assert.Contains(t, readme, "Migrations run automatically")

	claude := readFile(t, dir, "CLAUDE.md")
	assert.NotContains(t, claude, "make migrate")
	assert.Contains(t, claude, "Migrations run automatically")

	agents := readFile(t, dir, "AGENTS.md")
	assert.NotContains(t, agents, "make migrate")
	assert.Contains(t, agents, "Migrations run automatically")
}

func TestGenerateProject_gormMigrateAtStartup(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "gormmigproj")

	cfg := &ProjectConfig{
		Name:             "gormmigproj",
		Module:           "github.com/test/gormmigproj",
		CSS:              "plain",
		Database:         "postgres",
		DBConnector:      "gorm",
		MigrateAtStartup: true,
		GoVersion:        "1.25.0",
		IncludeSessions:  true,
		IncludeAuth:      true,
		AuthWithTables:   true,
	}

	require.NoError(t, GenerateProject(dir, cfg))

	// No cmd/migrate.
	assertFileNotExists(t, dir, "cmd/migrate/main.go")

	// Server main.go should auto-migrate at startup.
	mainGo := readFile(t, dir, "cmd/site/main.go")
	assert.Contains(t, mainGo, "appdb.AutoMigrate(database)")
	assert.Contains(t, mainGo, "auto-migration completed")
	assert.Contains(t, mainGo, "appdb.ConnectContext")

	// GORM model files exist.
	assertFileExists(t, dir, "internal/db/models.go")
	assertFileExists(t, dir, "internal/db/db.go")

	// No SQL migrations.
	assertFileNotExists(t, dir, "internal/db/migrations/001_initial.up.sql")

	claude := readFile(t, dir, "CLAUDE.md")
	assert.NotContains(t, claude, "make migrate")
	assert.Contains(t, claude, "Migrations run automatically")

	agents := readFile(t, dir, "AGENTS.md")
	assert.NotContains(t, agents, "make migrate")
	assert.Contains(t, agents, "Migrations run automatically")
	assert.Contains(t, agents, "Schema changes run during server startup via `appdb.AutoMigrate(...)`")
	assert.NotContains(t, agents, "cmd/migrate/            Standalone migration runner")
}

func TestGenerateProject_agentsWebsocketDocs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "wsdocsproj")

	cfg := &ProjectConfig{
		Name:      "wsdocsproj",
		Module:    "github.com/test/wsdocsproj",
		CSS:       "plain",
		Database:  "postgres",
		GoVersion: "1.25.0",
		IncludeWS: true,
	}

	require.NoError(t, GenerateProject(dir, cfg))

	agents := readFile(t, dir, "AGENTS.md")
	assert.Contains(t, agents, "hub := websocket.NewHub()")
	assert.Contains(t, agents, "websocket.WithSubjectIDFunc(...)")
	assert.NotContains(t, agents, "echoCtx")
}

func TestGenerateProject_gormNoMigrateAtStartup(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "gormnomig")

	cfg := &ProjectConfig{
		Name:        "gormnomig",
		Module:      "github.com/test/gormnomig",
		CSS:         "plain",
		Database:    "postgres",
		DBConnector: "gorm",
		GoVersion:   "1.25.0",
	}

	require.NoError(t, GenerateProject(dir, cfg))

	// cmd/migrate exists with GORM variant.
	assertFileExists(t, dir, "cmd/migrate/main.go")
	migrateGo := readFile(t, dir, "cmd/migrate/main.go")
	assert.Contains(t, migrateGo, "appdb.AutoMigrate")
	assert.Contains(t, migrateGo, "appdb.Connect")

	// Server main.go should NOT have auto-migrate.
	mainGo := readFile(t, dir, "cmd/site/main.go")
	assert.NotContains(t, mainGo, "appdb.AutoMigrate")

	// Makefile should have migrate target but NOT migrate-down/status/create (GORM).
	makefile := readFile(t, dir, "Makefile")
	assert.Contains(t, makefile, "## migrate:")
	assert.NotContains(t, makefile, "migrate-down")
	assert.NotContains(t, makefile, "migrate-status")
	assert.NotContains(t, makefile, "migrate-create")
}

func TestProjectConfig_Validate_invalidDBConnector(t *testing.T) {
	cfg := ProjectConfig{Name: "proj", Module: "github.com/test/proj", DBConnector: "mysql"}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid --db-connector value")
}

func TestProjectConfig_Validate_dbConnectorDefaults(t *testing.T) {
	cfg := ProjectConfig{Name: "proj", Module: "github.com/test/proj"}
	require.NoError(t, cfg.Validate())
	assert.Equal(t, "sqlx", cfg.DBConnector)
}

func TestBuildProjectFileList_locale(t *testing.T) {
	t.Run("IncludeLocale true", func(t *testing.T) {
		cfg := &ProjectConfig{
			Name:          "proj",
			Module:        "github.com/test/proj",
			CSS:           "plain",
			IncludeLocale: true,
			DefaultLocale: "en",
		}
		files := buildProjectFileList(cfg)

		dests := make(map[string]bool)
		for _, f := range files {
			dests[f.dest] = true
		}

		assert.True(t, dests["locales/en.json"], "expected locales/en.json")
	})

	t.Run("IncludeLocale false", func(t *testing.T) {
		cfg := &ProjectConfig{
			Name:   "proj",
			Module: "github.com/test/proj",
			CSS:    "plain",
		}
		files := buildProjectFileList(cfg)

		dests := make(map[string]bool)
		for _, f := range files {
			dests[f.dest] = true
		}

		assert.False(t, dests["locales/en.json"])
	})
}

func TestGenerateProject_locale(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "localeproj")

	cfg := &ProjectConfig{
		Name:          "localeproj",
		Module:        "github.com/test/localeproj",
		CSS:           "plain",
		Database:      "postgres",
		GoVersion:     "1.25.0",
		IncludeLocale: true,
		DefaultLocale: "en",
	}

	require.NoError(t, GenerateProject(dir, cfg))

	// locales/en.json exists and contains project name (not {{.Name}}).
	enJSON := readFile(t, dir, "locales/en.json")
	assert.Contains(t, enJSON, "localeproj")
	assert.NotContains(t, enJSON, "{{.Name}}")

	// internal/locale/locale.go is NOT scaffolded — it is generated by
	// "hamr locale gen" which runs as part of make build / make test.
	assertFileNotExists(t, dir, "internal/locale/locale.go")

	// hamr.toml contains [locale] section.
	hamrToml := readFile(t, dir, "hamr.toml")
	assert.Contains(t, hamrToml, "[locale]")

	// cmd/site/main.go contains locale setup.
	mainGo := readFile(t, dir, "cmd/site/main.go")
	assert.Contains(t, mainGo, "i18n.NewBundle")
	assert.Contains(t, mainGo, "ptr.To(true)")

	// internal/web/server.go contains LocaleBundle in Deps.
	serverGo := readFile(t, dir, "internal/web/server.go")
	assert.Contains(t, serverGo, "LocaleBundle")

	// Makefile contains locale-gen target and runs it in build/test.
	makefile := readFile(t, dir, "Makefile")
	assert.Contains(t, makefile, "locale-gen")
	// build and test targets should run hamr locale gen.
	assert.Contains(t, makefile, "hamr locale gen")

	agents := readFile(t, dir, "AGENTS.md")
	assert.Contains(t, agents, "internal/locale/         Generated type-safe i18n accessors after `hamr locale gen`")
	assert.Contains(t, agents, "after internal/locale/locale.go exists:")
}

func TestGenerateProject_noLocale(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nolocaleproj")

	cfg := &ProjectConfig{
		Name:      "nolocaleproj",
		Module:    "github.com/test/nolocaleproj",
		CSS:       "plain",
		Database:  "postgres",
		GoVersion: "1.25.0",
	}

	require.NoError(t, GenerateProject(dir, cfg))

	assertFileNotExists(t, dir, "locales/en.json")
	assertFileNotExists(t, dir, "internal/locale/locale.go")

	hamrToml := readFile(t, dir, "hamr.toml")
	assert.NotContains(t, hamrToml, "[locale]")

	mainGo := readFile(t, dir, "cmd/site/main.go")
	assert.NotContains(t, mainGo, "i18n.NewBundle")
	assert.NotContains(t, mainGo, "LocaleBundle")

	serverGo := readFile(t, dir, "internal/web/server.go")
	assert.NotContains(t, serverGo, "LocaleBundle")

	makefile := readFile(t, dir, "Makefile")
	assert.NotContains(t, makefile, "locale-gen")
	assert.NotContains(t, makefile, "hamr locale gen")
}

// --- Helpers ---

func assertFileExists(t *testing.T, dir, rel string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	_, err := os.Stat(path)
	assert.NoError(t, err, "expected file to exist: %s", rel)
}

func assertFileNotExists(t *testing.T, dir, rel string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	_, err := os.Stat(path)
	assert.True(t, os.IsNotExist(err), "expected file to not exist: %s", rel)
}

func readFile(t *testing.T, dir, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, rel))
	require.NoError(t, err, "read file: %s", rel)
	return strings.TrimSpace(string(data))
}
