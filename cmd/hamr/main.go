package main

import (
	"os"

	"github.com/FyrmForge/hamr/internal/cli/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
