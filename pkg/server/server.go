// Package server wraps Echo v4 with functional options, production-safe
// defaults, and graceful shutdown.
package server

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/FyrmForge/hamr/pkg/logging"
	"github.com/FyrmForge/hamr/pkg/middleware"
	"github.com/labstack/echo/v4"
	echoMw "github.com/labstack/echo/v4/middleware"
	"github.com/labstack/gommon/bytes"
)

// Server wraps an Echo instance with lifecycle management.
type Server struct {
	echo *echo.Echo

	host            string
	port            int
	devMode         bool
	noIndex         bool
	gzipConfig      GzipConfig
	timeout         time.Duration
	maxBodySize     string
	shutdownTimeout time.Duration

	trustedProxies  []string // CIDRs only; the cloudflare keyword is stripped in New
	cloudflare      bool
	ipExtractor     atomic.Pointer[echo.IPExtractor]
	schemeExtractor atomic.Pointer[echo.SchemeExtractor]

	userMiddleware []echo.MiddlewareFunc
	errorHandler   echo.HTTPErrorHandler
	staticDir      string
	staticDistDir  string
	embeddedFS     fs.FS
	embeddedPrefix string
	generatedDir   string
	staticPages    []staticRoute
}

// New creates a Server with sensible defaults, applies options, and configures
// production middleware. It returns an error if any option value is invalid.
func New(opts ...Option) (*Server, error) {
	s := &Server{
		host:            "",
		port:            8080,
		gzipConfig:      GzipConfig{Enabled: true},
		timeout:         30 * time.Second,
		maxBodySize:     "2M",
		shutdownTimeout: 10 * time.Second,
	}

	for _, o := range opts {
		o(s)
	}

	// Validate configuration.
	if s.port < 1 || s.port > 65535 {
		return nil, fmt.Errorf("server: invalid port %d: must be between 1 and 65535", s.port)
	}
	if s.timeout <= 0 {
		return nil, fmt.Errorf("server: timeout must be positive, got %v", s.timeout)
	}
	if s.shutdownTimeout <= 0 {
		return nil, fmt.Errorf("server: shutdown timeout must be positive, got %v", s.shutdownTimeout)
	}
	if _, err := bytes.Parse(s.maxBodySize); err != nil {
		return nil, fmt.Errorf("server: invalid max body size %q: %w", s.maxBodySize, err)
	}
	if s.embeddedFS != nil && s.embeddedPrefix == "" {
		return nil, fmt.Errorf("server: embedded static path prefix must not be empty")
	}
	if s.staticDistDir != "" && s.staticDir == "" {
		return nil, fmt.Errorf("server: WithStaticDistDir requires WithStaticDir")
	}

	s.trustedProxies, s.cloudflare = stripCloudflareKeyword(s.trustedProxies)
	cidrs := s.trustedProxies
	if s.cloudflare {
		cidrs = append(append([]string{}, cidrs...), CloudflareCIDRs...)
	}
	ipExtractor, err := buildIPExtractor(cidrs)
	if err != nil {
		return nil, err
	}
	s.ipExtractor.Store(&ipExtractor)
	schemeExtractor, err := buildSchemeExtractor(cidrs)
	if err != nil {
		return nil, err
	}
	s.schemeExtractor.Store(&schemeExtractor)

	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	// Controls how c.RealIP() derives the client IP, which is the default
	// rate-limit key. Without trusted proxies this ignores X-Forwarded-For,
	// closing the spoof-a-header-to-get-a-fresh-bucket bypass. Read through a
	// pointer so the Cloudflare refresh can swap it while serving.
	e.IPExtractor = func(r *http.Request) string { return (*s.ipExtractor.Load())(r) }
	// Same trust set for c.Scheme(): X-Forwarded-Proto is honoured only from the
	// configured proxies (echo's default trusts any private/loopback peer).
	e.SchemeExtractor = func(r *http.Request) string { return (*s.schemeExtractor.Load())(r) }

	// X-Robots-Tag goes in Pre so responses written by Pre middleware
	// (redirects, locale handling) and 404s carry it too.
	if s.noIndex {
		e.Pre(func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				c.Response().Header().Set("X-Robots-Tag", "noindex, nofollow")
				return next(c)
			}
		})
	}

	// Workaround: Echo v4 does not route HEAD requests to GET handlers, so
	// curl -I, uptime monitors and link checkers would get 404/405. Echo
	// routes on the *http.Request it captured in ServeHTTP and ignores
	// c.SetRequest, so a Pre-only rewrite is not enough. Instead, unless the
	// app registered its own HEAD route for the path, re-dispatch a GET copy
	// through e.ServeHTTP. The copy is marked, and the first Use middleware
	// flips its Method back to HEAD after routing, so handlers, loggers and
	// http.ServeContent see HEAD. net/http still holds the original HEAD
	// request and discards body writes itself.
	e.Pre(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			r := c.Request()
			if r.Method != http.MethodHead || hasHeadRoute(c.Echo(), r) {
				return next(c)
			}
			r2 := r.WithContext(context.WithValue(r.Context(), headRewrittenKey{}, true))
			r2.Method = http.MethodGet
			c.Echo().ServeHTTP(c.Response().Writer, r2)
			return nil
		}
	})
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if r := c.Request(); r.Context().Value(headRewrittenKey{}) != nil {
				r.Method = http.MethodHead // r is the private copy made above
			}
			return next(c)
		}
	})

	// Production defaults — applied in order.
	e.Use(echoMw.RecoverWithConfig(echoMw.RecoverConfig{
		LogErrorFunc: func(c echo.Context, err error, stack []byte) error {
			logging.FromContext(c.Request().Context()).Error("panic recovered",
				"error", err.Error(),
				"stack", string(stack),
			)
			return err
		},
	}))
	e.Use(echoMw.BodyLimit(s.maxBodySize))
	e.Use(echoMw.ContextTimeoutWithConfig(echoMw.ContextTimeoutConfig{
		Timeout: s.timeout,
	}))
	// Dev mode disables caching entirely: static URLs aren't fingerprinted
	// in dev, so a cached stylesheet would survive rebuilds for its whole
	// max-age and serve day-old styles.
	e.Use(middleware.CacheControl(s.devMode))
	if s.gzipConfig.Enabled {
		userSkip := s.gzipConfig.Skipper
		e.Use(echoMw.GzipWithConfig(echoMw.GzipConfig{
			// Never wrap protocol upgrades: the gzip writer holds the 101
			// status back across Hijack, so websockets never open.
			Skipper: func(c echo.Context) bool {
				return c.Request().Header.Get("Upgrade") != "" ||
					(userSkip != nil && userSkip(c))
			},
			Level:     s.gzipConfig.Level,
			MinLength: s.gzipConfig.MinLength,
		}))
	}
	if !s.devMode {
		e.Use(middleware.Secure())
	}

	// User-supplied middleware.
	for _, mw := range s.userMiddleware {
		e.Use(mw)
	}

	// Custom error handler.
	if s.errorHandler != nil {
		e.HTTPErrorHandler = s.errorHandler
	}

	// Static file serving.
	if s.staticDir != "" {
		if s.staticDistDir != "" {
			e.StaticFS("/static", newLayeredFS(
				os.DirFS(s.staticDistDir),
				os.DirFS(s.staticDir),
			))
		} else {
			e.Static("/static", s.staticDir)
		}
	}
	if s.embeddedFS != nil {
		e.StaticFS(s.embeddedPrefix, s.embeddedFS)
	}

	s.echo = e
	return s, nil
}

