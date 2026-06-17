package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FyrmForge/hamr/pkg/middleware"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runTrusted runs the middleware against a request and returns the subject ID
// the handler observed (empty if the header was not trusted).
func runTrusted(t *testing.T, mw echo.MiddlewareFunc, headers map[string]string, remoteAddr string) string {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if remoteAddr != "" {
		req.RemoteAddr = remoteAddr
	}
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	var got string
	h := mw(func(c echo.Context) error {
		got = middleware.GetSubjectID(c)
		return c.String(http.StatusOK, "ok")
	})
	require.NoError(t, h(c))
	return got
}

func TestTrustedSubject_ungatedTrustsHeader(t *testing.T) {
	got := runTrusted(t, middleware.TrustedSubject(),
		map[string]string{"X-Subject-ID": "user-1"}, "")
	assert.Equal(t, "user-1", got)
}

func TestTrustedSubjectWithConfig_sharedSecret(t *testing.T) {
	mw := middleware.TrustedSubjectWithConfig(middleware.TrustedSubjectConfig{
		SharedSecret: "s3cret",
	})

	// Absent secret: header ignored.
	assert.Empty(t, runTrusted(t, mw,
		map[string]string{"X-Subject-ID": "user-1"}, ""))
	// Wrong secret: header ignored.
	assert.Empty(t, runTrusted(t, mw,
		map[string]string{"X-Subject-ID": "user-1", "X-Internal-Secret": "wrong"}, ""))
	// Correct secret: header honored.
	assert.Equal(t, "user-1", runTrusted(t, mw,
		map[string]string{"X-Subject-ID": "user-1", "X-Internal-Secret": "s3cret"}, ""))
}

func TestTrustedSubjectWithConfig_trustedCIDR(t *testing.T) {
	mw := middleware.TrustedSubjectWithConfig(middleware.TrustedSubjectConfig{
		TrustedProxies: []string{"10.0.0.0/8"},
	})

	// Peer outside the trusted range: header ignored.
	assert.Empty(t, runTrusted(t, mw,
		map[string]string{"X-Subject-ID": "user-1"}, "203.0.113.5:1234"))
	// Peer inside the trusted range: header honored.
	assert.Equal(t, "user-1", runTrusted(t, mw,
		map[string]string{"X-Subject-ID": "user-1"}, "10.1.2.3:1234"))
}
