// Package server wraps Echo v4 with functional options, production-safe
// defaults, and lifecycle hooks.
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
	"syscall"
	"time"

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
	timeout         time.Duration
	maxBodySize     string
	shutdownTimeout time.Duration

	userMiddleware []echo.MiddlewareFunc
	errorHandler   echo.HTTPErrorHandler
	staticDir      string
	embeddedFS     fs.FS
	embeddedPrefix string

	onServerStart   []HookFunc
	onShutdown      []HookFunc
	onBeforeMigrate []HookFunc
	onAfterMigrate  []HookFunc
}

// New creates a Server with sensible defaults, applies options, and configures
// production middleware. It returns an error if any option value is invalid.
func New(opts ...Option) (*Server, error) {
	s := &Server{
		host:            "",
		port:            8080,
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

	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	// Production defaults — applied in order.
	e.Use(echoMw.Recover())
	e.Use(echoMw.BodyLimit(s.maxBodySize))
	e.Use(echoMw.ContextTimeoutWithConfig(echoMw.ContextTimeoutConfig{
		Timeout: s.timeout,
	}))
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
		e.Static("/static", s.staticDir)
	}
	if s.embeddedFS != nil {
		e.StaticFS(s.embeddedPrefix, s.embeddedFS)
	}

	s.echo = e
	return s, nil
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
// It runs on-start hooks after the listener is up and on-shutdown hooks during
// graceful shutdown.
func (s *Server) Start() error {
	addr := s.Addr()

	// Pre-bind the listener so on-start hooks can safely connect to the server.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("server: listen %s: %w", addr, err)
	}
	s.echo.Listener = ln

	errCh := make(chan error, 1)
	go func() {
		if err := s.echo.Start(addr); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	// Run on-server-start hooks — listener is guaranteed ready.
	if err := runHooks(context.Background(), s.onServerStart); err != nil {
		ctx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
		defer cancel()
		_ = s.Shutdown(ctx)
		return fmt.Errorf("server: on-start hook: %w", err)
	}

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

// Shutdown runs on-shutdown hooks (logging errors but continuing) and then
// shuts down the Echo server.
func (s *Server) Shutdown(ctx context.Context) error {
	for _, fn := range s.onShutdown {
		if err := fn(ctx); err != nil {
			slog.Default().Error("server: shutdown hook error", "error", err)
		}
	}
	return s.echo.Shutdown(ctx)
}
