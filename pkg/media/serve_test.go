package media

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FyrmForge/hamr/pkg/storage"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScopedServeKey(t *testing.T) {
	tests := []struct {
		name                      string
		reqPath, prefix, category string
		wantKey                   string
		wantOK                    bool
	}{
		{"local in-category", "/uploads/avatars/id/lg.webp", "/uploads", "avatars", "avatars/id/lg.webp", true},
		{"local cross-category", "/uploads/private/secret.webp", "/uploads", "avatars", "", false},
		{"local outside prefix", "/other/avatars/x.webp", "/uploads", "avatars", "", false},
		{"local traversal escapes category", "/uploads/avatars/../private/x.webp", "/uploads", "avatars", "", false},
		{"local prefix equals path", "/uploads", "/uploads", "avatars", "", false},
		{"s3 in-category", "/avatars/id/lg.webp", "", "avatars", "avatars/id/lg.webp", true},
		{"s3 cross-category", "/private/secret.webp", "", "avatars", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, ok := scopedServeKey(tt.reqPath, tt.prefix, tt.category)
			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Equal(t, tt.wantKey, key)
			}
		})
	}
}

// TestImageStore_ServeHandler_ScopesToCategory verifies the serve handler only
// returns objects under the store's own category and 404s a cross-category
// request — it must not proxy another category's object out of the storage root.
func TestImageStore_ServeHandler_ScopesToCategory(t *testing.T) {
	store, err := storage.NewLocalStorage(t.TempDir())
	require.NoError(t, err)
	is, err := NewLocalImageStore(store, "/uploads", ImageStoreConfig{
		Category: "avatars",
		Sizes:    SizesAvatar,
		Quality:  85,
		Format:   FormatWebP,
		MaxSize:  MB,
	})
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, store.Save(ctx, "avatars/abc/lg.webp", strings.NewReader("img-bytes")))
	require.NoError(t, store.Save(ctx, "private/secret.webp", strings.NewReader("TOPSECRET")))

	h := is.ServeHandler()
	e := echo.New()

	// In-category request serves the file.
	rec := httptest.NewRecorder()
	c := e.NewContext(httptest.NewRequest(http.MethodGet, "/uploads/avatars/abc/lg.webp", nil), rec)
	require.NoError(t, h(c))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "img-bytes", rec.Body.String())

	// Cross-category request must 404 — never leak the other category's object.
	rec2 := httptest.NewRecorder()
	c2 := e.NewContext(httptest.NewRequest(http.MethodGet, "/uploads/private/secret.webp", nil), rec2)
	err = h(c2)
	var he *echo.HTTPError
	require.ErrorAs(t, err, &he)
	assert.Equal(t, http.StatusNotFound, he.Code)
}

// TestImageStore_ServeHandler_MissingFile404 verifies an in-category request for
// a file that doesn't exist returns 404, not 500 — the storage layer wraps the
// os error with fmt.Errorf("%w"), which os.IsNotExist can't see through.
func TestImageStore_ServeHandler_MissingFile404(t *testing.T) {
	store, err := storage.NewLocalStorage(t.TempDir())
	require.NoError(t, err)
	is, err := NewLocalImageStore(store, "/uploads", ImageStoreConfig{
		Category: "avatars",
		Sizes:    SizesAvatar,
		Quality:  85,
		Format:   FormatWebP,
		MaxSize:  MB,
	})
	require.NoError(t, err)

	h := is.ServeHandler()
	e := echo.New()
	rec := httptest.NewRecorder()
	c := e.NewContext(httptest.NewRequest(http.MethodGet, "/uploads/avatars/missing/lg.webp", nil), rec)
	err = h(c)
	var he *echo.HTTPError
	require.ErrorAs(t, err, &he)
	assert.Equal(t, http.StatusNotFound, he.Code)
}
