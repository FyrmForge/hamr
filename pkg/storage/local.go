package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Compile-time check.
var _ FileStorage = (*LocalStorage)(nil)

// LocalStorage implements FileStorage on the local filesystem.
type LocalStorage struct {
	basePath string
	logger   *slog.Logger
}

// LocalOption configures a LocalStorage instance.
type LocalOption func(*LocalStorage)

// WithLocalLogger sets the logger used by LocalStorage.
func WithLocalLogger(l *slog.Logger) LocalOption {
	return func(s *LocalStorage) { s.logger = l }
}

// NewLocalStorage creates a LocalStorage rooted at basePath, creating the
// directory if it does not exist.
func NewLocalStorage(basePath string, opts ...LocalOption) (*LocalStorage, error) {
	abs, err := filepath.Abs(basePath)
	if err != nil {
		return nil, fmt.Errorf("storage: resolve base path: %w", err)
	}

	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("storage: create base directory: %w", err)
	}

	s := &LocalStorage{
		basePath: abs,
		logger:   slog.Default(),
	}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

// resolve joins path to the base directory and ensures the result stays within
// it, preventing directory-traversal attacks.
func (s *LocalStorage) resolve(path string) (string, error) {
	cleaned := filepath.Clean(path)
	if filepath.IsAbs(cleaned) || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) || cleaned == ".." {
		return "", fmt.Errorf("storage: path %q escapes base directory", path)
	}
	joined := filepath.Join(s.basePath, cleaned)
	if !strings.HasPrefix(joined, s.basePath+string(filepath.Separator)) {
		return "", fmt.Errorf("storage: path %q escapes base directory", path)
	}
	return joined, nil
}

func (s *LocalStorage) Save(_ context.Context, path string, r io.Reader) error {
	full, err := s.resolve(path)
	if err != nil {
		return err
	}

	dir := filepath.Dir(full)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("storage: create subdirectory: %w", err)
	}

	// Write to a temp file then rename for atomicity — a failed or
	// interrupted write never leaves a partial file at the target path.
	tmp, err := os.CreateTemp(dir, ".hamr-upload-*")
	if err != nil {
		return fmt.Errorf("storage: create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := io.Copy(tmp, r); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("storage: write file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("storage: close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, full); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("storage: rename temp file: %w", err)
	}

	s.logger.Debug("file saved", "path", path)
	return nil
}

func (s *LocalStorage) Open(_ context.Context, path string) (io.ReadCloser, error) {
	full, err := s.resolve(path)
	if err != nil {
		return nil, err
	}

	f, err := os.Open(full)
	if err != nil {
		return nil, fmt.Errorf("storage: open file: %w", err)
	}
	return f, nil
}

func (s *LocalStorage) Delete(_ context.Context, path string) error {
	full, err := s.resolve(path)
	if err != nil {
		return err
	}

	if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("storage: delete file: %w", err)
	}

	s.logger.Debug("file deleted", "path", path)
	return nil
}

func (s *LocalStorage) Exists(_ context.Context, path string) (bool, error) {
	full, err := s.resolve(path)
	if err != nil {
		return false, err
	}

	_, err = os.Stat(full)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("storage: stat file: %w", err)
}
