package middleware_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FyrmForge/hamr/pkg/middleware"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeFlashCookie(msg middleware.FlashMessage) *http.Cookie {
	data, _ := json.Marshal(msg)
	return &http.Cookie{
		Name:  middleware.FlashCookieName,
		Value: base64.StdEncoding.EncodeToString(data),
	}
}

func TestFlash_readsCookie(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(makeFlashCookie(middleware.FlashMessage{
		Message: "saved!",
		Type:    middleware.FlashSuccess,
	}))
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	var flash *middleware.FlashMessage
	handler := middleware.Flash()(func(c echo.Context) error {
		flash = middleware.GetFlash(c)
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)
	require.NotNil(t, flash)
	assert.Equal(t, "saved!", flash.Message)
	assert.Equal(t, middleware.FlashSuccess, flash.Type)
}

func TestFlash_clearsCookie(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(makeFlashCookie(middleware.FlashMessage{
		Message: "hello",
		Type:    middleware.FlashInfo,
	}))
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	handler := middleware.Flash()(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)

	// Response should have a clearing cookie (MaxAge=-1).
	cookies := rec.Result().Cookies()
	var found bool
	for _, cookie := range cookies {
		if cookie.Name == middleware.FlashCookieName {
			assert.Equal(t, -1, cookie.MaxAge)
			found = true
		}
	}
	assert.True(t, found, "expected clearing cookie in response")
}

func TestFlash_noCookie(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	handler := middleware.Flash()(func(c echo.Context) error {
		flash := middleware.GetFlash(c)
		assert.Nil(t, flash)
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)
}

func TestFlash_invalidBase64(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{
		Name:  middleware.FlashCookieName,
		Value: "not-valid-base64!!!",
	})
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	handler := middleware.Flash()(func(c echo.Context) error {
		flash := middleware.GetFlash(c)
		assert.Nil(t, flash)
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)
}

func TestFlash_invalidJSON(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{
		Name:  middleware.FlashCookieName,
		Value: base64.StdEncoding.EncodeToString([]byte("not json")),
	})
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	handler := middleware.Flash()(func(c echo.Context) error {
		flash := middleware.GetFlash(c)
		assert.Nil(t, flash)
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)
}

func TestSetFlash_setsCookie(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	middleware.SetFlash(c, "it worked!", middleware.FlashSuccess)

	cookies := rec.Result().Cookies()
	require.Len(t, cookies, 1)

	cookie := cookies[0]
	assert.Equal(t, middleware.FlashCookieName, cookie.Name)
	assert.Equal(t, 60, cookie.MaxAge)
	assert.True(t, cookie.HttpOnly)

	// Decode and verify.
	data, err := base64.StdEncoding.DecodeString(cookie.Value)
	require.NoError(t, err)
	var msg middleware.FlashMessage
	require.NoError(t, json.Unmarshal(data, &msg))
	assert.Equal(t, "it worked!", msg.Message)
	assert.Equal(t, middleware.FlashSuccess, msg.Type)
}

func TestGetFlash_returnsNil(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	assert.Nil(t, middleware.GetFlash(c))
}
