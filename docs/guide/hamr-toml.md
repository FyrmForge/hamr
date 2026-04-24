# `hamr.toml` — Configuration Reference

The `hamr.toml` file at the project root configures `hamr dev` (proxy, file
watchers, mocks, daemons), `hamr gen static` / `hamr locale gen`
(generators), `hamr lint templ` (linter), and records the scaffold
options chosen at `hamr new` time.

This is a schema reference. For tutorial-style usage see
[Development Workflow](02-dev-workflow.md) and
[`pkg/dev`](pkg/dev.md).

---

## Top-level tables

| Table                   | Used by                       | Notes |
|-------------------------|-------------------------------|-------|
| `[hamr]`                | `hamr dev` (version check)    | Scaffold metadata |
| `[options]`             | `hamr add skill`              | Records `hamr new` choices (only `alpine` is read at runtime today) |
| `[ai]`                  | `hamr ai install/upgrade`     | AI artifact directory |
| `[static]`              | `hamr gen static`             | Static asset fingerprinting |
| `[proxy]`               | `hamr dev`                    | Reverse proxy + live reload |
| `[dev]`                 | `hamr dev`                    | Watch rules, daemons, mocks |
| `[dev.email]`           | `hamr dev`                    | Mail mock at `/__hamr/mail` |
| `[dev.stripe]`          | `hamr dev`                    | Stripe mock at `/v1/*` + `/__hamr/stripe/*` |
| `[[dev.docker_compose]]`| `hamr dev`                    | Compose deps lifecycle |
| `[[dev.daemon]]`        | `hamr dev`                    | Long-running background processes |
| `[[dev.watch]]`         | `hamr dev`                    | File watch + build/run pipelines |
| `[lint.templ]`          | `hamr lint templ`             | Templ linter rule overrides |
| `[locale]`              | `hamr locale gen`             | i18n codegen |

---

## `[hamr]` — Scaffold metadata

| Field           | Type   | Default | Notes |
|-----------------|--------|---------|-------|
| `version`       | string | (set by `hamr new`) | Hamr CLI version that scaffolded this project. `hamr dev` (released builds only) refuses to start if the running CLI is older than this; `--skip-version-check` bypasses. Also read by `hamr ai upgrade` as the baseline for upgrade diffs. |
| `scaffolded_at` | string | (set by `hamr new`) | ISO date, informational only. |

```toml
[hamr]
version = "0.16.1"
scaffolded_at = "2026-04-24"
```

---

## `[options]` — Recorded `hamr new` choices

Informational record of what was opted into at scaffold time. Only `alpine`
is currently read at runtime (by `hamr add skill`, to tailor installed skill
content). The rest are kept as an audit trail for humans and for tooling
that may want to compare against current state.

| Field         | Type   | Values |
|---------------|--------|--------|
| `database`    | string | `"postgres"` \| `"sqlite"` |
| `db_connector`| string | `"sqlx"` \| `"gorm"` |
| `auth`        | string | `"session"` \| `"none"` |
| `css`         | string | `"plain"` \| `"tailwind"` |
| `websockets`  | bool   | |
| `e2e`         | bool   | |
| `stripe`      | bool   | |
| `locale`      | bool   | |
| `storage`     | string | `"local"` \| `"s3"` |
| `pgadmin`     | bool   | Only meaningful with `database = "postgres"`. |
| `alpine`      | bool   | |

---

## `[ai]` — AI artifact directory

| Field | Type   | Default     | Notes |
|-------|--------|-------------|-------|
| `dir` | string | `.hamr/ai`  | Where `hamr ai install` writes generated skill files. |

---

## `[static]` — Static asset fingerprinting

Read by `hamr gen static`. All four fields are required when the section is
present; the scaffold sets them.

| Field      | Type   | Example | Notes |
|------------|--------|---------|-------|
| `dir`      | string | `static` | Source directory of un-fingerprinted assets. |
| `dist`     | string | `dist` | Output directory for fingerprinted copies. |
| `manifest` | string | `internal/web/components/staticmanifest.go` | Generated Go file path. |
| `package`  | string | `components` | Package name for the generated file. |

