package devserver

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/FyrmForge/hamr/pkg/config"
)

// Mock serve is the headless entry point: it stands up the dev mocks (mail,
// sms, stripe) on plain listeners with no proxy, TUI, build, or watch — built for
// running in a dedicated container in a dev environment. All configuration
// comes from environment variables (the container's natural config surface);
// there is no hamr.toml dependency.
//
// Two listeners:
//   - the app-facing port (HAMR_MOCK_PORT): stripe /v1/* + /v2/* API + mail/sms ingest sinks
//   - the UI port (HAMR_MOCK_UI_PORT): the human dashboards. Optional — when
//     unset the UI mounts on the app-facing port too (single listener).
//
// Splitting the ports lets a dev environment expose the app-facing surface to
// the app while keeping the dashboards (captured email, fake payments) on a
// port it binds to loopback or firewalls separately.
//
// Both listeners bind all interfaces by default (correct for a container,
// where sibling containers reach it by service name). Set HAMR_MOCK_BIND to
// restrict the bind host (e.g. 127.0.0.1) when running on a shared host.
//
// Two optional gates for when the ports must be reachable beyond the compose
// network:
//   - HAMR_MOCK_UI_PASSWORD: HTTP basic auth on every /__hamr/* dashboard
//     route (any username, this password). The dashboards expose captured
//     emails (reset tokens, magic links) and a signed-webhook trigger.
//   - HAMR_MOCK_API_ALLOW: comma-separated IPs/CIDRs allowed to reach the
//     app-facing listener. Checked against the TCP peer only — forwarded
//     headers are spoofable and ignored — so behind a proxy keep the port
//     unpublished and reach it over the private network instead.

// MountedMock is what a provider returns: the route registrations for each
// surface. Either may be nil if a mock has no routes on that surface.
type MountedMock struct {
	RegisterAPI func(*http.ServeMux) // app-facing (stripe /v1 + /v2, mail/sms ingest)
	RegisterUI  func(*http.ServeMux) // human-facing dashboards
}

// MockProvider registers one mock. Each provider reads its own HAMR_* env
// vars in Build. Adding a new mock is one entry in mockProviders.
type MockProvider struct {
	Name  string
	Build func(logger *slog.Logger) (*MountedMock, error)
}

var mockProviders = []MockProvider{
	{Name: "mail", Build: buildMailMock},
	{Name: "sms", Build: buildSMSMock},
	{Name: "stripe", Build: buildStripeMock},
}

func buildMailMock(logger *slog.Logger) (*MountedMock, error) {
	var maxBytes int64
	if v := os.Getenv("HAMR_MAIL_MAX_MESSAGE_BYTES"); v != "" {
		// ParseInt (not Atoi) so a multi-GiB cap survives on 32-bit builds.
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			maxBytes = n
		} else {
			logger.Warn("invalid HAMR_MAIL_MAX_MESSAGE_BYTES, using 10MiB default", "value", v, "err", err)
		}
	}
	m := NewMailMock(MailMockOptions{
		MaxMessages:     config.GetEnvOrDefaultInt("HAMR_MAIL_MAX_MESSAGES", 0),
		MaxMessageBytes: maxBytes,
		PersistPath:     os.Getenv("HAMR_MAIL_PERSIST_PATH"),
		OnPersistError: func(err error) {
			logger.Warn("mail mock persistence error", "err", err)
		},
	})
	return &MountedMock{
		RegisterAPI: m.RegisterIngestRoutes,
		RegisterUI:  m.RegisterUIRoutes,
	}, nil
}

func buildSMSMock(logger *slog.Logger) (*MountedMock, error) {
	m := NewSMSMock(SMSMockOptions{
		MaxMessages: config.GetEnvOrDefaultInt("HAMR_SMS_MAX_MESSAGES", 0),
		PersistPath: os.Getenv("HAMR_SMS_PERSIST_PATH"),
		OnPersistError: func(err error) {
			logger.Warn("sms mock persistence error", "err", err)
		},
	})
	return &MountedMock{
		RegisterAPI: m.RegisterIngestRoutes,
		RegisterUI:  m.RegisterUIRoutes,
	}, nil
}

