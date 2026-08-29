package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	devCmd.Flags().Bool("headless", false, "no TUI: plain log lines on stdout (automatic when stdout is not a terminal)")
}

// devUI is what runDevLoop needs from its front end. *tui.Runtime is the
// interactive implementation; headlessUI is the plain-stdout one used when
// an agent or CI runs `hamr dev` in the background.
type devUI interface {
	Log(line string)
	SetVersion(label string)
	SetVersionStatus(status devserver.VersionStatus, msg string)
	SetVersionUpdateIfOK(msg string)
	RegisterDockerStacks(names []string) map[string]io.Writer
	Wire(opts []devserver.Option) []devserver.Option
	HotkeyActions() <-chan devserver.HotkeyAction
}

// headlessUI writes everything — hamr's own lines, rule/daemon output,
// docker logs — to stdout so `hamr dev --headless > dev.log &` captures a
// single stream. No hotkeys: Ctrl+C / SIGTERM via the signal context is the
// only way out, and the status-bar setters degrade to log lines.
type headlessUI struct{}

func (headlessUI) Log(line string) { fmt.Fprintln(os.Stdout, line) } //nolint:errcheck
func (headlessUI) SetVersion(label string) {
	headlessUI{}.Log(fmt.Sprintf("%s hamr %s (headless)", devserver.HamrDevTag(), label))
}
func (headlessUI) SetVersionStatus(_ devserver.VersionStatus, msg string) {
	if msg != "" {
		headlessUI{}.Log(fmt.Sprintf("%s %s", devserver.HamrDevTag(), msg))
	}
}
func (headlessUI) SetVersionUpdateIfOK(string) {} // already logged by the caller

// RegisterDockerStacks maps every compose entry to stdout. compose's own
// `service-1 |` prefix identifies lines.
// ponytail: the follower forces --ansi=always, so colour codes land in the
// log file; plumb --ansi=never through WithDockerLogSinks if that bites.
func (headlessUI) RegisterDockerStacks(names []string) map[string]io.Writer {
	out := make(map[string]io.Writer, len(names))
	for _, n := range names {
		out[n] = os.Stdout
	}
	return out
}

func (headlessUI) Wire(opts []devserver.Option) []devserver.Option {
	return append(opts,
		devserver.WithLogWriter(os.Stdout),
		devserver.WithProcessOutput(os.Stdout, os.Stdout),
	)
}

// HotkeyActions returns nil; WaitForConfigChangeOrQuit documents a nil
// channel as safe (blocks until ctx cancels or the config changes).
func (headlessUI) HotkeyActions() <-chan devserver.HotkeyAction { return nil }

func runDev(cmd *cobra.Command, _ []string) error {
	configPath, _ := cmd.Flags().GetString("config")
	noProxy, _ := cmd.Flags().GetBool("no-proxy")
	verbose, _ := cmd.Flags().GetBool("verbose")
	skipVersionCheck, _ := cmd.Flags().GetBool("skip-version-check")
	headless, _ := cmd.Flags().GetBool("headless")

	if err := ensureCLINotBehindScaffold(configPath, skipVersionCheck); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// No terminal on stdout (piped into a file by an agent or CI) means the
	// TUI can't render anyway — fall back to headless without the flag.
	if headless || !term.IsTerminal(int(os.Stdout.Fd())) {
		return runDevLoop(ctx, headlessUI{}, configPath, noProxy, verbose)
	}

	rt := tui.NewRuntime()

	runnerErrCh := make(chan error, 1)
	go func() {
		runnerErrCh <- runDevLoop(ctx, rt, configPath, noProxy, verbose)
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

// runDevLoop is the runner-side counterpart to the bubbletea program. It
// owns the config-reload retry behavior and writes status lines into the
// TUI viewport via rt.Log.
func runDevLoop(ctx context.Context, rt devUI, configPath string, noProxy, verbose bool) error {
	// Fire the version check once: the result lives for the whole TUI
	// session (a config reload doesn't change the CLI binary's version).
	var versionChecked bool

	for {
		cfg, err := devserver.LoadConfig(configPath)
		if err != nil {
			rt.Log(fmt.Sprintf("%s config error: %v", devserver.HamrDevTag(), err))
			rt.Log(fmt.Sprintf("%s waiting for config fix... (q to quit)", devserver.HamrDevTag()))
			waitErr := devserver.WaitForConfigChangeOrQuit(ctx, configPath, rt.HotkeyActions())
			if waitErr != nil {
				if errors.Is(waitErr, context.Canceled) {
					return nil
				}
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

			// Background latest-release check. The callback fires from a
			// goroutine, so it has to be goroutine-safe — Runtime's
			// SetVersionUpdateIfOK is.
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
//   - status: the indicator the TUI should show
//   - msg:    the persistent indicator text (e.g. "CLI is ahead (cli v0.1 proj v0.2)")
//   - warning: a one-time human-readable line for the log surface; "" when
//     there's nothing to say
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

// composeNames returns the compose entry names in config order — the
// TUI uses this to label each docker tab in `Tab` cycle order.
func composeNames(entries []devserver.DockerCompose) []string {
	out := make([]string, 0, len(entries))
	for i := range entries {
		out = append(out, entries[i].Name)
	}
	return out
}
