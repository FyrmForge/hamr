# Create a project

`hamr new` generates a working app with Echo, templ, htmx, migrations, session auth and a dev server already wired together. Run it, answer the wizard, and `hamr dev` serves the result.

## Run the wizard

```sh
hamr new
```

The wizard asks, in order:

| Prompt | Flag | Notes |
|--------|------|-------|
| Project name | positional `name` | Skipped when you pass a name or `.` |
| Project location | `--location subfolder\|current` | Skipped for `hamr new .` |
| GitHub username or org | `--module` | The prompt builds `github.com/<owner>/<name>`; `--module` sets the full path |
| CSS approach | `--css plain\|tailwind` | |
| Database | `--database postgres\|sqlite` | |
| DB connector | `--db-connector sqlx\|gorm` | |
| Migrate at startup | `--migrate-startup` | |
| File storage | `--storage local\|s3` | |
| Static asset syncing | `--static-s3` | Only asked when storage is `s3` |
| Localisation (i18n) | `--locale` | |
| WebSocket | `--websocket` | |
| E2E testing | `--e2e` | Skipped (forced off) for SQLite |
| Alpine.js | `--alpine` | |
| Stripe | `--stripe` | |
| Dev email inbox | `--email-mock` | |
| Feature flags | `--feature-flags` | |

Every project includes session auth regardless of flags: a `users` table, `pkg/auth` sessions, an `AuthService`, and working `/login`, `/register`, and `/logout` routes.

## Scaffold without prompts

Every prompt whose flag you don't pass is still asked — passing some flags does not skip the rest, and without a TTY (CI, scripts) `hamr new` fails with `could not open a new TTY`. A boolean flag only counts as answered when it appears on the command line, so write a "no" as `--flag=false`. A fully non-interactive run:

```sh
hamr new myapp \
  --module github.com/user/myapp \
  --location subfolder \
  --css plain \
  --database postgres \
  --db-connector sqlx \
  --migrate-startup=false \
  --storage local \
  --locale=false \
  --websocket=false \
  --e2e=false \
  --alpine=false \
  --stripe=false \
  --email-mock=false \
  --feature-flags=false
```

To scaffold into the current directory instead of a subfolder, pass `.` as the name (or `--location current`):

```sh
hamr new . --module github.com/user/myapp ...
```

See [CLI reference → `hamr new`](cli.md#hamr-new) for every flag and its default.

## Version check

Before scaffolding, `hamr new` checks GitHub for the latest hamr release and refuses to run if this binary is out of date — so generated projects never lag the templates they were designed for. Network failures only warn. Pass `--skip-version-check` in CI or offline environments where the check is noise rather than a safety net.

## First run

```sh
cd myapp
make install       # hamr + templ at the versions pinned in go.mod
make docker-up     # PostgreSQL or S3 projects: start the containers
make migrate       # apply the schema (skip with --migrate-startup)
hamr dev
```

`hamr new` does not run `templ generate`; the first `hamr dev` or `make build` does. The site is served at [http://localhost:3000](http://localhost:3000) through the dev proxy, which forwards to the app on `:8080`. If either port is busy, hamr walks to the next free one and prints the actual URL.

To log in, load the test accounts once with `make seed`: `admin@test.com` and `user@test.com`, password `Test1234!` (see [Dev actions](dev-actions.md#scaffold)).

Next: [Project structure](structure.md) explains what was generated, and [hamr dev](hamr-dev.md) covers the dev server.
