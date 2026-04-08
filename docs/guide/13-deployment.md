# Deployment

A typical HAMR deployment is a single Go binary + PostgreSQL + optional S3 for file storage. This guide covers Docker builds, CI/CD pipelines, production configuration, and environment variables.

**Package references:** [Server](pkg/server.md), [Config](pkg/config.md)

---

## Docker Build

### Multi-Stage Dockerfile

```dockerfile
# Build stage
FROM golang:1.23-alpine AS builder
# Update the Go version to match your go.mod

RUN apk --no-cache add git
RUN go install github.com/a-h/templ/cmd/templ@latest

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN templ generate
RUN go build -ldflags "-s -w" -o /bin/site ./cmd/site
# -s -w strips debug info and DWARF symbols to reduce binary size

# Generate static pages
RUN /bin/site --generate

# Runtime stage
FROM alpine:3.20

RUN apk --no-cache add ca-certificates tzdata
COPY --from=builder /bin/site /bin/site
COPY --from=builder /app/generated /app/generated
COPY --from=builder /app/static /app/static
COPY --from=builder /app/migrations /app/migrations

WORKDIR /app
EXPOSE 8080
CMD ["/bin/site"]
```

### Key Points

- Use multi-stage builds to keep the final image small
- Run `templ generate` before `go build` to compile `.templ` files into Go code
- Generate static pages during the build stage
- Copy `migrations/` if running migrations from the same binary
- Include `ca-certificates` for HTTPS calls and `tzdata` for time zones

---

## CI/CD Pipeline

### GitHub Actions

```yaml
name: CI

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

jobs:
  build:
    runs-on: ubuntu-latest

    services:
      postgres:
        image: postgres:16
        env:
          POSTGRES_USER: test
          POSTGRES_PASSWORD: test
          POSTGRES_DB: testdb
        ports:
          - 5432:5432
        options: >-
          --health-cmd pg_isready
          --health-interval 10s
          --health-timeout 5s
          --health-retries 5

    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - name: Install templ
        run: go install github.com/a-h/templ/cmd/templ@latest

      - name: Generate templates
        run: templ generate

      - name: Vet
        run: make vet

      - name: Test
        env:
          DATABASE_URL: postgres://test:test@localhost:5432/testdb?sslmode=disable
        run: make test

      - name: Build
        run: make build

      - name: Verify generated files
        run: |
          if ! git diff --quiet -- 'generated/'; then
            echo "::error::Generated static pages are out of date."
            exit 1
          fi
```

The `make build` target runs `templ generate`, `go build`, and `--generate` in sequence. The final step verifies the committed `generated/` directory matches the build output.

---

## Production Configuration

### Required Environment Variables

| Variable | Description |
|----------|-------------|
| `DATABASE_URL` | PostgreSQL connection string |
| `PORT` | Server port (default: 8080) |
| `DEV_MODE` | Set to `false` in production |

### Optional Environment Variables

| Variable | Description |
|----------|-------------|
| `STATIC_BASE_URL` | CDN URL for static assets (default: `/static`) |
| `REQUEST_TIMEOUT` | Request timeout duration (default: `30s`) |
| `S3_ENDPOINT` | S3 endpoint URL (if using S3 storage) |
| `S3_BUCKET` | S3 bucket name |
| `S3_REGION` | S3 region |
| `S3_ACCESS_KEY` | S3 access key |
| `S3_SECRET_KEY` | S3 secret key |

### Security Checklist

- Set `DEV_MODE=false` — enables security headers (CSP, X-Frame-Options, etc.)
- Use `config.GetEnvOrPanic` for required values — fail fast on missing config
- Set CSRF cookie to `Secure: true`
- Set session cookie to `Secure: true`, `HttpOnly: true`, `SameSite: Lax`
- Use S3 storage instead of local filesystem for multi-instance deployments

---

## Database Migrations

Run migrations before starting the server, either as a separate step or a dedicated command:

```bash
# Separate migration binary
go build -o bin/migrate ./cmd/migrate
./bin/migrate

# Then start the server
./bin/site
```

In Kubernetes or Docker, run migrations as an init container (a container that runs before the main application starts) or a pre-deploy job.

---

## Health Checks

Register a health endpoint for load balancers and orchestrators:

```go
srv.GET("/health", func(c echo.Context) error {
    return c.String(http.StatusOK, "ok")
})
```

For deeper checks, ping the database:

```go
srv.GET("/health", func(c echo.Context) error {
    if err := database.PingContext(c.Request().Context()); err != nil {
        return c.String(http.StatusServiceUnavailable, "db unhealthy")
    }
    return c.String(http.StatusOK, "ok")
})
```

---

## Static Asset Deployment

### S3 Sync in CI

```bash
hamr sync --dir static --bucket myapp-static
hamr sync --dir generated --bucket myapp-static
```

Set `STATIC_BASE_URL` to the bucket's public URL or a CDN in front of it. See [Static Assets](09-static-assets.md) for the full CDN deployment flow.

### Cache Headers

The server automatically sets cache headers via the built-in [CacheControl middleware](pkg/middleware.md):
- Images, fonts: `public, max-age=31536000, immutable`
- CSS, JS: `public, max-age=86400`

### Compression

The server enables gzip response compression by default. Use
`server.WithGzipConfig(server.GzipConfig{Enabled: false})` if compression should
be handled only by nginx, Caddy, Traefik, or a CDN. Use `Skipper` to exclude
specific routes such as streaming endpoints.

---

## Graceful Shutdown

The HAMR server handles SIGINT/SIGTERM automatically:

1. Stops accepting new connections
2. Waits for in-flight requests (configurable timeout, default 10s)
3. Closes the listener

```go
srv, _ := server.New(
    server.WithPort(envPort),
    server.WithShutdownTimeout(15 * time.Second),
)
```

---

## Related Guides

- [Project Setup](01-project-setup.md) — Makefile targets and env config
- [Testing](12-testing.md) — CI test setup and E2E artifacts