---

## `[proxy]` — Reverse proxy + live reload

`hamr dev` only starts the proxy when this section is present. With it absent,
file watching still runs but the browser-side reload pipeline does not.

| Field           | Type   | Default | Notes |
|-----------------|--------|---------|-------|
| `listen`        | string | `:3000` | Address the proxy listens on. |
| `target`        | string | `:8080` | Where to forward requests. Must match your app's `PORT`. |
| `inject_reload` | bool   | `true`  | Inject the SSE live-reload script into HTML responses. |

Both `listen` and `target` must be `host:port` or `:port` form. Required by
`[dev.email]` and `[dev.stripe]` (their UIs live on the proxy mux).

---

## `[dev]` — Watch rules, daemons, mocks

Top-level fields apply to logging. Sub-tables (`[dev.email]`, `[dev.stripe]`)
and array tables (`[[dev.watch]]`, `[[dev.daemon]]`, `[[dev.docker_compose]]`)
configure the rest. At least one of watch / daemon / docker_compose must be
present.

| Field                | Type   | Default                 | Notes |
|----------------------|--------|-------------------------|-------|
| `log_file`           | string | `.hamr/dev_logs.txt`    | Rolling log file (also consumed by `hamr ai`). |
| `log_file_max_lines` | int    | `200`                   | Lines retained. Must be > 0. |

### `[dev.email]` — Mail mock

When `enabled = true`, an inbox is mounted at `/__hamr/mail` on the proxy.
Pair with `pkg/emailmock` as your `email.Sender` impl in dev. Requires
`[proxy]`. See [`pkg/emailmock`](pkg/emailmock.md).

| Field               | Type   | Default                    | Notes |
|---------------------|--------|----------------------------|-------|
| `enabled`           | bool   | `false`                    | |
| `max_messages`      | int    | `500`                      | Ring buffer cap; oldest evicted. |
| `max_message_bytes` | int    | `10485760` (10 MiB)        | Per-message size cap. |
| `persist`           | bool   | `true`                     | Mirror to mbox file. `false` = in-memory only. |
| `persist_path`      | string | `.hamr/mail/inbox.mbox`    | mbox file path. |

### `[dev.stripe]` — Stripe mock

When `enabled = true`, a Stripe-compatible HTTP backend mounts at `/v1/*`
and a dashboard at `/__hamr/stripe/*` on the proxy. Apps point real
`stripe-go` at the proxy URL via `STRIPE_MOCK=true`. Requires `[proxy]`.
See [`pkg/stripemock`](pkg/stripemock.md).

| Field            | Type   | Default                     | Notes |
|------------------|--------|-----------------------------|-------|
| `enabled`        | bool   | `false`                     | |
| `webhook_url`    | string | (required when enabled)     | Where to POST signed events, e.g. `http://localhost:8080/api/webhooks/stripe`. |
| `webhook_secret` | string | (required when enabled)     | Must equal the app's `STRIPE_WEBHOOK_SECRET`. |
| `persist`        | bool   | `true`                      | Persist state across `hamr dev` restarts. |
| `persist_path`   | string | `.hamr/stripe/state.json`   | State file path. |

### `[[dev.docker_compose]]` — Compose deps

Each entry brings up a compose file before watch rules run.

| Field          | Type     | Notes |
|----------------|----------|-------|
| `name`         | string   | Required. Must be unique across all `dev.*` entries. |
| `file`         | string   | Required. Path to `docker-compose.yaml`. |
| `services`     | []string | Optional subset; defaults to all services in the file. |
| `keep_running` | bool     | `true` = leave running on `hamr dev` exit. Default `false`. |
| `wait_ready`   | bool     | Block until services pass healthcheck before continuing. |
| `env`          | []string | `KEY=value` pairs. |

### `[[dev.daemon]]` — Background processes

Long-running processes started once at launch. No file watching, no restart.

