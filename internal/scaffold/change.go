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
// This slice is intentionally empty until the first scaffold-breaking release.
// When the scaffold evolves, append Change entries here with the version that
// introduced each change. BuildReport filters them by version range.
var changes []Change

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
