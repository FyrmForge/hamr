# Static assets

HAMR doesn't mandate a CSS framework and vendors browser JS into the repo instead of bundling it, so the runtime and the Docker image never need Node.js. Node is only a build-time dependency when you pick Tailwind.

**Package references:** [Static Generation](pkg/static-generation.md), [Sync](pkg/sync.md), [CLI](cli.md) (vendor, sync)

## CSS

### Plain CSS (default)

Plain CSS lives in `frontend/static/css/`, split by concern:

```text
frontend/static/css/
├── base/        variables.css, reset.css, utilities.css
├── components/  buttons.css, forms.css, alerts.css
├── layout/      header.css, footer.css
└── pages/       home.css
```

Each file is linked individually from `internal/web/components/layout.templ` via `StaticURL(...)`. Add new files the same way — there is no bundling step.

### Tailwind CSS

Choose `--css tailwind` during scaffolding:

```sh
hamr new myapp --css tailwind
```

Everything frontend lives under `frontend/` — `package.json`, `tailwind.config.js`, the `css/input.css` source, the `static/` tree, and the generated `dist/`. Nothing npm-related sits at the repo root. Dev machines and CI need Node.js and npm (`make install` runs `npm install`; `make build` runs `npm run css:build`). The compiled `frontend/static/css/output.css` is committed, so the Docker build doesn't need Node.

The scaffold adds a Tailwind watch rule to `hamr.toml`:

```toml title="hamr.toml"
[[dev.watch]]
name = "tailwind"
watch = ["**/*.templ", "frontend/css/input.css"]
dir = "frontend"
cmd = "npm run css:build"
debounce = 200
```

A one-shot build per change, not a long-lived `--watch` daemon: the daemon leaks
memory over a working day. `dir` sets the working directory for `cmd` only —
`watch` globs stay relative to the project root.

### Reloads in dev

CSS changes trigger a full page reload, not a stylesheet hot-swap. The change goes through the `static` rule:

1. `tailwind` rebuilds `frontend/static/css/output.css` (with plain CSS, you edit a file under `frontend/static/css/` directly)
2. `static` (watches `frontend/static/**/*`) runs `hamr gen static`, which rewrites `frontend/dist/` and `staticmanifest.go`
3. The manifest is a `.go` file, so the `site` rule rebuilds the binary and triggers a full page reload

So every CSS change ends in a full page reload. A `reload = "css"` hot-swap rule doesn't help here: it re-requests the stylesheet URLs already in the page, and with fingerprinting those point at the old hash, which `hamr gen static` has just deleted.

## JS vendoring

Frontend JavaScript dependencies are vendored into `frontend/static/js/` — no npm runtime, no bundler:

```sh
hamr vendor                     # vendor all deps at locked versions
hamr vendor htmx                # vendor only htmx
hamr vendor alpine@3.14.9       # pin a specific version
hamr vendor --update            # re-vendor all at latest
hamr vendor --verify            # check checksums
```

Built-in deps: `htmx`, `alpine`, `idiomorph`. New projects vendor `htmx` and `idiomorph` automatically; `alpine` is only vendored when the project opts in (`hamr new --alpine`). Run `hamr vendor alpine` later to opt in. Checksums are recorded in `hamr.vendor.json`.

For custom dependencies:

```sh
hamr vendor --url https://cdn.example.com/lib.js --out frontend/static/js/lib.min.js
```

## Static page generation

Pre-render pages whose output never changes between requests (about, terms, privacy) to HTML files at build time.

### Register static pages

```go title="internal/web/server.go"
func RegisterStaticPages(srv *server.Server) {
    aboutHandler := about.NewHandler()
    srv.StaticPage("/about", aboutHandler.About)

    termsHandler := terms.NewHandler()
    srv.StaticPage("/terms", termsHandler.Terms)
}
```

`StaticPage` registers both a generation entry and a `GET` route. The route sits outside the site group's middleware — see [Routing and handlers → Static pages](routing.md#static-pages) for what that means for CSRF, sessions and the CSP.

### Generate at build time

The scaffolded `cmd/site/main.go` handles `--generate` before connecting to the database:

```go title="cmd/site/main.go"
generateFlag := flag.Bool("generate", false, "generate static pages and exit")
flag.Parse()

// ... server.New(...) ...

web.RegisterStaticPages(srv)
if *generateFlag {
    if err := srv.GenerateStatic("generated"); err != nil {
        log.Error("generate static pages failed", "error", err)
        os.Exit(1)
    }
    return
}
```

```sh
make build    # includes: make generate
make generate # standalone: ./bin/site --generate
```

`GenerateStatic` wipes the output directory first, so removed pages don't linger.

### Runtime serving

```go
srv, err := server.New(
    server.WithGeneratedDir("generated"),
)
```

`WithGeneratedDir` tells the server where to find pre-rendered files. Routes registered via `StaticPage` serve the generated file when it exists and fall back to the handler otherwise.

### What counts as static-eligible

A handler is safe for static generation when it has no database queries, no session/auth state, no CSRF tokens, and produces identical output on every request.

