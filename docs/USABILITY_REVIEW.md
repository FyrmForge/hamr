# Comprehensive Usability Review: hamr

## I. CLI (`hamr new`, wizard, vendor, sync, rename)

### A. Project Scaffolding (`hamr new`)

**1. No project name validation beyond empty check**
`internal/cli/cmd/prompt.go:56-58` — The wizard only checks `s == ""`. A user who types `my-app` (hyphen), `123app` (leading digit), or `func` (reserved keyword) gets a project whose Go imports break. The name is also used for S3 bucket names (`main.go.tmpl:55` → `{{.Name}}-uploads`), Docker service names, and database names — all with different naming rules.

**2. Wizard defaults WebSocket and E2E to "yes"**
`internal/cli/cmd/prompt.go:42-43` — Both `WebSocket: "yes"` and `E2E: "yes"`. A developer wanting a minimal project unknowingly gets a WebSocket hub, testcontainers setup, and extra dependencies. These are additive features — the default should be "no".

**3. Flag values don't pre-populate wizard defaults**
`internal/cli/cmd/prompt.go:37-44` — The `wizardResult` struct always initializes `CSS: "plain"`, `WebSocket: "yes"`, etc. If a user runs `hamr new myapp --css=tailwind`, and the wizard still prompts for unprovided flags, the displayed defaults don't reflect the already-provided `--css` value. The `else` branches at lines 118, 129, 186, 198 do correctly read flag values, but the initial struct values shown in the interactive form are wrong if a user happens to submit without changing them.

