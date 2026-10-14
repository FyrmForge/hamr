package devserver

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func appActionsMux(a *AppActions) *http.ServeMux {
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)
	return mux
}

func TestAppActions_Proxies(t *testing.T) {
	var gotPath, gotToken, gotCT, gotBody, gotMethod string
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.EscapedPath(), r.Method
		gotToken, gotCT = r.Header.Get("X-Hamr-Actions-Token"), r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"confirmation required"}`))
	}))
	defer app.Close()
	mux := appActionsMux(NewAppActions(strings.TrimPrefix(app.URL, "http://"), "sekret"))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/__hamr/app/actions", nil))
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, "/_devactions", gotPath)
	assert.Equal(t, http.MethodGet, gotMethod)
	assert.Equal(t, "sekret", gotToken)

	req := httptest.NewRequest(http.MethodPost, "/__hamr/app/actions/wipe%20db", strings.NewReader(`{"confirm":false}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.JSONEq(t, `{"error":"confirmation required"}`, rec.Body.String())
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.Equal(t, "/_devactions/wipe%20db", gotPath)
	assert.Equal(t, "sekret", gotToken)
	assert.Equal(t, "application/json", gotCT)
	assert.Equal(t, `{"confirm":false}`, gotBody)
}

func TestAppActions_Errors(t *testing.T) {
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	mux := appActionsMux(NewAppActions(deadURL, "t"))

	for _, tc := range []struct {
		name, method, path, origin string
		want                       int
	}{
		{"dead upstream list", http.MethodGet, "/__hamr/app/actions", "", http.StatusBadGateway},
		{"dead upstream run", http.MethodPost, "/__hamr/app/actions/x", "", http.StatusBadGateway},
		{"cross-origin post", http.MethodPost, "/__hamr/app/actions/x", "https://evil.example", http.StatusForbidden},
		{"empty name", http.MethodPost, "/__hamr/app/actions/", "", http.StatusNotFound},
		{"nested name", http.MethodPost, "/__hamr/app/actions/a/b", "", http.StatusNotFound},
		// ServeMux cleans ".." with a redirect before the handler runs.
		{"dotdot name", http.MethodPost, "/__hamr/app/actions/..", "", http.StatusTemporaryRedirect},
		{"get on run", http.MethodGet, "/__hamr/app/actions/x", "", http.StatusMethodNotAllowed},
		{"post on list", http.MethodPost, "/__hamr/app/actions", "", http.StatusMethodNotAllowed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			assert.Equal(t, tc.want, rec.Code)
			if tc.want == http.StatusBadGateway {
				assert.JSONEq(t, `{"error":"app actions unavailable"}`, rec.Body.String())
			}
		})
	}
}

func TestAppActions_BodyLimit(t *testing.T) {
	app := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("oversized body must not reach the app")
	}))
	defer app.Close()
	mux := appActionsMux(NewAppActions(app.URL, "t"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/__hamr/app/actions/x", strings.NewReader(strings.Repeat("a", maxActionBody+1))))
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
}

func TestAppActions_NoRedirectFollow(t *testing.T) {
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:1/steal", http.StatusFound)
	}))
	defer app.Close()
	rec := httptest.NewRecorder()
	appActionsMux(NewAppActions(app.URL, "t")).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/__hamr/app/actions", nil))
	assert.Equal(t, http.StatusFound, rec.Code)
}

func TestAppActions_NilSafe(t *testing.T) {
	assert.Nil(t, NewAppActions("", "t"))
	assert.Nil(t, NewAppActions("localhost:1", ""))
	mux := appActionsMux(nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/__hamr/app/actions", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestNewAppActions_NormalisesBase(t *testing.T) {
	for in, want := range map[string]string{
		"localhost:8080":       "http://localhost:8080",
		":8080":                "http://localhost:8080",
		"0.0.0.0:8080":         "http://localhost:8080",
		"http://app:8080/":     "http://app:8080",
		"https://app.internal": "https://app.internal",
	} {
		a := NewAppActions(in, "t")
		require.NotNil(t, a)
		assert.Equal(t, want, a.base, in)
	}
}

func TestTunnelBlocksAppActions(t *testing.T) {
	h := blockDevCommands(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	for _, p := range []string{"/__hamr/app/actions", "/__hamr/app/actions/wipe", "/__hamr/app/../app/actions"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, p, nil))
		assert.Equal(t, http.StatusForbidden, rec.Code, p)
	}
}

func TestAppActions_WhoAmIForwardsBrowserCookies(t *testing.T) {
	var gotPath, gotToken string
	var gotCookies []*http.Cookie
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotToken, gotCookies = r.URL.Path, r.Header.Get("X-Hamr-Actions-Token"), r.Cookies()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"account":{"email":"jack@test.com"}}`))
	}))
	t.Cleanup(app.Close)
	mux := appActionsMux(NewAppActions(app.URL, "tok"))

	req := httptest.NewRequest(http.MethodGet, "/__hamr/app/whoami", nil)
	req.Header.Set("Cookie", "session=abc; "+gateCookie+"=secret; csrf=x")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"account":{"email":"jack@test.com"}}`, rec.Body.String())
	assert.Equal(t, "/_devactions/whoami", gotPath)
	assert.Equal(t, "tok", gotToken)
	var names []string
	for _, c := range gotCookies {
		names = append(names, c.Name)
	}
	assert.Equal(t, []string{"session", "csrf"}, names, "the gate cookie must never reach the app")

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/__hamr/app/whoami", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestAppActions_ClientIgnoresProxyEnv(t *testing.T) {
	tr, ok := appActionsClient.Transport.(*http.Transport)
	require.True(t, ok)
	assert.Nil(t, tr.Proxy, "token must not be sent via HTTP_PROXY")
}