// stripCloudflareKeyword removes the "cloudflare" keyword (case-insensitive)
// from cidrs and reports whether it was present.
func stripCloudflareKeyword(cidrs []string) ([]string, bool) {
	var out []string
	found := false
	for _, c := range cidrs {
		if strings.EqualFold(strings.TrimSpace(c), cloudflareKeyword) {
			found = true
			continue
		}
		out = append(out, c)
	}
	return out, found
}

// buildIPExtractor returns the echo.IPExtractor implied by the configured
// trusted-proxy CIDRs. With none, X-Forwarded-For/X-Real-IP are ignored and
// the direct TCP peer is used (spoof-proof default). With CIDRs, only those
// ranges are trusted for X-Forwarded-For — auto-trust of loopback/link-local/
// private ranges is disabled so the trust set is exactly what was configured.
func buildIPExtractor(cidrs []string) (echo.IPExtractor, error) {
	opts, err := trustOptions(cidrs)
	if err != nil {
		return nil, err
	}
	if len(opts) == 3 {
		return echo.ExtractIPDirect(), nil
	}
	return echo.ExtractIPFromXFFHeader(opts...), nil
}

// buildSchemeExtractor is the c.Scheme() counterpart of buildIPExtractor. With
// no trusted proxies it keeps echo's default (X-Forwarded-Proto trusted only
// from loopback/link-local/private/unix peers), because PaaS edges (Railway,
// Fly) connect from private IPs and apps rarely set TRUSTED_PROXIES. With
// CIDRs, exactly that list is trusted. Unlike the client IP, a spoofed scheme
// can only mis-set a cookie Secure flag.
func buildSchemeExtractor(cidrs []string) (echo.SchemeExtractor, error) {
	opts, err := trustOptions(cidrs)
	if err != nil {
		return nil, err
	}
	if len(opts) == 3 {
		return echo.ExtractSchemeFromHeaders(), nil
	}
	return echo.ExtractSchemeFromHeaders(opts...), nil
}

