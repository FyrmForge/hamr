package media

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/FyrmForge/hamr/pkg/storage"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// ImageStore handles image upload, processing, serving, and deletion.
type ImageStore struct {
	storage     storage.FileStorage
	signable    storage.SignableStorage // non-nil only for S3
	config      ImageStoreConfig
	urlPrefix   string // local URL prefix (e.g. "/uploads")
	logger      *slog.Logger
	isLocal bool
}

// NewLocalImageStore creates an ImageStore backed by local filesystem storage.
// urlPrefix is the URL path prefix used to serve files (e.g. "/uploads").
func NewLocalImageStore(store *storage.LocalStorage, urlPrefix string, cfg ImageStoreConfig, opts ...Option) (*ImageStore, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}

	return &ImageStore{
		storage:   store,
		config:    cfg,
		urlPrefix: strings.TrimRight(urlPrefix, "/"),
		logger:    o.logger,
		isLocal:   true,
	}, nil
}

// NewS3ImageStore creates an ImageStore backed by S3-compatible storage.
// If cfg.BaseURL is set, public URLs use it as the base. If cfg.SignedExpiry is
// set, GetMediaCtx returns pre-signed URLs.
func NewS3ImageStore(store *storage.S3Storage, cfg ImageStoreConfig, opts ...Option) (*ImageStore, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}

	return &ImageStore{
		storage:  store,
		signable: store,
		config:   cfg,
		logger:   o.logger,
	}, nil
}

// Upload processes and stores an image from a multipart file header.
func (s *ImageStore) Upload(ctx context.Context, fh *multipart.FileHeader) (*ImageUploadResult, error) {
	if fh.Size > s.config.MaxSize {
		return nil, ErrFileTooLarge
	}

	f, err := fh.Open()
	if err != nil {
		return nil, fmt.Errorf("media: open upload: %w", err)
	}
	defer func() { _ = f.Close() }()

	return s.upload(ctx, f, fh.Size)
}

// UploadFromReader processes and stores an image from an io.Reader.
func (s *ImageStore) UploadFromReader(ctx context.Context, r io.Reader, size int64) (*ImageUploadResult, error) {
	if size > s.config.MaxSize {
		return nil, ErrFileTooLarge
	}
	return s.upload(ctx, r, size)
}

func (s *ImageStore) upload(ctx context.Context, r io.Reader, size int64) (*ImageUploadResult, error) {
	mimeType, raw, err := detectMIME(r)
	if err != nil {
		return nil, err
	}

	if !imageTypes[mimeType] {
		return nil, ErrUnknownType
	}

	if int64(len(raw)) > s.config.MaxSize {
		return nil, ErrFileTooLarge
	}

	id := uuid.New().String()

	processed, err := processImage(ctx, raw, s.config.Sizes, s.config.Format, s.config.Quality)
	if err != nil {
		return nil, err
	}

	ext := formatExt(s.config.Format)
	for _, p := range processed {
		path := fmt.Sprintf("%s/%s/%s.%s", s.config.Category, id, p.size.Name, ext)
		if err := s.storage.Save(ctx, path, bytes.NewReader(p.data)); err != nil {
			return nil, fmt.Errorf("media: save %q: %w", path, err)
		}
	}

	s.logger.Debug("image uploaded",
		"id", id,
		"category", s.config.Category,
		"mime", mimeType,
		"sizes", len(processed),
	)

	return &ImageUploadResult{
		ID:        id,
		MediaType: TypeImage,
		MimeType:  mimeType,
		sizes:     s.config.Sizes,
		category:  s.config.Category,
		format:    ext,
	}, nil
}

// Delete removes all size variants for the given media ID.
func (s *ImageStore) Delete(ctx context.Context, id string) error {
	ext := formatExt(s.config.Format)
	for _, sz := range s.config.Sizes {
		path := fmt.Sprintf("%s/%s/%s.%s", s.config.Category, id, sz.Name, ext)
		if err := s.storage.Delete(ctx, path); err != nil {
			return fmt.Errorf("media: delete %q: %w", path, err)
		}
	}
	s.logger.Debug("image deleted", "id", id, "category", s.config.Category)
	return nil
}

// GetMedia returns an ImageRef for constructing public URLs. For local stores
// it uses the URL prefix; for S3 stores with BaseURL it uses that.
func (s *ImageStore) GetMedia(id string) ImageRef {
	base := s.urlPrefix
	if !s.isLocal && s.config.BaseURL != "" {
		base = strings.TrimRight(s.config.BaseURL, "/")
	}
	return ImageRef{
		id:       id,
		category: s.config.Category,
		format:   formatExt(s.config.Format),
		sizes:    s.config.Sizes,
		baseURL:  base,
	}
}

