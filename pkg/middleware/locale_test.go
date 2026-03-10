package middleware

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/FyrmForge/hamr/pkg/i18n"
	"github.com/FyrmForge/hamr/pkg/ptr"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestBundle(t *testing.T) *i18n.Bundle {
	t.Helper()
	dir := t.TempDir()

	en := `{"app":{"title":"My App"},"greeting":"Hello"}`
	fr := `{"app":{"title":"Mon App"},"greeting":"Bonjour"}`

	require.NoError(t, os.WriteFile(filepath.Join(dir, "en.json"), []byte(en), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fr.json"), []byte(fr), 0o644))

	b, err := i18n.NewBundle(i18n.BundleConfig{
		LocaleDir:         dir,
		DefaultLocale:     "en",
		FallbackToDefault: ptr.To(true),
	})
	require.NoError(t, err)
	return b
}

// --- LocaleFromPath tests using e.Pre() with Echo's router ---

func TestLocaleFromPath_RouterIntegration(t *testing.T) {
	bundle := setupTestBundle(t)
	e := echo.New()
	e.Pre(LocaleFromPath(LocaleConfig{Bundle: bundle}))

	var capturedLocale string
	e.GET("/about", func(c echo.Context) error {
		capturedLocale = GetLocale(c)
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/fr/about", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "fr", capturedLocale)
}

func TestLocaleFromPath_RootPath(t *testing.T) {
	bundle := setupTestBundle(t)
	e := echo.New()
	e.Pre(LocaleFromPath(LocaleConfig{Bundle: bundle}))

	var capturedLocale string
	e.GET("/", func(c echo.Context) error {
		capturedLocale = GetLocale(c)
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/en/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "en", capturedLocale)
}

func TestLocaleFromPath_NoLocaleRedirects(t *testing.T) {
	bundle := setupTestBundle(t)
	e := echo.New()
	e.Pre(LocaleFromPath(LocaleConfig{Bundle: bundle}))

	e.GET("/about", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/about", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusMovedPermanently, rec.Code)
	assert.Equal(t, "/en/about", rec.Header().Get("Location"))
}

func TestLocaleFromPath_PostRedirects307(t *testing.T) {
	bundle := setupTestBundle(t)
	e := echo.New()
	e.Pre(LocaleFromPath(LocaleConfig{Bundle: bundle}))

	e.POST("/submit", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodPost, "/submit", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusTemporaryRedirect, rec.Code)
	assert.Equal(t, "/en/submit", rec.Header().Get("Location"))
}

func TestLocaleFromPath_SetsCookie(t *testing.T) {
	bundle := setupTestBundle(t)
	e := echo.New()
	e.Pre(LocaleFromPath(LocaleConfig{Bundle: bundle}))

	e.GET("/page", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/fr/page", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	cookies := rec.Result().Cookies() //nolint:bodyclose
	var found bool
	for _, cookie := range cookies {
		if cookie.Name == "hamr_locale" && cookie.Value == "fr" {
			found = true
			break
		}
	}
	assert.True(t, found, "expected hamr_locale=fr cookie")
}

func TestLocaleFromPath_UnknownSegmentNotStripped(t *testing.T) {
	bundle := setupTestBundle(t)
	e := echo.New()
	e.Pre(LocaleFromPath(LocaleConfig{Bundle: bundle}))

	// "xx" is not a supported locale — should redirect, not treat as path.
	e.GET("/xx/about", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/xx/about", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusMovedPermanently, rec.Code)
	assert.Equal(t, "/en/xx/about", rec.Header().Get("Location"))
}

func TestLocaleFromPath_RedirectPreservesQueryString(t *testing.T) {
	bundle := setupTestBundle(t)
	e := echo.New()
	e.Pre(LocaleFromPath(LocaleConfig{Bundle: bundle}))

	e.GET("/search", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/search?q=hello&page=2", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusMovedPermanently, rec.Code)
	assert.Equal(t, "/en/search?q=hello&page=2", rec.Header().Get("Location"))
}

// --- LocaleFromPreference tests ---

func TestLocaleFromPreference_Cookie(t *testing.T) {
	bundle := setupTestBundle(t)
	e := echo.New()
	mw := LocaleFromPreference(LocaleConfig{Bundle: bundle})

	var capturedLocale string
	handler := mw(func(c echo.Context) error {
		capturedLocale = GetLocale(c)
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.AddCookie(&http.Cookie{Name: "hamr_locale", Value: "fr"})
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	err := handler(c)
	require.NoError(t, err)
	assert.Equal(t, "fr", capturedLocale)
}

func TestLocaleFromPreference_AcceptLanguage_Weighted(t *testing.T) {
	bundle := setupTestBundle(t)
	e := echo.New()
	mw := LocaleFromPreference(LocaleConfig{Bundle: bundle})

	var capturedLocale string
	handler := mw(func(c echo.Context) error {
		capturedLocale = GetLocale(c)
		return c.String(http.StatusOK, "ok")
	})

	// fr has higher weight than en — should resolve to fr.
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.Header.Set("Accept-Language", "en;q=0.1,fr;q=1.0")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	err := handler(c)
	require.NoError(t, err)
	assert.Equal(t, "fr", capturedLocale)
}

func TestLocaleFromPreference_AcceptLanguage_RegionTag(t *testing.T) {
	bundle := setupTestBundle(t)
	e := echo.New()
	mw := LocaleFromPreference(LocaleConfig{Bundle: bundle})

	var capturedLocale string
	handler := mw(func(c echo.Context) error {
		capturedLocale = GetLocale(c)
		return c.String(http.StatusOK, "ok")
	})

	// fr-FR should match the "fr" locale.
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.Header.Set("Accept-Language", "fr-FR,fr;q=0.9,en;q=0.8")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	err := handler(c)
	require.NoError(t, err)
	assert.Equal(t, "fr", capturedLocale)
}

func TestLocaleFromPreference_UserFunc(t *testing.T) {
	bundle := setupTestBundle(t)
	e := echo.New()
	mw := LocaleFromPreference(LocaleConfig{
		Bundle: bundle,
		UserLocaleFunc: func(c echo.Context) string {
			return "fr"
		},
	})

	var capturedLocale string
	handler := mw(func(c echo.Context) error {
		capturedLocale = GetLocale(c)
		return c.String(http.StatusOK, "ok")
	})

	// Even with en cookie, user func takes priority.
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.AddCookie(&http.Cookie{Name: "hamr_locale", Value: "en"})
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	err := handler(c)
	require.NoError(t, err)
	assert.Equal(t, "fr", capturedLocale)
}

func TestLocaleFromPreference_UserFunc_RegionTag(t *testing.T) {
	bundle := setupTestBundle(t)
	e := echo.New()
	mw := LocaleFromPreference(LocaleConfig{
		Bundle: bundle,
		UserLocaleFunc: func(c echo.Context) string {
			return "fr-FR"
		},
	})

	var capturedLocale string
	handler := mw(func(c echo.Context) error {
		capturedLocale = GetLocale(c)
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	err := handler(c)
	require.NoError(t, err)
	assert.Equal(t, "fr", capturedLocale)
}

func TestLocaleFromPreference_Cookie_RegionTag(t *testing.T) {
	bundle := setupTestBundle(t)
	e := echo.New()
	mw := LocaleFromPreference(LocaleConfig{Bundle: bundle})

	var capturedLocale string
	handler := mw(func(c echo.Context) error {
		capturedLocale = GetLocale(c)
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.AddCookie(&http.Cookie{Name: "hamr_locale", Value: "fr-FR"})
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	err := handler(c)
	require.NoError(t, err)
	assert.Equal(t, "fr", capturedLocale)
}

func TestLocaleFromPreference_Default(t *testing.T) {
	bundle := setupTestBundle(t)
	e := echo.New()
	mw := LocaleFromPreference(LocaleConfig{Bundle: bundle})

	var capturedLocale string
	handler := mw(func(c echo.Context) error {
		capturedLocale = GetLocale(c)
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	err := handler(c)
	require.NoError(t, err)
	assert.Equal(t, "en", capturedLocale)
}

func TestGetDirection(t *testing.T) {
	dir := t.TempDir()
	ar := `{"_meta":{"direction":"rtl"},"greeting":"مرحبا"}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ar.json"), []byte(ar), 0o644))

	b, err := i18n.NewBundle(i18n.BundleConfig{
		LocaleDir:     dir,
		DefaultLocale: "ar",
	})
	require.NoError(t, err)

	e := echo.New()
	mw := LocaleFromPreference(LocaleConfig{Bundle: b})

	var direction string
	handler := mw(func(c echo.Context) error {
		direction = GetDirection(c)
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	err = handler(c)
	require.NoError(t, err)
	assert.Equal(t, "rtl", direction)
}