// trustOptions returns the three auto-trust disablers plus one TrustIPRange per
// CIDR, so the trust set is exactly what was configured. len == 3 means no CIDRs.
func trustOptions(cidrs []string) ([]echo.TrustOption, error) {
	opts := []echo.TrustOption{
		echo.TrustLoopback(false),
		echo.TrustLinkLocal(false),
		echo.TrustPrivateNet(false),
	}
	for _, raw := range cidrs {
		cidr := strings.TrimSpace(raw)
		if cidr == "" {
			continue
		}
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, fmt.Errorf("server: invalid trusted proxy CIDR %q: %w", cidr, err)
		}
		opts = append(opts, echo.TrustIPRange(ipNet))
	}
	return opts, nil
}

// Echo returns the underlying Echo instance for direct access.
func (s *Server) Echo() *echo.Echo { return s.echo }

// Addr returns the listen address as host:port.
func (s *Server) Addr() string {
	return fmt.Sprintf("%s:%d", s.host, s.port)
}

// GET registers a GET route.
func (s *Server) GET(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) *echo.Route {
	return s.echo.GET(path, h, m...)
}

// POST registers a POST route.
func (s *Server) POST(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) *echo.Route {
	return s.echo.POST(path, h, m...)
}

// PUT registers a PUT route.
func (s *Server) PUT(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) *echo.Route {
	return s.echo.PUT(path, h, m...)
}

// DELETE registers a DELETE route.
func (s *Server) DELETE(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) *echo.Route {
	return s.echo.DELETE(path, h, m...)
}

// PATCH registers a PATCH route.
func (s *Server) PATCH(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) *echo.Route {
	return s.echo.PATCH(path, h, m...)
}

// Group creates a route group with the given prefix and optional middleware.
func (s *Server) Group(prefix string, m ...echo.MiddlewareFunc) *echo.Group {
	return s.echo.Group(prefix, m...)
}

// Start begins listening and blocks until SIGINT/SIGTERM or a listener error.
// It performs graceful shutdown when a signal is received.
func (s *Server) Start() error {
	addr := s.Addr()

	// Pre-bind the listener before starting the server.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("server: listen %s: %w", addr, err)
	}
	s.echo.Listener = ln

	if s.cloudflare {
		refreshCtx, stopRefresh := context.WithCancel(context.Background())
		defer stopRefresh()
		go s.refreshCloudflare(refreshCtx)
	}

	errCh := make(chan error, 1)
	go func() {
		if err := s.echo.Start(addr); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	// Wait for signal or listener error.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(quit)

	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("server: listener: %w", err)
		}
	case <-quit:
	}

	ctx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
	defer cancel()

	if err := s.Shutdown(ctx); err != nil {
		// Drain any listener error that arrived during shutdown.
		if listenErr := <-errCh; listenErr != nil {
			slog.Default().Error("server: listener error during shutdown", "error", listenErr)
		}
		return err
	}

	// Drain any listener error that arrived during shutdown.
	if listenErr := <-errCh; listenErr != nil {
		return fmt.Errorf("server: listener: %w", listenErr)
	}
	return nil
}

// Shutdown shuts down the Echo server gracefully.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.echo.Shutdown(ctx)
}

// headRewrittenKey marks the GET copy of a HEAD request (see New).
type headRewrittenKey struct{}

// hasHeadRoute reports whether the app registered its own HEAD route matching
// r. Find on a scratch context resolves the path to a route pattern (or to a
// 404/405/group-not-found pattern); a real HEAD route is then one whose Method
// is HEAD and Path equals that pattern. Not-found catch-alls have Method
// echo.RouteNotFound, so they never match.
func hasHeadRoute(e *echo.Echo, r *http.Request) bool {
	sc := e.NewContext(r, nil)
	e.Router().Find(http.MethodHead, echo.GetPath(r), sc)
	for _, rt := range e.Routes() {
		if rt.Method == http.MethodHead && rt.Path == sc.Path() {
			return true
		}
	}
	return false
}
