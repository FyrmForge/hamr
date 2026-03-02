package generator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ProjectConfig holds the data used to render project templates.
type ProjectConfig struct {
	Name            string // "myproject"
	Module          string // "github.com/user/myproject"
	CSS             string // "plain" | "tailwind"
	Database        string // "postgres"
	GoVersion       string // "1.25.0"
	InPlace         bool   // generate into existing directory
	IncludeSessions bool
	IncludeAuth     bool
	AuthWithTables  bool
	IncludeStorage  bool   // true when StorageBackend != ""
	StorageBackend  string // "" | "local" | "s3"
	S3StaticWatcher bool   // include cmd/syncstatic watcher
	IncludeWS       bool
	IncludeNotify   bool
	IncludeE2E      bool
	HamrLocalPath   string // local path for replace directive (dev only)
}

// Validate checks that the ProjectConfig has all required fields and valid values.
func (cfg *ProjectConfig) Validate() error {
	if cfg.Name == "" {
		return fmt.Errorf("project name is required")
	}
	if cfg.Module == "" {
		return fmt.Errorf("module path is required (use --module)")
	}
	if cfg.CSS == "" {
		cfg.CSS = "plain"
	}
	if cfg.CSS != "plain" && cfg.CSS != "tailwind" {
		return fmt.Errorf("invalid --css value %q: must be \"plain\" or \"tailwind\"", cfg.CSS)
	}
	if cfg.Database == "" {
		cfg.Database = "postgres"
	}
	if cfg.GoVersion == "" {
		cfg.GoVersion = "1.25.0"
	}
	if cfg.IncludeAuth {
		cfg.IncludeSessions = true
	}
	if cfg.StorageBackend != "" {
		cfg.IncludeStorage = true
	}
	return nil
}

type templateFile struct {
	tmpl string // path inside embedded FS
	dest string // output path relative to project root
}

// GenerateProject scaffolds a new project directory with all required files.
// When cfg.InPlace is true, it generates into an existing directory, skipping
// files that already exist (notably go.mod).
func GenerateProject(dir string, cfg *ProjectConfig) error {
	if cfg.InPlace {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create project directory: %w", err)
		}
	} else {
		if _, err := os.Stat(dir); err == nil {
			return fmt.Errorf("directory %q already exists", dir)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create project directory: %w", err)
		}
	}

	files := buildProjectFileList(cfg)

	for _, f := range files {
		dest := filepath.Join(dir, f.dest)

		// In-place mode: skip files that already exist on disk.
		if cfg.InPlace {
			if _, err := os.Stat(dest); err == nil {
				continue
			}
		}

		if err := renderFromFS(newTemplates, f.tmpl, dest, cfg); err != nil {
			if !cfg.InPlace {
				_ = os.RemoveAll(dir)
			}
			return fmt.Errorf("render %s: %w", f.dest, err)
		}
	}

	return nil
}

// ReadExistingGoMod reads go.mod from dir and returns the module path and Go
// version. Returns empty strings (not an error) if go.mod doesn't exist.
func ReadExistingGoMod(dir string) (module, goVersion string, err error) {
	path := filepath.Join(dir, "go.mod")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "", "", nil
	}
	return parseGoMod(path)
}

