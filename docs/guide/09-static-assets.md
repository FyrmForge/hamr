# Static Assets

HAMR doesn't mandate a CSS framework and vendors JS dependencies instead of using npm at runtime — ensuring reproducible builds with no Node.js dependency. This guide covers CSS approaches, JS vendoring, static page generation, S3 sync, and CDN patterns.

**Package references:** [Static Generation](pkg/static-generation.md), [Sync](pkg/sync.md), [CLI](cli.md) (vendor, sync)

---

## CSS

### Plain CSS (Default)

HAMR ships a minimal CSS reset in `static/css/base/`. Write styles in `static/css/app.css`.

### Tailwind CSS

Choose `--css tailwind` during scaffolding:

```bash
hamr new myapp --css tailwind
```

This sets up a Tailwind daemon in `hamr.toml`:

```toml
[[dev.daemon]]
name = "tailwind"
cmd = "npm run css"

[[dev.watch]]
name = "css"
watch = "static/css/output.css"
reload = "css"
```

Tailwind watches `.templ` files, outputs to `static/css/output.css`, and the dev server hot-swaps stylesheets without page reload.

---

## JS Vendoring

Frontend JavaScript dependencies are vendored into `static/js/` — no npm runtime, no bundler:

```bash
hamr vendor                     # vendor all deps at locked versions
hamr vendor htmx                # vendor only htmx
hamr vendor alpine@3.14.9       # pin a specific version
hamr vendor --update            # re-vendor all at latest
hamr vendor --verify            # check checksums
```

Built-in deps: `htmx`, `alpine`, `idiomorph`. Checksums are recorded in `hamr.vendor.json`.

For custom dependencies:

```bash
hamr vendor --url https://cdn.example.com/lib.js --out static/js/lib.min.js
```

---

## Static Page Generation

Pre-render pages whose output never changes between requests (about, terms, privacy):

### Register Static Pages

```go
// internal/web/static.go
func RegisterStaticPages(srv *server.Server, log *slog.Logger) {
    aboutHandler := about.NewHandler(log)
    srv.StaticPage("/about", aboutHandler.About)

    termsHandler := terms.NewHandler(log)
    srv.StaticPage("/terms", termsHandler.Terms)
}
```

### Generate at Build Time

```go
// cmd/server/main.go
// var generateFlag = flag.Bool("generate", false, "generate static pages and exit")

web.RegisterStaticPages(srv, log)
if *generateFlag {
    if err := srv.GenerateStatic("generated"); err != nil {
        log.Fatal(err)
    }
    return
}
```

```bash
make build    # includes: make generate
make generate # standalone: ./bin/server --generate
```

### Runtime Serving

```go
srv, _ := server.New(
    server.WithGeneratedDir("generated"),
)
```

`WithGeneratedDir` adds middleware that serves matching files directly, bypassing the handler chain. Routes are also registered normally as fallback.

### What Counts as Static-Eligible

A handler is safe for static generation when it has no database queries, no session/auth state, no CSRF tokens, and produces identical output on every request.

---

## S3 Sync

Sync static assets to an S3-compatible bucket for CDN serving.

### CLI

```bash
hamr sync                              # one-shot sync of static/ to S3
hamr sync --watch                      # watch for changes and sync continuously
hamr sync --dir dist --bucket my-cdn   # sync a different directory
```

### Go API

```go
// One-shot upload
err := sync.SyncAll(ctx, s3Store, "static")

// Watch and sync continuously
err := sync.WatchAndSync(ctx, s3Store, "static")
```

### Development Setup

Add a sync daemon in `hamr.toml`:

```toml
[[dev.daemon]]
name = "sync-static"
cmd = "hamr sync --watch --bucket myapp-static"
```

---

## CDN Patterns

### StaticBaseURL

All generated projects get a `STATIC_BASE_URL` env var (default: `/static`). Templates use `StaticURL("css/app.css")` instead of hardcoded paths.

In production, override to point at your CDN:

```bash
STATIC_BASE_URL=https://cdn.example.com/static
```

### Deployment Flow

1. Build static assets (Tailwind, etc.)
2. Generate static pages: `./bin/server --generate`
3. Sync to S3: `hamr sync --dir static --bucket myapp-static`
4. Sync generated pages: `hamr sync --dir generated --bucket myapp-static`
5. Set `STATIC_BASE_URL` to your CDN URL

---

## Next Steps

- [Background Jobs](10-background-jobs.md) — Scheduled tasks and async helpers
- [Deployment](13-deployment.md) — Production CDN and S3 deployment
