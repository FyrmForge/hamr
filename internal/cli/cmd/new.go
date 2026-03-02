package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/FyrmForge/hamr/internal/cli/generator"
	"github.com/spf13/cobra"
)

var newCmd = &cobra.Command{
	Use:   "new [name]",
	Short: "Create a new HAMR project",
	Long: `Create a new HAMR project with all the scaffolding needed to start building.

Creates a complete project directory with:
  cmd/server/          - Application entry point and Dockerfile
  internal/            - Config, DB, repo, web layers
  static/              - CSS, JS, images
  docker/              - Docker Compose for PostgreSQL
  docs/                - ADR, feature specs, AI guides

Usage:
  hamr new myapp       Create project in ./myapp subfolder
  hamr new .           Scaffold into the current directory
  hamr new             Interactive — asks for name and location

When flags are omitted, an interactive wizard guides you through each option.
Pass --no-prompt to skip the wizard and use defaults.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		noPrompt, _ := cmd.Flags().GetBool("no-prompt")
		interactive := !noPrompt

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
			// In interactive mode, still offer the location choice.
			if interactive {
				needsLocation = true
			}

		default:
			// hamr new → fully interactive
			if !interactive {
				return fmt.Errorf("project name argument is required with --no-prompt")
			}
			needsName = true
			needsLocation = true
		}

		hamrLocal := generator.FindHamrLocalPath()
		if hamrLocal == "" {
			return fmt.Errorf("could not locate hamr source directory\n\n" +
				"Set HAMR_LOCAL_PATH to the hamr source directory, or symlink\n" +
				"the hamr binary from the source tree so it can be resolved.\n\n" +
				"  export HAMR_LOCAL_PATH=/path/to/hamr")
		}

		cfg := &generator.ProjectConfig{
			Name:          name,
			GoVersion:     "1.25.0",
			HamrLocalPath: hamrLocal,
		}

		if interactive {
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
		} else {
			applyFlags(cmd, name, cfg)
		}

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

		if err := cfg.Validate(); err != nil {
			return err
		}

		if err := generator.GenerateProject(dir, cfg); err != nil {
			return fmt.Errorf("generate project: %w", err)
		}

		// Auto-vendor JS dependencies (non-fatal).
		if err := generator.VendorAll(dir, false); err != nil {
			fmt.Printf("Warning: could not vendor JS dependencies: %v\n", err)
		}

		// Run go mod tidy (non-fatal).
		fmt.Println("Running go mod tidy...")
		tidy := exec.Command("go", "mod", "tidy")
		tidy.Dir = dir
		tidy.Stdout = os.Stdout
		tidy.Stderr = os.Stderr
		if err := tidy.Run(); err != nil {
			fmt.Printf("Warning: go mod tidy failed: %v\n", err)
		}

		fmt.Printf("\nProject %q created successfully!\n\n", name)
		fmt.Println("Next steps:")
		if !inPlace {
			fmt.Printf("  cd %s\n", dir)
		}
		fmt.Println("  docker compose -f docker/docker-compose.yaml up -d")
		fmt.Println("  templ generate")
		fmt.Println("  make dev")

		return nil
	},
}

// applyFlags reads all flag values for the non-interactive path.
func applyFlags(cmd *cobra.Command, name string, cfg *generator.ProjectConfig) {
	module, _ := cmd.Flags().GetString("module")
	if module == "" {
		module = fmt.Sprintf("github.com/user/%s", name)
	}
	cfg.Module = module
	cfg.CSS, _ = cmd.Flags().GetString("css")
	cfg.Database, _ = cmd.Flags().GetString("database")
	cfg.IncludeSessions, _ = cmd.Flags().GetBool("sessions")
	cfg.IncludeWS, _ = cmd.Flags().GetBool("websocket")
	cfg.IncludeNotify, _ = cmd.Flags().GetBool("notify")
	cfg.IncludeE2E, _ = cmd.Flags().GetBool("e2e")

	storageFlag, _ := cmd.Flags().GetString("storage")
	switch storageFlag {
	case "local":
		cfg.IncludeStorage = true
		cfg.StorageBackend = "local"
	case "s3":
		cfg.IncludeStorage = true
		cfg.StorageBackend = "s3"
		cfg.S3StaticWatcher, _ = cmd.Flags().GetBool("s3-watcher")
	}

	authFlag, _ := cmd.Flags().GetString("auth")
	switch authFlag {
	case "full":
		cfg.IncludeAuth = true
		cfg.AuthWithTables = true
	case "empty":
		cfg.IncludeAuth = true
	}
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

	switch res.Auth {
	case "full":
		cfg.IncludeAuth = true
		cfg.AuthWithTables = true
	case "empty":
		cfg.IncludeAuth = true
	}

	cfg.IncludeSessions = res.Sessions == "yes"
	cfg.IncludeWS = res.WebSocket == "yes"
	cfg.IncludeNotify = res.Notify == "yes"
	cfg.IncludeE2E = res.E2E == "yes"

	if res.StorageBackend != "none" && res.StorageBackend != "" {
		cfg.IncludeStorage = true
		cfg.StorageBackend = res.StorageBackend
		cfg.S3StaticWatcher = res.S3StaticWatcher == "yes"
	}
}

func init() {
	newCmd.Flags().String("module", "", "Go module path (e.g. github.com/user/project); prompted if omitted")
	newCmd.Flags().String("css", "plain", "CSS approach: \"plain\" or \"tailwind\"")
	newCmd.Flags().Bool("sessions", true, "include session support")
	newCmd.Flags().String("auth", "none", "auth scaffolding: \"full\", \"empty\", or \"none\"")
	newCmd.Flags().String("storage", "none", "storage backend: \"none\", \"local\", or \"s3\"")
	newCmd.Flags().Bool("s3-watcher", false, "include S3 static asset watcher for development")
	newCmd.Flags().Bool("websocket", false, "include WebSocket support")
	newCmd.Flags().Bool("notify", false, "include notification support")
	newCmd.Flags().Bool("e2e", false, "include E2E testing scaffolding")
	newCmd.Flags().String("database", "postgres", "database type")
	newCmd.Flags().Bool("no-prompt", false, "skip interactive wizard, use flags and defaults")
}
