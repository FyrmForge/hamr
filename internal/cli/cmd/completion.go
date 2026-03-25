package cmd

import (
	"bufio"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var completionCmd = &cobra.Command{
	Use:   "completion",
	Short: "Generate or install shell completion scripts",
	Long: `Generate or install shell completion scripts for bash, zsh, or fish.

To generate a completion script, run one of:

  hamr completion bash
  hamr completion zsh
  hamr completion fish

To install completions so they load automatically:

  hamr completion install          # per-user install (auto-detects shell)
  hamr completion install --system # system-wide install (requires root)
  hamr completion install --shell zsh`,
}

var completionBashCmd = &cobra.Command{
	Use:   "bash",
	Short: "Generate bash completion script",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return rootCmd.GenBashCompletionV2(os.Stdout, true)
	},
}

var completionZshCmd = &cobra.Command{
	Use:   "zsh",
	Short: "Generate zsh completion script",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return rootCmd.GenZshCompletion(os.Stdout)
	},
}

var completionFishCmd = &cobra.Command{
	Use:   "fish",
	Short: "Generate fish completion script",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return rootCmd.GenFishCompletion(os.Stdout, true)
	},
}

var completionInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install shell completion scripts",
	Long: `Install shell completion scripts for the current user or system-wide.

By default, detects your shell from $SHELL and installs per-user completions.
Use --system to install system-wide (requires root).
Use --shell to override shell detection.
Use -y/--yes to skip the confirmation prompt (useful for scripts/CI).

Supported shells: bash, zsh, fish`,
	Args: cobra.NoArgs,
	RunE: runCompletionInstall,
}

func init() {
	completionCmd.AddCommand(completionBashCmd)
	completionCmd.AddCommand(completionZshCmd)
	completionCmd.AddCommand(completionFishCmd)
	completionCmd.AddCommand(completionInstallCmd)

	completionInstallCmd.Flags().String("shell", "", "target shell (bash, zsh, fish); auto-detected from $SHELL if omitted")
	completionInstallCmd.Flags().Bool("system", false, "install system-wide (requires root)")
	completionInstallCmd.Flags().BoolP("yes", "y", false, "skip confirmation prompt")
}

// shellPaths holds the file path where the completion script will be written.
type shellPaths struct {
	shell string
	path  string
}

func runCompletionInstall(cmd *cobra.Command, _ []string) error {
	shell, _ := cmd.Flags().GetString("shell")
	system, _ := cmd.Flags().GetBool("system")
	yes, _ := cmd.Flags().GetBool("yes")

	if shell == "" {
		shell = detectShell()
	}
	if shell == "" {
		return fmt.Errorf("could not detect shell from $SHELL; use --shell to specify one (bash, zsh, fish)")
	}

	sp, err := completionPath(shell, system)
	if err != nil {
		return err
	}

	// System-wide install requires root.
	if system && os.Geteuid() != 0 {
		return fmt.Errorf("system-wide install requires root; re-run with sudo")
	}

	// Confirmation prompt.
	fmt.Println("The following changes will be made:")
	fmt.Println()
	fmt.Printf("  Write: %s\n", sp.path)
	fmt.Println()
	if !yes && !confirm("Proceed?") {
		fmt.Println("Aborted.")
		return nil
	}

	if err := writeCompletionFile(sp); err != nil {
		return err
	}

	fmt.Printf("\n✓ Completion script installed to %s\n", sp.path)

	// Print post-install instructions for shells that need them.
	printPostInstallInstructions(sp, system)

	return nil
}

// completionPath returns the target path for the given shell and install mode.
func completionPath(shell string, system bool) (shellPaths, error) {
	if system {
		return systemCompletionPath(shell)
	}
	return userCompletionPath(shell)
}

func systemCompletionPath(shell string) (shellPaths, error) {
	switch shell {
	case "bash":
		return shellPaths{shell: shell, path: "/usr/share/bash-completion/completions/hamr"}, nil
	case "zsh":
		return shellPaths{shell: shell, path: "/usr/share/zsh/site-functions/_hamr"}, nil
	case "fish":
		return shellPaths{shell: shell, path: "/usr/share/fish/vendor_completions.d/hamr.fish"}, nil
	default:
		return shellPaths{}, fmt.Errorf("unsupported shell: %s (supported: bash, zsh, fish)", shell)
	}
}

func userCompletionPath(shell string) (shellPaths, error) {
	home, err := homeDir()
	if err != nil {
		return shellPaths{}, fmt.Errorf("determine home directory: %w", err)
	}

	switch shell {
	case "bash":
		return shellPaths{
			shell: shell,
			path:  filepath.Join(home, ".local", "share", "bash-completion", "completions", "hamr"),
		}, nil
	case "zsh":
		return shellPaths{
			shell: shell,
			path:  filepath.Join(home, ".zsh", "completions", "_hamr"),
		}, nil
	case "fish":
		return shellPaths{
			shell: shell,
			path:  filepath.Join(home, ".config", "fish", "completions", "hamr.fish"),
		}, nil
	default:
		return shellPaths{}, fmt.Errorf("unsupported shell: %s (supported: bash, zsh, fish)", shell)
	}
}

// writeCompletionFile generates the completion script and writes it to disk.
func writeCompletionFile(sp shellPaths) error {
	dir := filepath.Dir(sp.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}

	f, err := os.Create(sp.path)
	if err != nil {
		return fmt.Errorf("create %s: %w", sp.path, err)
	}
	defer f.Close() //nolint:errcheck

	switch sp.shell {
	case "bash":
		err = rootCmd.GenBashCompletionV2(f, true)
	case "zsh":
		err = rootCmd.GenZshCompletion(f)
	case "fish":
		err = rootCmd.GenFishCompletion(f, true)
	}
	if err != nil {
		return fmt.Errorf("generate %s completion: %w", sp.shell, err)
	}

	return nil
}

// printPostInstallInstructions prints any manual steps the user needs to take.
func printPostInstallInstructions(sp shellPaths, system bool) {
	if system {
		// System-wide installs are auto-discovered by all three shells.
		return
	}

	switch sp.shell {
	case "zsh":
		fmt.Println()
		fmt.Println("Add the following to your ~/.zshrc if not already present:")
		fmt.Println()
		fmt.Printf("  fpath=(~/.zsh/completions $fpath)\n")
		fmt.Printf("  autoload -Uz compinit && compinit\n")
		fmt.Println()
		fmt.Println("Then restart your shell or run: source ~/.zshrc")
	case "bash", "fish":
		fmt.Println()
		fmt.Println("Restart your shell or open a new terminal to activate completions.")
	}
}

// detectShell returns the shell name (bash, zsh, fish) from $SHELL.
func detectShell() string {
	sh := os.Getenv("SHELL")
	if sh == "" {
		return ""
	}
	base := filepath.Base(sh)
	switch base {
	case "bash", "zsh", "fish":
		return base
	default:
		return ""
	}
}

// confirm prompts the user with a [y/N] question and returns true on "y"/"yes".
func confirm(prompt string) bool {
	fmt.Printf("%s [y/N] ", prompt)
	reader := bufio.NewReader(os.Stdin)
	answer, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	answer = strings.TrimSpace(strings.ToLower(answer))
	return answer == "y" || answer == "yes"
}

// homeDir returns the current user's home directory.
func homeDir() (string, error) {
	if h := os.Getenv("HOME"); h != "" {
		return h, nil
	}
	u, err := user.Current()
	if err != nil {
		return "", err
	}
	return u.HomeDir, nil
}
