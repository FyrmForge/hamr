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
// internal/web/server.go
func RegisterStaticPages(srv *server.Server) {
    aboutHandler := about.NewHandler()
    srv.StaticPage("/about", aboutHandler.About)

    termsHandler := terms.NewHandler()
    srv.StaticPage("/terms", termsHandler.Terms)
}
```

### Generate at Build Time

```go
// cmd/site/main.go
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
make generate # standalone: ./bin/site --generate
```

### Runtime Serving

```go
srv, _ := server.New(
    server.WithGeneratedDir("generated"),
)
```

`WithGeneratedDir` tells the server where to find pre-rendered files. Routes registered via `StaticPage` serve the generated file when available, falling back to the handler otherwise.

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

## Asset Fingerprinting

HAMR uses content-based file hashing for cache busting. Source files live in `static/`, fingerprinted copies go to `dist/` — a separate output directory that is committed to the repo.

### CLI

```bash
hamr gen static          # fingerprint static/ → dist/
hamr gen static --clean  # remove dist/
```

Configuration in `hamr.toml`:

```toml
[static]
dir = "static"    # source directory
dist = "dist"     # output directory
```

### How It Works

1. `hamr gen static` walks the `static/` directory
2. For each file, it computes a SHA-256 hash of the contents (12-char hex prefix)
3. Writes fingerprinted copies to `dist/` mirroring the directory structure (e.g. `dist/css/output.a1b2c3d4e5f6.css`)
4. Generates a Go source file (`internal/web/components/staticmanifest.go`) with the manifest baked in as a compiled map — no runtime loading needed
5. `StaticURL("css/output.css")` returns `/static/css/output.a1b2c3d4e5f6.css` at compile time
6. The server serves from `dist/` first, falling back to `static/` for non-fingerprinted files
7. In dev mode (no fingerprinting), `StaticManifest` is nil — `StaticURL` returns the plain path

### Build Pipeline

The `make build` target runs fingerprinting before `go build` so the manifest is baked in:

```
templ generate → [locale gen] → [css:build] → hamr gen static → go build → make generate
```

### CI Verification

`dist/` is committed. CI should verify it's up to date:

```bash
hamr gen static
git diff --exit-code dist/
```

### Cache Headers

Fingerprinted assets (any URL matching `*.HASH.*`) are served with `public, max-age=31536000, immutable` headers — the filename itself is the cache buster, so they can be cached indefinitely. Non-fingerprinted assets fall back to extension-based cache policies.

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
2. Fingerprint assets: `hamr gen static` (generates compiled manifest)
3. Build the binary: `go build -ldflags ... -o bin/site ./cmd/site` (manifest baked in)
4. Generate static pages: `./bin/site --generate` (emits fingerprinted URLs)
5. Sync to S3: `hamr sync --dir dist --bucket myapp-static`
6. Sync generated pages: `hamr sync --dir generated --bucket myapp-static`
7. Set `STATIC_BASE_URL` to your CDN URL

---

## Next Steps

- [Background Jobs](10-background-jobs.md) — Scheduled tasks and async helpers
- [Deployment](13-deployment.md) — Production CDN and S3 deployment
