# Static Generation — Build-Time Page Rendering

`hamr/pkg/server` supports build-time static page generation for routes whose output never
changes between requests (marketing pages, about, terms, etc.). Eligible pages are
pre-rendered into `generated/` and committed to git. At runtime, `StaticPage` routes
serve the generated file when available, falling back to the handler otherwise.

## Quick Start

```go
// internal/web/server.go — register pages for generation and serving
func RegisterStaticPages(srv *server.Server) {
    h := about.NewHandler()
    srv.StaticPage("/about", h.About)
}
```

```go
// cmd/site/main.go — generate before DB setup
web.RegisterStaticPages(srv)
if *generateFlag {
    if err := srv.GenerateStatic("generated"); err != nil {
        log.Fatal(err)
    }
    return
}
```

```bash
# Build and generate
make build    # includes: make generate
make generate # standalone: ./bin/site --generate
```

## Design

### What counts as static-eligible

A handler is safe for static generation when it:

- Has **no database queries** — no runtime data
- Has **no session or auth state** — no user-specific content
- Has **no CSRF tokens** — no form submissions
- Produces **identical output on every request**

Good candidates: about, terms, privacy policy, marketing pages.

Bad candidates: home page with user greeting, dashboard, forms.

### How it works

1. **Registration**: `StaticPage(path, handler)` stores the path+handler for generation
   and registers a GET route. The route serves the pre-rendered file from the generated
   directory when available, falling back to the handler otherwise.

2. **Generation**: `GenerateStatic(dir)` wipes the output directory first to remove stale
   files from previous runs (e.g. pages that were renamed or removed), then renders each
   registered handler using synthetic HTTP requests, writing output to files:
   - `/` → `generated/index.html`
   - `/about` → `generated/about/index.html`
   - `/terms/privacy` → `generated/terms/privacy/index.html`

3. **Serving**: `WithGeneratedDir("generated")` tells the server where to find pre-rendered
   files. Routes registered via `StaticPage` check this directory first and serve the file
   if it exists; otherwise the handler runs normally.

### Generated files are committed

Like templ output and compiled Tailwind CSS, generated pages live in `generated/` and are
committed to git. CI verifies they're up to date.

## Usage

### Registration API

Each `StaticPage` call registers both the generation entry and a GET route. Static
handlers should have no dependencies — no database, sessions, or request-specific state:

```go
// internal/web/server.go
package web

func RegisterStaticPages(srv *server.Server) {
    h := about.NewHandler()
    srv.StaticPage("/about", h.About)

    t := terms.NewHandler()
    srv.StaticPage("/terms", t.Terms)
    srv.StaticPage("/terms/privacy", t.Privacy)
}
```

### Server setup

```go
srv, _ := server.New(
    server.WithPort(8080),
    server.WithGeneratedDir("generated"),
)

web.RegisterStaticPages(srv)
if *generateFlag {
    srv.GenerateStatic("generated")
    return
}
```

### Makefile integration

```makefile
## build: Build the site binary
build: check-templ
    templ generate
    go build -ldflags "..." -o bin/site ./cmd/site
    $(MAKE) generate

## generate: Generate static pages
generate:
    ./bin/site --generate
```

### CI verification

CI builds the binary, generates pages, and verifies they match what's committed:

```yaml
- name: Generate static pages
  run: |
    go build -o bin/site ./cmd/site
    ./bin/site --generate

- name: Verify generated static pages are committed
  run: |
    if ! git diff --quiet -- 'generated/'; then
      echo "::error::Generated static pages are out of date."
      exit 1
    fi
```

### S3/CDN deployment

For CDN serving, sync the `generated/` directory to S3. Add to `hamr.toml`:

```toml
[[dev.daemon]]
name = "sync-generated"
cmd = "hamr sync --dir generated --watch --bucket myapp-static"
```

The server always serves from local disk. A CDN in front handles caching if configured.

## API Reference

```go
// Registration — stores path+handler for generation and registers a GET route.
func (s *Server) StaticPage(path string, handler echo.HandlerFunc)

// Generation — renders all registered static pages to files in dir.
func (s *Server) GenerateStatic(dir string) error

// Option — sets the directory for pre-rendered static page files.
func WithGeneratedDir(dir string) Option
```
