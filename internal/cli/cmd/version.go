package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	version = "dev"
	commit  = "none"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version of hamr",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("hamr %s (%s)\n", version, commit)
	},
}
