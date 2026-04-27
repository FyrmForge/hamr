package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/FyrmForge/hamr/internal/devserver"
	"github.com/FyrmForge/hamr/internal/devserver/tui"
	"github.com/FyrmForge/hamr/internal/scaffold"
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
	devCmd.Flags().Bool("skip-version-check", false, "skip the \"scaffold newer than CLI\" guard")
	devCmd.Flags().Bool("tui", false, "run the experimental bubbletea TUI instead of the legacy stdout dev shell")
}

func runDev(cmd *cobra.Command, _ []string) error {
	configPath, _ := cmd.Flags().GetString("config")
	noProxy, _ := cmd.Flags().GetBool("no-proxy")
	verbose, _ := cmd.Flags().GetBool("verbose")
	skipVersionCheck, _ := cmd.Flags().GetBool("skip-version-check")
	tuiMode, _ := cmd.Flags().GetBool("tui")

	if err := ensureCLINotBehindScaffold(configPath, skipVersionCheck); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if tuiMode {
		return runDevTUI(ctx, configPath, noProxy, verbose)
	}

	// Save terminal state so we can always restore it, even after a panic
	// (the hotkey reader puts the terminal into raw mode).
	if fd := int(os.Stdin.Fd()); term.IsTerminal(fd) {
		if oldState, err := term.GetState(fd); err == nil {
			defer func() { _ = term.Restore(fd, oldState) }()
		}
	}

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

			statusBar.SetVersion("v" + version)
			checkVersionStatus(&statusBar, configPath)

			// Check for newer hamr release in the background (non-blocking).
			if releaseBuild {
				devserver.CheckLatestVersion(ctx, version, func(latest string) {
					if statusBar.SetVersionUpdateIfOK("v" + latest) {
						fmt.Printf("%s \033[1;93mupdate available: v%s → v%s\033[0m\r\n", devserver.HamrDevTag(), version, latest)
					}
				})
			}
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

// runDevTUI is the bubbletea-driven dev shell. The legacy raw-stdin path
// stays the default; this runs only when --tui is passed.
//
// Layout: a bubbletea program owns the screen on the main goroutine; the
// runner loop runs in a goroutine and writes all subprocess output, its
// own slog lines, and the dev command's status messages into a viewport
// sink that the program drains as LogLineMsg's. Hotkeys flow the other
// way, via a HotkeySource the model writes to from its Update.
//
// Config reload is handled inside the runner goroutine — the bubbletea
// program lives for the whole `hamr dev --tui` session.
func runDevTUI(ctx context.Context, configPath string, noProxy, verbose bool) error {
	rt := tui.NewRuntime()

	runnerErrCh := make(chan error, 1)
	go func() {
		runnerErrCh <- runDevTUILoop(ctx, rt, configPath, noProxy, verbose)
		// Tell bubbletea to exit once the runner is fully unwound — Quit
		// is safe to call multiple times and a no-op if the program has
		// already returned (e.g. user hit q).
		rt.Quit()
	}()

	if err := rt.Start(); err != nil {
		// Bubbletea bailed before the runner finished: tear it down so
		// goroutines drain and we surface the most informative error.
		<-runnerErrCh
		return err
	}
	return <-runnerErrCh
}

// runDevTUILoop is the runner-side counterpart to runDevTUI. It mirrors
// the CLI loop's config-reload behavior but writes status lines into the
// TUI viewport instead of os.Stdout.
func runDevTUILoop(ctx context.Context, rt *tui.Runtime, configPath string, noProxy, verbose bool) error {
	// Status bar lives only in the legacy CLI shell. The TUI renders its
	// own status bar from ErrorState (subscribed by Runtime.onActions).
	var statusBar devserver.StatusBar

	// Fire the version check once: the result lives for the whole TUI
	// session (a config reload doesn't change the CLI binary's version).
	var versionChecked bool

	for {
		cfg, err := devserver.LoadConfig(configPath)
		if err != nil {
			rt.Log(fmt.Sprintf("%s config error: %v", devserver.HamrDevTag(), err))
			rt.Log(fmt.Sprintf("%s waiting for config fix...", devserver.HamrDevTag()))
			if waitErr := devserver.WaitForConfigChange(ctx, configPath); waitErr != nil {
				return waitErr
			}
			rt.Log(fmt.Sprintf("%s --- config changed, retrying ---", devserver.HamrDevTag()))
			continue
		}

		if !versionChecked {
			rt.SetVersion("v" + version)
			status, msg, warning := computeVersionStatus(configPath)
			if warning != "" {
				rt.Log(fmt.Sprintf("%s %s", devserver.HamrDevTag(), warning))
			}
			rt.SetVersionStatus(status, msg)

			// Background latest-release check: same logic as the legacy
			// shell. The callback fires from a goroutine so it has to be
			// goroutine-safe — Runtime.SetVersionUpdateIfOK is.
			if releaseBuild {
				devserver.CheckLatestVersion(ctx, version, func(latest string) {
					rt.Log(fmt.Sprintf("%s update available: v%s → v%s", devserver.HamrDevTag(), version, latest))
					rt.SetVersionUpdateIfOK("v" + latest)
				})
			}
			versionChecked = true
		}

		// Surface the configured docker compose stacks to the TUI so
		// Tab can cycle through them, and hand the runner the per-entry
		// log sinks it needs to spawn `compose logs -f` followers.
		dockerSinks := rt.RegisterDockerStacks(composeNames(cfg.Dev.DockerCompose))

		opts := []devserver.Option{
			devserver.WithConfigPath(configPath),
			devserver.WithVerbose(verbose),
			devserver.WithNoProxy(noProxy),
			devserver.WithStatusBar(&statusBar),
			devserver.WithDockerLogSinks(dockerSinks),
		}
		opts = rt.Wire(opts)
		runner := devserver.NewRunner(cfg, opts...)

		err = runner.Run(ctx)
		if errors.Is(err, devserver.ErrConfigReload) {
			rt.Log(fmt.Sprintf("%s --- config changed, restarting ---", devserver.HamrDevTag()))
			continue
		}
		return err
	}
}

// ensureCLINotBehindScaffold blocks hamr dev when the project's hamr.toml
// declares a version newer than the CLI. Using an older CLI against a newer
// scaffold risks missing template/runtime features the scaffold depends on.
// Dev CLI builds, missing/old [hamr] sections, and unparseable versions all
// fall through so local hacking on hamr itself is not punished.
func ensureCLINotBehindScaffold(configPath string, skip bool) error {
	if skip || !releaseBuild {
		return nil
	}

	meta, err := scaffold.LoadMetadata(configPath)
	if err != nil || !meta.HasHamrSection() {
		return nil
	}

	cliVer, err := scaffold.ParseVersion(version)
	if err != nil {
		return nil
	}
	projVer, err := scaffold.ParseVersion(meta.Hamr.Version)
	if err != nil {
		return nil
	}

	if cliVer.Less(projVer) {
		return fmt.Errorf("scaffold was generated with hamr v%s but this CLI is v%s — please update hamr with `go install github.com/FyrmForge/hamr/cmd/hamr@latest`, or pass --skip-version-check to bypass", projVer, cliVer)
	}
	return nil
}

// computeVersionStatus inspects the CLI and project versions and returns:
//   - status: the indicator the bar/TUI should show
//   - msg:    the persistent indicator text (e.g. "CLI is ahead (cli v0.1 proj v0.2)")
//   - warning: a one-time human-readable line for log surfaces; "" when
//     there's nothing to say
//
// Pure compute so both the legacy CLI bar and the TUI can share the
// decision logic without one of them silently drifting.
func computeVersionStatus(configPath string) (status devserver.VersionStatus, msg, warning string) {
	if !releaseBuild {
		return devserver.VersionDev, "", ""
	}

	meta, err := scaffold.LoadMetadata(configPath)
	if err != nil || !meta.HasHamrSection() {
		return devserver.VersionOK, "", ""
	}

	cliVer, err := scaffold.ParseVersion(version)
	if err != nil {
		return devserver.VersionOK, "", ""
	}

	projVer, err := scaffold.ParseVersion(meta.Hamr.Version)
	if err != nil {
		return devserver.VersionOK, "", ""
	}

	if cliVer != projVer {
		var direction string
		switch {
		case cliVer.Less(projVer):
			direction = "CLI is behind scaffold"
		case projVer.Less(cliVer):
			direction = "CLI is ahead of scaffold"
		}
		return devserver.VersionMismatch,
			fmt.Sprintf("%s (cli v%s proj v%s)", direction, cliVer, projVer),
			fmt.Sprintf("%s: cli v%s, project v%s", direction, cliVer, projVer)
	}

	return devserver.VersionOK, "", ""
}

// checkVersionStatus compares the CLI version against the project's [hamr].version
// and updates the legacy CLI status bar indicator accordingly. The TUI
// path uses computeVersionStatus directly and routes the warning into
// the viewport via rt.Log.
func checkVersionStatus(sb *devserver.StatusBar, configPath string) {
	status, msg, warning := computeVersionStatus(configPath)
	if warning != "" {
		fmt.Printf("%s %s\r\n", devserver.HamrDevTag(), warning)
	}
	sb.SetVersionStatus(status, msg)
}

// composeNames returns the compose entry names in config order — the
// TUI uses this to label each docker tab in `Tab` cycle order.
func composeNames(entries []devserver.DockerCompose) []string {
	out := make([]string, 0, len(entries))
	for i := range entries {
		out = append(out, entries[i].Name)
	}
	return out
}
