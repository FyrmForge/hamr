package devserver

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newFrontDoorUpstream(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><body>app " + r.URL.Path + "</body></html>"))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func frontDoorGet(h http.Handler, path string, setup func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if setup != nil {
		setup(req)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// withGateHeader authenticates every request so tests can focus on routing.
func withGateHeader(pw string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("X-Hamr-Gate", pw)
		h.ServeHTTP(w, r)
	})
}

func TestFrontDoorPublicHandler(t *testing.T) {
	t.Setenv("HAMR_MOCK_UI_PASSWORD", "s3cret")
	upstream, hits := newFrontDoorUpstream(t)
	mail := []MockProvider{{Name: "mail", Build: buildMailMock}}
	_, gated, err := buildFrontDoor(slog.Default(), mail, upstream.URL)
	require.NoError(t, err, "composed mux must not have conflicting patterns")
	public := withGateHeader("s3cret", gated)

	t.Run("app proxied with widget injected", func(t *testing.T) {
		rec := frontDoorGet(public, "/some/page", nil)
		require.Equal(t, http.StatusOK, rec.Code)
		body := rec.Body.String()
		assert.Contains(t, body, "app /some/page")
		assert.Contains(t, body, "/__hamr/reload")
	})

	t.Run("mock UI and widget assets served locally", func(t *testing.T) {
		before := hits.Load()
		assert.Equal(t, http.StatusOK, frontDoorGet(public, "/__hamr/mail", nil).Code)
		assert.Equal(t, http.StatusOK, frontDoorGet(public, "/__hamr/logo.png", nil).Code)
		assert.Equal(t, http.StatusNotFound, frontDoorGet(public, "/__hamr/nope", nil).Code)
		assert.Equal(t, before, hits.Load(), "/__hamr/* must never reach the app")
	})

	t.Run("ingest is not on the public port", func(t *testing.T) {
		rec := httptest.NewRecorder()
		public.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/__hamr/mail/ingest", strings.NewReader("{}")))
		assert.NotEqual(t, http.StatusOK, rec.Code)
	})

	t.Run("/_devactions is never proxied", func(t *testing.T) {
		before := hits.Load()
		for _, p := range []string{"/_devactions", "/_devactions/", "/_devactions/wipe", "/%5Fdevactions/wipe", "/_devactions%2Fwipe", "/x/../_devactions"} {
			assert.Equal(t, http.StatusNotFound, frontDoorGet(public, p, nil).Code, p)
		}
		assert.Equal(t, before, hits.Load())
	})

	t.Run("app actions not configured → 404", func(t *testing.T) {
		assert.Equal(t, http.StatusNotFound, frontDoorGet(public, "/__hamr/app/actions", nil).Code)
	})
}

func TestFrontDoorAPIPortIsPrivateAndOpen(t *testing.T) {
	t.Setenv("HAMR_MOCK_UI_PASSWORD", "s3cret")
	upstream, hits := newFrontDoorUpstream(t)
	mail := []MockProvider{{Name: "mail", Build: buildMailMock}}
	api, _, err := buildFrontDoor(slog.Default(), mail, upstream.URL)
	require.NoError(t, err)

	body := `{"From":{"Email":"app@example.com"},"To":[{"Email":"ada@example.com"}],"Subject":"hi","Text":"hi"}`
	req := httptest.NewRequest(http.MethodPost, "/__hamr/mail/ingest", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code, "ingest is never gated")

	assert.Equal(t, http.StatusNotFound, frontDoorGet(api, "/some/page", nil).Code, "private port does not proxy the app")
	assert.Zero(t, hits.Load())
}

func TestFrontDoorGate(t *testing.T) {
	t.Setenv("HAMR_MOCK_UI_PASSWORD", "s3cret")
	t.Setenv("HAMR_GATE_BYPASS", "/webhooks")
	upstream, _ := newFrontDoorUpstream(t)
	mail := []MockProvider{{Name: "mail", Build: buildMailMock}}
	_, public, err := buildFrontDoor(slog.Default(), mail, upstream.URL)
	require.NoError(t, err)

	assert.Equal(t, http.StatusUnauthorized, frontDoorGet(public, "/page", nil).Code)
	assert.Equal(t, http.StatusUnauthorized, frontDoorGet(public, "/__hamr/mail", nil).Code)
	assert.Equal(t, http.StatusOK, frontDoorGet(public, "/webhooks/stripe", nil).Code, "bypass prefix skips the gate")
	assert.Equal(t, http.StatusOK, frontDoorGet(public, "/page", func(r *http.Request) {
		r.Header.Set("X-Hamr-Gate", "s3cret")
	}).Code)
	// The gate must not open /_devactions.
	assert.Equal(t, http.StatusNotFound, frontDoorGet(public, "/_devactions", func(r *http.Request) {
		r.Header.Set("X-Hamr-Gate", "s3cret")
	}).Code)
}

