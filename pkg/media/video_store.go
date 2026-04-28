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

// VideoStore handles video upload, processing, serving, and deletion.
type VideoStore struct {
	storage   storage.FileStorage
	signable  storage.SignableStorage
	config    VideoStoreConfig
	urlPrefix string
	logger    *slog.Logger
	isLocal   bool
}

// NewLocalVideoStore creates a VideoStore backed by local filesystem storage.
func NewLocalVideoStore(store *storage.LocalStorage, urlPrefix string, cfg VideoStoreConfig, opts ...Option) (*VideoStore, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}

	return &VideoStore{
		storage:   store,
		config:    cfg,
		urlPrefix: strings.TrimRight(urlPrefix, "/"),
		logger:    o.logger,
		isLocal:   true,
	}, nil
}

// NewS3VideoStore creates a VideoStore backed by S3-compatible storage.
func NewS3VideoStore(store *storage.S3Storage, cfg VideoStoreConfig, opts ...Option) (*VideoStore, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}

	return &VideoStore{
		storage:  store,
		signable: store,
		config:   cfg,
		logger:   o.logger,
	}, nil
}

// Upload processes and stores a video from a multipart file header.
func (s *VideoStore) Upload(ctx context.Context, fh *multipart.FileHeader) (*VideoUploadResult, error) {
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

// UploadFromReader processes and stores a video from an io.Reader. The id
// is generated internally as a fresh UUID.
func (s *VideoStore) UploadFromReader(ctx context.Context, r io.Reader, size int64) (*VideoUploadResult, error) {
	if size > s.config.MaxSize {
		return nil, ErrFileTooLarge
	}
	return s.upload(ctx, r, "", false)
}

// UploadFromReaderWithID processes and stores a video using the supplied
// id as the storage path component instead of generating one internally.
//
// The id MUST be in the 36-char canonical UUID form
// `xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx`. Other forms accepted by
// uuid.Parse (hyphenless, urn, braced) and the empty string are
// rejected with ErrInvalidID — same contract as the image variant; see
// ImageStore.UploadFromReaderWithID for the rationale.
//
// If storage already holds bytes under the id, the call returns
// ErrIDExists unless overwrite=true. Workers retrying a failed transcode
// typically want overwrite=true so the retry replaces any residue.
// ErrIDExists is a best-effort precheck — see ImageStore.UploadFromReaderWithID
// for the concurrent-callers caveat.
//
// On ErrFileTooLarge (size pre-check), ErrInvalidID, and ErrIDExists the
// reader r is NOT consumed — all three errors are detected before
// detectMIME / ffprobe run, so the rejection path doesn't drain the
// payload. Callers relying on the upload to drain a network or pipe
// reader must discard or reuse r explicitly on these errors.
// (ErrFileTooLarge can also fire after the MIME sniff against the
// actual buffered byte count; in that case r has been drained.)
//
// Thumbnail generation/save failures are logged and ignored — the video
// bytes are still stored, and result.ThumbnailPath stays empty when this
// branch fails. There is no partial-write window because the video Save
// is the only step that can fail after the id is fixed.
func (s *VideoStore) UploadFromReaderWithID(ctx context.Context, id string, r io.Reader, size int64, overwrite bool) (*VideoUploadResult, error) {
	if size > s.config.MaxSize {
		return nil, ErrFileTooLarge
	}
	if err := validateCanonicalUUID(id); err != nil {
		return nil, err
	}
	return s.upload(ctx, r, id, overwrite)
}

// upload is the shared core for the three public upload methods. id is
// the caller-supplied canonical UUID for *WithID variants (validated at
// the entry point AND defensively re-validated below to fence the
// invariant against future entry points); pass "" to have one generated
// internally. overwrite is meaningful only when id is non-empty.
func (s *VideoStore) upload(ctx context.Context, r io.Reader, id string, overwrite bool) (*VideoUploadResult, error) {
	// captured before the UUID-fill below mutates id — `external` answers
	// "did the caller supply this id?" which the existence-check and
	// validation paths both branch on.
	external := id != ""

	// Defensive re-validation — see the matching note in
	// ImageStore.upload re: future entry points.
	if external {
		if err := validateCanonicalUUID(id); err != nil {
			return nil, err
		}
	}

	// Existence precheck — runs BEFORE detectMIME / ffprobe to
	// short-circuit the retry-after-failure hot path: if the id is
	// already taken, we don't want to slurp the whole payload AND fork
	// ffprobe just to return ErrIDExists. Storage path is computable
	// from config + id alone.
	//
	// Only meaningful when an external id was supplied; internal IDs
	// are fresh UUIDs with astronomical non-collision odds.
	if external && !overwrite {
		probePath := fmt.Sprintf("%s/%s/video.mp4", s.config.Category, id)
		exists, err := s.storage.Exists(ctx, probePath)
		if err != nil {
			return nil, fmt.Errorf("media: check existing %q: %w", probePath, err)
		}
		if exists {
			return nil, ErrIDExists
		}
	}

	mimeType, raw, err := detectMIME(r)
	if err != nil {
		return nil, err
	}

	if !videoTypes[mimeType] {
		return nil, ErrUnknownType
	}

	if int64(len(raw)) > s.config.MaxSize {
		return nil, ErrFileTooLarge
	}

	// Probe duration.
	duration, err := probeVideoDuration(ctx, raw)
	if err != nil {
		return nil, err
	}
	if duration > s.config.MaxDuration {
		return nil, ErrVideoTooLong
	}

	if !external {
		id = uuid.New().String()
	}

	videoPath := fmt.Sprintf("%s/%s/video.mp4", s.config.Category, id)
	thumbPath := fmt.Sprintf("%s/%s/thumb.jpg", s.config.Category, id)

	if err := s.storage.Save(ctx, videoPath, bytes.NewReader(raw)); err != nil {
		return nil, fmt.Errorf("media: save video: %w", err)
	}

	result := &VideoUploadResult{
		ID:        id,
		MediaType: TypeVideo,
		MimeType:  mimeType,
		Duration:  duration,
		FileSize:  int64(len(raw)),
		category:  s.config.Category,
	}

	// Generate thumbnail if configured. Thumbnail generation/save failures
	// are logged and ignored — the video bytes are already stored, so we
	// don't unwind the video save. ThumbnailPath stays empty when this
	// branch fails.
	if s.config.GenerateThumbnail {
		thumbData, err := generateThumbnail(ctx, raw, s.config.ThumbnailWidth)
		if err != nil {
			s.logger.Warn("thumbnail generation failed", "id", id, "error", err)
		} else {
			if err := s.storage.Save(ctx, thumbPath, bytes.NewReader(thumbData)); err != nil {
				s.logger.Warn("thumbnail save failed", "id", id, "error", err)
			} else {
				result.ThumbnailPath = thumbPath
			}
		}
	}

	s.logger.Debug("video uploaded",
		"id", id,
		"category", s.config.Category,
		"mime", mimeType,
		"duration", duration,
		"size", len(raw),
	)

	return result, nil
}

// Delete removes the video and its thumbnail for the given media ID.
func (s *VideoStore) Delete(ctx context.Context, id string) error {
	videoPath := fmt.Sprintf("%s/%s/video.mp4", s.config.Category, id)
	if err := s.storage.Delete(ctx, videoPath); err != nil {
		return fmt.Errorf("media: delete video: %w", err)
	}

	if s.config.GenerateThumbnail {
		thumbPath := fmt.Sprintf("%s/%s/thumb.jpg", s.config.Category, id)
		// Thumbnail deletion is best-effort.
		_ = s.storage.Delete(ctx, thumbPath)
	}

	s.logger.Debug("video deleted", "id", id, "category", s.config.Category)
	return nil
}

// GetMedia returns a VideoRef for constructing public URLs.
func (s *VideoStore) GetMedia(id string) VideoRef {
	base := s.urlPrefix
	if !s.isLocal && s.config.BaseURL != "" {
		base = strings.TrimRight(s.config.BaseURL, "/")
	}
	return VideoRef{
		id:           id,
		category:     s.config.Category,
		baseURL:      base,
		hasThumbnail: s.config.GenerateThumbnail,
	}
}

// GetMediaCtx returns a VideoRef that may use signed URLs for S3 stores.
func (s *VideoStore) GetMediaCtx(ctx context.Context, id string) VideoRef {
	if s.signable != nil && s.config.SignedExpiry > 0 {
		return VideoRef{
			id:           id,
			category:     s.config.Category,
			hasThumbnail: s.config.GenerateThumbnail,
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

// SignedURL generates a pre-signed URL for a specific storage path.
func (s *VideoStore) SignedURL(ctx context.Context, path string, expiry time.Duration) (string, error) {
	if s.signable == nil {
		return "", fmt.Errorf("media: signed URLs not available for local storage")
	}
	return s.signable.SignURL(ctx, path, expiry)
}

// ServeHandler returns an Echo handler that serves video files from the store.
func (s *VideoStore) ServeHandler() echo.HandlerFunc {
	if s.isLocal {
		return s.serveLocal()
	}
	return s.serveS3()
}

func (s *VideoStore) serveLocal() echo.HandlerFunc {
	return func(c echo.Context) error {
		reqPath := c.Request().URL.Path
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

		ext := filepath.Ext(storagePath)
		ct := "application/octet-stream"
		switch ext {
		case ".mp4":
			ct = "video/mp4"
		case ".webm":
			ct = "video/webm"
		case ".jpg", ".jpeg":
			ct = "image/jpeg"
		}

		c.Response().Header().Set("Content-Type", ct)
		c.Response().Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		c.Response().WriteHeader(http.StatusOK)
		_, err = io.Copy(c.Response(), rc)
		return err
	}
}

func (s *VideoStore) serveS3() echo.HandlerFunc {
	return func(c echo.Context) error {
		reqPath := c.Request().URL.Path
		storagePath := strings.TrimPrefix(reqPath, "/")

		rc, err := s.storage.Open(c.Request().Context(), storagePath)
		if err != nil {
			return echo.NewHTTPError(http.StatusNotFound)
		}
		defer func() { _ = rc.Close() }()

		ext := filepath.Ext(storagePath)
		ct := "application/octet-stream"
		switch ext {
		case ".mp4":
			ct = "video/mp4"
		case ".webm":
			ct = "video/webm"
		case ".jpg", ".jpeg":
			ct = "image/jpeg"
		}

		c.Response().Header().Set("Content-Type", ct)
		c.Response().Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		c.Response().WriteHeader(http.StatusOK)
		_, err = io.Copy(c.Response(), rc)
		return err
	}
}
