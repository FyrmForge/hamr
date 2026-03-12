package cmd

import "github.com/spf13/cobra"

var aiCmd = &cobra.Command{
	Use:   "ai",
	Short: "AI-oriented tools for capture, context export, and agent workflows",
	Long: `AI-oriented tools for browser capture, structured context export,
and future agent-facing workflows.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return cmd.Help()
	},
}

func init() {
	aiCmd.AddCommand(captureCmd)
}