## Asset fingerprinting

HAMR uses content-based file hashing for cache busting. Source files live in `frontend/static/`, fingerprinted copies go to `frontend/dist/` — a separate output directory that is committed to the repo.

### CLI

```sh
hamr gen static          # fingerprint frontend/static/ → frontend/dist/
hamr gen static --clean  # remove frontend/dist/ and reset the manifest
```

Configuration in `hamr.toml`:

```toml title="hamr.toml"
[static]
dir = "frontend/static"
dist = "frontend/dist"
manifest = "internal/web/components/staticmanifest.go"
package = "components"
```

### How it works

1. `hamr gen static` wipes `frontend/dist/` and walks `frontend/static/`
2. For each file, it computes a SHA-256 hash of the contents (12-char hex prefix)
3. Writes fingerprinted copies to `frontend/dist/`, mirroring the directory structure (e.g. `frontend/dist/css/output.a1b2c3d4e5f6.css`)
4. Generates `internal/web/components/staticmanifest.go` with the manifest baked in as a compiled map — no runtime loading needed
5. `StaticURL("css/output.css")` returns `STATIC_BASE_URL + "/css/output.a1b2c3d4e5f6.css"`; paths missing from the manifest fall back to the plain path
6. The server serves `/static/*` from `frontend/dist/` first, falling back to `frontend/static/`

Under `hamr dev`, the scaffold's `static` watch rule runs `hamr gen static` at startup and on every change under `frontend/static/`, so dev pages use fingerprinted URLs too. The manifest is only empty (plain paths) when `hamr gen static` has never run, or after `--clean`.

### Build pipeline

The `make build` target runs fingerprinting before `go build` so the manifest is baked in:

```text
templ generate → [gen locale] → [npm run css:build] → hamr gen static → go build → make generate
```

### CI verification

`frontend/dist/` and `staticmanifest.go` are committed. The scaffolded CI doesn't check them; add a step after `make build`:

```yaml title=".github/workflows/ci.yml"
- name: Verify fingerprinted assets are committed
  run: git diff --exit-code -- frontend/dist/ internal/web/components/staticmanifest.go
```

### Cache headers

The server's built-in [CacheControl middleware](pkg/middleware.md) sets:

| Response | `Cache-Control` |
|----------|-----------------|
| Fingerprinted `/static/*` URLs (`name.HASH.ext`) | `public, max-age=31536000, immutable` |
| Images and fonts by extension (`.webp .jpg .jpeg .png .gif .svg .ico .woff2 .woff .ttf .eot`), fingerprinted or not | `public, max-age=31536000, immutable` |
| Non-fingerprinted `.css` / `.js` | `public, max-age=86400` |
| Everything else (pages, HTMX fragments, API) | `no-store, private` |

Handlers can override the dynamic default by setting their own `Cache-Control`. Unfingerprinted images are cached immutably for a year, so always reference images through `StaticURL`.

In dev mode (`WithDevMode(true)`) caching is disabled entirely — every response gets `no-cache, no-store, must-revalidate`.

## S3 sync

Sync a directory to an S3-compatible bucket for CDN serving.

### CLI

```sh
hamr sync                                       # one-shot sync of [static].dir to S3
hamr sync --watch                               # watch for changes and sync continuously
hamr sync --dir frontend/dist --bucket my-cdn   # sync the fingerprinted output
```

Object keys are paths relative to `--dir` (`frontend/dist/css/output.HASH.css` → `css/output.HASH.css`). A missing bucket is created as private — grant your CDN read access separately. Credentials come from flags or the `S3_*` env vars; see [hamr sync](cli.md#hamr-sync).

### Go API

```go
// One-shot upload
err := sync.SyncAll(ctx, s3Store, "frontend/dist")

// Watch and sync continuously
err := sync.WatchAndSync(ctx, s3Store, "frontend/static")
```

### Development setup

`hamr new --static-s3` adds a sync daemon to `hamr.toml` and a `make sync-static` target:

```toml title="hamr.toml"
[[dev.daemon]]
name = "sync-static"
cmd = "hamr sync --watch --bucket myapp-static"
```

Both sync `[static].dir` (`frontend/static`, unfingerprinted). For a production CDN, sync `frontend/dist` instead, because that's what `StaticURL` points at.

## CDN patterns

### StaticBaseURL

All generated projects get a `STATIC_BASE_URL` env var (default: `/static`). Templates use `StaticURL("css/output.css")` instead of hardcoded paths.

In production, point it at the CDN origin that maps to the root of the synced `frontend/dist`:

```env
STATIC_BASE_URL=https://cdn.example.com
```

### Deployment flow

1. Build and fingerprint: `make build` (runs `css:build`, `hamr gen static`, `go build`, `--generate`)
2. Commit `frontend/dist/`, `staticmanifest.go`, and `generated/`
3. Sync to S3: `hamr sync --dir frontend/dist --bucket myapp-static`
4. Set `STATIC_BASE_URL` to the CDN URL for that bucket

The app keeps serving `/static/*` itself, so the CDN is optional and can be enabled per environment.