func buildStripeMock(logger *slog.Logger) (*MountedMock, error) {
	baseURL := os.Getenv("HAMR_STRIPE_BASE_URL")
	if baseURL == "" {
		// No sensible default in a container: localhost:<port> is not
		// reachable from the host (published port differs) or sibling
		// containers (need the service name). Force the caller to state it.
		return nil, errors.New("HAMR_STRIPE_BASE_URL is required when stripe is selected (browser-reachable origin of the mock's UI, e.g. http://localhost:4501)")
	}
	webhookURL := os.Getenv("HAMR_STRIPE_WEBHOOK_URL")
	webhookSecret := os.Getenv("HAMR_STRIPE_WEBHOOK_SECRET")
	if webhookURL == "" {
		return nil, errors.New("HAMR_STRIPE_WEBHOOK_URL is required when stripe is selected (your app's webhook handler, e.g. http://app:8080/api/webhooks/stripe)")
	}
	if webhookSecret == "" {
		return nil, errors.New("HAMR_STRIPE_WEBHOOK_SECRET is required when stripe is selected (must match your app's STRIPE_WEBHOOK_SECRET)")
	}
	m := NewStripeMock(StripeMockOptions{
		BaseURL:     baseURL,
		Logger:      logger,
		PersistPath: os.Getenv("HAMR_STRIPE_PERSIST_PATH"),
		OnPersistError: func(err error) {
			logger.Warn("stripe mock persistence error", "err", err)
		},
	})
	m.SetWebhookEndpoint(WebhookEndpoint{
		URL:     webhookURL,
		ThinURL: os.Getenv("HAMR_STRIPE_THIN_WEBHOOK_URL"), // optional: v2 thin events
		Secret:  webhookSecret,
	})
	return &MountedMock{
		RegisterAPI: m.RegisterAPIRoutes,
		RegisterUI:  m.RegisterUIRoutes,
	}, nil
}

// RunMockServe stands up the selected mocks and serves until ctx is cancelled.
// Selection and all configuration come from environment variables.
func RunMockServe(ctx context.Context, logger *slog.Logger) error {
	if logger == nil {
		logger = newDevLogger(os.Stderr, false)
	}

	selected, err := selectedMocks(config.GetEnvCSV("HAMR_MOCKS"))
	if err != nil {
		return err
	}

	bind := os.Getenv("HAMR_MOCK_BIND") // "" = all interfaces
	port, err := parsePort("HAMR_MOCK_PORT", 4500)
	if err != nil {
		return err
	}
	// UI port: unset → 0, meaning "share the app-facing port". A set value is
	// validated strictly, so a typo errors loudly instead of silently sharing.
	uiPort, err := parsePort("HAMR_MOCK_UI_PORT", 0)
	if err != nil {
		return err
	}
	if uiPort != 0 && uiPort == port {
		return fmt.Errorf("HAMR_MOCK_UI_PORT (%d) must differ from HAMR_MOCK_PORT; leave it unset to serve the UI on the app-facing port", port)
	}

	apiHandler, uiHandler, err := buildMockHandlers(logger, selected, uiPort != 0)
	if err != nil {
		return err
	}

	servers := []*http.Server{newMockServer(bind, port, apiHandler)}
	logger.Info("mock server listening", "addr", servers[0].Addr, "surface", "api+ingest")
	if uiPort != 0 {
		ui := newMockServer(bind, uiPort, uiHandler)
		servers = append(servers, ui)
		logger.Info("mock server listening", "addr", ui.Addr, "surface", "ui")
	} else {
		logger.Info("mock UI mounted on app-facing port", "addr", servers[0].Addr)
	}
	defer shutdownAll(servers, logger)

	errCh := make(chan error, len(servers))
	for _, s := range servers {
		go func(s *http.Server) {
			if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- fmt.Errorf("listen %s: %w", s.Addr, err)
			}
		}(s)
	}

	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		return err
	}
}

// buildMockHandlers builds the selected mocks and returns the handler for each
// listener, with the env-configured gates applied. split=false means one
// shared listener: ui is nil and everything is served by api.
//
// UI routes go on their own mux mounted under /__hamr/ so one handler can
// gate them all. On a shared port the ingest sinks (/__hamr/mail/ingest,
// /__hamr/sms/ingest) are exact patterns on the API mux and win over the
// prefix, so they stay open to the app.
func buildMockHandlers(logger *slog.Logger, selected []MockProvider, split bool) (api, ui http.Handler, err error) {
	apiMux := http.NewServeMux()
	uiMux := apiMux
	if split {
		uiMux = http.NewServeMux()
	}
	uiRoutes := http.NewServeMux()

	for _, p := range selected {
		mounted, berr := p.Build(logger)
		if berr != nil {
			return nil, nil, fmt.Errorf("%s mock: %w", p.Name, berr)
		}
		if mounted.RegisterAPI != nil {
			mounted.RegisterAPI(apiMux)
		}
		if mounted.RegisterUI != nil {
			mounted.RegisterUI(uiRoutes)
		}
		logger.Info("mock enabled", "name", p.Name)
	}

	var uiGated http.Handler = uiRoutes
	if pw := os.Getenv("HAMR_MOCK_UI_PASSWORD"); pw != "" {
		uiGated = requirePassword(pw, uiRoutes)
		logger.Info("mock UI password protected")
	}
	uiMux.Handle("/__hamr/", uiGated)

	api = apiMux
	if allow := config.GetEnvCSV("HAMR_MOCK_API_ALLOW"); len(allow) > 0 {
		prefixes, perr := parsePrefixes(allow)
		if perr != nil {
			return nil, nil, fmt.Errorf("HAMR_MOCK_API_ALLOW: %w", perr)
		}
		api = allowPeers(logger, prefixes, apiMux)
		logger.Info("mock API restricted to peers", "allow", allow)
	}
	if split {
		ui = uiMux
	}
	return api, ui, nil
}

