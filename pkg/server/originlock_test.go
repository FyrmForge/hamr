package server

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSecret = "s3cr3t-value"

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

func lockedServer(t *testing.T, cfg OriginSecretConfig, opts ...Option) *Server {
	t.Helper()
	s, err := New(append([]Option{WithOriginSecret(cfg)}, opts...)...)
	require.NoError(t, err)
	ok := func(c echo.Context) error { return c.String(http.StatusOK, "ok") }
	s.GET("/", ok)
	s.GET("/api/health", ok)
	s.GET("/api/:x", ok)
	s.GET("/ip", func(c echo.Context) error { return c.String(http.StatusOK, c.RealIP()) })
	return s
}

func doReq(s *Server, method, target string, h map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	for k, v := range h {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	s.Echo().ServeHTTP(rec, req)
	return rec
}

func TestOriginSecret_gate(t *testing.T) {
	s := lockedServer(t, OriginSecretConfig{Secret: testSecret})
	rec := doReq(s, http.MethodGet, "/", nil)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Empty(t, rec.Body.String())
	assert.Equal(t, http.StatusForbidden, doReq(s, http.MethodGet, "/", map[string]string{DefaultOriginSecretHeader: "wrong"}).Code)
	rec = doReq(s, http.MethodGet, "/", map[string]string{DefaultOriginSecretHeader: testSecret})
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "ok", rec.Body.String())
}

func TestOriginSecret_customHeader(t *testing.T) {
	s := lockedServer(t, OriginSecretConfig{Secret: testSecret, Header: "X-Edge-Key"})
	assert.Equal(t, http.StatusOK, doReq(s, http.MethodGet, "/", map[string]string{"X-Edge-Key": testSecret}).Code)
	assert.Equal(t, http.StatusForbidden, doReq(s, http.MethodGet, "/", map[string]string{DefaultOriginSecretHeader: testSecret}).Code)
}

func TestOriginSecret_exemptPaths(t *testing.T) {
	s := lockedServer(t, OriginSecretConfig{Secret: testSecret, ExemptPaths: []string{"/api/health"}})
	assert.Equal(t, http.StatusOK, doReq(s, http.MethodGet, "/api/health", nil).Code)
	assert.Equal(t, http.StatusOK, doReq(s, http.MethodHead, "/api/health", nil).Code, "HEAD to exempt path")
	for _, p := range []string{"/api/health/", "/API/HEALTH", "/api/health/x", "/api/%68ealth", "/api/healthz", "/"} {
		assert.Equal(t, http.StatusForbidden, doReq(s, http.MethodGet, p, nil).Code, p)
	}
}

func TestOriginSecret_offIsNoop(t *testing.T) {
	buf := captureLog(t)
	s := lockedServer(t, OriginSecretConfig{ExemptPaths: []string{"/x"}})
	assert.Equal(t, http.StatusOK, doReq(s, http.MethodGet, "/", nil).Code)
	assert.NotContains(t, buf.String(), "origin lock")
}

func TestOriginSecret_logging(t *testing.T) {
	buf := captureLog(t)
	s := lockedServer(t, OriginSecretConfig{Secret: testSecret, ExemptPaths: []string{"/api/health"}})
	assert.Contains(t, buf.String(), "origin lock enabled")
	assert.Contains(t, buf.String(), DefaultOriginSecretHeader)
	assert.Contains(t, buf.String(), "/api/health")

	for range 3 {
		doReq(s, http.MethodGet, "/", map[string]string{DefaultOriginSecretHeader: "guess-" + testSecret[:3]})
	}
	assert.Equal(t, 1, strings.Count(buf.String(), "level=WARN"))
	assert.NotContains(t, buf.String(), testSecret)
	assert.NotContains(t, buf.String(), "guess-")
}

func TestOriginSecret_doesNotChangeRealIP(t *testing.T) {
	h := map[string]string{DefaultOriginSecretHeader: testSecret, "X-Forwarded-For": "1.2.3.4"}
	locked := lockedServer(t, OriginSecretConfig{Secret: testSecret})
	plain, err := New()
	require.NoError(t, err)
	plain.GET("/ip", func(c echo.Context) error { return c.String(http.StatusOK, c.RealIP()) })
	assert.Equal(t, doReq(plain, http.MethodGet, "/ip", h).Body.String(), doReq(locked, http.MethodGet, "/ip", h).Body.String())
}

func TestOriginSecret_gatesStaticAndHead(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(dir+"/a.txt", []byte("hi"), 0o644))
	s := lockedServer(t, OriginSecretConfig{Secret: testSecret}, WithStaticDir(dir))
	auth := map[string]string{DefaultOriginSecretHeader: testSecret}

	assert.Equal(t, http.StatusForbidden, doReq(s, http.MethodGet, "/static/a.txt", nil).Code)
	assert.Equal(t, http.StatusOK, doReq(s, http.MethodGet, "/static/a.txt", auth).Code)
	assert.Equal(t, http.StatusForbidden, doReq(s, http.MethodHead, "/", nil).Code)
	assert.Equal(t, http.StatusOK, doReq(s, http.MethodHead, "/", auth).Code)
}
