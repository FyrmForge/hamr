# CLI Reference

`hamr` is a Go full-stack framework CLI for project scaffolding, development, and tooling.

```
hamr
├── new [name]              Scaffold a new project
├── dev                     Start dev server with file watching + live reload
├── add
│   └── skill <target>      Install an AI agent skill describing hamr
├── ai
│   ├── capture <url>       Capture a browser screenshot of a page
│   └── upgrade             Show scaffold changes between versions via git diff
├── gen
│   └── static              Fingerprint static assets into dist/
├── sync                    Sync local directory to S3-compatible bucket
├── vendor [dep[@version]]  Download/checksum frontend JS deps
├── lint
│   └── templ               Lint .templ files
├── locale
│   └── gen                 Generate type-safe Go accessors from locale JSON
├── rename
│   └── module <new-path>   Rename Go module and rewrite imports
├── completion
│   ├── bash                Generate bash completion script
│   ├── zsh                 Generate zsh completion script
│   ├── fish                Generate fish completion script
│   └── install             Install completion scripts for your shell
└── version                 Print hamr version
```

---

## hamr new

Scaffold a new HAMR project with all required files.

```bash
hamr new [name] [flags]
```

When called without flags, an interactive wizard asks for each option. When all flags are provided, no prompts are shown.

| Flag | Default | Description |
|------|---------|-------------|
| `--module` | prompted | Go module path (e.g. `github.com/user/project`) |
| `--css` | `plain` | CSS approach: `plain` or `tailwind` |
| `--storage` | `none` | Storage backend: `none`, `local`, or `s3` |
| `--websocket` | `false` | Include WebSocket support |
| `--e2e` | `false` | Include E2E testing scaffolding |
| `--database` | `postgres` | Database type |
| `--location` | `subfolder` | Project location: `subfolder` or `current` |
| `--static-s3` | `false` | Sync static assets to a dedicated S3 bucket |
| `--pgadmin` | `false` | Include pgAdmin in Docker Compose |
| `--stripe` | `false` | Include Stripe webhook handler |

```bash
hamr new myapp                                          # interactive wizard
hamr new myapp --module github.com/user/myapp           # set module path
hamr new myapp --css tailwind --storage s3 --websocket  # all features
hamr new .                                              # scaffold into current directory
```

**Guide:** [Storage](pkg/storage.md) covers the `--storage` and `--static-s3` flags in detail.

---

## hamr dev

Start the development server with file watching, build orchestration, process management, and live reload.

