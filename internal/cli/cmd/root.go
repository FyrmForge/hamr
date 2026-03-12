package cmd

import (
	"bufio"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:          "hamr",
	Short:        "HAMR - Go full-stack framework and project scaffolding CLI",
	SilenceUsage: true,
}

// loadDotenv reads a .env file and sets any variables not already present
// in the environment. Missing file is silently ignored.
func loadDotenv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close() //nolint:errcheck // best-effort

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		// Strip surrounding quotes.
		if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
			val = val[1 : len(val)-1]
		}
		// Don't override existing env vars.
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
}

func init() {
	rootCmd.AddCommand(newCmd)
	rootCmd.AddCommand(renameCmd)
	rootCmd.AddCommand(vendorCmd)
	rootCmd.AddCommand(syncCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(lintCmd)
	rootCmd.AddCommand(devCmd)
	rootCmd.AddCommand(aiCmd)
	rootCmd.AddCommand(localeCmd)
}

// Execute runs the root command.
func Execute() error {
	loadDotenv(".env")
	return rootCmd.Execute()
}
