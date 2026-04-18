# hamr CLI Reference

`hamr` is the framework's CLI for scaffolding, dev, codegen, linting, vendoring, and AI-adjacent tooling. Every subcommand reads `hamr.toml` from the current directory unless noted.

```
hamr
├── new [name]              Scaffold a new project
├── dev                     Dev server: file watching, build orchestration, live reload
├── add
│   └── skill <target>      Install an AI agent skill describing hamr (e.g. claude)
├── ai
│   ├── capture <url>       Browser screenshot + HTML/text/metadata sidecars
│   └── upgrade             Diff scaffold changes between versions
├── gen
│   └── static              Content-hash static assets into dist/, emit Go manifest
├── sync                    Sync local directory to an S3-compatible bucket
├── vendor [dep[@ver]]      Download/checksum frontend JS deps
├── lint
│   └── templ               Lint .templ files
├── locale
│   └── gen                 Generate type-safe Go accessors from locale JSON
├── rename
│   └── module <new-path>   Rewrite the Go module path and all imports
├── completion              Install shell completions (bash/zsh/fish)
└── version                 Print hamr version
```

## hamr new [name]

Scaffold a new HAMR project. No flags → interactive wizard. All flags set → non-interactive.

```bash
hamr new myapp
hamr new myapp --module github.com/user/myapp --css tailwind --storage s3
hamr new .                                 # scaffold into current dir
hamr new myapp --database sqlite --db-connector sqlx
```

Key flags: `--module`, `--css {plain|tailwind}`, `--storage {local|s3}`, `--database {postgres|sqlite}`, `--db-connector {sqlx|gorm}`, `--websocket`, `--e2e`, `--locale`, `--stripe`, `--static-s3`, `--pgadmin`, `--migrate-startup`, `--location {subfolder|current}`.

The command auto-runs `go mod tidy`, `hamr vendor`, and `git init` after scaffolding.

## hamr dev

Development server. Reads `[proxy]`, `[dev]` from `hamr.toml`. Manages:

- Docker Compose dependencies (Postgres, etc.)
- File watchers with debouncing
- Build commands (`templ generate`, `go build`, `npm run` for Tailwind)
- Long-running daemons
- Reverse proxy with SSE-based live reload

```bash
hamr dev
hamr dev --no-proxy      # skip proxy, just watchers
hamr dev -v              # verbose: watcher/rebuild detail
```

Mirrors recent logs to `.hamr/dev_logs.txt` (rolling 200 lines, escape codes stripped) so an LLM agent can read the current dev state. Configure via `[dev] log_file` and `log_file_max_lines`; `log_file = "none"` disables.

## hamr add skill <target>

Install an AI agent skill describing the HAMR framework. Currently supports `claude`; more targets (`codex`, `opencode`, etc.) will follow.

```bash
hamr add skill claude            # project-local: ./.claude/skills/hamr/
hamr add skill claude --global   # user-global:   ~/.claude/skills/hamr/
hamr add skill claude --force    # overwrite an existing skill dir
```

Project-local installs must be run from a HAMR project root (a directory containing `hamr.toml`). Global installs run anywhere.

## hamr ai capture <url>

Browser screenshot with HTML/text/metadata sidecars. Built for debugging, visual review, and LLM workflows.

```bash
hamr ai capture http://localhost:3000
hamr ai capture localhost:3000/login --text --html --json
hamr ai capture https://ex.com --selector '#app' --full-page --wait 2s
hamr ai capture https://ex.com --scroll-to middle --width 1280 --height 720
hamr ai capture https://ex.com/docs --tiles --tile-overlap 80
```

Output lands under `.hamr/ai/captures/<timestamp>/` with `screenshot.png`, optional `.txt`/`.html`, and `meta.json`. Tiled mode (`--tiles`) produces viewport-sized PNGs top-to-bottom, which is usually easier for LLM review than a single very tall image.

Key flags: `--full-page`, `--tiles`, `--tile-overlap`, `--selector`, `--scroll-to {top|middle|bottom}`, `--scroll-selector`, `--width`, `--height`, `--scale`, `--wait`, `--timeout`, `--html`, `--text`, `--json`, `--out`.

## hamr ai upgrade

Clone the HAMR repo (bare, partial), diff between version tags, and produce an upgrade report covering all scaffold/package/config changes.

