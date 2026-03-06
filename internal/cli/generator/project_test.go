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
	assert.Equal(t, "1.25.0", cfg.GoVersion)
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
		"cmd/server/main.go",
		"cmd/server/Dockerfile",
		"internal/db/db.go",
		"internal/repo/repo.go",
		"internal/repo/postgres/store.go",
		"internal/web/server.go",
		"internal/web/handler/home/handler.go",
		"internal/web/handler/health/handler.go",
		"internal/web/handler/errors/handler.go",
		"internal/web/components/layout.templ",
		".gitignore",
		"Makefile",
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
	assert.True(t, dests["docs/ai-guides/css.md"])
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
	assert.True(t, dests["docs/ai-guides/tailwind.md"])
	assert.False(t, dests["static/css/base/variables.css"])
	assert.False(t, dests["docs/ai-guides/css.md"])
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
	assert.True(t, dests["internal/web/handler/auth/handler.go"])
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
	assert.True(t, dests["cmd/server/main.go"])
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
	assert.True(t, dests["cmd/server/main.go"])
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

	assert.True(t, dests["e2e-go/main_test.go"])
	assert.True(t, dests["e2e-go/helpers.go"])
	assert.True(t, dests["e2e-go/home_test.go"])
	assert.True(t, dests["e2e-go/testdata/seed_e2e.sql"])
	assert.True(t, dests["e2e-go/README.md"])
}

func TestBuildProjectFileList_noE2E(t *testing.T) {
	cfg := &ProjectConfig{Name: "proj", Module: "github.com/test/proj", CSS: "plain"}
	files := buildProjectFileList(cfg)

	dests := make(map[string]bool)
	for _, f := range files {
		dests[f.dest] = true
	}

	assert.False(t, dests["e2e-go/main_test.go"])
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
	assertFileExists(t, dir, "cmd/server/main.go")
	assertFileExists(t, dir, "internal/web/server.go")
	assertFileExists(t, dir, "go.mod")
	assertFileExists(t, dir, ".gitignore")

	// Check module substitution.
	gomod := readFile(t, dir, "go.mod")
	assert.Contains(t, gomod, "module github.com/test/testproj")

	// Check name substitution.
	readme := readFile(t, dir, "README.md")
	assert.Contains(t, readme, "testproj")

	// Check main.go has correct imports and env config pattern.
	mainGo := readFile(t, dir, "cmd/server/main.go")
	assert.Contains(t, mainGo, "github.com/FyrmForge/hamr/pkg/config")
	assert.Contains(t, mainGo, "github.com/FyrmForge/hamr/pkg/server")
	assert.Contains(t, mainGo, "envPort")
	assert.Contains(t, mainGo, "envDatabaseURL")
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
	assertFileExists(t, dir, "internal/web/handler/auth/handler.go")

	// Check migrations include users table.
	upSQL := readFile(t, dir, "internal/db/migrations/001_initial.up.sql")
	assert.Contains(t, upSQL, "CREATE TABLE sessions")
	assert.Contains(t, upSQL, "CREATE TABLE users")

	// Check main.go includes auth imports.
	mainGo := readFile(t, dir, "cmd/server/main.go")
	assert.Contains(t, mainGo, "github.com/FyrmForge/hamr/pkg/auth")
	assert.Contains(t, mainGo, "github.com/test/authproj/internal/service")

	// Check server.go includes auth routes.
	serverGo := readFile(t, dir, "internal/web/server.go")
	assert.Contains(t, serverGo, "authhandler")
	assert.Contains(t, serverGo, "RequireNotAuth")
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
	mainGo := readFile(t, dir, "cmd/server/main.go")
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
	assertFileExists(t, dir, "docs/ai-guides/tailwind.md")

	// Plain CSS files should NOT exist.
	assertFileNotExists(t, dir, "static/css/base/variables.css")
	assertFileNotExists(t, dir, "docs/ai-guides/css.md")
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

	assertFileExists(t, dir, "e2e-go/main_test.go")
	assertFileExists(t, dir, "e2e-go/helpers.go")
	assertFileExists(t, dir, "e2e-go/home_test.go")

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

	mainGo := readFile(t, dir, "cmd/server/main.go")
	assert.Contains(t, mainGo, "cfgproj?sslmode=disable")
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

	assertFileExists(t, dir, "cmd/server/main.go")
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
	assertFileExists(t, dir, "cmd/server/main.go")
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

func TestGenerateProject_s3Storage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "s3proj")

	cfg := &ProjectConfig{
		Name:            "s3proj",
		Module:          "github.com/test/s3proj",
		CSS:             "plain",
		Database:        "postgres",
		GoVersion:       "1.25.0",
		IncludeStorage:  true,
		StorageBackend:  "s3",
		S3StaticWatcher: true,
	}

	require.NoError(t, GenerateProject(dir, cfg))

	// docker-compose should have MinIO.
	compose := readFile(t, dir, "docker/docker-compose.yaml")
	assert.Contains(t, compose, "minio:")
	assert.Contains(t, compose, "minio_data:")

	// .env should have S3 vars.
	envFile := readFile(t, dir, ".env.example")
	assert.Contains(t, envFile, "S3_ENDPOINT")
	assert.Contains(t, envFile, "S3_BUCKET")
	assert.NotContains(t, envFile, "STORAGE_PATH")

	// main.go should have S3 env vars and use S3 storage init.
	mainGo := readFile(t, dir, "cmd/server/main.go")
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

	// Makefile should have sync-static target using hamr sync.
	makefile := readFile(t, dir, "Makefile")
	assert.Contains(t, makefile, "sync-static:")
	assert.Contains(t, makefile, "hamr sync --watch")

	// go.mod should exist with correct module.
	gomod := readFile(t, dir, "go.mod")
	assert.Contains(t, gomod, "module github.com/test/s3proj")
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

	// docker-compose should NOT have MinIO.
	compose := readFile(t, dir, "docker/docker-compose.yaml")
	assert.NotContains(t, compose, "minio:")

	// .env should have STORAGE_PATH.
	envFile := readFile(t, dir, ".env.example")
	assert.Contains(t, envFile, "STORAGE_PATH")
	assert.NotContains(t, envFile, "S3_ENDPOINT")

	// main.go should have local storage env vars.
	mainGo := readFile(t, dir, "cmd/server/main.go")
	assert.Contains(t, mainGo, "envStoragePath")
	assert.NotContains(t, mainGo, "envS3Endpoint")
	assert.Contains(t, mainGo, "NewLocalStorage")
	assert.NotContains(t, mainGo, "NewS3Storage")

	// Makefile should NOT have sync-static target for local storage.
	localMakefile := readFile(t, dir, "Makefile")
	assert.NotContains(t, localMakefile, "sync-static:")
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
	assert.Contains(t, ci, "templ generate")
	assert.Contains(t, ci, "templ files are out of date")
	assert.Contains(t, ci, "golangci-lint-action")
	assert.NotContains(t, ci, "setup-node")

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
	assert.Contains(t, ci, "npm ci")
	assert.Contains(t, ci, "npm run css:build")
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
