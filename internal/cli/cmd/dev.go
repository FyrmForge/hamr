package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/FyrmForge/hamr/pkg/devserver"
	"github.com/spf13/cobra"
)

var devCmd = &cobra.Command{
	Use:   "dev",
	Short: "Start the dev server with file watching, builds, and live reload",
	Long: `Reads [dev] and [proxy] from hamr.toml (or --config) and runs:

  - File watchers for each [[dev.watch]] rule
  - Build commands with dependency ordering
  - Long-running processes with automatic restart
  - Reverse proxy with SSE-based live reload`,
	Args: cobra.NoArgs,
	RunE: runDev,
}

func init() {
	devCmd.Flags().String("config", "hamr.toml", "path to config file")
	devCmd.Flags().Bool("no-proxy", false, "skip the reverse proxy, just run watchers")
	devCmd.Flags().BoolP("verbose", "v", false, "enable verbose (debug) logging")
}

func runDev(cmd *cobra.Command, _ []string) error {
	configPath, _ := cmd.Flags().GetString("config")
	noProxy, _ := cmd.Flags().GetBool("no-proxy")
	verbose, _ := cmd.Flags().GetBool("verbose")

	cfg, err := devserver.LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	runner := devserver.NewRunner(cfg,
		devserver.WithVerbose(verbose),
		devserver.WithNoProxy(noProxy),
	)

	return runner.Run(ctx)
}
