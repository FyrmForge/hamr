package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/FyrmForge/hamr/pkg/storage"
	ssync "github.com/FyrmForge/hamr/pkg/sync"
	"github.com/spf13/cobra"
)

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Sync a local directory to an S3-compatible bucket",
	Long: `Upload files from a local directory to an S3-compatible bucket.

By default performs a one-shot sync of the static/ directory, then exits.
Use --watch to keep running and sync changes as they happen.

S3 credentials come from flags or environment variables:
  S3_ENDPOINT, S3_BUCKET, S3_REGION, S3_ACCESS_KEY, S3_SECRET_KEY

Examples:
  hamr sync                              One-shot sync of static/ to S3
  hamr sync --watch                      Watch for changes and sync continuously
  hamr sync --dir dist --bucket my-cdn   Sync dist/ to a specific bucket`,
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, _ := cmd.Flags().GetString("dir")
		watch, _ := cmd.Flags().GetBool("watch")
		pathStyle, _ := cmd.Flags().GetBool("path-style")

		// Flags take priority; fall back to env vars (which may come from .env).
		endpoint := flagOrEnv(cmd, "endpoint", "S3_ENDPOINT", "http://localhost:9000")
		bucket := flagOrEnv(cmd, "bucket", "S3_BUCKET", "")
		region := flagOrEnv(cmd, "region", "S3_REGION", "us-east-1")
		accessKey := flagOrEnv(cmd, "access-key", "S3_ACCESS_KEY", "")
		secretKey := flagOrEnv(cmd, "secret-key", "S3_SECRET_KEY", "")

		if bucket == "" {
			return fmt.Errorf("bucket is required (use --bucket or set S3_BUCKET)")
		}

		info, err := os.Stat(dir)
		if err != nil {
			return fmt.Errorf("directory %q: %w", dir, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("%q is not a directory", dir)
		}

		s3, err := storage.NewS3Storage(storage.S3Config{
			Endpoint:       endpoint,
			Bucket:         bucket,
			Region:         region,
			AccessKeyID:    accessKey,
			SecretAccessKey: secretKey,
			UsePathStyle:   pathStyle,
		})
		if err != nil {
			return fmt.Errorf("init S3 storage: %w", err)
		}

		ctx := context.Background()

		fmt.Printf("syncing %s/ → s3://%s ...\n", dir, bucket)
		if err := ssync.SyncAll(ctx, s3, dir); err != nil {
			return fmt.Errorf("sync failed: %w", err)
		}
		fmt.Println("sync complete")

		if !watch {
			return nil
		}

		return ssync.WatchAndSync(ctx, s3, dir)
	},
}

func init() {
	syncCmd.Flags().String("dir", "static", "local directory to sync")
	syncCmd.Flags().Bool("watch", false, "watch for changes after initial sync")
	syncCmd.Flags().String("endpoint", "", "S3 endpoint URL (default http://localhost:9000)")
	syncCmd.Flags().String("bucket", "", "S3 bucket name")
	syncCmd.Flags().String("region", "", "S3 region (default us-east-1)")
	syncCmd.Flags().String("access-key", "", "S3 access key")
	syncCmd.Flags().String("secret-key", "", "S3 secret key")
	syncCmd.Flags().Bool("path-style", true, "use path-style addressing (required for RustFS / path-style backends)")
}

// flagOrEnv returns the flag value if explicitly set, otherwise the env var,
// otherwise the fallback default.
func flagOrEnv(cmd *cobra.Command, flag, envKey, def string) string {
	if cmd.Flags().Changed(flag) {
		v, _ := cmd.Flags().GetString(flag)
		return v
	}
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	return def
}
