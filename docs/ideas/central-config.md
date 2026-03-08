# Central Config (`hamr.toml`)

## Problem

As hamr grows, different features will each need configuration — dev runner, static generation, project metadata, deployment, etc. Scattering these across separate files (`hamr.dev.toml`, `hamr.static.json`, etc.) adds clutter and makes it harder to reason about a project's setup.

## Proposal

A single `hamr.toml` at the project root that acts as the central config for all hamr tooling. Each feature owns a top-level section:

```toml
# hamr.toml

[project]
name = "myapp"
module = "github.com/user/myapp"

[dev]
# See: docs/ideas/dev-runner.md

[dev.proxy]
listen = ":3000"
target = ":8080"
inject_reload = true

[[dev.watch]]
name = "templ"
pattern = "**/*.templ"
command = "templ generate"
blocking = true

[[dev.watch]]
name = "go"
pattern = ["**/*.go", "go.mod"]
depends = ["templ"]
command = "go build -o ./tmp/app ./cmd/server"
run = "./tmp/app"
env = { PORT = "8080" }

[static]
# See: docs/ideas/static-page-generation.md
output = "dist/static"
enabled = true

# Future sections as needed:
# [deploy]
# [db]
# [auth]
```

### Why TOML

- Human-readable, easy to hand-edit
- First-class support for nested tables and arrays of tables (`[[dev.watch]]`)
- Well-supported in Go (`github.com/BurntSushi/toml` or `github.com/pelletier/go-toml`)
- Familiar to Go developers (used by `goreleaser`, `golangci-lint`, etc.)

### Design Principles

1. **One file, namespaced sections** — each feature gets its own top-level key, no collisions
2. **Optional everything** — a minimal `hamr.toml` can be just `[project]` with a name; features only appear when configured
3. **Convention over configuration** — sensible defaults for everything; the config is for overrides
4. **Discoverable** — `hamr init` generates a commented skeleton; `hamr config` prints the resolved config with defaults filled in

### CLI Integration

```
hamr init                  # generate a starter hamr.toml
hamr config                # print resolved config (with defaults)
hamr config dev.proxy      # print a specific section
hamr dev                   # reads [dev] section
hamr generate              # reads [static] section
```

All `hamr` subcommands look for `hamr.toml` in the current directory (or walk up to find it). Override with `--config path/to/hamr.toml`.

### Extensibility

New features just add a new top-level section. No changes to existing config structure needed. Sections that hamr doesn't recognize are ignored (forward-compatible with plugins or future versions).

## Open Questions

- Should there be a `hamr.local.toml` for developer-specific overrides (gitignored)?
- Environment variable interpolation in values? e.g. `target = "${APP_PORT}"`
- Should the config support profiles/environments? e.g. `[dev.staging]` vs `[dev.local]`
