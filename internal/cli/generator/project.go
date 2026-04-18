package generator

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/FyrmForge/hamr/llmsdocs"
)

// ProjectConfig holds the data used to render project templates.
type ProjectConfig struct {
	Name             string // "myproject"
	Module           string // "github.com/user/myproject"
	CSS              string // "plain" | "tailwind"
	Database         string // "postgres"
	DBConnector      string // "sqlx" | "gorm"
	MigrateAtStartup bool   // run migrations when server starts
	GoVersion        string // "1.25.0"
	InPlace          bool   // generate into existing directory
	IncludeSessions  bool
	IncludeAuth      bool
	AuthWithTables   bool
	IncludeStorage   bool   // true when StorageBackend != ""
	StorageBackend   string // "" | "local" | "s3"
	StaticS3         bool   // sync static/ to a dedicated S3 bucket
	IncludePgAdmin   bool   // include pgAdmin in Docker Compose
	IncludeWS        bool
	IncludeE2E       bool
	IncludeStripe    bool
	IncludeLocale    bool
	IncludeAlpine    bool
	DefaultLocale    string // default: "en"
	HamrVersion      string // HAMR version at scaffold time
	ScaffoldedAt     string // date the project was scaffolded (YYYY-MM-DD)
}

// Validate checks that the ProjectConfig has all required fields and valid values.
func (cfg *ProjectConfig) Validate() error {
	if err := ValidateProjectName(cfg.Name); err != nil {
		return err
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
	if cfg.DBConnector == "" {
		cfg.DBConnector = "sqlx"
	}
	if cfg.DBConnector != "sqlx" && cfg.DBConnector != "gorm" {
		return fmt.Errorf("invalid --db-connector value %q: must be \"sqlx\" or \"gorm\"", cfg.DBConnector)
	}
	if cfg.GoVersion == "" {
		cfg.GoVersion = DetectGoVersion()
	}
	if cfg.GoVersion == "" {
		return fmt.Errorf("could not detect Go version; install Go or set GoVersion explicitly")
	}
	if cfg.IncludeAuth {
		cfg.IncludeSessions = true
	}
	if cfg.IncludeLocale && cfg.DefaultLocale == "" {
		cfg.DefaultLocale = "en"
	}
	if cfg.StorageBackend == "" && cfg.IncludeStorage {
		cfg.StorageBackend = "local"
	}
	switch cfg.StorageBackend {
	case "":
		cfg.IncludeStorage = false
	case "local", "s3":
		cfg.IncludeStorage = true
	default:
		return fmt.Errorf("invalid --storage value %q: must be \"local\" or \"s3\"", cfg.StorageBackend)
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
	// Apply defaults for fields that may not be set when called without Validate().
	if cfg.DBConnector == "" {
		cfg.DBConnector = "sqlx"
	}

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

	var skipped []string

	for _, f := range files {
		dest := filepath.Join(dir, f.dest)

		// In-place mode: skip files that already exist on disk.
		if cfg.InPlace {
			if _, err := os.Stat(dest); err == nil {
				skipped = append(skipped, f.dest)
				continue
			}
		}

		if err := renderFromFS(templates, f.tmpl, dest, cfg); err != nil {
			if !cfg.InPlace {
				_ = os.RemoveAll(dir)
			}
			return fmt.Errorf("render %s: %w", f.dest, err)
		}
	}

	// Create the .hamr/ai/ playground directory for AI command artifacts.
	if err := os.MkdirAll(filepath.Join(dir, ".hamr", "ai"), 0o755); err != nil {
		return fmt.Errorf("create .hamr/ai directory: %w", err)
	}

	// Copy framework reference docs (not templates, raw embedded files).
	if err := copyRawFile(llmsdocs.LLMsTxt, filepath.Join(dir, "docs", "llms.txt")); err != nil {
		return fmt.Errorf("copy llms.txt: %w", err)
	}
	if err := copyRawFile(llmsdocs.LLMsFullTxt, filepath.Join(dir, "docs", "llms-full.txt")); err != nil {
		return fmt.Errorf("copy llms-full.txt: %w", err)
	}

	// Make scripts executable.
	if err := os.Chmod(filepath.Join(dir, "scripts", "db-shell.sh"), 0o755); err != nil {
		return fmt.Errorf("chmod scripts/db-shell.sh: %w", err)
	}

	if len(skipped) > 0 {
		fmt.Printf("Skipped %d existing files: %s\n", len(skipped), strings.Join(skipped, ", "))
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
		// cmd/site
		{"templates/new/cmd/site/main.go.tmpl", "cmd/site/main.go"},
		{"templates/new/cmd/site/Dockerfile.tmpl", "cmd/site/Dockerfile"},

		// internal/repo
		{"templates/new/internal/repo/repo.go.tmpl", "internal/repo/repo.go"},
		{"templates/new/internal/repo/postgres/store.go.tmpl", "internal/repo/postgres/store.go"},

		// internal/api
		{"templates/new/internal/api/server.go.tmpl", "internal/api/server.go"},
		{"templates/new/internal/api/handler/health/handler.go.tmpl", "internal/api/handler/health/handler.go"},

		// internal/web
		{"templates/new/internal/web/server.go.tmpl", "internal/web/server.go"},
		{"templates/new/internal/web/handler/home/handler.go.tmpl", "internal/web/handler/home/handler.go"},
		{"templates/new/internal/web/handler/home/home.templ.tmpl", "internal/web/handler/home/home.templ"},
		{"templates/new/internal/web/handler/about/handler.go.tmpl", "internal/web/handler/about/handler.go"},
		{"templates/new/internal/web/handler/about/about.templ.tmpl", "internal/web/handler/about/about.templ"},

		// internal/middleware
		{"templates/new/internal/middleware/logging.go.tmpl", "internal/middleware/logging.go"},

		// internal/web/components
		{"templates/new/internal/web/components/layout.templ.tmpl", "internal/web/components/layout.templ"},
		{"templates/new/internal/web/components/error.templ.tmpl", "internal/web/components/error.templ"},
		{"templates/new/internal/web/components/helpers.go.tmpl", "internal/web/components/helpers.go"},
		{"templates/new/internal/web/components/staticmanifest.go.tmpl", "internal/web/components/staticmanifest.go"},
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
		// GitHub Actions
		{"templates/new/github/workflows/ci.yml.tmpl", ".github/workflows/ci.yml"},
		{"templates/new/github/workflows/deploy.yml.tmpl", ".github/workflows/deploy.yml"},

		// root files
		{"templates/new/root/gitignore.tmpl", ".gitignore"},
		{"templates/new/root/dockerignore.tmpl", ".dockerignore"},
		{"templates/new/root/Makefile.tmpl", "Makefile"},
		{"templates/new/root/env.example.tmpl", ".env.example"},
		{"templates/new/root/env.example.tmpl", ".env"},
		{"templates/new/root/AGENTS.md.tmpl", "AGENTS.md"},
		{"templates/new/root/CLAUDE.md.tmpl", "CLAUDE.md"},
		{"templates/new/root/README.md.tmpl", "README.md"},
		{"templates/new/scripts/db-shell.sh.tmpl", "scripts/db-shell.sh"},
		{"templates/new/root/go.mod.tmpl", "go.mod"},
		{"templates/new/root/golangci.yml.tmpl", ".golangci.yml"},
		{"templates/new/root/hamr.toml.tmpl", "hamr.toml"},
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
		)
	}

	// Tailwind files.
	if cfg.CSS == "tailwind" {
		files = append(files,
			templateFile{"templates/new/root/tailwind.config.js.tmpl", "tailwind.config.js"},
			templateFile{"templates/new/root/package.json.tmpl", "package.json"},
			templateFile{"templates/new/css/input.css.tmpl", "css/input.css"},
		)
	}

	// Auth files.
	if cfg.IncludeAuth {
		files = append(files,
			templateFile{"templates/new/internal/repo/user.go.tmpl", "internal/repo/user.go"},
			templateFile{"templates/new/internal/repo/postgres/users.go.tmpl", "internal/repo/postgres/users.go"},
			templateFile{"templates/new/internal/service/auth.go.tmpl", "internal/service/auth.go"},
			templateFile{"templates/new/internal/web/handler/auth/handler.go.tmpl", "internal/web/handler/auth/handler.go"},
			templateFile{"templates/new/internal/web/handler/auth/login.templ.tmpl", "internal/web/handler/auth/login.templ"},
			templateFile{"templates/new/internal/web/handler/auth/register.templ.tmpl", "internal/web/handler/auth/register.templ"},
		)
	}

	// WebSocket files.
	if cfg.IncludeWS {
		files = append(files,
			templateFile{"templates/new/static/js/ws.js.tmpl", "static/js/ws.js"},
		)
	}

	// Stripe webhook handler.
	if cfg.IncludeStripe {
		files = append(files,
			templateFile{"templates/new/internal/api/handler/stripe/handler.go.tmpl", "internal/api/handler/stripe/handler.go"},
		)
	}

	// pgAdmin server config.
	if cfg.IncludePgAdmin {
		files = append(files,
			templateFile{"templates/new/docker/pgadmin-servers.json.tmpl", "docker/pgadmin/servers.json"},
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

	// DB connector-specific files.
	if cfg.DBConnector == "gorm" {
		files = append(files,
			templateFile{"templates/new/internal/db/gorm-db.go.tmpl", "internal/db/db.go"},
			templateFile{"templates/new/internal/db/models.go.tmpl", "internal/db/models.go"},
		)
	} else {
		// sqlx (default)
		files = append(files,
			templateFile{"templates/new/internal/db/db.go.tmpl", "internal/db/db.go"},
			templateFile{"templates/new/internal/db/migrations/001_initial.up.sql.tmpl", "internal/db/migrations/001_initial.up.sql"},
			templateFile{"templates/new/internal/db/migrations/001_initial.down.sql.tmpl", "internal/db/migrations/001_initial.down.sql"},
		)
	}

	// Locale (i18n) files.
	if cfg.IncludeLocale {
		files = append(files,
			templateFile{"templates/new/locales/en.json.tmpl", "locales/en.json"},
		)
	}

	// cmd/migrate — only when migrations are NOT run at startup.
	if !cfg.MigrateAtStartup {
		files = append(files,
			templateFile{"templates/new/cmd/migrate/main.go.tmpl", "cmd/migrate/main.go"},
		)
	}

	return files
}

// DetectGoVersion returns the Go version installed on the system (e.g. "1.25.0")
// by running "go env GOVERSION". Returns "" if detection fails.
// Build metadata suffixes (e.g. "-X:nodwarf5") are stripped because go.mod only
// accepts versions matching the format 1.N or 1.N.P.
func DetectGoVersion() string {
	out, err := exec.Command("go", "env", "GOVERSION").Output()
	if err != nil {
		return ""
	}
	v := strings.TrimPrefix(strings.TrimSpace(string(out)), "go")
	if i := strings.IndexByte(v, '-'); i != -1 {
		v = v[:i]
	}
	return v
}
