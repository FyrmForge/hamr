package cmd

import (
	"fmt"

	"github.com/FyrmForge/hamr/internal/cli/generator"
	"github.com/spf13/cobra"
)

var storageFlag string

var addServiceCmd = &cobra.Command{
	Use:   "service <name>",
	Short: "Scaffold a new service into the project",
	Long: `Scaffold a new service into an existing HAMR project.

Creates:
  cmd/<name>/main.go          - Config, server start, graceful shutdown
  cmd/<name>/Dockerfile        - Multi-stage Docker build
  internal/<name>/config/      - Environment-based configuration
  internal/<name>/handler/     - Health check endpoint

Updates:
  docker/docker-compose.yaml   - Adds service definition
  Makefile                     - Adds run-<name> and build-<name> targets`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		switch storageFlag {
		case "", "local", "s3":
		default:
			return fmt.Errorf("invalid --storage value %q: must be \"local\" or \"s3\"", storageFlag)
		}

		cfg, err := generator.NewServiceConfigFromProject(name)
		if err != nil {
			return fmt.Errorf("read project config: %w", err)
		}
		cfg.Storage = storageFlag

		if err := generator.GenerateService(cfg); err != nil {
			return fmt.Errorf("generate service: %w", err)
		}

		fmt.Printf("Service %q scaffolded successfully.\n\n", name)
		fmt.Printf("  cmd/%s/main.go\n", name)
		fmt.Printf("  cmd/%s/Dockerfile\n", name)
		fmt.Printf("  internal/%s/config/config.go\n", name)
		fmt.Printf("  internal/%s/handler/health.go\n", name)
		fmt.Println()
		fmt.Printf("Run it:   go run ./cmd/%s\n", name)
		fmt.Printf("Build it: make build-%s\n", name)

		return nil
	},
}

func init() {
	addServiceCmd.Flags().StringVar(&storageFlag, "storage", "", "file storage backend: \"local\" or \"s3\" (adds MinIO to docker-compose)")
}
