package devserver

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/FyrmForge/hamr/pkg/config"
)

// frontDoorHeaderTimeout is how long the front door waits for the upstream app
// to start responding. Staging apps are slower than the dev loop's 2s.
const frontDoorHeaderTimeout = 60 * time.Second

// buildFrontDoor builds the handlers for front-door mode (HAMR_MOCK_UPSTREAM).
//
// api is the private listener, identical to plain mode: mock API + ingest,
// HAMR_MOCK_API_ALLOW applies, never gated. public is the UI listener: the
// mock dashboards, the widget's routes and the app proxy (widget injected),
// all behind the password gate. /_devactions is never proxied: it is reachable
// only from hamr over the private network.
func buildFrontDoor(logger *slog.Logger, selected []MockProvider, upstream string) (api, public http.Handler, err error) {
	// Fail closed: the front door sits on a public domain, so an unset
	// password would publish the app, captured mail and the actions panel.
	pw := os.Getenv("HAMR_MOCK_UI_PASSWORD")
	if pw == "" {
		return nil, nil, fmt.Errorf("HAMR_MOCK_UI_PASSWORD is required when HAMR_MOCK_UPSTREAM is set")
	}
	u, err := url.Parse(upstream)
	if err != nil || u.Scheme != "http" || u.Host == "" {
		return nil, nil, fmt.Errorf("HAMR_MOCK_UPSTREAM=%q must be an http://host:port URL", upstream)
	}

	bypass := config.GetEnvCSV("HAMR_GATE_BYPASS")
	for _, b := range bypass {
		if c := path.Clean("/" + strings.TrimSpace(b)); c == "/" || c == "/__hamr" || strings.HasPrefix(c, "/__hamr/") {
			return nil, nil, fmt.Errorf("HAMR_GATE_BYPASS entry %q would open the whole site or the hamr panel", b)
		}
	}

	apiMux, uiRoutes, err := buildMocks(logger, selected)
	if err != nil {
		return nil, nil, err
	}
	api, err = restrictAPI(logger, apiMux)
	if err != nil {
		return nil, nil, err
	}

	has := func(name string) bool {
		return slices.ContainsFunc(selected, func(p MockProvider) bool { return p.Name == name })
	}
	broker := NewSSEBroker(nil, nil, nil, has("mail"), has("sms"), has("stripe"), false, false)

	notFound := http.NotFoundHandler()
	handler := newProxyHandler(u.Host, proxyOpts{
		headerTimeout:    frontDoorHeaderTimeout,
		forwardedProto:   forwardedProto(),
		keepForwardedFor: true,
		extraRoutes: func(mux *http.ServeMux) {
			mux.Handle("/__hamr/", uiRoutes)
			mux.Handle("/_devactions", notFound)
			mux.Handle("/_devactions/", notFound)
		},
	}, broker, nil, nil, nil, nil, nil, nil, nil, nil, nil, NewAppActions(upstream, os.Getenv("HAMR_ACTIONS_TOKEN")), true)

	// The mux matches the escaped path, so "/_devactions%2Fwipe" would fall
	// through to "/"; block on the decoded, cleaned path instead.
	proxied := handler
	handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := path.Clean("/" + r.URL.Path); p == "/_devactions" || strings.HasPrefix(p, "/_devactions/") {
			http.NotFound(w, r)
			return
		}
		proxied.ServeHTTP(w, r)
	})
	handler = NewGate(pw, bypass, baseURLIsHTTPS(), handler)
	logger.Info("front door proxying", "upstream", upstream)
	return api, handler, nil
}

// forwardedProto is the scheme of HAMR_BASE_URL ("" when unset), which the
// front door reports to the app as X-Forwarded-Proto.
func forwardedProto() string {
	u, err := url.Parse(os.Getenv("HAMR_BASE_URL"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return u.Scheme
}
