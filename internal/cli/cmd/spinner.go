package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"golang.org/x/term"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// runWithSpinner runs cmd while displaying a braille spinner on stderr.
// On success it prints a checkmark; on failure an X. Falls back to plain
// output when stderr is not a terminal (e.g. CI).
func runWithSpinner(label string, cmd *exec.Cmd) error {
	isTTY := term.IsTerminal(int(os.Stderr.Fd()))

	if !isTTY {
		fmt.Fprintf(os.Stderr, "%s ... ", label)
		if err := cmd.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "✗")
			return err
		}
		fmt.Fprintln(os.Stderr, "✓")
		return nil
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Run() }()

	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()

	i := 0
	fmt.Fprintf(os.Stderr, "  %s %s", spinnerFrames[i], label)

	for {
		select {
		case err := <-done:
			if err != nil {
				fmt.Fprintf(os.Stderr, "\r  \033[31m✗\033[0m %s\n", label)
				return err
			}
			fmt.Fprintf(os.Stderr, "\r  \033[32m✓\033[0m %s\n", label)
			return nil
		case <-ticker.C:
			i = (i + 1) % len(spinnerFrames)
			fmt.Fprintf(os.Stderr, "\r  %s %s", spinnerFrames[i], label)
		}
	}
}
