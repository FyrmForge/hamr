package cmd

import (
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:          "hamr",
	Short:        "HAMR - Go full-stack framework and project scaffolding CLI",
	SilenceUsage: true,
}

func init() {
	// Disable Cobra's default completion command — we provide our own.
	rootCmd.CompletionOptions.DisableDefaultCmd = true
}

func init() {
	rootCmd.AddCommand(newCmd)
	rootCmd.AddCommand(renameCmd)
	rootCmd.AddCommand(vendorCmd)
	rootCmd.AddCommand(syncCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(lintCmd)
	rootCmd.AddCommand(devCmd)
	rootCmd.AddCommand(mockCmd)
	rootCmd.AddCommand(mcpCmd)
	rootCmd.AddCommand(aiCmd)
	rootCmd.AddCommand(localeCmd)
	rootCmd.AddCommand(completionCmd)
	rootCmd.AddCommand(genCmd)
	rootCmd.AddCommand(addCmd)
	rootCmd.AddCommand(envCmd)
}

// Execute runs the root command. The CLI deliberately does NOT load .env
// into its own process env — that would mutate global state and leak
// stale values into spawned children's envs (a subtle bug where edits to
// .env wouldn't reach live-reloaded site binaries because their
// godotenv/autoload skipped already-set vars). The one CLI command that
// needs .env values (`hamr sync` for S3 creds) reads them in a scoped
// way via flagOrEnv → readDotenvKey.
func Execute() error {
	return rootCmd.Execute()
}