**4. `hamr new myapp` then wizard name = mismatch**
`internal/cli/cmd/new.go:48-51` — When `hamr new myapp` is used, `name` is set to `"myapp"` from `filepath.Base(dir)`. But if `needsName` were somehow true (it's not in this path, but the logic is fragile), the wizard could set a different name while the directory remains `myapp`. The more real issue: `hamr new` (no arg) + wizard name `MyApp` + location "subfolder" → directory is `MyApp` but all the generated code uses that casing, which is unconventional for Go directories.

**5. Success message printed even when `go get` / `go mod tidy` fail**
`internal/cli/cmd/new.go:121-142` — Failures from `go get ./...` and `go mod tidy` are printed as warnings, but the final output is unconditionally `Project "myapp" created successfully!`. A developer reads "success" and moves on. This should be `"created with warnings"` or a clearer call-to-action.

**6. Quick start says `make migrate` but Makefile has no `migrate` target**
`README.md:96` says `make migrate` but the generated `Makefile.tmpl` has no `migrate` target — migrations run automatically in `main.go` via `db.Migrate()` at startup. This will confuse every new user.

**7. README lists `hamr add service` but command doesn't exist**
`README.md:49` — `hamr add service <name>` is listed in the CLI commands table, but `internal/cli/cmd/root.go:11-16` only registers `new`, `rename`, `vendor`, `sync`, and `version`. No `add` command exists. This is false documentation.

**8. `hamr sync` not listed in README commands table**
`README.md:44-51` — The sync command exists and works (`internal/cli/cmd/sync.go`) but isn't mentioned in the commands table. Meanwhile the nonexistent `hamr add service` is listed.

### B. Vendor System

**9. No version format validation**
`internal/cli/generator/vendor.go:68-90` — `parseNameVersion("htmx@not-a-version")` happily splits on `@` and passes `"not-a-version"` to `resolveURL()`. The resulting URL `https://unpkg.com/htmx.org@not-a-version/dist/htmx.min.js` returns 404, but the error message is just `"download <url>: HTTP 404"` with no hint about an invalid version.

**10. Lock file writes are not atomic**
`internal/cli/generator/vendor.go:206-214` — `writeLockFile()` calls `os.WriteFile()` directly. If the process is killed mid-write, `hamr.vendor.json` is corrupted. The `LocalStorage.Save()` in `pkg/storage/local.go:80-97` correctly uses temp-file-then-rename — the vendor code should do the same.

**11. Verify doesn't suggest how to fix**
`internal/cli/generator/vendor.go:104-119` — When checksums mismatch, the error is `"verification failed:\n  htmx: checksum mismatch (...)"` with no hint to run `hamr vendor --update` to fix it.

### C. Rename Module

**12. No dry-run, confirmation, or backup**
`internal/cli/cmd/rename_module.go:10-37` — `hamr rename module <path>` immediately rewrites every `.go` and `.templ` file with no `--dry-run` flag, no "are you sure?" prompt, and no backup. A typo like `hamr rename module github.com/typo/oops` requires git to recover.

**13. String references to old module path aren't updated**
`internal/cli/generator/rename.go:68-86` — `rewriteImports()` uses AST parsing to update import paths, which is excellent. But module paths appearing in string literals (e.g., `const serviceName = "github.com/old/module/service"`) or comments won't be caught. The `rewriteTemplImports()` does text replacement for `.templ` files, but `.go` string literals are missed.

---

## II. Generated Project Templates

### A. main.go.tmpl

**14. DATABASE_URL has hardcoded credentials as default**
`templates/new/cmd/server/main.go.tmpl:44` — `config.GetEnvOrDefault("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/{{.Name}}?sslmode=disable")`. This silently works without `.env` being set, masking misconfiguration. In production, if `DATABASE_URL` is accidentally unset, the server connects to localhost with default creds instead of failing fast. Should use `config.GetEnvOrPanic("DATABASE_URL")` or at least not embed credentials in the default.

**15. `db.StartKeepAlive` goroutines leak**
`templates/new/cmd/server/main.go.tmpl:75` calls `db.StartKeepAlive(database, 30*time.Second, 2)` which spawns goroutines that can never be stopped (`pkg/db/db.go:101-113`). No context, no cancel function returned. During graceful shutdown these goroutines keep running.

**16. S3 MinIO default credentials hardcoded**
`templates/new/cmd/server/main.go.tmpl:56-60` — `envS3AccessKey` defaults to `"minioadmin"` and `envS3SecretKey` defaults to `"minioadmin"`. Same issue as the DATABASE_URL — silently works with wrong credentials if env vars are unset.

### B. server.go.tmpl (web layer)

**17. Flash-to-context bridge is an undocumented inline middleware**
`templates/new/internal/web/server.go.tmpl:97-105` — There's an anonymous middleware function that copies flash messages from Echo context to Go's `context.Context` via `components.FlashCtxKey`. This is a critical piece of the flash message architecture but it's an unexplained lambda. Developers extending the project will be confused by why flash messages work in templ components — it's not documented in AGENTS.md or CLAUDE.md.

**18. CSP string concatenation is fragile**
`templates/new/internal/web/server.go.tmpl:76-86` — The Content-Security-Policy is built by string concatenation with conditional blocks. If `IncludeWS` is true, `"; connect-src 'self' ws: wss:"` is appended. But if someone adds a data: URI or a new origin, they must manually extend the string. This is error-prone — a CSP builder pattern would be cleaner, but at minimum it should be documented where to modify CSP.

### C. Auth handler

**19. Registration error leaks internal details**
`templates/new/internal/web/handler/auth/handler.go.tmpl:103` — `middleware.SetFlash(c, "Registration failed: "+err.Error(), middleware.FlashError)`. If `err` comes from the database (e.g., constraint violation), the flash message could expose table names, SQL details, or internal paths. Should map errors to user-safe messages.

**20. Validation errors redirect without preserving form data**
`templates/new/internal/web/handler/auth/handler.go.tmpl:92-97` — When validation fails, the handler does `c.Redirect(http.StatusSeeOther, "/register")`. The user's previously entered name and email are lost. The form components in `components/form/fields.templ.tmpl` support `FieldError()` rendering, but the auth forms don't use them — they redirect instead of re-rendering with errors.

**21. `validate.PasswordStrength` used but requirements not shown to user**
`templates/new/internal/web/handler/auth/handler.go.tmpl:92` calls `validate.PasswordStrength(password)` which checks 5 requirements (8+ chars, upper, lower, digit, special). But neither the register form template nor any client-side hint tells the user what the requirements are. They submit, get "Password is too weak", and have to guess.

### D. Migration template

**22. Down migration order is wrong**
`templates/new/internal/db/migrations/001_initial.down.sql.tmpl:2-5` — Users table is dropped before sessions table. But sessions has `subject_id` that logically references users. While there's no actual foreign key constraint (just a text column), the ordering convention should mirror creation order in reverse. If a future developer adds a FK constraint, this migration breaks.

### E. Generated documentation

**23. `.env.example` is incomplete**
`templates/new/root/env.example.tmpl` — Only lists `PORT`, `DEV_MODE`, `DATABASE_URL`, and storage-specific vars. Missing: `STATIC_BASE_URL` (always used), any session/cookie configuration, rate limiting settings, or log level. A developer looking at `.env.example` to understand what's configurable won't find most options.

**24. Generated README says "Prerequisites: templ CLI" but doesn't mention Docker**
`templates/new/root/README.md.tmpl:9-11` — Lists Go, Docker, and templ as prerequisites but doesn't mention `reflex` which is required for `make dev` (the live-reload watcher). The `Makefile.tmpl` has `check-reflex` that errors with "reflex not found. Run: make install" — but the README doesn't mention `make install` in the quick start.

---

## III. Framework Packages (`pkg/`)

### A. `pkg/config`

**25. Silent default on parse failure**
`pkg/config/config.go:42-48` — `GetEnvOrDefaultInt` returns `def` when `strconv.Atoi` fails. Setting `PORT=abc` silently falls back to the default with no log. Same for `GetEnvOrDefaultBool` and `GetEnvOrDefaultDuration`. At minimum these should log a warning.

### B. `pkg/validate`

**26. `Match()` recompiles regex on every call**
`pkg/validate/validate.go:113-126` — `regexp.Compile(pattern)` is called every time `Match()` or `MatchMsg()` is invoked. For hot validation paths (API endpoints, form submissions), this is wasteful. Should either accept a pre-compiled `*regexp.Regexp` or cache internally.

**27. Empty string silently passes all non-Required validators**
`pkg/validate/validate.go:38, 56, 72, 89, 121, 140` — `Email("")`, `Phone("")`, `URL("")`, `Match("", ...)`, `OneOf("")`, `MinAge("")` all return `""` (valid). This is defensible behavior but completely undocumented. A developer who writes `validate.Email(c.FormValue("email"))` expecting it to catch empty emails will be surprised — they need `validate.Required()` first.

**28. `MinLength` counts against empty strings**
`pkg/validate/validate.go:97-99` — `MinLength("", 8)` returns `MsgMinLength` ("Too short") because `len([]rune("")) == 0 < 8`. But `Email("")` returns `""` (valid). Inconsistent — some validators treat empty as "skip", others as "fail".

### C. `pkg/auth`

**29. No session sliding-window refresh**
`pkg/auth/session.go:107-123` — `ValidateSession()` checks expiry and deletes expired sessions, but never extends the expiry on activity. An active user's session expires at the original creation time + duration regardless of usage. Need a `RefreshSession()` method or an option to auto-extend on validation.

**30. No password hash upgrade path**
`pkg/auth/auth.go:25-32` — `DefaultHashConfig` is a package-level var with fixed Argon2id parameters. `CheckPassword()` (`auth.go:63-73`) parses the stored parameters from the PHC string but doesn't compare them to the current `DefaultHashConfig`. If you change parameters (e.g., increase memory), old passwords still verify but there's no signal to rehash them. Should return a "valid but needs rehash" indication.

### D. `pkg/db`

**31. `StartKeepAlive` goroutines cannot be stopped**
`pkg/db/db.go:101-113` — Spawns `poolSize` goroutines with `time.NewTicker` that run forever. No context parameter, no returned cancel function. These leak on graceful shutdown.

**32. `ConnMaxLifetime` default is only 1 minute**
`pkg/db/db.go:64` — `ConnMaxLifetime: 1 * time.Minute` is aggressive. PostgreSQL connections are expensive to establish. The typical production default is 30 minutes to 1 hour. 1 minute means constant reconnection under load.

### E. `pkg/respond`

**33. `Error()` variadic component parameter is confusing**
`pkg/respond/respond.go:59-66` — `func Error(c echo.Context, status int, msg string, component ...templ.Component)` uses a variadic to make the component optional. Calling `respond.Error(c, 500, "fail", nil)` passes len=1 with `component[0] == nil`, which falls through to `c.String()`. This works but is unintuitive — a `*templ.Component` pointer or separate function would be clearer.

### F. `pkg/ctx`

**34. `MustGet` panic message gives no debug context**
`pkg/ctx/ctx.go:40-47` — Panics with `"ctx: missing required value for key " + key.name`. Doesn't include the request path, handler name, or middleware chain. In production with error tracking, this is very hard to debug. Should at minimum include the request path from `c.Request().URL.Path`.

### G. `pkg/async`

**35. Panic recovery drops stack traces**
`pkg/async/async.go:32-36` — `fmt.Errorf("async: panic in job %d: %v", i, r)` converts the panic to a string, losing the goroutine stack trace. Should capture `runtime/debug.Stack()` for debuggability.

**36. `Fire()` uses `slog.Default()` at call time**
`pkg/async/group.go:12` — `Fire()` calls `slog.Default()` inside the goroutine's recover. If the default logger was changed after `Fire()` was called, this picks up the new one. Inconsistent with `NewGroup()` which captures the logger at construction time.

### H. `pkg/server`

**37. `WithDevMode(true)` skips all Secure middleware**
`pkg/server/server.go:93-95` — In dev mode, the `middleware.Secure()` middleware is entirely skipped. This means no security headers at all in development. While this reduces friction, it means developers never see CSP violations during development — they only appear in production. Should apply a relaxed CSP in dev mode rather than none.

**38. `onServerStart` hooks use `context.Background()` with no timeout**
`pkg/server/server.go:174` — `runHooks(context.Background(), s.onServerStart)`. A misbehaving hook that hangs will hang the server startup forever. Should use a context with the `shutdownTimeout` as a ceiling.

### I. `pkg/storage`

**39. `LocalStorage.resolve()` produces identical errors for different violations**
`pkg/storage/local.go:55-65` — Both the "absolute path" check and the "prefix traversal" check return `fmt.Errorf("storage: path %q escapes base directory", path)`. A developer debugging a path issue can't tell which check failed.

**40. `S3Storage` doesn't validate path traversal**
`pkg/storage/s3.go` — Unlike `LocalStorage` which has `resolve()` to prevent directory traversal, `S3Storage` passes the key directly to the S3 API. While S3 doesn't have directory traversal in the filesystem sense, keys like `../../etc/passwd` could create confusing object names. Should sanitize keys.

### J. `pkg/websocket`

**41. `Hub.Handler()` returns `echo.HandlerFunc` — tightly coupled**
`pkg/websocket/hub.go:87-122` — The only way to use the hub is through Echo. Projects using standard `net/http` handlers or other routers can't use it without wrapping. The core upgrade logic should accept `http.ResponseWriter` and `*http.Request`, with an Echo adapter.

**42. Messages silently dropped when buffer full**
`pkg/websocket/hub.go:326-334` — `safeSend()` drops messages when `c.send` is full, logging only at Warn level. Application code has no way to detect this happened. Should either return a boolean or provide a configurable callback.

**43. Only text messages supported**
`pkg/websocket/client.go:56` — `c.conn.Write(writeCtx, ws.MessageText, msg)` — hardcoded to `MessageText`. No support for binary frames, which are needed for efficient binary protocols.

### K. `pkg/janitor`

**44. `Stop()` can block forever**
`pkg/janitor/janitor.go:102-104` — `Stop()` closes the `done` channel to signal the ticker goroutine, but if a task is currently executing, there's no timeout on waiting for it to finish. A hung task means `Stop()` never returns.

**45. Tasks run with `context.Background()` not the hub context**
`pkg/janitor/janitor.go:108` — `runTick()` creates `ctx := context.Background()`. The janitor has no way to propagate a parent context from `Start()`. If the application wants to cancel all janitor work (e.g., during shutdown), it can't.

### L. `pkg/middleware`

**46. CORS middleware missing `AllowCredentials`**
`pkg/middleware/cors.go` — The `CORSConfig` wraps Echo's CORS but never exposes `AllowCredentials`. Any project doing cross-origin cookie auth with a SPA frontend can't configure this without bypassing the middleware.

**47. Rate limiter `MemoryStore` has no max entries**
`pkg/middleware/ratelimit.go` — The `MemoryStore` map grows unbounded. Under a DDoS with unique IPs, this becomes a memory leak. Should have an LRU eviction or max-size cap.

### M. `pkg/client`

**48. `ResponseError.Error()` hides the response body**
`pkg/client/client.go:168-177` — `Error()` returns `"client: unexpected status 400 Bad Request"` but the `Body` field (which is populated) is never included. When debugging API integrations, the body contains the actual error. Should include the first ~200 chars.

**49. Content-Type always set to application/json**
`pkg/client/client.go:109` — `req.Header.Set("Content-Type", "application/json")` is set on every request, including GETs with no body. Not harmful, but unconventional and could confuse intermediate proxies.

### N. `pkg/media`

**50. No documentation guide**
No `docs/guide/media.md` exists. This package handles image resizing, format conversion, video processing, and thumbnail generation — significant functionality that's invisible to developers browsing the README package table.

**51. `checkFFmpeg()` called on every upload**
`pkg/media/process.go:24-26` — `exec.LookPath("ffmpeg")` runs on every image/video upload. Should be cached at package init or first use.

### O. `pkg/sync`

**52. No documentation guide**
No `docs/guide/sync.md` exists. The sync package powers the S3 static asset workflow but is undiscoverable.

### P. `pkg/e2e`

**53. `NoSandbox(true)` hardcoded**
`pkg/e2e/browser.go` — Browser launches always set `NoSandbox(true)`, which disables Chromium's sandbox. This is a security concern in CI environments that run untrusted code. Should be configurable.

---

## IV. Documentation

**54. Two packages missing from README table**
`README.md:24-42` — `pkg/media` and `pkg/sync` are not listed in the framework packages table, despite being substantial packages.

**55. Quick start references nonexistent `make migrate`**
`README.md:96` — The generated Makefile has no `migrate` target. Migrations run automatically on server start.

**56. No "what to do after scaffolding" section**
After `hamr new` + `make dev`, there's no guidance on: what URL to open, how to add new pages, how to extend the schema, or where the generated AI guides live (`docs/ai-guides/`, `AGENTS.md`).

**57. Generated CLAUDE.md doesn't mention `make install` for dev deps**
`templates/new/root/CLAUDE.md.tmpl` — Lists `make dev`, `make build`, etc., but not `make install` which installs `reflex` and `templ` — required prerequisites.

---

## Summary by Severity

| Severity | Count | Most Impactful |
|----------|-------|----------------|
| **Breaks the build** | 3 | No name validation (#1), false docs `hamr add service` (#7), `make migrate` doesn't exist (#6) |
| **Data/security risk** | 5 | Hardcoded DB creds (#14), error message leaks (#19), S3 creds default (#16), memory store unbounded (#47), NoSandbox (#53) |
| **Poor DX — immediate friction** | 12 | Wizard defaults (#2), success-despite-warnings (#5), no form error preservation (#20), password rules hidden (#21), undocumented packages (#50, #52) |
| **Missing functionality** | 8 | No session refresh (#29), no hash upgrade (#30), goroutine leaks (#15, #31), janitor can't stop (#44), dropped WS messages (#42) |
| **Polish / consistency** | 8 | Regex recompilation (#26), empty-string semantics (#27-28), panic stack loss (#35), ConnMaxLifetime too short (#32) |
| **Docs gaps** | 6 | Missing packages in README (#54), false command listing (#7, #8), no post-scaffold guide (#56) |

## Recommended Priority Fixes

1. Fix README — remove `hamr add service`, add `hamr vendor`/`hamr sync`, fix `make migrate` → `make dev` (#6, #7, #8)
2. Add project name validation with Go/S3/Docker rules (#1)
3. Change wizard defaults: WebSocket and E2E to "no" (#2)
4. Make `db.StartKeepAlive` accept a context and return a cancel func (#15, #31)
5. Map registration errors to user-safe messages (#19)
6. Add `docs/guide/media.md` and `docs/guide/sync.md` (#50, #52)
7. Re-render auth forms with errors instead of redirect-and-lose-data (#20)
