package cmd

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/FyrmForge/hamr/internal/devserver"
	"github.com/spf13/cobra"
)

var mockServeCmd = &cobra.Command{
	Use:   "mock-serve",
	Short: "Serve the selected dev mocks headlessly, configured via environment",
	Long: `Stands up the dev mocks on plain listeners — no proxy, TUI, build, or
watch — for running in a dedicated container in a dev environment. All
configuration comes from environment variables:

  HAMR_MOCKS                   comma-separated list to start (required), e.g. "mail,stripe"
  HAMR_MOCK_PORT               app-facing port: stripe /v1/* + mail ingest (default 4500)
  HAMR_MOCK_UI_PORT            dashboards port; unset → UI on HAMR_MOCK_PORT
  HAMR_MOCK_BIND               bind host; empty → all interfaces (set 127.0.0.1 on a shared host)

  HAMR_MAIL_MAX_MESSAGES       inbox cap (default 500)
  HAMR_MAIL_MAX_MESSAGE_BYTES  per-message byte cap (default 10MiB)
  HAMR_MAIL_PERSIST_PATH       mbox path; empty → in-memory only

  HAMR_STRIPE_BASE_URL         browser-reachable origin of the mock UI (required for stripe)
  HAMR_STRIPE_WEBHOOK_URL      app's webhook handler (required for stripe)
  HAMR_STRIPE_WEBHOOK_SECRET   matches the app's STRIPE_WEBHOOK_SECRET (required for stripe)
  HAMR_STRIPE_PERSIST_PATH     state JSON path; empty → in-memory only`,
	Args: cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return devserver.RunMockServe(ctx, nil)
	},
}

func init() {
	rootCmd.AddCommand(mockServeCmd)
}
