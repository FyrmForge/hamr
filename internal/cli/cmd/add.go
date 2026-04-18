package cmd

import "github.com/spf13/cobra"

var addCmd = &cobra.Command{
	Use:   "add",
	Short: "Add optional artifacts to a HAMR project (skills, etc.)",
}
