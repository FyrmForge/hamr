# Deployment

A HAMR app deploys as a single Go binary plus its `frontend/` assets, backed by PostgreSQL or SQLite and optionally S3 for uploads. `hamr new` generates the Dockerfile, a CI workflow and a commented-out Railway deploy workflow; what's left is production configuration.

**Package references:** [Server](pkg/server.md), [Config](pkg/config.md)

## Docker image

The scaffolded `cmd/site/Dockerfile` (SQLite variant shown):

```dockerfile title="cmd/site/Dockerfile"
FROM golang:1.27.3-alpine AS builder

ARG VERSION=dev

WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags "-X main.version=${VERSION}" -o /site ./cmd/site

FROM alpine:3.24
RUN apk --no-cache add ca-certificates
COPY --from=builder /site /site
COPY --from=builder /app/frontend /frontend

# SQLite database file directory — mount a volume here for persistence.
RUN mkdir -p /data
VOLUME ["/data"]
ENV DATABASE_PATH=/data/myapp.db

EXPOSE 8080
ENTRYPOINT ["/site"]
```

Build from the repo root:

```sh
docker build -f cmd/site/Dockerfile --build-arg VERSION=$(git rev-parse --short HEAD) -t myapp .
```

### Key points

- **No code generation in the image.** The build doesn't run `templ`, npm or `hamr gen static`. It compiles the committed `*_templ.go`, `frontend/static/css/output.css` (Tailwind), `frontend/dist/` and `staticmanifest.go`, so run `make build` and commit before building. CI checks most of these (see below).
- **Pure Go.** `CGO_ENABLED=0` works for both databases — the SQLite driver (`modernc.org/sqlite`) needs no C toolchain.
- **Migrations are embedded** in the binary via `internal/db` (`//go:embed migrations/*.sql`); there is no migrations directory to copy.
- **Assets resolve from `/`.** The runtime stage has no `WORKDIR`, so the server's relative `frontend/static` and `frontend/dist` paths resolve to the copied `/frontend`.
- **`generated/` is not copied.** Routes registered with `StaticPage` fall back to rendering live on each request. To serve the pre-rendered files, add `COPY --from=builder /app/generated /generated`.
- **Media processing needs ffmpeg.** If you use `pkg/media` (images or video), add it to the runtime stage: `RUN apk --no-cache add ca-certificates ffmpeg` (includes `ffprobe`).
- **Time zones.** Alpine has no zoneinfo. If you call `time.LoadLocation`, add `tzdata` to the `apk add` line or `import _ "time/tzdata"` in `main.go`.

### SQLite deployments

The SQLite variant declares `/data` as a volume and sets `DATABASE_PATH=/data/<project-slug>.db`. Mount persistent storage there:

```sh
docker run -v /srv/myapp/data:/data -p 8080:8080 myapp
```

In Kubernetes, use a `PersistentVolumeClaim` bound to `/data`. SQLite is a single-writer local file, so run exactly **one** replica — horizontal scaling requires PostgreSQL.

**Backups.** With WAL mode enabled (the default), a live copy of the `.db` file is not consistent. Use one of:

