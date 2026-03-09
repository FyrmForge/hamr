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

		if err := cfg.Validate(); err != nil {
			return err
		}

		if err := generator.GenerateProject(dir, cfg); err != nil {
			return fmt.Errorf("generate project: %w", err)
		}

		var warnings []string

		// Auto-vendor JS dependencies (non-fatal).
		if err := generator.VendorAll(dir, false); err != nil {
			warnings = append(warnings, fmt.Sprintf("could not vendor JS dependencies: %v", err))
		}

		// Resolve dependencies (non-fatal).
		fmt.Println("Running go get ./...")
		goget := exec.Command("go", "get", "./...")
		goget.Dir = dir
		goget.Stdout = os.Stdout
		goget.Stderr = os.Stderr
		if err := goget.Run(); err != nil {
			warnings = append(warnings, fmt.Sprintf("go get ./... failed: %v", err))
		}

		fmt.Println("Running go mod tidy...")
		tidy := exec.Command("go", "mod", "tidy")
		tidy.Dir = dir
		tidy.Stdout = os.Stdout
		tidy.Stderr = os.Stderr
		if err := tidy.Run(); err != nil {
			warnings = append(warnings, fmt.Sprintf("go mod tidy failed: %v", err))
		}

		// Install npm dependencies for Tailwind (non-fatal).
		if cfg.CSS == "tailwind" {
			fmt.Println("Running npm install...")
			npmInstall := exec.Command("npm", "install")
			npmInstall.Dir = dir
			npmInstall.Stdout = os.Stdout
			npmInstall.Stderr = os.Stderr
			if err := npmInstall.Run(); err != nil {
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
			gitInit.Stdout = os.Stdout
			gitInit.Stderr = os.Stderr
			if err := gitInit.Run(); err != nil {
				warnings = append(warnings, fmt.Sprintf("git init failed: %v", err))
			} else {
				gitAdd := exec.Command("git", "add", "-A")
				gitAdd.Dir = dir
				if err := gitAdd.Run(); err != nil {
					warnings = append(warnings, fmt.Sprintf("git add failed: %v", err))
				} else {
					gitCommit := exec.Command("git", "commit", "-m", "Initial commit from hamr new")
					gitCommit.Dir = dir
					if err := gitCommit.Run(); err != nil {
						warnings = append(warnings, fmt.Sprintf("git commit failed: %v", err))
					}
				}
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

	cfg.IncludeWS = res.WebSocket == "yes"
	cfg.IncludeE2E = res.E2E == "yes"
	cfg.IncludeStripe = res.Stripe == "yes"

	if res.StorageBackend != "none" && res.StorageBackend != "" {
		cfg.IncludeStorage = true
		cfg.StorageBackend = res.StorageBackend
		if res.StorageBackend == "s3" {
			cfg.StaticS3 = res.StaticS3 == "yes"
		}
	}
}

func init() {
	newCmd.Flags().String("module", "", "Go module path (e.g. github.com/user/project); prompted if omitted")
	newCmd.Flags().String("css", "plain", "CSS approach: \"plain\" or \"tailwind\"")
	newCmd.Flags().String("storage", "none", "storage backend: \"none\", \"local\", or \"s3\"")
	newCmd.Flags().Bool("websocket", false, "include WebSocket support")
	newCmd.Flags().Bool("e2e", false, "include E2E testing scaffolding")
	newCmd.Flags().String("database", "postgres", "database type")
	newCmd.Flags().String("location", "subfolder", "project location: \"subfolder\" or \"current\"")
	newCmd.Flags().Bool("static-s3", false, "sync static assets to a dedicated S3 bucket")
	newCmd.Flags().Bool("stripe", false, "include Stripe webhook handler")
}