```bash
hamr ai upgrade
hamr ai upgrade --json
hamr ai upgrade --from 0.1.0
hamr ai upgrade --applied        # bump [hamr] version in hamr.toml
```

Reports save to `.hamr/ai/upgrades/`. The JSON form is meant to be consumed by an LLM agent guiding the developer through adoption.

## hamr gen static

Content-fingerprint static assets. Reads from `static/`, writes fingerprinted copies (e.g. `output.a1b2c3d4e5f6.css`) to `dist/`, and generates a Go source file with a compiled-in manifest.

```bash
hamr gen static
hamr gen static --clean      # remove dist/ and reset the manifest
```

Config under `[static]` in `hamr.toml`:

```toml
[static]
dir      = "static"
dist     = "dist"
manifest = "internal/web/components/staticmanifest.go"
package  = "components"
```

The scaffolded `Makefile` runs this automatically during `make build`. Cache-control middleware recognises fingerprinted paths and serves them `immutable`.

## hamr sync

One-shot or continuous sync of a local directory to an S3-compatible bucket.

```bash
hamr sync
hamr sync --watch
hamr sync --dir dist --bucket my-cdn
```

Credentials can come from flags or env vars: `S3_ENDPOINT`, `S3_BUCKET`, `S3_REGION`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`. Flags take precedence. `--path-style` defaults `true` (required for RustFS and most non-AWS S3 deployments).

## hamr lint templ

Static analysis for `.templ` files. Detects templ-specific silent failures (inline control flow with HTML bodies) plus a11y and hygiene issues.

```bash
hamr lint templ
hamr lint templ --rule inline-if,img-alt
hamr lint templ --severity error
```

Exit code `0` when no errors, `1` when any error-severity diagnostic fires.

Rules:

| ID              | Severity | What it catches                                             |
|-----------------|----------|-------------------------------------------------------------|
| `inline-if`     | error    | Inline `if` with HTML body (silently dropped by templ)      |
| `inline-for`    | error    | Inline `for` with HTML body                                 |
| `inline-switch` | error    | Inline `switch` with HTML body                              |
| `img-alt`       | warning  | `<img>` missing `alt`                                       |
| `no-href`       | warning  | `<a>` missing `href`                                        |
| `inline-style`  | warning  | Inline `style=""`                                           |
| `empty-class`   | warning  | Empty `class=""`                                            |
| `js-href`       | warning  | `href="javascript:..."` links                               |

Configure in `hamr.toml` under `[lint.templ]`. Use `make templint` to run it against the whole repo with project config.

## hamr locale gen

Generate type-safe Go accessor methods from locale JSON. Reads the default locale, flattens keys, and emits a `T` struct with one typed method per key. Non-default locales are validated — missing keys are warnings, interpolation mismatches are errors.

```bash
hamr locale gen
hamr locale gen --dir locales --out internal/locale/locale.go
```

Config under `[locale]` in `hamr.toml`:

```toml
[locale]
default = "en"
dir     = "locales"
output  = "internal/locale/locale.go"
package = "locale"
```

The scaffolded `Makefile` wires this into `make build` and `make test` when locale support is enabled.

## hamr vendor [dep[@version]]

Download and checksum frontend JS dependencies into `static/js/`, tracking versions/hashes in `hamr.vendor.json`.

```bash
hamr vendor                        # all deps at pinned versions
hamr vendor htmx                   # just htmx
hamr vendor alpine@3.14.9          # pin a specific version
hamr vendor --update               # re-download at latest
hamr vendor --verify               # check checksums
hamr vendor --url <url> --out <path>  # custom dep
```

Built-in deps: `htmx`, `alpine`, `idiomorph`. Never check vendored JS in by hand — always go through `hamr vendor`.

## hamr rename module <new-path>

Rewrite the Go module path in `go.mod` and every `.go` import site.

```bash
hamr rename module github.com/neworg/myproject
hamr rename module github.com/neworg/myproject --dry-run
```

## hamr completion

Generate or install shell completion scripts.

```bash
hamr completion bash
hamr completion install               # auto-detect shell
hamr completion install --shell zsh
hamr completion install --system      # /usr/share/... (needs root)
```

## hamr version

```bash
hamr version        # → hamr <version> (<commit>)
```

## Environment

Every `hamr` invocation loads `.env` from the current directory, setting any variables not already in the environment. Do not commit `.env` — `.env.example` is the convention for template values.
