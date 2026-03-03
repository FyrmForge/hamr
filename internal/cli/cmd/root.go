package cmd

import "github.com/spf13/cobra"

var rootCmd = &cobra.Command{
	Use:   "hamr",
	Short: "HAMR - Go full-stack framework and project scaffolding CLI",
}

func init() {
	rootCmd.AddCommand(newCmd)
	rootCmd.AddCommand(renameCmd)
	rootCmd.AddCommand(vendorCmd)
	rootCmd.AddCommand(syncCmd)
	rootCmd.AddCommand(versionCmd)
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}
