package devserver

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSelectedMocks(t *testing.T) {
	t.Run("maps names preserving registry order", func(t *testing.T) {
		got, err := selectedMocks([]string{"stripe", "mail"})
		require.NoError(t, err)
		require.Len(t, got, 2)
		// Registry order is mail, stripe — not input order.
		assert.Equal(t, "mail", got[0].Name)
		assert.Equal(t, "stripe", got[1].Name)
	})

	t.Run("empty is an error", func(t *testing.T) {
		_, err := selectedMocks(nil)
		assert.Error(t, err)
	})

	t.Run("unknown name is an error", func(t *testing.T) {
		_, err := selectedMocks([]string{"mail", "bogus"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "bogus")
	})
}

func TestParsePort(t *testing.T) {
	t.Run("unset returns default", func(t *testing.T) {
		n, err := parsePort("HAMR_TEST_PORT_UNSET", 4500)
		require.NoError(t, err)
		assert.Equal(t, 4500, n)
	})

	t.Run("valid value parsed", func(t *testing.T) {
		t.Setenv("HAMR_TEST_PORT", "8080")
		n, err := parsePort("HAMR_TEST_PORT", 4500)
		require.NoError(t, err)
		assert.Equal(t, 8080, n)
	})

	t.Run("unparseable errors instead of silently defaulting", func(t *testing.T) {
		t.Setenv("HAMR_TEST_PORT", "8O80")
		_, err := parsePort("HAMR_TEST_PORT", 4500)
		assert.Error(t, err)
	})

	t.Run("out of range errors", func(t *testing.T) {
		t.Setenv("HAMR_TEST_PORT", "70000")
		_, err := parsePort("HAMR_TEST_PORT", 4500)
		assert.Error(t, err)
		t.Setenv("HAMR_TEST_PORT", "0")
		_, err = parsePort("HAMR_TEST_PORT", 4500)
		assert.Error(t, err)
	})
}

// TestMockServeSplitRouting proves the app-facing surface (ingest) and the
// human UI land on different muxes — the core of the optional --ui-port split.
func TestMockServeSplitRouting(t *testing.T) {
	mounted, err := buildMailMock(slog.Default())
	require.NoError(t, err)

	apiMux := http.NewServeMux()
	uiMux := http.NewServeMux()
	mounted.RegisterAPI(apiMux)
	mounted.RegisterUI(uiMux)

	status := func(mux *http.ServeMux, method, path string) int {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		return rec.Code
	}

	// Ingest lives on the API mux only.
	assert.NotEqual(t, http.StatusNotFound, status(apiMux, http.MethodPost, "/__hamr/mail/ingest"))
	// uiMux has the /__hamr/mail/ subtree (no exact /ingest), so the request
	// reaches handleInboxOrDetail which returns 404 "message not found" — the
	// ingest handler is not invoked. Check the body to distinguish this
	// handler-level 404 from a routing-absent 404.
	rec := httptest.NewRecorder()
	uiMux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/__hamr/mail/ingest", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "message not found", "ingest must not be processed via uiMux")

	// Inbox UI lives on the UI mux only.
	assert.NotEqual(t, http.StatusNotFound, status(uiMux, http.MethodGet, "/__hamr/mail"))
	assert.Equal(t, http.StatusNotFound, status(apiMux, http.MethodGet, "/__hamr/mail"))
}

func TestBuildStripeMockRequiresEnv(t *testing.T) {
	// No env set → base URL error first.
	_, err := buildStripeMock(slog.Default())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HAMR_STRIPE_BASE_URL")

	t.Setenv("HAMR_STRIPE_BASE_URL", "http://localhost:4501")
	_, err = buildStripeMock(slog.Default())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HAMR_STRIPE_WEBHOOK_URL")

	t.Setenv("HAMR_STRIPE_WEBHOOK_URL", "http://app:8080/api/webhooks/stripe")
	t.Setenv("HAMR_STRIPE_WEBHOOK_SECRET", "whsec_test")
	mounted, err := buildStripeMock(slog.Default())
	require.NoError(t, err)
	assert.NotNil(t, mounted.RegisterAPI)
	assert.NotNil(t, mounted.RegisterUI)
}