| Field  | Type     | Notes |
|--------|----------|-------|
| `name` | string   | Required. Must be unique. |
| `cmd`  | string   | Required. Shell command. |
| `env`  | []string | `KEY=value` pairs. |

### `[[dev.watch]]` — Build/run pipelines

Each rule watches files, runs `cmd` on change, and optionally keeps `run`
alive between builds. Rules can depend on each other to form a build graph.

| Field      | Type            | Default | Notes |
|------------|-----------------|---------|-------|
| `name`     | string          |         | Required. Unique across all `dev.*` entries. |
| `watch`    | string\|[]string|         | Required. Glob patterns relative to project root. |
| `ignore`   | string\|[]string|         | Glob patterns to exclude. |
| `cmd`      | string          |         | One-shot build command. Required if `run` is unset. |
| `run`      | string          |         | Long-running process; restarted after each successful `cmd`. |
| `depends`  | []string        |         | Other watch-rule names that must succeed first. |
| `debounce` | int (ms) or string | `100`ms | E.g. `200` or `"200ms"`. |
| `reload`   | string or bool  | `"none"`| `"full"` / `"css"` / `"none"`. `true`/`false` = `full`/`none`. |
| `env`      | []string        |         | `KEY=value` pairs for `cmd` and `run`. |

Cycles in `depends` fail validation at startup. Unknown deps fail too.

```toml
[[dev.watch]]
name = "site"
watch = ["**/*.go", "**/*.templ", ".env"]
ignore = ["*_templ.go", "*_test.go"]
cmd = "go build -o ./bin/site ./cmd/site"
run = "./bin/site"
depends = ["templ"]
reload = "full"
```

---

## `[lint.templ]` — Templ linter rule overrides

Read by `hamr lint templ` and the `templ` watch rule. All rules ship enabled
by default; override per-rule.

| Field     | Type   | Values |
|-----------|--------|--------|
| `enabled` | bool   | Toggle the rule on/off. |
| `severity`| string | `"error"` \| `"warning"` |

**Rules and default severities:**

| Rule            | Default severity | Checks |
|-----------------|------------------|--------|
| `inline-if`     | error            | `if` must use braces, not inline `@` syntax |
| `inline-for`    | error            | `for` must use braces, not inline `@` syntax |
| `inline-switch` | error            | `switch` must use braces, not inline `@` syntax |
| `img-alt`       | warning          | `<img>` tags require `alt` attribute |
| `no-href`       | warning          | `<a>` tags require `href` attribute |
| `inline-style`  | warning          | `style="..."` attributes flagged |
| `empty-class`   | warning          | `class=""` flagged |
| `js-href`       | warning          | `href="javascript:..."` flagged |

```toml
[lint.templ.rules.inline-style]
enabled = false

[lint.templ.rules.img-alt]
severity = "error"
```

---

## `[locale]` — i18n codegen

Read by `hamr locale gen`. Only present when scaffolded with `--locale`.

| Field     | Type   | Example                         | Notes |
|-----------|--------|---------------------------------|-------|
| `default` | string | `"en"`                          | Fallback locale when a key is missing in the requested one. |
| `dir`     | string | `"locales"`                     | Directory of `<lang>.json` files. |
| `output`  | string | `"internal/locale/locale.go"`   | Generated Go file path. |
| `package` | string | `"locale"`                      | Package name for the generated file. |

---

## Common patterns

**Multi-service: web + API.** Two `[[dev.watch]]` rules with non-overlapping
`watch` and `ignore` patterns. Only the web service goes through the proxy;
the API rule sets `reload = "none"`.

**S3 sync of fingerprinted assets.** Add a `[[dev.daemon]]` running
`hamr sync --watch --bucket my-bucket-static`.

**Tailwind.** Add a `[[dev.daemon]]` running `npm run css`. Scaffold the
project with `--css tailwind` to get this wired automatically.

For worked examples of each, see [Dev Server: Examples](pkg/dev.md#examples).
