package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/FyrmForge/hamr/internal/devserver"
	"github.com/spf13/cobra"
)

var composeCmd = &cobra.Command{
	Use:   "compose [docker compose args...]",
	Short: "Run docker compose with the config hamr dev is actually using",
	Long: `Passthrough to ` + "`docker compose`" + ` with this project's compose file — and the
generated port-walk override when one exists.

When a host port is busy, hamr dev walks it and records the result in
.hamr/compose.<name>.override.yaml. A plain ` + "`docker compose`" + ` call merges only the
base file, so it sees the running stack as drifted and recreates it on the
original ports — which fails outright when those ports are busy, which is why
they were walked. Going through hamr compose merges the same files hamr does.

Arguments after the first are passed to docker compose untouched; stdin, stdout,
stderr and the exit code pass straight through.

Examples:
  hamr compose up -d
  hamr compose down -v
  hamr compose exec -T postgres psql -U postgres
  hamr compose --name deps logs -f`,
	Args:                  cobra.ArbitraryArgs,
	RunE:                  runCompose,
	SilenceUsage:          true,
	DisableFlagsInUseLine: true,
}

func init() {
	composeCmd.Flags().String("name", "", "which [[dev.docker_compose]] entry to use (required when there is more than one)")
	// Stop cobra from eating docker's flags: everything after the first
	// positional argument (`up`, `down`, …) belongs to docker compose.
	composeCmd.Flags().SetInterspersed(false)
	rootCmd.AddCommand(composeCmd)
}

func runCompose(cmd *cobra.Command, args []string) error {
	name, _ := cmd.Flags().GetString("name")

	root, err := resolveProjectRoot("")
	if err != nil {
		return err
	}

	cfg, err := devserver.LoadConfig(filepath.Join(root, "hamr.toml"))
	if err != nil {
		return err
	}

	entry, err := pickComposeEntry(cfg.Dev.DockerCompose, name)
	if err != nil {
		return err
	}

	// composeArgs resolves the compose file and override by project-relative
	// path, so docker has to run from the project root regardless of where the
	// user invoked this.
	dockerArgs := append(devserver.ComposeArgs(entry), args...)
	docker := exec.Command("docker", dockerArgs...)
	docker.Dir = root
	docker.Stdin = os.Stdin
	docker.Stdout = os.Stdout
	docker.Stderr = os.Stderr

	if err := docker.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// Propagate docker's exit code verbatim: callers (Makefiles, CI,
			// scripts) branch on it, and cobra's own error path would flatten
			// every failure to 1 plus a redundant "Error:" line.
			os.Exit(exitErr.ExitCode())
		}
		return fmt.Errorf("run docker compose: %w", err)
	}
	return nil
}

// pickComposeEntry resolves which [[dev.docker_compose]] entry to act on.
// A single entry is used implicitly; more than one requires --name, because
// guessing would silently act on the wrong stack.
func pickComposeEntry(entries []devserver.DockerCompose, name string) (*devserver.DockerCompose, error) {
	if len(entries) == 0 {
		return nil, fmt.Errorf("no [[dev.docker_compose]] entries in hamr.toml")
	}

	if name == "" {
		if len(entries) > 1 {
			return nil, fmt.Errorf("hamr.toml has %d docker_compose entries (%s) — pick one with --name",
				len(entries), strings.Join(composeEntryNames(entries), ", "))
		}
		return &entries[0], nil
	}

	for i := range entries {
		if entries[i].Name == name {
			return &entries[i], nil
		}
	}
	return nil, fmt.Errorf("no docker_compose entry named %q (have: %s)",
		name, strings.Join(composeEntryNames(entries), ", "))
}

func composeEntryNames(entries []devserver.DockerCompose) []string {
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name)
	}
	return names
}