func TestFrontDoorRequiresPassword(t *testing.T) {
	t.Setenv("HAMR_MOCK_UI_PASSWORD", "")
	upstream, _ := newFrontDoorUpstream(t)
	_, _, err := buildFrontDoor(slog.Default(), nil, upstream.URL)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HAMR_MOCK_UI_PASSWORD")
}

func TestFrontDoorBadUpstream(t *testing.T) {
	t.Setenv("HAMR_MOCK_UI_PASSWORD", "s3cret")
	for _, up := range []string{"app:8080", "https://app:8080", "http://", "::bad"} {
		_, _, err := buildFrontDoor(slog.Default(), nil, up)
		require.Error(t, err, up)
		assert.Contains(t, err.Error(), "HAMR_MOCK_UPSTREAM", up)
	}
}

func TestRunMockServeUpstreamRequiresUIPort(t *testing.T) {
	t.Setenv("HAMR_MOCKS", "mail")
	t.Setenv("HAMR_MOCK_UPSTREAM", "http://app:8080")
	t.Setenv("HAMR_MOCK_UI_PORT", "")
	err := RunMockServe(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HAMR_MOCK_UI_PORT")
}

func TestStripeBaseURLFallback(t *testing.T) {
	t.Setenv("HAMR_STRIPE_BASE_URL", "")
	t.Setenv("HAMR_BASE_URL", "https://staging.example.com")

	t.Setenv("HAMR_MOCK_UPSTREAM", "")
	assert.Empty(t, stripeBaseURL(), "plain mode ignores HAMR_BASE_URL")

	t.Setenv("HAMR_MOCK_UPSTREAM", "http://app:8080")
	assert.Equal(t, "https://staging.example.com", stripeBaseURL())

	t.Setenv("HAMR_STRIPE_BASE_URL", "http://override:1")
	assert.Equal(t, "http://override:1", stripeBaseURL(), "explicit value wins")
}

func TestNewProxyHandlerHeaderTimeout(t *testing.T) {
	// Upstream answers after the dev 2s limit would not matter here; assert the
	// option is plumbed by a quick slow-header check with a tiny timeout.
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer slow.Close()
	h := newProxyHandler(slow.Listener.Addr().String(), proxyOpts{headerTimeout: 50e6}, NewSSEBroker(nil, nil, nil, false, false, false, false, false), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, true)
	rec := frontDoorGet(h, "/", nil)
	assert.Equal(t, http.StatusBadGateway, rec.Code)
}

func TestFrontDoorForwardedHeaders(t *testing.T) {
	t.Setenv("HAMR_MOCK_UI_PASSWORD", "s3cret")
	t.Setenv("HAMR_BASE_URL", "https://staging.example.com")
	var proto, xff string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proto, xff = r.Header.Get("X-Forwarded-Proto"), r.Header.Get("X-Forwarded-For")
	}))
	t.Cleanup(upstream.Close)
	_, public, err := buildFrontDoor(slog.Default(), nil, upstream.URL)
	require.NoError(t, err)

	frontDoorGet(withGateHeader("s3cret", public), "/x", func(r *http.Request) {
		r.Header.Set("X-Forwarded-For", "203.0.113.7")
	})
	assert.Equal(t, "https", proto, "TLS ends at the platform edge; the app must still see https")
	assert.True(t, strings.HasPrefix(xff, "203.0.113.7, "), "edge's client IP kept, got %q", xff)
}

func TestFrontDoorRejectsWideBypass(t *testing.T) {
	t.Setenv("HAMR_MOCK_UI_PASSWORD", "s3cret")
	upstream, _ := newFrontDoorUpstream(t)
	for _, b := range []string{"/", "/__hamr", "/__hamr/app", "/x/../__hamr/mail"} {
		t.Setenv("HAMR_GATE_BYPASS", "/webhooks,"+b)
		_, _, err := buildFrontDoor(slog.Default(), nil, upstream.URL)
		require.Error(t, err, b)
		assert.Contains(t, err.Error(), "HAMR_GATE_BYPASS", b)
	}
}