- `sqlite3 /data/myapp.db ".backup /backup/myapp-$(date +%F).db"` — online backup API, safe while the app is writing
- Stop the app, copy the `.db`, `.db-wal` and `.db-shm` files together, then restart
- For point-in-time recovery, use [Litestream](https://litestream.io/) to stream WAL frames to S3

### Local file storage

With `--storage local`, uploads go to `STORAGE_PATH` (default `./uploads`, which is `/uploads` in the container). That path is not a volume: mount one there, or use S3 for anything you can't lose and for multi-instance deployments.

## CI/CD pipeline

### CI

The scaffolded `.github/workflows/ci.yml` runs on pushes and pull requests to `master` and `main` (docs-only changes are skipped). In order:

1. Set up Go (version pinned to the scaffold's), and Node 24 for Tailwind projects
2. `make install` — installs the `hamr` and `templ` CLIs at the versions in `go.mod` (plus `npm install` for Tailwind)
3. `make templ-check` — fails if committed `*_templ.go` files are stale (runs before the build, which would regenerate them)
4. `make build` — `templ generate`, CSS build, `hamr gen static`, `go build`, `--generate`
5. Fails if `frontend/static/css/output.css` (Tailwind) or `generated/` changed — i.e. weren't committed
6. Migrations — SQLite: `make migrate` against a fresh file (omitted with `--migrate-startup`). PostgreSQL: a commented-out `postgres` service and migrate step you can enable
7. `make test`, `golangci-lint`, `make templint`

It doesn't run E2E tests or check `frontend/dist/` and `staticmanifest.go` — see [Testing](testing.md#ci-setup) and [Static assets](static-assets.md#ci-verification) for steps to add.

### Deploy (Railway)

`.github/workflows/deploy.yml` is a commented-out Railway deploy that runs `railway up` on pushes to a `production` branch. To enable it:

1. Uncomment the workflow
2. Fix the secret expressions — the template omits the leading `$`; GitHub Actions needs `${{ secrets.RAILWAY_SERVICE }}` and `${{ secrets.RAILWAY_TOKEN }}`
3. Add `RAILWAY_TOKEN` and `RAILWAY_SERVICE` (the service ID) as repository secrets
4. Point the Railway service at the Dockerfile with the service variable `RAILWAY_DOCKERFILE_PATH=cmd/site/Dockerfile`, and attach a volume at `/data` for SQLite
5. Set `TRUSTED_PROXIES=railway` (or `railway,cloudflare` behind Cloudflare); otherwise all visitors share one rate-limit bucket and `c.Scheme()` reports `http`

Any platform that builds a Dockerfile works the same way: build from the repo root with `-f cmd/site/Dockerfile`, set the environment variables below, and expose port `8080` (or set `PORT`).

## Production configuration

### Environment variables

Read by the scaffolded `cmd/site/main.go`:

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8080` | Listen port |
| `DEV_MODE` | `false` | Leave unset in production. `true` disables secure cookies, the server-level security headers and caching, and switches logs from JSON to coloured text. The site group's CSP is applied either way |
| `NOINDEX` | `false` | `true` adds `X-Robots-Tag: noindex, nofollow` to every response and logs a startup warning. Honoured by the site and html service scaffolds. Staging/preview deploys only |
| `BASE_URL` | — | Public origin, e.g. `https://example.com`. Sets the cookie domain, the CORS allowed origin and `components.BaseURL` (absolute URLs) |
| `TRUSTED_PROXIES` | — | Comma-separated CIDRs of your load balancer / reverse proxy, plus the keywords `cloudflare` and `railway`. Unset: `X-Forwarded-For` is ignored. On Railway see [Running on Railway](#running-on-railway) |
| `DATABASE_URL` | local dev URL | PostgreSQL connection string (PostgreSQL projects) |
| `DATABASE_PATH` | `./data/<project-slug>.db` | SQLite file path (SQLite projects; the Dockerfile sets `/data/<project-slug>.db`) |
| `STORAGE_PATH` | `./uploads` | Upload directory (`--storage local`) |
| `S3_ENDPOINT`, `S3_BUCKET`, `S3_REGION`, `S3_ACCESS_KEY`, `S3_SECRET_KEY` | RustFS dev values | Upload storage (`--storage s3`). Override all of them — the defaults are the local RustFS credentials |
| `STATIC_BASE_URL` | `/static` | CDN origin for fingerprinted assets (see [CDN patterns](static-assets.md#cdn-patterns)) |

Feature-specific: `STRIPE_KEY`, `STRIPE_WEBHOOK_SECRET`, `STRIPE_WEBHOOK_SECRET_V2` (`--stripe`), `FLAGS_DIRECT_DATABASE_URL` (`--feature-flags`, PostgreSQL behind PgBouncer). `.env` is excluded from the image by `.dockerignore`, so production values must come from the platform's environment.

### Production checklist

The scaffold's defaults are production-safe (`DEV_MODE` off, secure cookies, CSRF, security headers). What's left to you:

- **Set `BASE_URL`** to the public origin.
- **Set `TRUSTED_PROXIES`** when behind a load balancer or CDN. Otherwise every request appears to come from the proxy's IP, so `c.RealIP()` and the default `middleware.RateLimit` key lump all users together. `cloudflare` alone is enough only when Cloudflare connects to the app directly (a VPS, or nginx listed alongside it). On Railway see [Running on Railway](#running-on-railway). Check what your platform actually sends with the [debug route](pkg/server.md#verifying).
- **Staging or preview deploy? Set `NOINDEX=true`** so search engines don't index a duplicate of the live site. Use this, not `robots.txt`: `robots.txt` controls crawling, not indexing, and a blocked page can still be listed (and its noindex never seen). Leave it unset in production.
- **Leave dev-only variables unset**: `DEV_MODE`, `HAMR_ACTIONS_TOKEN` / `HAMR_ACTIONS_ENV` (dev actions endpoint), `STRIPE_MOCK`, `EMAIL_MOCK`.
- **Replace the S3 defaults** with real credentials, and create the bucket (or call `EnsureBucket` at startup — the scaffold doesn't).
- **Persist uploads and SQLite**: volumes for `/data` and `STORAGE_PATH`, or S3.
- **WebSockets (`--websocket`) are single-instance.** The hub is in-memory, so a broadcast only reaches clients connected to the same process. Run one replica, or add your own cross-instance fan-out.
- **Background jobs run per replica.** A [janitor](background-jobs.md#janitor-scheduler) wired into the site binary runs every task on every instance; keep tasks idempotent or gate them.
- **Install ffmpeg** in the image if you use `pkg/media`.

## Running on Railway

Railway's proxy connects from `100.64.0.0/10`, and a CIDR-only `TRUSTED_PROXIES` cannot recover the visitor there. Use the `railway` keyword:

| Setup | `TRUSTED_PROXIES` |
|---|---|
| Railway alone | `railway` |
| Cloudflare in front of Railway | `railway,cloudflare` |

For peers in that range hamr takes the client IP from Railway's `X-Real-IP` and trusts `X-Forwarded-Proto` from it. With `railway,cloudflare` it uses `CF-Connecting-IP` when `X-Real-IP` is a Cloudflare address. Behaviour detail (fallbacks, the replaced private default, Cloudflare combination): [Server: Railway](pkg/server.md#railway).

### What was measured

Measured on 2026-11-24 on two production hamr apps on Railway. These facts are not generalised beyond them.

- Railway rewrites `X-Forwarded-For` to `<address that connected to Railway's edge>, <Railway edge address>`. A client-sent `X-Forwarded-For` is dropped. Railway's edge addresses are not published, so no `TRUSTED_PROXIES` CIDR list can make the `X-Forwarded-For` walk return the visitor. With the range unset, `RealIP()` is the internal `100.64.x.x` proxy; with `100.64.0.0/10` or `cloudflare,100.64.0.0/10` it is the Railway edge address. Either way all visitors share rate-limit buckets.
- `X-Real-IP` held the true client IP for direct requests and for requests through Cloudflare's proxy. Client-sent `X-Real-IP`, `X-Forwarded-For`, `True-Client-IP` and `Forwarded` values did not change it.
- `CF-Connecting-IP` passes through Railway unfiltered. On a request that skips Cloudflare a client can set it to anything, so never trust it on its own; hamr reads it only when `X-Real-IP` is a Cloudflare address.

Documented versus observed: Railway's docs (Networking, Specs & Limits) document `X-Real-IP` as "for identifying client's remote IP" and say the page is subject to change. `X-Forwarded-For` is not documented there. That Railway unwraps Cloudflare into `X-Real-IP` is observed only. That is why the `railway,cloudflare` fallback to `CF-Connecting-IP` exists: hamr stays correct if Railway stops doing it.

Not measured: other platforms (Render, Fly, Heroku), other CDNs in front of Railway, and IPv6 visitors. The `railway` keyword and `X-Real-IP` are not verified anywhere else; on another platform, verify first with the [debug route](pkg/server.md#verifying).

Assumed, not verified: hamr decides purely by the peer address, so it assumes every peer in `100.64.0.0/10` is Railway's proxy or one of your own services. Requests over Railway's private network do not pass Railway's edge and nothing sets a trustworthy `X-Real-IP` on them; a caller in that range would have its `X-Real-IP` believed.

## Origin lock

A CDN in front of the app (Cloudflare's firewall, bot rules, rate limits) only helps if every request goes through it. On platforms like Railway the origin stays reachable directly, even with only a custom domain, and the platform cannot filter by source address. The origin lock closes that gap with a shared secret: the CDN adds a request header, and the app answers an empty 403 to any request without it. Where you control the network (a VPS or a cloud firewall), allowing only the CDN's address ranges at the firewall is the simpler way to do this; the lock is for platforms that cannot filter by source address.

It is opt-in. The scaffold does not wire it and reads no variable for it. To use it, add this to your `cmd/site/main.go`, in the `server.New(...)` options:

```go
server.WithOriginSecret(server.OriginSecretConfig{
    Secret:      config.GetEnvOrDefault("ORIGIN_SECRET", ""),
    ExemptPaths: []string{"/api/health"}, // whatever route your platform health-checks
})
```

`ORIGIN_SECRET` is just the example name: it is your app's own variable, hamr and the scaffold never read it. An empty secret turns the lock off. The lock runs before routing and static files, compares in constant time, and is generic: any CDN or proxy that can add a request header works. It is independent of client-IP handling. It does not replace `TRUSTED_PROXIES=railway` and is not needed to get a correct client IP on Railway.

### Cloudflare example

1. Generate the secret: `openssl rand -hex 32`.
2. In Cloudflare, create the rule first: Rules, Transform Rules, Modify Request Header. Set a static header `X-Origin-Secret` to the secret, for all incoming requests.
3. Only then set `ORIGIN_SECRET` on the app. The other order returns 403 for the whole site until the rule exists.

Verify: the site loads on its domain, and a direct request to the platform gets 403, e.g. `curl -k --resolve <domain>:443:<platform-ip> https://<domain>/`, or a request to the platform's own domain.

At startup the app logs one Info line (`origin lock enabled`, the header name and the exempt paths) and, on the first refused request, one Warn. The secret is never logged.

### Consequences

- Platform health checks call the container directly, so exempt their path (the scaffold's health route is `/api/health`). `ExemptPaths` are exact matches: no prefix, no case folding, no trailing slash, no percent-encoded variants.
- PR/preview deployments on a platform domain, and service-to-service calls over a private network, do not pass through the CDN. Leave `ORIGIN_SECRET` unset there, or have the caller send the header.
- External uptime monitors should use the public domain.

### Rotating the secret

Unset `ORIGIN_SECRET`, change the proxy rule, then set the new value. The lock is off in between. Two simultaneous secrets are not supported.

## Database migrations

`make migrate` runs `go run ./cmd/migrate up` from a checkout; the Docker image contains only the site binary. Pick one:

- **Run at startup.** Scaffold with `hamr new --migrate-startup` (or copy its `db.Migrate(database, appdb.MigrateConfig())` call into `main.go`). The site applies pending migrations right after connecting. This is the simplest option for SQLite, where the database lives on the container's volume and nothing else can reach it.
- **Separate step (PostgreSQL).** Run `make migrate` (or `go run ./cmd/migrate up`) with the production `DATABASE_URL` from CI or a pre-deploy job before rolling out the new image.
- **Ship the migrate binary.** Add `RUN CGO_ENABLED=0 go build -o /migrate ./cmd/migrate` to the builder stage and `COPY --from=builder /migrate /migrate` to the runtime stage, then run `/migrate up` as an init container or release command.

See [Database](database.md#migrations) for writing migrations.

## Health checks

The scaffold registers `GET /api/health` (`internal/api/handler/health`). It pings the database through `store.Health` and returns `200 {"status":"healthy"}`, or `503` with the error. Point your load balancer or orchestrator at it. If you enable the opt-in [origin lock](#origin-lock), add this path to `ExemptPaths` so the platform can probe the container directly.

## Static assets and caching

The image serves `/static/*` from `/frontend/dist` (fingerprinted) with a fallback to `/frontend/static`. To move assets to a CDN, sync `frontend/dist` to a bucket and set `STATIC_BASE_URL` — see [Static assets](static-assets.md#cdn-patterns).

The built-in [CacheControl middleware](pkg/middleware.md) sets:

- Fingerprinted `/static/*` files, and images/fonts by extension: `public, max-age=31536000, immutable`
- Non-fingerprinted CSS/JS: `public, max-age=86400`
- Everything else (pages, HTMX fragments, API): `no-store, private` — handlers can override

### Compression

The server enables gzip response compression by default. Use
`server.WithGzipConfig(server.GzipConfig{Enabled: false})` if compression should
be handled only by nginx, Caddy, Traefik, or a CDN. Use `Skipper` to exclude
specific routes such as streaming endpoints. Websocket (`Upgrade`) requests are
never compressed.

## Startup and shutdown

`main.go` handles `--generate` (static page generation) **before** connecting to the database, so `make build` and CI can generate pages without a database.

`srv.Start()` handles SIGINT/SIGTERM:

1. Stops accepting new connections
2. Waits for in-flight requests (default 10s)
3. Returns, so `main` can stop background work and close the database

```go
srv, err := server.New(
    server.WithPort(envPort),
    server.WithShutdownTimeout(15*time.Second),
)
```

The request context timeout defaults to 30s (`server.WithTimeout`) and the body limit to 2 MB (`server.WithMaxBodySize`). See [Background jobs](background-jobs.md#running-the-scheduler) for shutdown ordering with a janitor.