// GetMediaCtx returns an ImageRef that may use signed URLs for S3 stores with
// SignedExpiry configured. For local stores it behaves identically to GetMedia.
func (s *ImageStore) GetMediaCtx(ctx context.Context, id string) ImageRef {
	if s.signable != nil && s.config.SignedExpiry > 0 {
		// For signed URLs, we build a ref that uses the signed base.
		// Since each size needs its own signature, we pre-sign the "first" size
		// and use a special signedRef approach. For simplicity, return a ref
		// whose baseURL is empty and Size() calls produce signed URLs.
		return ImageRef{
			id:       id,
			category: s.config.Category,
			format:   formatExt(s.config.Format),
			sizes:    s.config.Sizes,
			baseURL:  "", // signals that paths must be signed
			signFn: func(path string) string {
				url, err := s.signable.SignURL(ctx, path, s.config.SignedExpiry)
				if err != nil {
					s.logger.Error("failed to sign URL", "path", path, "error", err)
					return ""
				}
				return url
			},
		}
	}
	return s.GetMedia(id)
}

// SignedURL generates a pre-signed URL for a specific storage path with a
// custom expiry. Only works with S3-backed stores.
func (s *ImageStore) SignedURL(ctx context.Context, path string, expiry time.Duration) (string, error) {
	if s.signable == nil {
		return "", fmt.Errorf("media: signed URLs not available for local storage")
	}
	return s.signable.SignURL(ctx, path, expiry)
}

// ServeHandler returns an Echo handler that serves image files from the store.
// For local stores it serves from the filesystem. For S3 stores it proxies
// through the storage layer.
func (s *ImageStore) ServeHandler() echo.HandlerFunc {
	if s.isLocal {
		return s.serveLocal()
	}
	return s.serveS3()
}

func (s *ImageStore) serveLocal() echo.HandlerFunc {
	return func(c echo.Context) error {
		// Extract the path after the category prefix.
		reqPath := c.Request().URL.Path
		// Strip the URL prefix to get the storage-relative path.
		storagePath := strings.TrimPrefix(reqPath, s.urlPrefix+"/")
		if storagePath == reqPath {
			return echo.NewHTTPError(http.StatusNotFound)
		}

		rc, err := s.storage.Open(c.Request().Context(), storagePath)
		if err != nil {
			if os.IsNotExist(err) || strings.Contains(err.Error(), "not exist") {
				return echo.NewHTTPError(http.StatusNotFound)
			}
			return echo.NewHTTPError(http.StatusInternalServerError)
		}
		defer func() { _ = rc.Close() }()

		// Detect content type from extension.
		ext := filepath.Ext(storagePath)
		ct := "application/octet-stream"
		switch ext {
		case ".webp":
			ct = "image/webp"
		case ".jpeg", ".jpg":
			ct = "image/jpeg"
		case ".png":
			ct = "image/png"
		}

		c.Response().Header().Set("Content-Type", ct)
		c.Response().Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		c.Response().WriteHeader(http.StatusOK)
		_, err = io.Copy(c.Response(), rc)
		return err
	}
}

func (s *ImageStore) serveS3() echo.HandlerFunc {
	return func(c echo.Context) error {
		reqPath := c.Request().URL.Path
		// For S3, strip any prefix to get the storage key.
		storagePath := strings.TrimPrefix(reqPath, "/")
		// Try to strip common prefixes.
		for _, prefix := range []string{s.config.Category + "/"} {
			if strings.HasPrefix(storagePath, prefix) {
				break
			}
		}

		rc, err := s.storage.Open(c.Request().Context(), storagePath)
		if err != nil {
			return echo.NewHTTPError(http.StatusNotFound)
		}
		defer func() { _ = rc.Close() }()

		ext := filepath.Ext(storagePath)
		ct := "application/octet-stream"
		switch ext {
		case ".webp":
			ct = "image/webp"
		case ".jpeg", ".jpg":
			ct = "image/jpeg"
		case ".png":
			ct = "image/png"
		}

		c.Response().Header().Set("Content-Type", ct)
		c.Response().Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		c.Response().WriteHeader(http.StatusOK)
		_, err = io.Copy(c.Response(), rc)
		return err
	}
}
