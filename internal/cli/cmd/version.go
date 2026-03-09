package cmd

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"
)

var (
	version = "dev"
	commit  = "none"
)

func init() {
	if version == "dev" || commit == "none" {
		if bi, ok := debug.ReadBuildInfo(); ok {
			if version == "dev" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
				version = bi.Main.Version
			}
			if commit == "none" {
				for _, s := range bi.Settings {
					if s.Key == "vcs.revision" && len(s.Value) >= 7 {
						commit = s.Value[:7]
						break
					}
				}
			}
		}
	}
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version of hamr",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("hamr %s (%s)\n", version, commit)
	},
}
