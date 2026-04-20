package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/FyrmForge/hamr/internal/cli/generator"
	"github.com/spf13/cobra"
)

var newCmd = &cobra.Command{
	Use:   "new [name]",
	Short: "Create a new HAMR project",
	Long: `Create a new HAMR project with all the scaffolding needed to start building.

Creates a complete project directory with:
  cmd/site/            - Application entry point and Dockerfile
  internal/            - Config, DB, repo, web layers
  static/              - CSS, JS, images
  docker/              - Docker Compose (PostgreSQL only; skipped for SQLite)
  docs/                - ADR, feature specs, AI guides

Usage:
  hamr new myapp       Create project in ./myapp subfolder
  hamr new .           Scaffold into the current directory
  hamr new             Interactive — asks for name and location

Flags are optional. Any flag not provided will be asked interactively.
When all flags are provided, no interactive prompts are shown.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var dir, name string
		var inPlace, needsName, needsLocation bool

		switch {
		case len(args) == 1 && args[0] == ".":
			// hamr new . → scaffold into current directory
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}
			dir = cwd
			name = filepath.Base(cwd)
			inPlace = true

		case len(args) == 1:
			// hamr new myapp → create subfolder
			dir = args[0]
			name = filepath.Base(dir)
			needsLocation = true

			// If --location flag was explicitly set, skip the interactive prompt.
			if cmd.Flags().Changed("location") {
				needsLocation = false
				loc, _ := cmd.Flags().GetString("location")
				if loc == "current" {
					cwd, err := os.Getwd()
					if err != nil {
						return fmt.Errorf("get working directory: %w", err)
					}
					dir = cwd
					name = filepath.Base(cwd)
					inPlace = true
				}
			}

		default:
			// hamr new → fully interactive
			needsName = true
			needsLocation = true

			if cmd.Flags().Changed("location") {
				needsLocation = false
				loc, _ := cmd.Flags().GetString("location")
				if loc == "current" {
					cwd, err := os.Getwd()
					if err != nil {
						return fmt.Errorf("get working directory: %w", err)
					}
					dir = cwd
					name = filepath.Base(cwd)
					inPlace = true
				}
			}
		}

		cfg := &generator.ProjectConfig{
			Name:            name,
			GoVersion:       generator.DetectGoVersion(),
			IncludeAuth:     true,
			AuthWithTables:  true,
			IncludeSessions: true,
		}

		res, err := runInteractiveForm(cmd, name, needsName, needsLocation)
		if err != nil {
			return err
		}

		// Apply name/location from wizard.
		if needsName {
			name = res.Name
			cfg.Name = name
		}
		if res.Location == "current" {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}
			dir = cwd
			name = filepath.Base(cwd)
			cfg.Name = name
			inPlace = true
		} else if dir == "" {
			// Subfolder with wizard-provided name.
			dir = res.Name
		}

		applyWizardResult(cmd, name, res, cfg)

		cfg.InPlace = inPlace

		// If generating in-place and a go.mod already exists, read module/version
		// from it so we don't overwrite it and the config stays consistent.
		if inPlace {
			existingMod, existingGoVer, err := generator.ReadExistingGoMod(dir)
			if err != nil {
				return fmt.Errorf("read existing go.mod: %w", err)
			}
			if existingMod != "" {
				cfg.Module = existingMod
				if existingGoVer != "" {
					cfg.GoVersion = existingGoVer
				}
				fmt.Printf("Using existing go.mod: module %s (go %s)\n", cfg.Module, cfg.GoVersion)
			}
		}

		cfg.HamrVersion = normalizeHamrVersion(version)
		cfg.ScaffoldedAt = time.Now().Format("2006-01-02")

		if err := cfg.Validate(); err != nil {
			return err
		}

		if err := generator.GenerateProject(dir, cfg); err != nil {
			return fmt.Errorf("generate project: %w", err)
		}

		var warnings []string

		// Auto-vendor JS dependencies (non-fatal).
		deps := []string{"htmx", "idiomorph"}
		if cfg.IncludeAlpine {
			deps = append(deps, "alpine")
		}
		if err := generator.VendorAll(dir, false, deps); err != nil {
			warnings = append(warnings, fmt.Sprintf("could not vendor JS dependencies: %v", err))
		}

		// Resolve dependencies (non-fatal).
		goget := exec.Command("go", "get", "./...")
		goget.Dir = dir
		if err := runWithSpinner("go get ./...", goget); err != nil {
			warnings = append(warnings, fmt.Sprintf("go get ./... failed: %v", err))
		}

		tidy := exec.Command("go", "mod", "tidy")
		tidy.Dir = dir
		if err := runWithSpinner("go mod tidy", tidy); err != nil {
			warnings = append(warnings, fmt.Sprintf("go mod tidy failed: %v", err))
		}

		// Install npm dependencies for Tailwind (non-fatal).
		if cfg.CSS == "tailwind" {
			npmInstall := exec.Command("npm", "install")
			npmInstall.Dir = dir
			if err := runWithSpinner("npm install", npmInstall); err != nil {
				warnings = append(warnings, fmt.Sprintf("npm install failed: %v", err))
			}
		}

		// Initialize git repo and create initial commit (non-fatal).
		isGitRepo := false
		if inPlace {
			chk := exec.Command("git", "rev-parse", "--git-dir")
			chk.Dir = dir
			isGitRepo = chk.Run() == nil
		}
		if !isGitRepo {
			gitInit := exec.Command("git", "init")
			gitInit.Dir = dir
			if err := runWithSpinner("Initializing git repository", gitInit); err != nil {
				warnings = append(warnings, fmt.Sprintf("git init failed: %v", err))
			}
		}

		if len(warnings) > 0 {
			fmt.Printf("\nProject %q created with warnings:\n\n", name)
			for _, w := range warnings {
				fmt.Printf("  - %s\n", w)
			}
			fmt.Println("\nYou may need to run 'go mod tidy' manually to resolve these.")
		} else {
			fmt.Printf("\nProject %q created successfully!\n", name)
		}

		fmt.Println("\nNext steps:")
		if !inPlace {
			fmt.Printf("  cd %s\n", dir)
		}
		fmt.Println("  hamr dev")

		return nil
	},
}

// applyWizardResult maps the interactive form results onto the ProjectConfig.
func applyWizardResult(cmd *cobra.Command, name string, res *wizardResult, cfg *generator.ProjectConfig) {
	if cmd.Flags().Changed("module") {
		cfg.Module, _ = cmd.Flags().GetString("module")
	} else {
		cfg.Module = fmt.Sprintf("github.com/%s/%s", res.Owner, name)
	}

	cfg.CSS = res.CSS
	cfg.Database = res.Database
	cfg.DBConnector = res.DBConnector
	cfg.MigrateAtStartup = res.MigrateAtStartup == "yes"

	cfg.IncludeWS = res.WebSocket == "yes"
	cfg.IncludeE2E = res.E2E == "yes"
	cfg.IncludeStripe = res.Stripe == "yes"
	cfg.IncludeLocale = res.Locale == "yes"
	cfg.IncludeAlpine = res.Alpine == "yes"

	cfg.StorageBackend = res.StorageBackend
	cfg.IncludeStorage = res.StorageBackend == "local" || res.StorageBackend == "s3"
	if res.StorageBackend == "s3" {
		cfg.StaticS3 = res.StaticS3 == "yes"
	}
	if res.Database == "postgres" {
		cfg.IncludePgAdmin = res.PgAdmin == "yes"
	}
}

// normalizeHamrVersion returns a clean semver string for embedding in hamr.toml.
// Released builds pass through as-is. Dev builds resolve the latest git tag and
// append "-dev" (e.g. "0.5.0-dev"). Falls back to "0.0.0-dev" when no tag exists.
func normalizeHamrVersion(v string) string {
	v = strings.TrimPrefix(v, "v")
	if v != "dev" && v != "" {
		return v
	}
	// Dev build — resolve latest tag from git.
	tag := latestGitTag()
	if tag != "" {
		return tag + "-dev"
	}
	return "0.0.0-dev"
}

// latestGitTag returns the most recent semver tag (without "v" prefix), or ""
// if no tags exist or git is unavailable.
func latestGitTag() string {
	out, err := exec.Command("git", "describe", "--tags", "--abbrev=0").Output()
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.TrimSpace(string(out)), "v")
}

func init() {
	newCmd.Flags().String("module", "", "Go module path (e.g. github.com/user/project); prompted if omitted")
	newCmd.Flags().String("css", "plain", "CSS approach: \"plain\" or \"tailwind\"")
	newCmd.Flags().String("storage", "local", "storage backend: \"local\" or \"s3\"")
	newCmd.Flags().Bool("websocket", false, "include WebSocket support")
	newCmd.Flags().Bool("e2e", false, "include E2E testing scaffolding")
	newCmd.Flags().String("database", "postgres", "database type: \"postgres\" or \"sqlite\"")
	newCmd.Flags().String("db-connector", "sqlx", "DB connector: \"sqlx\" or \"gorm\"")
	newCmd.Flags().Bool("migrate-startup", false, "run migrations at server startup instead of separate command")
	newCmd.Flags().String("location", "subfolder", "project location: \"subfolder\" or \"current\"")
	newCmd.Flags().Bool("static-s3", false, "sync static assets to a dedicated S3 bucket")
	newCmd.Flags().Bool("pgadmin", false, "include pgAdmin in Docker Compose")
	newCmd.Flags().Bool("stripe", false, "include Stripe webhook handler")
	newCmd.Flags().Bool("locale", false, "include localisation (i18n) support")
	newCmd.Flags().Bool("alpine", false, "include Alpine.js for local UI state")
}
