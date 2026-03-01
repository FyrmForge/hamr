package server

import (
	"io/fs"
	"time"

	"github.com/labstack/echo/v4"
)

// Option configures a Server.
type Option func(*Server)

// WithHost sets the bind address.
func WithHost(host string) Option {
	return func(s *Server) { s.host = host }
}

// WithPort sets the listen port. Must be between 1 and 65535.
func WithPort(port int) Option {
	return func(s *Server) { s.port = port }
}

// WithDevMode enables or disables development mode.
// In dev mode, security headers middleware is skipped.
func WithDevMode(dev bool) Option {
	return func(s *Server) { s.devMode = dev }
}

// WithMiddleware appends global middleware to the server.
func WithMiddleware(mw ...echo.MiddlewareFunc) Option {
	return func(s *Server) { s.userMiddleware = append(s.userMiddleware, mw...) }
}

// WithStaticDir serves static files from the given filesystem path at /static.
func WithStaticDir(path string) Option {
	return func(s *Server) { s.staticDir = path }
}

// WithEmbeddedStatic serves static files from an embed.FS at the given path prefix.
// The pathPrefix must not be empty.
func WithEmbeddedStatic(fsys fs.FS, pathPrefix string) Option {
	return func(s *Server) {
		s.embeddedFS = fsys
		s.embeddedPrefix = pathPrefix
	}
}

// WithErrorHandler sets a custom Echo error handler.
func WithErrorHandler(h echo.HTTPErrorHandler) Option {
	return func(s *Server) { s.errorHandler = h }
}

// WithTimeout sets the request context timeout. Must be positive.
func WithTimeout(d time.Duration) Option {
	return func(s *Server) { s.timeout = d }
}

// WithMaxBodySize sets the maximum request body size in Echo BodyLimit format
// (e.g. "2M", "500K"). Validated at construction time.
func WithMaxBodySize(size string) Option {
	return func(s *Server) { s.maxBodySize = size }
}

// WithShutdownTimeout sets the graceful shutdown timeout (default 10s). Must be positive.
func WithShutdownTimeout(d time.Duration) Option {
	return func(s *Server) { s.shutdownTimeout = d }
}
