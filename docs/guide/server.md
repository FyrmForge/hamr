# Server — Echo Wrapper with Lifecycle Hooks

`hamr/pkg/server` wraps Echo v4 with functional options, production-safe defaults, and
lifecycle hooks. Handles graceful shutdown on SIGINT/SIGTERM.

## Quick Start

```go
import "github.com/FyrmForge/hamr/pkg/server"
```

## Creating a Server

```go
srv, err := server.New(
    server.WithPort(8080),
    server.WithDevMode(true),
)
if err != nil {
    log.Fatal(err)
}

srv.GET("/", homeHandler)
srv.GET("/health", healthHandler)

if err := srv.Start(); err != nil {
    log.Fatal(err)
}
```

`Start` blocks until SIGINT/SIGTERM or a listener error.

## Options

| Option | Default | Description |
|--------|---------|-------------|
| `WithHost(host)` | `""` | Bind address |
| `WithPort(port)` | 8080 | Listen port (1-65535) |
| `WithDevMode(bool)` | `false` | Skips security headers in dev mode |
| `WithMiddleware(mw...)` | — | Append global middleware |
| `WithStaticDir(path)` | — | Serve static files from filesystem at `/static` |
| `WithEmbeddedStatic(fs, prefix)` | — | Serve static files from `embed.FS` |
| `WithErrorHandler(h)` | — | Custom Echo error handler |
| `WithTimeout(d)` | 30s | Request context timeout |
| `WithMaxBodySize(size)` | `"2M"` | Max request body (`"500K"`, `"2M"`, etc.) |
| `WithShutdownTimeout(d)` | 10s | Graceful shutdown timeout |

## Production Defaults

Enabled unless overridden:

- Panic recovery (`middleware.Recover()`)
- Request timeout: 30s
- Max request body: 2MB
- Security headers (dev mode disables these):
  - `X-Content-Type-Options: nosniff`
  - `X-Frame-Options: DENY`
  - `Referrer-Policy: strict-origin-when-cross-origin`
  - `Content-Security-Policy: default-src 'self'`

## Route Groups

```go
site := srv.Group("", sessionMiddleware, csrfMiddleware)
site.GET("/", homeHandler)
site.GET("/login", loginHandler)

api := srv.Group("/api", corsMiddleware, rateLimitMiddleware)
api.GET("/users", listUsersHandler)
api.POST("/users", createUserHandler)
```

## Lifecycle Hooks

Register callbacks for server events:

```go
srv, err := server.New(
    server.WithPort(8080),
    server.WithOnServerStart(func(ctx context.Context) error {
        log.Println("Server started")
        return nil
    }),
    server.WithOnShutdown(func(ctx context.Context) error {
        log.Println("Shutting down...")
        return nil
    }),
)
```

### Migration hooks

Run hooks before/after database migrations:

```go
srv, err := server.New(
    server.WithOnBeforeMigrate(func(ctx context.Context) error {
        log.Println("Running migrations...")
        return nil
    }),
    server.WithOnAfterMigrate(func(ctx context.Context) error {
        log.Println("Migrations complete")
        return nil
    }),
)

// Trigger migration hooks manually
srv.RunBeforeMigrate(ctx)
db.Migrate(database, migrateCfg)
srv.RunAfterMigrate(ctx)
```

### Hook execution

- **OnServerStart**: runs after the listener is up
- **OnShutdown**: runs during graceful shutdown, errors are logged but don't stop shutdown
- **OnBeforeMigrate / OnAfterMigrate**: called explicitly by the project, stop on first error

## Escape Hatch

Access the underlying Echo instance for anything not covered by the wrapper:

```go
e := srv.Echo()
e.Validator = myValidator
e.IPExtractor = echo.ExtractIPFromXFFHeader()
```

## Typical Usage

```go
package main

import (
    "github.com/FyrmForge/hamr/pkg/config"
    "github.com/FyrmForge/hamr/pkg/server"

    _ "github.com/joho/godotenv/autoload"
)

var (
    envPort    = config.GetEnvOrDefaultInt("PORT", 8080)
    envDevMode = config.GetEnvOrDefaultBool("DEV_MODE", false)
)

func main() {
    srv, err := server.New(
        server.WithPort(envPort),
        server.WithDevMode(envDevMode),
        server.WithTimeout(30*time.Second),
        server.WithOnShutdown(func(ctx context.Context) error {
            database.Close()
            return nil
        }),
    )
    if err != nil {
        log.Fatal(err)
    }

    // Register routes
    srv.GET("/health", healthHandler)

    site := srv.Group("", middleware.Flash(), middleware.CSRF())
    site.GET("/", homeHandler)

    api := srv.Group("/api", middleware.CORS())
    api.GET("/users", listUsersHandler)

    if err := srv.Start(); err != nil {
        log.Fatal(err)
    }
}
```

## API Reference

```go
// Server
type Server struct { ... }
func New(opts ...Option) (*Server, error)
func (s *Server) Echo() *echo.Echo
func (s *Server) Addr() string
func (s *Server) Start() error
func (s *Server) Shutdown(ctx context.Context) error

// Routes
func (s *Server) GET(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) *echo.Route
func (s *Server) POST(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) *echo.Route
func (s *Server) PUT(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) *echo.Route
func (s *Server) DELETE(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) *echo.Route
func (s *Server) PATCH(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) *echo.Route
func (s *Server) Group(prefix string, m ...echo.MiddlewareFunc) *echo.Group

// Options
type Option func(*Server)
func WithHost(host string) Option
func WithPort(port int) Option
func WithDevMode(dev bool) Option
func WithMiddleware(mw ...echo.MiddlewareFunc) Option
func WithStaticDir(path string) Option
func WithEmbeddedStatic(fsys fs.FS, pathPrefix string) Option
func WithErrorHandler(h echo.HTTPErrorHandler) Option
func WithTimeout(d time.Duration) Option
func WithMaxBodySize(size string) Option
func WithShutdownTimeout(d time.Duration) Option

// Hooks
type HookFunc func(ctx context.Context) error
func WithOnServerStart(fn HookFunc) Option
func WithOnShutdown(fn HookFunc) Option
func WithOnBeforeMigrate(fn HookFunc) Option
func WithOnAfterMigrate(fn HookFunc) Option
func (s *Server) RunBeforeMigrate(ctx context.Context) error
func (s *Server) RunAfterMigrate(ctx context.Context) error
```
