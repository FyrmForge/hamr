// Package storage provides a pluggable file storage abstraction with local
// filesystem and S3-compatible backends (AWS S3, MinIO, Cloudflare R2).
package storage

import (
	"context"
	"io"
	"time"
)

// FileStorage defines the operations every storage backend must support.
type FileStorage interface {
	// Save writes the contents of r to the given path, creating intermediate
	// directories as needed. If a file already exists at path it is overwritten.
	Save(ctx context.Context, path string, r io.Reader) error

	// Open returns a ReadCloser for the file at path. The caller is
	// responsible for closing the returned reader.
	Open(ctx context.Context, path string) (io.ReadCloser, error)

	// Delete removes the file at path. It is idempotent — deleting a
	// non-existent file returns nil.
	Delete(ctx context.Context, path string) error

	// Exists reports whether a file exists at path.
	Exists(ctx context.Context, path string) (bool, error)
}

// SignableStorage extends FileStorage with the ability to generate
// pre-signed URLs for direct client downloads.
type SignableStorage interface {
	FileStorage

	// SignURL returns a pre-signed GET URL for the file at path that
	// expires after the given duration.
	SignURL(ctx context.Context, path string, expiry time.Duration) (string, error)
}

// S3Config holds the parameters needed to connect to an S3-compatible service.
type S3Config struct {
	Endpoint       string // e.g. "http://localhost:9000" for MinIO
	Bucket         string
	Region         string
	AccessKeyID    string
	SecretAccessKey string
	UsePathStyle   bool // true for MinIO / path-style addressing
}
