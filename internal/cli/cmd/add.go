package cmd

import "github.com/spf13/cobra"

var addCmd = &cobra.Command{
	Use:   "add",
	Short: "Add components to an existing HAMR project",
}

func init() {
	addCmd.AddCommand(addServiceCmd)
}
