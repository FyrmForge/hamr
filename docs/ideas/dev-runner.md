# Dev Runner

## Problem

During development a typical hamr project requires juggling multiple watchers and processes:
- `templ generate --watch` for templates
- `go build && ./app` restart on Go file changes
- Tailwind CLI rebuild on CSS changes
- Manual browser refresh after any of the above

Developers end up stitching together `air`, `templ`, shell scripts, and Makefiles. Each tool has its own config file and its own file-watching logic. There's no coordination between them — a template change triggers templ but the Go rebuild doesn't wait for it to finish, leading to stale output or race conditions.

## Proposal

A single `hamr dev` command that:
1. Watches the filesystem with user-defined rules
2. Runs actions in response to specific file changes
3. Proxies the running app and injects live-reload into HTML responses

All configured from the `[dev]` section of the project's central `hamr.toml` (see [central-config.md](central-config.md)).

### Config (in `hamr.toml`)

```toml
# hamr.toml

[dev.proxy]
listen = ":3000"          # dev proxy listens here
target = ":8080"          # your app listens here
inject_reload = true      # inject SSE live-reload script into HTML responses

[[dev.watch]]
name = "templ"
pattern = "**/*.templ"
command = "templ generate"
# wait for this to finish before triggering dependent actions
blocking = true

[[dev.watch]]
name = "tailwind"
pattern = ["**/*.templ", "**/*.html", "tailwind.config.js"]
command = "npx tailwindcss -i input.css -o static/css/app.css --minify"
blocking = true

[[dev.watch]]
name = "go"
pattern = ["**/*.go", "go.mod", "go.sum"]
ignore = ["**/*_test.go"]
command = "go build -o ./tmp/app ./cmd/server"
blocking = true
# after build, manage the app process
run = "./tmp/app"         # start/restart this process after command succeeds
env = { PORT = "8080" }

[[dev.watch]]
name = "static"
pattern = ["static/**/*"]
# no command — just trigger a reload
reload = true
```

### Watch Rules

Each `[[dev.watch]]` block defines:

| Field       | Description |
|-------------|-------------|
| `name`      | Label for log output |
| `pattern`   | Glob(s) to match changed files |
| `ignore`    | Glob(s) to exclude |
| `command`   | Shell command to run on change |
| `blocking`  | If true, downstream watchers wait for this to finish |
| `run`       | Long-running process to (re)start after command succeeds |
| `env`       | Environment variables for command/run |
| `reload`    | Trigger browser reload without running a command |
| `debounce`  | Delay in ms before firing (default: 100ms) |
| `depends`   | List of watcher names that must complete first |

### Dependency Graph

Watchers can declare dependencies to enforce ordering:

```toml
[[dev.watch]]
name = "go"
depends = ["templ"]       # wait for templ generation before Go build
pattern = ["**/*.go"]
command = "go build -o ./tmp/app ./cmd/server"
run = "./tmp/app"
```

When a `.templ` file changes:
1. `templ` watcher runs `templ generate` (blocking)
2. `go` watcher sees new `.go` files AND its dependency `templ` has completed
3. Go build runs, app restarts
4. Proxy triggers browser reload

### Dev Proxy

The proxy sits between the browser and the app:

```
Browser :3000  →  hamr dev proxy  →  App :8080
                      ↓
               SSE /__hamr/reload
```

- Intercepts `text/html` responses and injects a small script before `</body>`
- The script opens an SSE connection to `/__hamr/reload`
- When any watcher completes (or a `reload`-only rule fires), the proxy sends an SSE event
- The browser receives the event and reloads the page (or morphs the DOM via idiomorph if available)
- Non-HTML responses (API calls, static files) pass through untouched

### CLI

```
hamr dev                    # reads [dev] from hamr.toml
hamr dev --config my.toml   # custom config path
hamr dev --no-proxy         # skip proxy, just run watchers
hamr dev --verbose          # detailed watcher/rebuild logs
```

---

## Stretch Goals

### DOM Morphing Instead of Full Reload

Instead of a full page reload, fetch the new page and morph the current DOM using idiomorph (already vendored). This preserves scroll position, form state, and Alpine.js component state.

### Selective Reload

Tag watchers with a reload scope:
```toml
[[dev.watch]]
name = "tailwind"
pattern = ["**/*.templ"]
command = "npx tailwindcss ..."
reload = "css"             # only reload stylesheets, not the full page
```

Supported scopes: `full`, `css` (hot-swap stylesheets), `none` (rebuild only, no reload).

### Init Command

```
hamr dev init
```

Scan the project and generate a starter `[dev]` section in `hamr.toml` based on what's detected (templ files, tailwind config, Go entrypoint, etc.).

---

## Prior Art

- [air](https://github.com/air-verse/air) — Go live reload, single command only, no proxy
- [hot-reloader-proxy](https://github.com/JamesTiberiusKirk/hot-reloader-proxy) — SSE-based reload proxy for Go
- [templ generate --proxy](https://templ.guide/commands-and-tools/cli/#proxy-mode) — templ's built-in proxy with reload injection
- [vite](https://vitejs.dev/) — HMR dev server for JS, inspiration for the DX

## Open Questions

- Should the proxy support WebSocket passthrough for apps using WebSockets?
- How to handle errors — overlay in the browser (like Vite) or just terminal output?
- Should `hamr dev` be part of the core hamr binary or a separate companion tool?
