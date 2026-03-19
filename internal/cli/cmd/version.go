package cmd

import (
	"fmt"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"
)

const devVersion = "dev"

var (
	version      = devVersion
	commit       = "none"
	releaseBuild bool
)

func init() {
	if version == devVersion || commit == "none" {
		if bi, ok := debug.ReadBuildInfo(); ok {
			if version == devVersion && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
				version = strings.TrimPrefix(bi.Main.Version, "v")
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
	releaseBuild = version != devVersion
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version of hamr",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("hamr %s (%s)\n", version, commit)
	},
}
