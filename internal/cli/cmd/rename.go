package cmd

import "github.com/spf13/cobra"

var renameCmd = &cobra.Command{
	Use:   "rename",
	Short: "Rename project components",
}

func init() {
	renameCmd.AddCommand(renameModuleCmd)
}
