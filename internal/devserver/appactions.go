package devserver

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	appActionsPath = "/__hamr/app/actions"
	appWhoAmIPath  = "/__hamr/app/whoami"
	// appActionsTokenHeader carries the shared secret to the app's /_devactions.
	appActionsTokenHeader = "X-Hamr-Actions-Token"
	maxActionBody         = 1 << 20
	maxActionResponse     = 4 << 20
)

// appActionsClient never follows redirects (the token must not leak to a
// Location) and waits long enough for a slow reseed. Its transport never uses
// HTTP_PROXY: the token goes straight to the local app, not a corporate proxy.
var appActionsClient = &http.Client{
	Transport: &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 2 * time.Second, KeepAlive: 30 * time.Second}).DialContext(ctx, network, addr)
		},
		IdleConnTimeout: 30 * time.Second,
		MaxIdleConns:    10,
	},
	Timeout:       60 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// AppActions re-exposes the app's /_devactions endpoint to the widget under
// /__hamr/app/actions, adding the shared token. Methods are nil-safe.
type AppActions struct {
	base  string // http://host:port, no trailing slash
	token string
}

// NewAppActions returns nil when either argument is empty. appBaseURL may be
// "host:port", ":port" or a full URL.
func NewAppActions(appBaseURL, token string) *AppActions {
	if appBaseURL == "" || token == "" {
		return nil
	}
	if !strings.Contains(appBaseURL, "://") {
		appBaseURL = "http://" + normalizeHost(appBaseURL)
	}
	return &AppActions{base: strings.TrimRight(appBaseURL, "/"), token: token}
}

// RegisterRoutes mounts /__hamr/app/actions on mux. With no AppActions the
// routes stay absent, so the widget sees a 404 and hides its actions section.
func (a *AppActions) RegisterRoutes(mux *http.ServeMux) {
	if a == nil {
		return
	}
	mux.HandleFunc(appActionsPath, guardUnsafe(a.handleList))
	mux.HandleFunc(appActionsPath+"/", guardUnsafe(a.handleRun))
	mux.HandleFunc(appWhoAmIPath, a.handleWhoAmI)
}

// handleWhoAmI asks the app who the browser is logged in as. hamr sits on the
// traffic, so it hands the browser's own cookies to the app (over the private
// network, with the token); the app resolves its session cookie. hamr's gate
// cookie is never forwarded.
func (a *AppActions) handleWhoAmI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var cookies []*http.Cookie
	for _, c := range r.Cookies() {
		if c.Name != gateCookie {
			cookies = append(cookies, c)
		}
	}
	a.forwardWith(w, r, http.MethodGet, "/_devactions/whoami", nil, cookies)
}

func (a *AppActions) handleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	a.forward(w, r, http.MethodGet, "/_devactions", nil)
}

func (a *AppActions) handleRun(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, appActionsPath+"/")
	if name == "" || name == "." || name == ".." || strings.Contains(name, "/") {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxActionBody))
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body too large"})
		return
	}
	a.forward(w, r, http.MethodPost, "/_devactions/"+url.PathEscape(name), body)
}

// forward sends the request upstream and relays status, Content-Type and body.
func (a *AppActions) forward(w http.ResponseWriter, r *http.Request, method, path string, body []byte) {
	a.forwardWith(w, r, method, path, body, nil)
}

func (a *AppActions) forwardWith(w http.ResponseWriter, r *http.Request, method, path string, body []byte, cookies []*http.Cookie) {
	req, err := http.NewRequestWithContext(r.Context(), method, a.base+path, bytes.NewReader(body))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "app actions unavailable"})
		return
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	req.Header.Set(appActionsTokenHeader, a.token)
	if ct := r.Header.Get("Content-Type"); ct != "" && body != nil {
		req.Header.Set("Content-Type", ct)
	}
	resp, err := appActionsClient.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "app actions unavailable"})
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, maxActionResponse))
}
