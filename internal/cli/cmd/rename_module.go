package cmd

import (
	"fmt"

	"github.com/FyrmForge/hamr/internal/cli/generator"
	"github.com/spf13/cobra"
)

var renameModuleCmd = &cobra.Command{
	Use:   "module <new-module-path>",
	Short: "Rename the Go module and update all import paths",
	Long: `Rename the Go module path and rewrite all import paths in .go files.

Reads the current module path from go.mod, then replaces it with the new path
in every .go file under the target directory and in the go.mod module directive.

Examples:
  hamr rename module github.com/neworg/myproject
  hamr rename module github.com/neworg/myproject/tools --dir ./tools`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		newModule := args[0]
		dir, _ := cmd.Flags().GetString("dir")
		dryRun, _ := cmd.Flags().GetBool("dry-run")

		oldModule, filesUpdated, err := generator.RenameModule(dir, newModule, dryRun)
		if err != nil {
			return fmt.Errorf("rename module: %w", err)
		}

		if dryRun {
			fmt.Printf("Dry run: would rename module.\n\n")
		} else {
			fmt.Printf("Module renamed successfully.\n\n")
		}
		fmt.Printf("  old: %s\n", oldModule)
		fmt.Printf("  new: %s\n", newModule)
		if dryRun {
			fmt.Printf("  files would update: %d\n", filesUpdated)
		} else {
			fmt.Printf("  files updated: %d\n", filesUpdated)
		}

		return nil
	},
}

func init() {
	renameModuleCmd.Flags().String("dir", ".", "directory containing go.mod")
	renameModuleCmd.Flags().Bool("dry-run", false, "show what would change without writing files")
}
