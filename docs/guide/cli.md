# CLI Reference

`hamr` is a Go full-stack framework CLI for project scaffolding, development, and tooling.

```
hamr
├── new [name]              Scaffold a new project
├── dev                     Start dev server with file watching + live reload
├── sync                    Sync local directory to S3-compatible bucket
├── vendor [dep[@version]]  Download/checksum frontend JS deps
├── lint
│   └── templ               Lint .templ files
├── rename
│   └── module <new-path>   Rename Go module and rewrite imports
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

**Guide:** [Dev Server](pkg/dev.md) covers configuration, watch rules, daemons, Docker Compose, and examples.

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
| `--path-style` | `true` | Use path-style addressing (required for MinIO) |

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
| `--config` | `.templint.yml` | Path to config file |
| `--severity` | all | Minimum severity to report: `warning` or `error` |

```bash
hamr lint templ                          # lint current directory (recursive)
hamr lint templ --rule inline-if,img-alt # run only specific rules
hamr lint templ --severity error         # only report errors
hamr lint templ --config .templint.yml   # use a custom config file
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

## Environment

`hamr` loads `.env` from the current directory on every invocation, setting any variables not already present in the environment.
