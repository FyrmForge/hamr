package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/FyrmForge/hamr/internal/devserver"
	"github.com/spf13/cobra"
	"golang.org/x/term"
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

	// Save terminal state so we can always restore it, even after a panic
	// (the hotkey reader puts the terminal into raw mode).
	if fd := int(os.Stdin.Fd()); term.IsTerminal(fd) {
		if oldState, err := term.GetState(fd); err == nil {
			defer func() { _ = term.Restore(fd, oldState) }()
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Create hotkey reader and status bar once so they survive config reloads.
	// Defer Stop immediately — both are safe on zero value.
	// Only Start after the first successful config load so the terminal stays
	// in cooked mode (Ctrl+C works, no staircase output) during config errors.
	var hotkeys devserver.HotkeyReader
	defer hotkeys.Stop()

	var statusBar devserver.StatusBar
	defer statusBar.Stop()

	var started bool

	for {
		cfg, err := devserver.LoadConfig(configPath)
		if err != nil {
			fmt.Printf("%s config error: %v\r\n", devserver.HamrDevTag(), err)
			fmt.Printf("%s waiting for config fix...\r\n", devserver.HamrDevTag())
			var waitErr error
			if started {
				waitErr = devserver.WaitForConfigChangeOrQuit(ctx, configPath, hotkeys.Actions())
			} else {
				waitErr = devserver.WaitForConfigChange(ctx, configPath)
			}
			if waitErr != nil {
				return waitErr
			}
			fmt.Printf("\r\n%s--- config changed, retrying ---\r\n", devserver.HamrDevTag())
			continue
		}

		if !started {
			hotkeys.Start(ctx)
			statusBar.Start()
			started = true
		}

		runner := devserver.NewRunner(cfg,
			devserver.WithConfigPath(configPath),
			devserver.WithVerbose(verbose),
			devserver.WithNoProxy(noProxy),
			devserver.WithHotkeys(&hotkeys),
			devserver.WithStatusBar(&statusBar),
		)

		err = runner.Run(ctx)
		if errors.Is(err, devserver.ErrConfigReload) {
			fmt.Printf("\r\n%s--- config changed, restarting ---\r\n", devserver.HamrDevTag())
			continue
		}
		return err
	}
}
