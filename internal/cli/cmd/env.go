package cmd

import (
	"fmt"
	"io"
	"strings"

	"github.com/FyrmForge/hamr/internal/devserver"
	"github.com/spf13/cobra"
)

var envCmd = &cobra.Command{
	Use:   "env",
	Short: "Print env-var rewrites derived from the running dev server's port walks",
	Long: `Reads .hamr/walks.json (written by hamr dev when ports walk +1 on busy)
and .env, applies the same rewrite rules hamr dev uses internally, and prints
the resulting KEY=VALUE pairs to stdout.

Use this in Makefiles, shell scripts, or anything else that runs outside hamr
dev but needs the walked port values:

    SHELL := bash
    .SHELLFLAGS := -ec
    ENV_LOAD := eval "$$(hamr env --export)";

    migrate:
            $(ENV_LOAD) go run ./cmd/migrate

When no walks are recorded (port_walk disabled, or every port was free), the
command exits 0 with no output — sourcing it is a no-op and the consumer
falls through to .env unchanged.

Match rules (must contain a known walked port):
  - (localhost|127.0.0.1|0.0.0.0|[::1]):<port>  → port swapped, host kept
  - whole-value :<port>                         → port swapped (Go listener form)

Values without an explicit port (e.g. postgres://user@localhost/db relying
on the scheme default) are NOT rewritten — include the port in .env if you
want auto-walking.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		export, _ := cmd.Flags().GetBool("export")
		dir, _ := cmd.Flags().GetString("dir")
		return runEnv(dir, export, cmd.OutOrStdout())
	},
}

func init() {
	envCmd.Flags().Bool("export", false, "emit `export KEY=VALUE` for shell sourcing (e.g. eval \"$(hamr env --export)\")")
	envCmd.Flags().String("dir", ".", "project root to resolve .hamr/walks.json and .env from (lets shell scripts call `hamr env --dir \"$ROOT\"` without subshell cd gymnastics)")
}

// runEnv is the Cobra-free body of `hamr env`. Lives here so tests can drive
// it directly with a working directory and writer, sidestepping Cobra's
// global os.Args parsing — which made shared-state tests on the package-level
// envCmd brittle.
func runEnv(dir string, export bool, out io.Writer) error {
	rewrites, err := devserver.ResolveEnvRewrites(dir)
	if err != nil {
		return err
	}
	for _, kv := range rewrites {
		if export {
			k, v, _ := strings.Cut(kv, "=")
			if _, err := fmt.Fprintf(out, "export %s=%s\n", k, shellSingleQuote(v)); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintln(out, kv); err != nil {
			return err
		}
	}
	return nil
}

// shellSingleQuote wraps v in single quotes for safe shell sourcing,
// escaping any embedded single quotes via the standard '\” dance.
// Single-quoted values aren't subject to $-expansion or backtick
// substitution, so they preserve URLs, JSON, etc. byte-for-byte.
func shellSingleQuote(v string) string {
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}