// requirePassword gates next behind HTTP basic auth. The username is ignored;
// only the password is compared (constant-time).
func requirePassword(password string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, got, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(got), []byte(password)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="hamr mocks", charset="UTF-8"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// allowPeers rejects requests whose TCP peer is outside the given prefixes.
// It deliberately ignores X-Forwarded-For and friends: they are client-set.
// Rejections are logged with the peer address so a deploy whose app shows up
// from an unexpected range (e.g. an IPv6 private network) is debuggable.
func allowPeers(logger *slog.Logger, prefixes []netip.Prefix, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ap, err := netip.ParseAddrPort(r.RemoteAddr); err == nil {
			// Unmap: ::ffff:10.0.0.5 → 10.0.0.5 so v4 CIDRs match on a dual-stack
			// listener. Drop the zone: Prefix.Contains is always false for a
			// zoned (link-local) address.
			addr := ap.Addr().Unmap().WithZone("")
			for _, p := range prefixes {
				if p.Contains(addr) {
					next.ServeHTTP(w, r)
					return
				}
			}
		}
		logger.Warn("mock API request rejected: peer not in HAMR_MOCK_API_ALLOW", "remote", r.RemoteAddr, "path", r.URL.Path)
		http.Error(w, "forbidden", http.StatusForbidden)
	})
}

// parsePrefixes accepts CIDRs ("10.0.0.0/8") and bare addresses ("10.0.0.5",
// treated as a single-host prefix).
func parsePrefixes(items []string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(items))
	for _, item := range items {
		if p, err := netip.ParsePrefix(item); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(item)
		if err != nil {
			return nil, fmt.Errorf("%q is not an IP or CIDR", item)
		}
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

// mockReadHeaderTimeout and mockIdleTimeout match the values in serveProxy —
// keep them in sync if either changes.
const (
	mockReadHeaderTimeout = 10 * time.Second
	mockIdleTimeout       = 120 * time.Second
)

func newMockServer(bind string, port int, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              fmt.Sprintf("%s:%d", bind, port),
		Handler:           handler,
		ReadHeaderTimeout: mockReadHeaderTimeout,
		IdleTimeout:       mockIdleTimeout,
	}
}

func shutdownAll(servers []*http.Server, logger *slog.Logger) {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for _, s := range servers {
		wg.Add(1)
		go func(s *http.Server) {
			defer wg.Done()
			if err := s.Shutdown(shutdownCtx); err != nil {
				logger.Warn("mock server shutdown", "addr", s.Addr, "err", err)
			}
		}(s)
	}
	wg.Wait()
}

// parsePort reads a TCP port from key. Unset/empty returns def unchanged (so a
// 0 default can act as a sentinel); a set value must be a valid 1-65535 port or
// it errors — a misconfigured container fails loudly instead of binding the
// wrong port or an OS-assigned ephemeral one the app can't find.
func parsePort(key string, def int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s=%q is not a valid port number", key, v)
	}
	if n < 1 || n > 65535 {
		return 0, fmt.Errorf("%s=%d is out of range (must be 1-65535)", key, n)
	}
	return n, nil
}

// selectedMocks maps the requested names to the matching providers, preserving
// registry order. Unknown names are an error so a typo fails loudly rather than
// silently starting nothing.
func selectedMocks(names []string) ([]MockProvider, error) {
	want := map[string]bool{}
	for _, name := range names {
		want[name] = true
	}
	if len(want) == 0 {
		return nil, errors.New("HAMR_MOCKS is empty: set it to a comma-separated list, e.g. HAMR_MOCKS=mail,sms,stripe")
	}

	var out []MockProvider
	for _, p := range mockProviders {
		if want[p.Name] {
			out = append(out, p)
			delete(want, p.Name)
		}
	}
	if len(want) > 0 {
		unknown := make([]string, 0, len(want))
		for name := range want {
			unknown = append(unknown, name)
		}
		return nil, fmt.Errorf("HAMR_MOCKS has unknown mock(s): %s (known: %s)", strings.Join(unknown, ", "), knownMockNames())
	}
	return out, nil
}

func knownMockNames() string {
	names := make([]string, len(mockProviders))
	for i, p := range mockProviders {
		names[i] = p.Name
	}
	return strings.Join(names, ", ")
}
