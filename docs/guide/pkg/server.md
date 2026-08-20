# Server — Echo Wrapper

`hamr/pkg/server` wraps Echo v4 with functional options, production-safe defaults, and
graceful shutdown on SIGINT/SIGTERM.

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
| `WithDevMode(bool)` | `false` | Dev mode: skips security headers and disables all response caching (`no-cache, no-store, must-revalidate` on every response — static URLs aren't fingerprinted in dev, so cached assets would go stale across rebuilds) |
| `WithMiddleware(mw...)` | — | Append global middleware |
| `WithStaticDir(path)` | — | Serve static files from filesystem at `/static` |
| `WithStaticDistDir(path)` | — | Dist directory takes priority over static dir (layered serving) |
| `WithEmbeddedStatic(fs, prefix)` | — | Serve static files from `embed.FS` |
| `WithErrorHandler(h)` | — | Custom Echo error handler |
| `WithTimeout(d)` | 30s | Request context timeout |
| `WithMaxBodySize(size)` | `"2M"` | Max request body (`"500K"`, `"2M"`, etc.) |
| `WithShutdownTimeout(d)` | 10s | Graceful shutdown timeout |
| `WithGeneratedDir(dir)` | — | Serve pre-rendered static pages from directory |
| `WithTrustedProxies(cidrs...)` | — (direct) | Proxy CIDRs trusted to set `X-Forwarded-For`; unset ignores it so `RealIP()` can't be spoofed |

## Trusted Proxies & Client IP

`c.RealIP()` is the default rate-limit key and the source for client-IP audit
logging, so trusting the wrong source lets a client spoof its IP and evade
limits. The server therefore defaults to the **direct TCP peer** and **ignores**
`X-Forwarded-For` / `X-Real-IP` — safe, but behind a reverse proxy or load
balancer every request then looks like it came from the proxy.

To recover the real client IP, tell the server which upstream hops to trust:

```go
server.WithTrustedProxies("10.0.0.0/8") // or via the TRUSTED_PROXIES env var (scaffold default)
```

Only the listed ranges are trusted (loopback/link-local/private auto-trust is
disabled), and `RealIP()` returns the **left-most untrusted hop** in
`X-Forwarded-For` — i.e. the actual client. Scaffolded apps wire this from the
`TRUSTED_PROXIES` env var (comma-separated CIDRs); an existing app adds the
`WithTrustedProxies` option itself.

**This is opt-in: behind a proxy you MUST set it, to your proxy's range.**

| Deployment | `TRUSTED_PROXIES` |
|---|---|
| nginx / Caddy on the same host | `127.0.0.1/32` — loopback is *not* auto-trusted |
| Docker Compose behind a proxy container | the proxy's network subnet, e.g. `172.16.0.0/12` |
| Self-hosted Traefik | the network Traefik runs on (its Docker subnet / host IP) — you control it |
| AWS ALB / GCP LB | the VPC/subnet CIDR the LB sits in |
| Kubernetes ingress | the ingress-controller / node pod CIDR |
| Managed PaaS (Railway, Fly, Render) | the platform's internal proxy range — often a private CIDR you don't control and that can change; **verify it** (below) |

**Two non-obvious requirements:**

1. **Your proxy must actually emit `X-Forwarded-For`.** If it doesn't, the CIDR
   config does nothing and you still see the proxy IP. nginx needs
   `proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;`. Traefik and
   most cloud LBs add it by default.
2. **Never use `0.0.0.0/0`.** Trusting all sources re-enables spoofing — any
   client can forge `X-Forwarded-For`. Trust *only* your proxy's actual range.

**Verify before relying on it.** Add a throwaway handler, hit it through the
proxy from a known client (e.g. your phone on cellular), and read the values:

```go
e.GET("/__debug/ip", func(c echo.Context) error {
    return c.JSON(200, map[string]string{
        "remote_addr": c.Request().RemoteAddr,                  // direct peer → the CIDR to trust
        "x_forwarded": c.Request().Header.Get("X-Forwarded-For"),
        "real_ip":     c.RealIP(),                              // what the framework resolved
    })
})
```

`remote_addr`'s IP is the range to put in `TRUSTED_PROXIES`; once configured,
`real_ip` should equal your real client IP. Remove the handler afterward.

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
func WithStaticDistDir(path string) Option
func WithEmbeddedStatic(fsys fs.FS, pathPrefix string) Option
func WithErrorHandler(h echo.HTTPErrorHandler) Option
func WithTimeout(d time.Duration) Option
func WithMaxBodySize(size string) Option
func WithShutdownTimeout(d time.Duration) Option
func WithGeneratedDir(dir string) Option
func WithTrustedProxies(cidrs ...string) Option

// Static generation
func (s *Server) StaticPage(path string, handler echo.HandlerFunc)
func (s *Server) GenerateStatic(dir string) error

```
