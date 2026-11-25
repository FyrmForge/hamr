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
| `WithNoIndex(bool)` | `false` | Adds `X-Robots-Tag: noindex, nofollow` to every response (HTML, images, PDFs, static files, errors, 404s, Pre-middleware redirects). For staging/preview deploys; the scaffold wires it to `NOINDEX` |
| `WithTrustedProxies(cidrs...)` | — (direct) | Proxy CIDRs trusted to set `X-Forwarded-For` and `X-Forwarded-Proto` (unset: `Scheme()` trusts private/loopback peers only); unset ignores it so `RealIP()` can't be spoofed. `"cloudflare"` adds Cloudflare's ranges, refreshed every 24h. `"railway"` is for apps on Railway: client IP from Railway's `X-Real-IP` ([details](#railway)) |
| `WithOriginSecret(cfg)` | off | Refuse (empty 403) every request without the shared secret header your CDN/proxy adds. Empty `Secret` = off ([details](#origin-lock)) |

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

`c.Scheme()` (`e.SchemeExtractor`) follows the same list when you set one:
`X-Forwarded-Proto` is honoured only from the listed ranges. With no
`WithTrustedProxies` it keeps Echo's default, trusting `X-Forwarded-Proto` only
from loopback, link-local and private-network (RFC 1918, `fc00::/7`) peers
(and unix sockets). Railway's proxy connects from `100.64.0.0/10`, which is not
one of those, so on Railway `Scheme()` needs the `railway` keyword (or that
range) to report `https`. A public proxy (e.g. Cloudflare) must be listed,
otherwise `Scheme()` is `"http"` and the `flags` anonymous cookie is not marked
`Secure`.

**This is opt-in: behind a proxy you MUST set it, to your proxy's range.**

| Deployment | `TRUSTED_PROXIES` |
|---|---|
| nginx / Caddy on the same host | `127.0.0.1/32` — loopback is *not* auto-trusted |
| Cloudflare proxy (orange cloud) | `cloudflare` — see [Cloudflare](#cloudflare) |
| Cloudflare Tunnel (`cloudflared`) / ngrok on the same host | `127.0.0.1/32` — traffic arrives from the tunnel daemon, not Cloudflare's edge (use its container IP/subnet if it runs in another container). Unset, every visitor is `127.0.0.1` and shares one rate-limit bucket |
| Docker Compose behind a proxy container | the proxy's network subnet, e.g. `172.16.0.0/12` |
| Self-hosted Traefik | the network Traefik runs on (its Docker subnet / host IP) — you control it |
| AWS ALB / GCP LB | the VPC/subnet CIDR the LB sits in |
| Kubernetes ingress | the ingress-controller / node pod CIDR |
| Railway | `railway` — a CIDR list can't recover the visitor there, see [Railway](#railway) |
| Railway behind Cloudflare | `railway,cloudflare` — see [Railway](#railway) |
| Other managed PaaS (Fly, Render, Heroku) | unverified: the platform's internal proxy range, which you often don't control and which can change; **verify it** (below). If the platform delivers the client in a single header rather than a walkable `X-Forwarded-For`, this advice cannot work; set `e.IPExtractor` yourself, see [Escape Hatch](#escape-hatch) |

**Two non-obvious requirements:**

1. **Your proxy must actually emit `X-Forwarded-For`.** If it doesn't, the CIDR
   config does nothing and you still see the proxy IP. nginx needs
   `proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;`. Traefik and
   most cloud LBs add it by default.
2. **Never use `0.0.0.0/0`.** Trusting all sources re-enables spoofing — any
   client can forge `X-Forwarded-For`. Trust *only* your proxy's actual range.

### Cloudflare

Behind Cloudflare's proxy, the peer is a Cloudflare edge IP. When Cloudflare
connects to the app directly (a VPS, or nginx listed alongside it), add the
`cloudflare` keyword. If something else sits between Cloudflare and the app
(Railway, say), `cloudflare` alone is not enough; see [Railway](#railway):

```go
server.WithTrustedProxies("cloudflare") // or TRUSTED_PROXIES=cloudflare
```

It trusts `server.CloudflareCIDRs` (Cloudflare's list, pinned at release) from
startup, and `Start()` refreshes it from
`https://api.cloudflare.com/client/v4/ips` right away and then every 24h. A
failed or implausible fetch (error status, bad JSON, empty IPv4/IPv6 list,
invalid CIDR, range wider than `/8` IPv4 or `/16` IPv6) logs a warning and keeps
the current list. The server needs outbound HTTPS for the refresh.

No outbound access? Pass the pinned list directly — it's static and never
fetched, so you update hamr to pick up changes:

```go
server.WithTrustedProxies(server.CloudflareCIDRs...)
```

Only trust the header Cloudflare forwards via this list — never read
`CF-Connecting-IP` on its own; anyone who can reach your origin directly can
forge it.

**Stacked proxies** (client → Cloudflare → your nginx → app): trust both hops,
e.g. `TRUSTED_PROXIES=cloudflare,127.0.0.1/32`. `RealIP()` walks
`X-Forwarded-For` right to left, skipping trusted hops. Your proxy must
**append** to the header (nginx `$proxy_add_x_forwarded_for`), not overwrite it
(`$remote_addr`) — overwriting drops the client and every request resolves to a
Cloudflare edge IP.

### Railway

Railway's proxy connects from `100.64.0.0/10` and rewrites `X-Forwarded-For`
to `<address that connected to Railway's edge>, <Railway edge address>`; the
edge addresses are unpublished, so no CIDR list recovers the visitor from it. Use the `railway`
keyword (case-insensitive, combinable with CIDRs and `cloudflare`):

```go
server.WithTrustedProxies("railway")               // or TRUSTED_PROXIES=railway
server.WithTrustedProxies("railway", "cloudflare") // Cloudflare in front of Railway
```

- If the direct peer is in `100.64.0.0/10`, the client IP is the `X-Real-IP`
  header. A missing, unparseable or repeated (several values) `X-Real-IP`
  yields the peer address; IPv6 zones are stripped. Peers
  outside that range behave as without the keyword (their headers are not
  trusted unless listed).
- `c.Scheme()` also trusts `X-Forwarded-Proto` from that range. Like any
  non-empty trusted list, `railway` replaces the loopback/private default: the
  scheme is then trusted only from `100.64.0.0/10` plus whatever else you list,
  so e.g. a local nginx must be listed too.
- With `railway,cloudflare`: if `X-Real-IP` is itself a Cloudflare address
  (the request provably came through Cloudflare), the client IP is
  `CF-Connecting-IP`; if `CF-Connecting-IP` is missing or not an IP, the
  result is that `X-Real-IP` (Cloudflare) address. Only the `cloudflare` keyword enables this, not a static
  `server.CloudflareCIDRs` list. The 24h refresh keeps working.
- hamr decides purely by the peer address and assumes every peer in
  `100.64.0.0/10` is Railway's proxy or one of your own services; this was not
  verified (see [Deployment: What was measured](../deployment.md#what-was-measured)).

Measurements, the documented-versus-observed split and the platforms that are
not verified: [Deployment: What was measured](../deployment.md#what-was-measured).

### Verifying

**Verify before relying on it**, on any platform. Add a throwaway handler,
request it through the public domain and directly at the platform's own domain
(from a known client, e.g. your phone on cellular), each with and without forged
`X-Forwarded-For`, `X-Real-IP` and `CF-Connecting-IP` headers, and read the values:

```go
e.GET("/__debug/ip", func(c echo.Context) error {
    h := c.Request().Header
    return c.JSON(200, map[string]string{
        "remote_addr":      c.Request().RemoteAddr, // direct peer
        "real_ip":          c.RealIP(),             // what the framework resolved
        "x_forwarded_for":  h.Get("X-Forwarded-For"),
        "x_real_ip":        h.Get("X-Real-IP"),
        "cf_connecting_ip": h.Get("CF-Connecting-IP"),
    })
})
```

When the proxy passes the client in a walkable `X-Forwarded-For`,
`remote_addr`'s IP is the range to put in `TRUSTED_PROXIES`. A platform may
instead deliver the client in a single header (Railway does with `X-Real-IP`);
then a CIDR list cannot work. Once configured, `real_ip` should equal your real
client IP and should not change when you send forged headers. Remove the handler
afterward.

## Origin lock

Opt-in and not wired by the scaffold: you add it yourself. A CDN's firewall only helps if every request passes through it, and on
platforms like Railway the origin stays reachable directly. `WithOriginSecret`
refuses traffic that skipped the CDN:

```go
server.WithOriginSecret(server.OriginSecretConfig{
    Secret:      os.Getenv("ORIGIN_SECRET"), // empty = off
    ExemptPaths: []string{"/api/health"},    // platform health checks
})
```

| Field | Meaning |
|---|---|
| `Secret` | Shared secret the CDN/proxy adds to every request. Empty turns the lock off |
| `Header` | Header name. Empty uses `server.DefaultOriginSecretHeader` (`"X-Origin-Secret"`) |
| `ExemptPaths` | Paths let through without the secret. Exact matches only: no prefix, no case folding, no trailing slash, no percent-encoded variants |

The lock is the first thing every request meets, before routing and static
files. A request without the matching header gets an empty 403. The compare is
constant-time. It logs one Info line at startup (`origin lock enabled`, header
name, exempt paths) and one Warn on the first refused request; the secret is
never logged. hamr does not read environment variables for this: you read your own
environment variable (e.g. `ORIGIN_SECRET`) and pass it in. The lock is independent of client-IP
handling and is not needed to get the right IP on Railway. Rollout order,
Cloudflare setup, consequences and rotation:
[Deployment: Origin lock](../deployment.md#origin-lock).

## Production Defaults

Always on (no option):

- HEAD requests are answered by the matching GET handler with no body; an app's own HEAD route takes precedence, and handlers see `Method == HEAD`

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

Overwriting `e.IPExtractor` (or `e.SchemeExtractor`) discards the
`WithTrustedProxies` config, including the Cloudflare refresh.

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
func WithNoIndex(on bool) Option
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
func WithOriginSecret(cfg OriginSecretConfig) Option

type OriginSecretConfig struct {
    Secret      string   // empty = off
    Header      string   // empty = DefaultOriginSecretHeader
    ExemptPaths []string // exact path matches
}

const DefaultOriginSecretHeader = "X-Origin-Secret"

// Static generation
func (s *Server) StaticPage(path string, handler echo.HandlerFunc)
func (s *Server) GenerateStatic(dir string) error

```