```bash
hamr dev [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `hamr.toml` | Path to config file |
| `--no-proxy` | `false` | Skip the reverse proxy, just run watchers |
| `--verbose`, `-v` | `false` | Enable verbose (debug) logging |

```bash
hamr dev                    # reads hamr.toml from current directory
hamr dev --config my.toml   # custom config path
hamr dev --no-proxy         # skip proxy, just run watchers
hamr dev --verbose          # detailed watcher/rebuild logs
```

Reads `[proxy]`, `[dev]` sections from `hamr.toml`. Manages Docker Compose deps, file watchers, build commands, long-running processes, and a reverse proxy with SSE-based live reload.

By default, `hamr dev` also mirrors its recent log stream to `.hamr/dev_logs.txt` (rolling window, 200 lines, escape sequences stripped) for LLM consumption. This includes `[hamr dev]` messages plus stdout/stderr from watched commands and daemons. Configure via `log_file` and `log_file_max_lines` in `[dev]`.

**Guide:** [Dev Server](pkg/dev.md) covers configuration, watch rules, daemons, Docker Compose, and examples.

---

## hamr add skill

Install an AI agent skill that teaches the target tool about HAMR — the CLI, the `pkg/*` packages, and the project's Go + templ + HTMX + Alpine conventions.

```bash
hamr add skill <target> [flags]
```

| Flag       | Default | Description                                                           |
|------------|---------|-----------------------------------------------------------------------|
| `--global` | `false` | Install to `~/.<target>/skills/hamr/` instead of `./.<target>/skills/hamr/` |
| `--force`  | `false` | Overwrite an existing skill directory                                 |

```bash
hamr add skill claude            # project-local: ./.claude/skills/hamr/
hamr add skill claude --global   # user-global:   ~/.claude/skills/hamr/
hamr add skill claude --force    # replace an existing install
```

Currently supported targets: `claude`. Support for `codex`, `opencode`, and other AI coding tools will follow.

Project-local installs must be run from the root of a HAMR project (a directory containing `hamr.toml`) and are typically committed so the whole team benefits. Global installs work from any directory and persist per-user.

---

## hamr ai capture

Capture a browser screenshot of a page for debugging, visual review, or LLM workflows.

```bash
hamr ai capture <url> [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--out` | — | Output PNG path |
| `--dir` | `.hamr/captures` | Root directory for per-capture folders |
| `--browser` | auto-detect | Browser binary path |
| `--text` | `false` | Also save visible page text to a `.txt` sidecar |
| `--html` | `false` | Also save page HTML to a `.html` sidecar |
| `--json` | `false` | Print capture metadata as JSON |
| `--selector` | — | Capture only the first element matching this CSS selector |
| `--full-page` | `false` | Capture the full scrollable page instead of only the viewport |
| `--headless` | `true` | Run Chromium headlessly |
| `--no-sandbox` | `true` | Launch Chromium with `--no-sandbox` |
| `--width` | `1440` | Viewport width in pixels |
| `--height` | `1024` | Viewport height in pixels |
| `--scale` | `1` | Device scale factor |
| `--scroll-to` | — | Scroll to `top`, `middle`, or `bottom` before capture |
| `--scroll-x` | `0` | Horizontal scroll offset in pixels |
| `--scroll-y` | `0` | Vertical scroll offset in pixels |
| `--scroll-selector` | — | CSS selector for a scroll container; defaults to the window |
| `--timeout` | `15s` | Timeout for browser operations |
| `--wait` | `1s` | Extra delay after page load before capture |

```bash
hamr ai capture http://localhost:3000
hamr ai capture localhost:3000/login --text --html --json
hamr ai capture https://example.com --selector '#app' --dir .hamr/captures
hamr ai capture https://example.com/docs --full-page --wait 2s
hamr ai capture https://example.com/pricing --scroll-to middle --width 1280 --height 720
hamr ai capture https://example.com/dashboard --scroll-selector '.results-pane' --scroll-to bottom
```

By default the command creates a per-capture folder under `.hamr/captures/` and writes `screenshot.png`, optional `screenshot.txt` / `screenshot.html`, plus `meta.json`. `--out` lets you force a specific PNG path instead.

---

## hamr ai upgrade

Show scaffold changes between the project's baseline version and the current HAMR version by diffing the actual HAMR repository between version tags.

```bash
hamr ai upgrade [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--json` | `false` | Output as JSON |
| `--from` | — | Override the base version to diff from |
| `--applied` | `false` | Update project baseline to current HAMR version |
| `--dir` | `.hamr/ai/upgrades` | Directory to save the upgrade report |

```bash
hamr ai upgrade                    # diff between project version and current HAMR
hamr ai upgrade --json             # structured JSON output
hamr ai upgrade --from 0.1.0      # override the base version
hamr ai upgrade --applied          # bump [hamr] version in hamr.toml
```

The command clones the HAMR repository (bare, partial clone for speed) and runs `git diff` between the two version tags. The output includes a unified diff and stat summary covering all changes — scaffold templates, packages, and configuration.

Reports are saved to `.hamr/ai/upgrades/` as JSON files. An LLM agent can consume the structured output to present changes conversationally and guide the developer through what to adopt.

---

## hamr gen static

Fingerprint static assets by content-hashing filenames.

```bash
hamr gen static [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `hamr.toml` | Path to hamr.toml config file |
| `--clean` | `false` | Remove dist/ and reset the generated manifest |

```bash
hamr gen static          # fingerprint static/ → dist/
hamr gen static --clean  # remove dist/ and reset manifest
```

Reads source files from `static/`, creates fingerprinted copies (e.g. `output.a1b2c3d4e5f6.css`) in `dist/`, and generates a Go source file with the manifest baked in at compile time. The `make build` target runs this automatically before `go build`.

**Configuration** via `hamr.toml`:

```toml
[static]
dir = "static"
dist = "dist"
manifest = "internal/web/components/staticmanifest.go"
package = "components"
```

**Guide:** [Static Assets](09-static-assets.md) covers fingerprinting, cache headers, and deployment.

---

## hamr sync

Sync a local directory to an S3-compatible bucket.

```bash
hamr sync [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--dir` | `static` | Local directory to sync |
| `--watch` | `false` | Watch for changes after initial sync |
| `--endpoint` | env `S3_ENDPOINT` | S3 endpoint URL |
| `--bucket` | env `S3_BUCKET` | S3 bucket name |
| `--region` | env `S3_REGION` | S3 region |
| `--access-key` | env `S3_ACCESS_KEY` | S3 access key |
| `--secret-key` | env `S3_SECRET_KEY` | S3 secret key |
| `--path-style` | `true` | Use path-style addressing (required for RustFS) |

```bash
hamr sync                              # one-shot sync of static/ to S3
hamr sync --watch                      # watch for changes and sync continuously
hamr sync --dir dist --bucket my-cdn   # sync a different directory to a specific bucket
```

S3 credentials can be provided via flags or environment variables (`S3_ENDPOINT`, `S3_BUCKET`, `S3_REGION`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`). Flags take precedence.

**Guide:** [Sync](pkg/sync.md) covers the Go library API. [Storage](pkg/storage.md) covers S3 setup.

---

## hamr lint templ

Lint `.templ` files for common issues.

```bash
hamr lint templ [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--rule` | all | Run only these rules (comma-separated IDs) |
| `--config` | `hamr.toml` | Path to hamr.toml config file |
| `--severity` | all | Minimum severity to report: `warning` or `error` |

```bash
hamr lint templ                          # lint current directory (recursive)
hamr lint templ --rule inline-if,img-alt # run only specific rules
hamr lint templ --severity error         # only report errors
hamr lint templ --config my-hamr.toml    # use a custom config file
```

**Exit codes:** `0` if no error-severity diagnostics, `1` if any errors found.

**Available rules:**

| ID | Severity | Description |
|----|----------|-------------|
| `inline-if` | error | Inline if with HTML body (silently dropped by templ) |
| `inline-for` | error | Inline for with HTML body (silently dropped) |
| `inline-switch` | error | Inline switch with HTML body (silently dropped) |
| `img-alt` | warning | `<img>` missing `alt` attribute |
| `no-href` | warning | `<a>` missing `href` attribute |
| `inline-style` | warning | Inline `style` attributes |
| `empty-class` | warning | Empty `class=""` attributes |
| `js-href` | warning | `href="javascript:..."` links |

**Guide:** [Templint](pkg/templint.md) covers rules, configuration, and library usage.

---

## hamr locale gen

Generate type-safe Go accessor methods from locale JSON files.

```bash
hamr locale gen [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `hamr.toml` | Path to hamr.toml config file |
| `--dir` | from config | Locale directory (overrides config) |
| `--out` | from config | Output file path (overrides config) |

```bash
hamr locale gen                            # use hamr.toml settings
hamr locale gen --dir locales --out internal/locale/locale.go
```

Reads the default locale JSON, flattens all keys, and generates a Go file with
a `T` wrapper struct containing a typed method per translation key. Non-default
locales are validated — interpolation mismatches are errors, missing keys are
warnings.

Keys starting with a digit (e.g. `2fa.title`) produce method names prefixed
with `X` (e.g. `X2faTitle`).

**Configuration** via `hamr.toml`:

```toml
[locale]
default = "en"
dir     = "locales"
output  = "internal/locale/locale.go"
package = "locale"
```

The scaffolded `Makefile` runs `hamr locale gen` as part of `make build` and
`make test` when the project includes locale support.

**Guide:** [I18n](pkg/i18n.md) covers the runtime library.

---

## hamr vendor

Download and checksum frontend JavaScript dependencies.

```bash
hamr vendor [dep[@version]] [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--update` | `false` | Re-download all dependencies at latest versions |
| `--verify` | `false` | Verify checksums of vendored files |
| `--url` | — | Custom URL to download |
| `--out` | — | Output path for custom URL (relative to project root) |

```bash
hamr vendor                          # vendor all deps at default/locked versions
hamr vendor htmx                     # vendor only htmx
hamr vendor alpine@3.14.9            # vendor alpine at pinned version
hamr vendor --update                 # re-vendor all at latest
hamr vendor --verify                 # check checksums
hamr vendor --url <url> --out <path> # custom dependency
```

Downloads files to `static/js/` and records checksums in `hamr.vendor.json`. Built-in deps: `htmx`, `alpine`, `idiomorph`.

---

## hamr rename module

Rename the Go module path and rewrite all import paths.

```bash
hamr rename module <new-module-path> [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--dir` | `.` | Directory containing go.mod |
| `--dry-run` | `false` | Show what would change without writing files |

```bash
hamr rename module github.com/neworg/myproject
hamr rename module github.com/neworg/myproject --dry-run
hamr rename module github.com/neworg/tools --dir ./tools
```

Reads the current module path from `go.mod`, then replaces it in every `.go` file and the `go.mod` module directive.

---

## hamr version

Print the hamr version and commit hash.

```bash
hamr version
```

Output: `hamr <version> (<commit>)`

---

## hamr completion

Generate or install shell completion scripts for bash, zsh, or fish.

### Generate to stdout

```bash
hamr completion bash
hamr completion zsh
hamr completion fish
```

### Install

```bash
hamr completion install              # per-user install (auto-detects shell from $SHELL)
hamr completion install --shell zsh  # override shell detection
hamr completion install --system     # system-wide install (requires root)
```

| Flag | Default | Description |
|------|---------|-------------|
| `--shell` | auto-detect | Target shell: `bash`, `zsh`, or `fish` |
| `--system` | `false` | Install system-wide instead of per-user |
| `-y`, `--yes` | `false` | Skip confirmation prompt |

**Per-user paths:**

| Shell | Path |
|-------|------|
| bash  | `~/.local/share/bash-completion/completions/hamr` |
| zsh   | `~/.zsh/completions/_hamr` |
| fish  | `~/.config/fish/completions/hamr.fish` |

**System-wide paths:**

| Shell | Path |
|-------|------|
| bash  | `/usr/share/bash-completion/completions/hamr` |
| zsh   | `/usr/share/zsh/site-functions/_hamr` |
| fish  | `/usr/share/fish/vendor_completions.d/hamr.fish` |

For zsh per-user installs, you may need to add the following to `~/.zshrc`:

```bash
fpath=(~/.zsh/completions $fpath)
autoload -Uz compinit && compinit
```

---

## Environment

`hamr` loads `.env` from the current directory on every invocation, setting any variables not already present in the environment.
