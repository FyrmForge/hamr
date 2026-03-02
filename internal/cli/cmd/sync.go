package cmd

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/FyrmForge/hamr/pkg/config"
	"github.com/FyrmForge/hamr/pkg/storage"
	"github.com/fsnotify/fsnotify"
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
		if err := syncAll(ctx, s3, dir); err != nil {
			return fmt.Errorf("sync failed: %w", err)
		}
		fmt.Println("sync complete")

		if !watch {
			return nil
		}

		return watchAndSync(ctx, s3, dir)
	},
}

func syncAll(ctx context.Context, s3 storage.FileStorage, dir string) error {
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		key := s3Key(dir, path)
		if key == "" {
			return nil
		}
		if err := uploadFile(ctx, s3, path, key); err != nil {
			return fmt.Errorf("upload %s: %w", key, err)
		}
		fmt.Printf("  %s\n", key)
		return nil
	})
}

func watchAndSync(ctx context.Context, s3 storage.FileStorage, dir string) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create watcher: %w", err)
	}
	defer watcher.Close()

	if err := addWatchDirs(watcher, dir); err != nil {
		return fmt.Errorf("watch directories: %w", err)
	}

	fmt.Printf("watching %s/ for changes... (ctrl+c to stop)\n", dir)

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			key := s3Key(dir, event.Name)
			if key == "" {
				continue
			}

			switch {
			case event.Has(fsnotify.Create), event.Has(fsnotify.Write):
				info, err := os.Stat(event.Name)
				if err != nil {
					continue
				}
				if info.IsDir() {
					_ = addWatchDirs(watcher, event.Name)
					continue
				}
				if err := uploadFile(ctx, s3, event.Name, key); err != nil {
					fmt.Printf("upload %s: %v\n", key, err)
				} else {
					fmt.Printf("uploaded %s\n", key)
				}

			case event.Has(fsnotify.Remove), event.Has(fsnotify.Rename):
				if err := s3.Delete(ctx, key); err != nil {
					fmt.Printf("delete %s: %v\n", key, err)
				} else {
					fmt.Printf("deleted %s\n", key)
				}
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			fmt.Printf("watcher error: %v\n", err)
		}
	}
}

func uploadFile(ctx context.Context, s3 storage.FileStorage, path, key string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return s3.Save(ctx, key, f)
}

func s3Key(dir, path string) string {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return ""
	}
	return strings.ReplaceAll(rel, string(filepath.Separator), "/")
}

func addWatchDirs(w *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return w.Add(path)
		}
		return nil
	})
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