func buildProjectFileList(cfg *ProjectConfig) []templateFile {
	files := []templateFile{
		// cmd/server
		{"templates/new/cmd/server/main.go.tmpl", "cmd/server/main.go"},
		{"templates/new/cmd/server/Dockerfile.tmpl", "cmd/server/Dockerfile"},

		// internal/db
		{"templates/new/internal/db/db.go.tmpl", "internal/db/db.go"},
		{"templates/new/internal/db/migrations/001_initial.up.sql.tmpl", "internal/db/migrations/001_initial.up.sql"},
		{"templates/new/internal/db/migrations/001_initial.down.sql.tmpl", "internal/db/migrations/001_initial.down.sql"},

		// internal/repo
		{"templates/new/internal/repo/repo.go.tmpl", "internal/repo/repo.go"},
		{"templates/new/internal/repo/postgres/store.go.tmpl", "internal/repo/postgres/store.go"},

		// internal/web
		{"templates/new/internal/web/server.go.tmpl", "internal/web/server.go"},
		{"templates/new/internal/web/handler/home/handler.go.tmpl", "internal/web/handler/home/handler.go"},
		{"templates/new/internal/web/handler/health/handler.go.tmpl", "internal/web/handler/health/handler.go"},
		{"templates/new/internal/web/handler/errors/handler.go.tmpl", "internal/web/handler/errors/handler.go"},

		// internal/web/components
		{"templates/new/internal/web/components/layout.templ.tmpl", "internal/web/components/layout.templ"},
		{"templates/new/internal/web/components/helpers.go.tmpl", "internal/web/components/helpers.go"},
		{"templates/new/internal/web/components/form/fields.templ.tmpl", "internal/web/components/form/fields.templ"},
		{"templates/new/internal/web/components/form/helpers.go.tmpl", "internal/web/components/form/helpers.go"},

		// static
		{"templates/new/static/js/main.js.tmpl", "static/js/main.js"},
		{"templates/new/static/images/gitkeep.tmpl", "static/images/.gitkeep"},

		// docker
		{"templates/new/docker/docker-compose.yaml.tmpl", "docker/docker-compose.yaml"},

		// docs
		{"templates/new/docs/adr/000-base-framework.md.tmpl", "docs/adr/000-base-framework.md"},
		{"templates/new/docs/features/TEMPLATE.md.tmpl", "docs/features/TEMPLATE.md"},
		{"templates/new/docs/ai-guides/handler-patterns.md.tmpl", "docs/ai-guides/handler-patterns.md"},
		{"templates/new/docs/ai-guides/validation.md.tmpl", "docs/ai-guides/validation.md"},
		{"templates/new/docs/ai-guides/forms.md.tmpl", "docs/ai-guides/forms.md"},

		// root files
		{"templates/new/root/gitignore.tmpl", ".gitignore"},
		{"templates/new/root/Makefile.tmpl", "Makefile"},
		{"templates/new/root/env.example.tmpl", ".env.example"},
		{"templates/new/root/env.example.tmpl", ".env"},
		{"templates/new/root/AGENTS.md.tmpl", "AGENTS.md"},
		{"templates/new/root/CLAUDE.md.tmpl", "CLAUDE.md"},
		{"templates/new/root/README.md.tmpl", "README.md"},
		{"templates/new/root/go.mod.tmpl", "go.mod"},
		{"templates/new/root/golangci.yml.tmpl", ".golangci.yml"},
	}

	// Plain CSS files.
	if cfg.CSS == "plain" {
		files = append(files,
			templateFile{"templates/new/static/css/base/variables.css.tmpl", "static/css/base/variables.css"},
			templateFile{"templates/new/static/css/base/reset.css.tmpl", "static/css/base/reset.css"},
			templateFile{"templates/new/static/css/base/utilities.css.tmpl", "static/css/base/utilities.css"},
			templateFile{"templates/new/static/css/components/buttons.css.tmpl", "static/css/components/buttons.css"},
			templateFile{"templates/new/static/css/components/forms.css.tmpl", "static/css/components/forms.css"},
			templateFile{"templates/new/static/css/components/alerts.css.tmpl", "static/css/components/alerts.css"},
			templateFile{"templates/new/static/css/layout/header.css.tmpl", "static/css/layout/header.css"},
			templateFile{"templates/new/static/css/layout/footer.css.tmpl", "static/css/layout/footer.css"},
			templateFile{"templates/new/static/css/pages/home.css.tmpl", "static/css/pages/home.css"},
			templateFile{"templates/new/docs/ai-guides/css.md.tmpl", "docs/ai-guides/css.md"},
		)
	}

	// Tailwind files.
	if cfg.CSS == "tailwind" {
		files = append(files,
			templateFile{"templates/new/root/tailwind.config.js.tmpl", "tailwind.config.js"},
			templateFile{"templates/new/root/package.json.tmpl", "package.json"},
			templateFile{"templates/new/docs/ai-guides/tailwind.md.tmpl", "docs/ai-guides/tailwind.md"},
		)
	}

	// Auth files.
	if cfg.IncludeAuth {
		files = append(files,
			templateFile{"templates/new/internal/repo/user.go.tmpl", "internal/repo/user.go"},
			templateFile{"templates/new/internal/repo/postgres/users.go.tmpl", "internal/repo/postgres/users.go"},
			templateFile{"templates/new/internal/service/auth.go.tmpl", "internal/service/auth.go"},
			templateFile{"templates/new/internal/web/handler/auth/handler.go.tmpl", "internal/web/handler/auth/handler.go"},
		)
	}

	// E2E files.
	if cfg.IncludeE2E {
		files = append(files,
			templateFile{"templates/new/e2e-go/main_test.go.tmpl", "e2e-go/main_test.go"},
			templateFile{"templates/new/e2e-go/testcontainers_setup.go.tmpl", "e2e-go/testcontainers_setup.go"},
			templateFile{"templates/new/e2e-go/helpers.go.tmpl", "e2e-go/helpers.go"},
			templateFile{"templates/new/e2e-go/accounts.go.tmpl", "e2e-go/accounts.go"},
			templateFile{"templates/new/e2e-go/auth_test.go.tmpl", "e2e-go/auth_test.go"},
			templateFile{"templates/new/e2e-go/home_test.go.tmpl", "e2e-go/home_test.go"},
			templateFile{"templates/new/e2e-go/testdata/seed_e2e.sql.tmpl", "e2e-go/testdata/seed_e2e.sql"},
			templateFile{"templates/new/e2e-go/README.md.tmpl", "e2e-go/README.md"},
		)
	}

	return files
}

// FindHamrLocalPath locates the hamr module root on disk so generated projects
// can use a replace directive. Checks in order:
//  1. HAMR_LOCAL_PATH env var (explicit override)
//  2. Walk up from the running executable (works for `go build ./cmd/hamr`)
//
// Returns "" if not found (e.g. installed via go install from a published version).
func FindHamrLocalPath() string {
	// Explicit env var — reliable escape hatch for go install.
	if p := os.Getenv("HAMR_LOCAL_PATH"); p != "" {
		if isHamrModuleRoot(p) {
			return p
		}
	}

	// Walk up from the executable.
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	// Resolve symlinks (e.g. ~/bin/hamr -> /home/user/FyrmForge/hamr/hamr).
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return walkUpForHamrMod(filepath.Dir(exe))
}

func isHamrModuleRoot(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "module github.com/FyrmForge/hamr")
}

func walkUpForHamrMod(dir string) string {
	for {
		if isHamrModuleRoot(dir) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}
