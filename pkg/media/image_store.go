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
	storage   storage.FileStorage
	signable  storage.SignableStorage // non-nil only for S3
	config    ImageStoreConfig
	urlPrefix string // local URL prefix (e.g. "/uploads")
	logger    *slog.Logger
	isLocal   bool
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

	return s.upload(ctx, f, "", false)
}

// UploadFromReader processes and stores an image from an io.Reader. The id
// is generated internally as a fresh UUID. size is the upper-bound byte
// count from the caller's perspective; the actual buffer is checked
// against MaxSize after MIME detection.
func (s *ImageStore) UploadFromReader(ctx context.Context, r io.Reader, size int64) (*ImageUploadResult, error) {
	if size > s.config.MaxSize {
		return nil, ErrFileTooLarge
	}
	return s.upload(ctx, r, "", false)
}

// UploadFromReaderWithID processes and stores an image, using the supplied
// id as the storage path component instead of generating one internally.
//
// The id MUST be in the 36-char canonical UUID form
// `xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx` — the value of `id` is
// interpolated into storage paths, so loose validation would open the
// door to traversal (`"../foo"`), key collisions, and silent overwrite
// of unrelated assets. Other forms accepted by uuid.Parse (hyphenless
// 32-char, urn:uuid:..., {...} braced) are deliberately rejected: they'd
// let the same logical UUID land at two different storage keys. The
// empty string is also rejected — the whole point of *WithID is "the
// caller supplies the id," so falling through to UUID generation would
// make the method name a lie. All bad inputs return ErrInvalidID.
//
// If storage already holds bytes under the id (e.g. a previous attempt
// partially wrote variants and didn't clean up), the call returns
// ErrIDExists unless overwrite=true. Workers retrying a failed job
// typically want overwrite=true so the retry replaces any residue.
// ErrIDExists is a best-effort precheck: concurrent *WithID calls with
// the same id and overwrite=false can both pass the existence check and
// race on Save (last-write-wins on S3, clobber on local FS). Callers
// that need hard exclusion should serialize at the source-of-truth
// layer — typically a unique constraint on the DB row that owns the id.
//
// On ErrFileTooLarge (size pre-check), ErrInvalidID, and ErrIDExists the
// reader r is NOT consumed — all three errors are detected before
// detectMIME runs, so the rejection path doesn't drain the payload.
// Callers relying on the upload to drain a network or pipe reader must
// discard or reuse r explicitly on these errors. (ErrFileTooLarge can
// also fire AFTER detectMIME, when the buffered byte count exceeds
// MaxSize; in that case r has been drained by the MIME sniff.)
//
// On any error after the first variant has been written, the method makes
// a best-effort attempt to delete the variants it has already saved
// before returning. Cleanup failures are logged via the configured slog
// logger and do not mask the original error.
func (s *ImageStore) UploadFromReaderWithID(ctx context.Context, id string, r io.Reader, size int64, overwrite bool) (*ImageUploadResult, error) {
	if size > s.config.MaxSize {
		return nil, ErrFileTooLarge
	}
	if err := validateCanonicalUUID(id); err != nil {
		return nil, err
	}
	return s.upload(ctx, r, id, overwrite)
}

// upload is the shared core for the three public upload methods. id is the
// caller-supplied canonical UUID for *WithID variants (validated at the
// entry point AND defensively re-validated below to fence the invariant
// against future entry points); pass "" to have one generated internally.
// overwrite is meaningful only when id is non-empty.
func (s *ImageStore) upload(ctx context.Context, r io.Reader, id string, overwrite bool) (*ImageUploadResult, error) {
	// captured before the UUID-fill below mutates id — `external` answers
	// "did the caller supply this id?" which the existence-check and
	// validation paths both branch on.
	external := id != ""

	// Defensive re-validation. The public *WithID entry validates and is
	// the only current caller passing a non-empty id; this re-check
	// fences the invariant inside the function so a future fourth entry
	// point that forgets to validate doesn't reintroduce a
	// path-traversal hole.
	if external {
		if err := validateCanonicalUUID(id); err != nil {
			return nil, err
		}
	}

	ext := formatExt(s.config.Format)

	// Existence precheck — runs BEFORE detectMIME to short-circuit the
	// retry-after-failure hot path: if the id is already taken, we don't
	// want to slurp the whole payload into memory just to return
	// ErrIDExists. Storage path is computable from config + id alone, so
	// no ordering dependency on the MIME sniff.
	//
	// Only meaningful when an external id was supplied. Internal IDs are
	// fresh UUIDs with astronomical non-collision odds, so the check
	// would just be a wasted round-trip. We probe the first variant's
	// path; if it's present we assume the prefix is populated rather
	// than scanning every variant.
	//
	// Probe path uses s.config.Sizes[0] which is correct only because
	// processImage (called below) returns variants in the same order as
	// s.config.Sizes — so the first variant the save loop writes is
	// always the one this probe checks. Pre-existing assumption; called
	// out here because the probe makes it load-bearing.
	if external && !overwrite {
		first := fmt.Sprintf("%s/%s/%s.%s", s.config.Category, id, s.config.Sizes[0].Name, ext)
		exists, err := s.storage.Exists(ctx, first)
		if err != nil {
			return nil, fmt.Errorf("media: check existing %q: %w", first, err)
		}
		if exists {
			return nil, ErrIDExists
		}
	}

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

	if !external {
		id = uuid.New().String()
	}

	processed, err := processImage(ctx, raw, s.config.Sizes, s.config.Format, s.config.Quality)
	if err != nil {
		return nil, err
	}

	written := make([]string, 0, len(processed))
	for _, p := range processed {
		path := fmt.Sprintf("%s/%s/%s.%s", s.config.Category, id, p.size.Name, ext)
		if err := s.storage.Save(ctx, path, bytes.NewReader(p.data)); err != nil {
			s.cleanupPartial(ctx, written)
			return nil, fmt.Errorf("media: save %q: %w", path, err)
		}
		written = append(written, path)
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

// cleanupPartial best-effort removes paths written during a failed upload.
// Cleanup itself can fail (storage flaky, transient permission issue);
// failures are logged but not propagated so the caller sees the original
// upload error rather than a cleanup error.
func (s *ImageStore) cleanupPartial(ctx context.Context, paths []string) {
	for _, p := range paths {
		if err := s.storage.Delete(ctx, p); err != nil {
			s.logger.Warn("media: cleanup partial upload failed", "path", p, "error", err)
		}
	}
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
