package cmd

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
	"github.com/FyrmForge/hamr/pkg/fingerprint"
	"github.com/spf13/cobra"
)

// staticTomlConfig mirrors the [static] section of hamr.toml.
type staticTomlConfig struct {
	Dir      string `toml:"dir"`
	Dist     string `toml:"dist"`
	Manifest string `toml:"manifest"`
	Package  string `toml:"package"`
}

type staticTomlFile struct {
	Static staticTomlConfig `toml:"static"`
}

// staticDirFromConfig returns [static].dir from hamr.toml in the current
// directory, or fallback when hamr.toml is missing, unparseable, or has no
// [static] table. Lets commands that touch the static tree (sync, skill
// install) follow a project's configured layout instead of assuming one.
func staticDirFromConfig(fallback string) string {
	data, err := os.ReadFile("hamr.toml")
	if err != nil {
		return fallback
	}
	var f staticTomlFile
	if err := toml.Unmarshal(data, &f); err != nil {
		return fallback
	}
	if f.Static.Dir == "" {
		return fallback
	}
	return f.Static.Dir
}

var genStaticCmd = &cobra.Command{
	Use:   "static",
	Short: "Fingerprint static assets into the dist directory",
	Long: `Fingerprint static assets by content-hashing filenames.

Reads source files from the static directory, creates fingerprinted copies
(e.g. output.a1b2c3d4e5f6.css) in the dist directory, and generates a Go
source file with the manifest baked in at compile time.

Configuration is read from [static] in hamr.toml:

  [static]
  dir = "frontend/static"
  dist = "frontend/dist"
  manifest = "internal/web/components/staticmanifest.go"
  package = "components"

Examples:
  hamr gen static          # fingerprint [static].dir → [static].dist
  hamr gen static --clean  # remove the dist dir and reset the manifest`,
	RunE: func(cmd *cobra.Command, args []string) error {
		configPath, _ := cmd.Flags().GetString("config")
		clean, _ := cmd.Flags().GetBool("clean")

		cfg := staticTomlConfig{
			Dir:      "static",
			Dist:     "dist",
			Manifest: "internal/web/components/staticmanifest.go",
			Package:  "components",
		}

		// Load config from TOML — required.
		data, err := os.ReadFile(configPath)
		if err != nil {
			return fmt.Errorf("reading %s: %w (hamr gen static must run inside a hamr project)", configPath, err)
		}
		var f staticTomlFile
		if err := toml.Unmarshal(data, &f); err != nil {
			return fmt.Errorf("parsing %s: %w", configPath, err)
		}
		if f.Static.Dir != "" {
			cfg.Dir = f.Static.Dir
		}
		if f.Static.Dist != "" {
			cfg.Dist = f.Static.Dist
		}
		if f.Static.Manifest != "" {
			cfg.Manifest = f.Static.Manifest
		}
		if f.Static.Package != "" {
			cfg.Package = f.Static.Package
		}

		if clean {
			if err := fingerprint.Clean(cfg.Dist); err != nil {
				return err
			}
			// Reset the Go manifest to an empty declaration.
			empty := &fingerprint.Manifest{Files: nil}
			if err := empty.WriteGoManifest(cfg.Manifest, cfg.Package); err != nil {
				return err
			}
			fmt.Printf("Cleaned %s\n", cfg.Dist)
			return nil
		}

		m, err := fingerprint.Fingerprint(cfg.Dir, cfg.Dist)
		if err != nil {
			return err
		}

		if err := m.WriteGoManifest(cfg.Manifest, cfg.Package); err != nil {
			return err
		}

		fmt.Printf("Fingerprinted %d files → %s\n", len(m.Files), cfg.Dist)
		return nil
	},
}

func init() {
	genStaticCmd.Flags().String("config", "hamr.toml", "path to hamr.toml config file")
	genStaticCmd.Flags().Bool("clean", false, "remove the dist directory and reset manifest")
	genCmd.AddCommand(genStaticCmd)
}
