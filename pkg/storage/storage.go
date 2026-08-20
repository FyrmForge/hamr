// Package storage provides a pluggable file storage abstraction with local
// filesystem and S3-compatible backends (AWS S3, RustFS, Cloudflare R2).
package storage

import (
	"context"
	"io"
	"mime"
	"strings"
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

	// List returns all file paths under the given prefix. Paths are relative
	// to the storage root. An empty prefix lists all files.
	List(ctx context.Context, prefix string) ([]string, error)
}

// SignableStorage extends FileStorage with the ability to generate
// pre-signed URLs for direct client downloads.
type SignableStorage interface {
	FileStorage

	// SignURL returns a pre-signed GET URL for the file at path that
	// expires after the given duration. Options such as WithAttachment
	// alter how the backend serves the response.
	SignURL(ctx context.Context, path string, expiry time.Duration, opts ...SignOption) (string, error)
}

// signConfig holds per-call SignURL settings, populated by SignOptions.
type signConfig struct {
	contentDisposition string
}

// SignOption configures a single SignURL call.
type SignOption func(*signConfig)

// WithAttachment signs the URL so the response carries a
// Content-Disposition: attachment header with the given filename, forcing
// browsers to download the file instead of displaying it. An empty filename
// still forces the download but lets the browser pick the name.
func WithAttachment(filename string) SignOption {
	return func(c *signConfig) {
		c.contentDisposition = contentDispositionAttachment(filename)
	}
}

// contentDispositionAttachment builds an RFC 6266 Content-Disposition header.
// mime.FormatMediaType handles the RFC 5987 ext-value encoding; when it emits
// the authoritative filename* form (non-ASCII or control chars in name) we
// prepend a sanitized plain-ASCII filename as a fallback for legacy parsers.
// Keep in sync with the copy in internal/devserver/mailmock.go (devserver
// cannot import pkg/storage without pulling the AWS SDK into the dev binary).
func contentDispositionAttachment(name string) string {
	name = strings.ToValidUTF8(name, "_")
	if name == "" {
		return "attachment"
	}
	cd := mime.FormatMediaType("attachment", map[string]string{"filename": name})
	if cd == "" {
		return "attachment"
	}
	if !strings.Contains(cd, "filename*") {
		return cd
	}
	ascii := strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7E || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	return mime.FormatMediaType("attachment", map[string]string{"filename": ascii}) + strings.TrimPrefix(cd, "attachment")
}

// S3Config holds the parameters needed to connect to an S3-compatible service.
type S3Config struct {
	Endpoint       string // e.g. "http://localhost:9000" for RustFS
	Bucket         string
	Region         string
	AccessKeyID    string
	SecretAccessKey string
	UsePathStyle   bool // true for RustFS / path-style addressing
}
