package cmd

import "github.com/spf13/cobra"

var localeCmd = &cobra.Command{
	Use:   "locale",
	Short: "Locale (i18n) commands",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

func init() {
	localeCmd.AddCommand(localeGenCmd)
}
