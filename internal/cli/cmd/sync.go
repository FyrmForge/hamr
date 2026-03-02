package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/FyrmForge/hamr/pkg/config"
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
		endpoint, _ := cmd.Flags().GetString("endpoint")
		bucket, _ := cmd.Flags().GetString("bucket")
		region, _ := cmd.Flags().GetString("region")
		accessKey, _ := cmd.Flags().GetString("access-key")
		secretKey, _ := cmd.Flags().GetString("secret-key")
		pathStyle, _ := cmd.Flags().GetBool("path-style")

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
	syncCmd.Flags().String("endpoint", config.GetEnvOrDefault("S3_ENDPOINT", "http://localhost:9000"), "S3 endpoint URL")
	syncCmd.Flags().String("bucket", config.GetEnvOrDefault("S3_BUCKET", ""), "S3 bucket name")
	syncCmd.Flags().String("region", config.GetEnvOrDefault("S3_REGION", "us-east-1"), "S3 region")
	syncCmd.Flags().String("access-key", config.GetEnvOrDefault("S3_ACCESS_KEY", ""), "S3 access key")
	syncCmd.Flags().String("secret-key", config.GetEnvOrDefault("S3_SECRET_KEY", ""), "S3 secret key")
	syncCmd.Flags().Bool("path-style", true, "use path-style addressing (required for MinIO)")
}
