package scaffold

// Category classifies a scaffold change.
type Category string

const (
	CategoryStructural    Category = "structural"
	CategoryPackageUpdate Category = "package_update"
	CategoryNewPackage    Category = "new_package"
	CategoryNewOption     Category = "new_option"
	CategoryPattern       Category = "pattern"
	CategoryConfig        Category = "config"
)

// Change describes a single scaffold change between HAMR versions.
type Change struct {
	Category             Category `json:"category"`
	Title                string   `json:"title"`
	Since                string   `json:"since"`
	Summary              string   `json:"summary"`
	AffectedScaffoldFiles []string `json:"affected_scaffold_files"`
	// RelevantOptions lists option keys that make this change relevant.
	// Empty means relevant to all projects.
	RelevantOptions []string `json:"relevant_options,omitempty"`
}

// changes is the registry of all known scaffold changes.
// When the scaffold evolves, append Change entries here with the version that
// introduced each change. BuildReport filters them by version range.
var changes = []Change{
	{
		Category: CategoryStructural,
		Title:    "Rename cmd/server to cmd/site",
		Since:    "0.2.0",
		Summary:  "The application entry point directory was renamed from cmd/server to cmd/site. This affects the binary name, Dockerfile, Makefile build targets, CI workflows, and hamr.toml dev runner configuration.",
		AffectedScaffoldFiles: []string{
			"cmd/site/main.go",
			"cmd/site/Dockerfile",
			"Makefile",
			"README.md",
			"AGENTS.md",
			".env.example",
			"hamr.toml",
			"docs/adr/000-base-framework.md",
			".github/workflows/ci.yml",
			"e2e-go/testcontainers_setup.go",
		},
	},
	{
		Category: CategoryPattern,
		Title:    "Remove logger from handler structs",
		Since:    "0.2.0",
		Summary:  "Handlers no longer accept *slog.Logger as a constructor parameter or store it as a struct field. Use logging.FromContext(ctx) to obtain the request-scoped logger instead. The Log field was also removed from web.Deps and api.Deps.",
		AffectedScaffoldFiles: []string{
			"cmd/site/main.go",
			"internal/api/server.go",
			"internal/api/handler/stripe/handler.go",
			"internal/web/server.go",
			"internal/web/static.go",
			"internal/web/handler/about/handler.go",
			"internal/web/handler/auth/handler.go",
			"internal/web/handler/errors/handler.go",
			"internal/web/handler/home/handler.go",
			"AGENTS.md",
		},
	},
	{
		Category: CategoryNewPackage,
		Title:    "Project-local middleware package",
		Since:    "0.2.0",
		Summary:  "A new internal/middleware package provides a Logging() middleware that combines request ID generation, structured logger injection, and request completion logging. Replaces the previous middleware.RequestID() usage. The hamr pkg/middleware is now imported with alias hamrmw in web/server.go.",
		AffectedScaffoldFiles: []string{
			"internal/middleware/logging.go",
			"internal/api/server.go",
			"internal/web/server.go",
		},
	},
	{
		Category: CategoryPattern,
		Title:    "CSRFField accepts echo.Context",
		Since:    "0.2.0",
		Summary:  "form.CSRFField now takes echo.Context instead of a string token. Update calls from form.CSRFField(c.Get(\"csrf\").(string)) to form.CSRFField(c).",
		AffectedScaffoldFiles: []string{
			"internal/web/components/form/fields.templ",
			"internal/web/handler/auth/login.templ",
			"internal/web/handler/auth/register.templ",
			"AGENTS.md",
		},
		RelevantOptions: []string{"auth"},
	},
	{
		Category: CategoryConfig,
		Title:    "hamr.toml metadata and options sections",
		Since:    "0.2.0",
		Summary:  "hamr.toml now includes [hamr], [options], and [ai] sections for version tracking, scaffold option recording, and AI directory configuration. The .hamr/ directory is added to .gitignore.",
		AffectedScaffoldFiles: []string{
			"hamr.toml",
			".gitignore",
		},
	},
}

// Changes returns all registered scaffold changes.
func Changes() []Change {
	return changes
}

// IsRelevant returns true if the change is relevant to a project with the given options.
// A change with no RelevantOptions is relevant to all projects.
func (c Change) IsRelevant(opts Options) bool {
	if len(c.RelevantOptions) == 0 {
		return true
	}
	for _, key := range c.RelevantOptions {
		if optionEnabled(opts, key) {
			return true
		}
	}
	return false
}

// optionEnabled checks whether a specific option is enabled in the project options.
func optionEnabled(opts Options, key string) bool {
	switch key {
	case "database":
		return opts.Database != "" && opts.Database != "none"
	case "db_connector":
		return opts.DBConnector != ""
	case "auth":
		return opts.Auth != "" && opts.Auth != "none"
	case "css":
		return opts.CSS != "" && opts.CSS != "none"
	case "websockets":
		return opts.WebSockets
	case "e2e":
		return opts.E2E
	case "stripe":
		return opts.Stripe
	case "locale":
		return opts.Locale
	case "storage":
		return opts.Storage != "" && opts.Storage != "none"
	default:
		return false
	}
}
