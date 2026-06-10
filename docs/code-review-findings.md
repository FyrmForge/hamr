# Whole-Repo Code Review Findings

Date: 2026-06-10
Scope: all Go code (~62k lines) except `sandbox/` (generated example apps).
Each finding: **severity / confidence — location** + explanation. Grouped by area, ordered by severity within each.

---

## Critical (fix first)

These are the highest-impact items across all areas, collected here for triage. Full detail in the per-area sections below.

1. Rate limiting trivially bypassed via spoofed `X-Forwarded-For` — `pkg/middleware/ratelimit.go:57` (Middleware #1)
2. Stripe mock concurrent map access can crash the dev server — `internal/devserver/stripemock_paymentintent.go:153` (Mocks #1, #2)
3. Migration helpers leak a dedicated pool connection per call — `pkg/db/migrate.go:91` (Infra #1)
4. Unbounded memory buffering before upload size check — `pkg/media/process.go:103` (Media #1)
5. S3 serve handler broken and leaks cross-category bucket objects — `pkg/media/image_store.go:372` (Media #3)
6. Proxy silently truncates large chunked HTML responses — `internal/devserver/proxy.go:213` (Devserver #1)
7. `hamr dev` shutdown orphans grandchild server processes — `internal/devserver/devserver.go:265` (Devserver #2)
8. Janitor task panic crashes the whole process — `pkg/janitor/janitor.go:102` (Infra #2)

---

## pkg/middleware, pkg/auth, pkg/server (security layer)

1. **HIGH / high — `pkg/middleware/ratelimit.go:57-58` + `pkg/server/server.go`.** Default rate-limit key is `c.RealIP()`, and no `echo.IPExtractor` is configured anywhere in the repo. Echo v4 `RealIP()` then falls back to blindly trusting `X-Forwarded-For` / `X-Real-IP` request headers. An unauthenticated attacker sends a unique spoofed header per request and gets a fresh bucket every time — the limiter (including on login/brute-force routes) is defeated out of the box. Highest-value fix: configure a trusted `IPExtractor`.

2. **MEDIUM / high — `pkg/middleware/cors.go:25-27`.** `CORS()` with empty `AllowOrigins` passes an empty slice to Echo, which replaces it with `["*"]` — the framework default allows any origin. Credentials default off limits the worst case, but the default should deny or require explicit configuration.

3. **MEDIUM / medium — `pkg/middleware/cache.go:71-86`.** `CacheControl(false)` only sets headers on static-asset paths; dynamic HTML (including authenticated pages) gets no `Cache-Control` at all — browser back-button after logout and shared proxy caches can serve sensitive pages. Default should emit `no-store`/`private` on non-static responses.

4. **MEDIUM / medium — `pkg/middleware/ratelimit.go:175-185`.** MemoryStore eviction is FIFO by insert order, and a key that resets its window is not re-inserted, so an actively-attacked key stays at the front. Under `WithMaxSize` pressure an attacker spraying keys flushes their own counter out and resets their limit. Compounds with #1. Use LRU/most-recently-active eviction.

5. **MEDIUM / medium — `pkg/middleware/trusted.go:22-31`.** `TrustedSubject` establishes identity purely from the attacker-controllable `X-Subject-ID` header — no signature, allowlist, or source-IP check. One misconfigured mount = total auth bypass. Should support a shared-secret or trusted-CIDR gate.

6. **MEDIUM / medium — `pkg/middleware/audit.go:55-67`.** Audit entries record the raw query string and all path params unconditionally — password-reset tokens, invite tokens, API keys in URLs get persisted verbatim to the audit sink. Needs redaction or omission by default.

7. **LOW / medium — `pkg/middleware/ratelimit.go:74-79`.** Rate limiting fails open by default on store errors (only a warn log). During a backing-store outage all brute-force protection is silently off. Deliberate tradeoff, but deserves a louder call-out / per-route override.

8. **LOW / low — `pkg/middleware/flash.go:73-86`.** Flash cookie is base64 with no MAC; `Message`/`Type` are attacker-influenceable and must be treated as untrusted at render. Templ auto-escape limits impact; document the contract at the read site.

9. **LOW / medium — `pkg/middleware/csrf.go:28-33`.** CSRF cookie ships without `HttpOnly` and without explicit `SameSite`. Both should be set for a double-submit token.

10. **LOW / medium — `pkg/auth/auth.go:80-83`.** `decodePHC` parses the PHC `version` field but never validates it against `argon2.Version` — hashes from a different Argon2 version are silently verified with the current implementation. Reject unknown versions.

11. **LOW / low — `pkg/respond/respond.go:18-20`.** `HTML` writes the status header before rendering; a templ render error mid-stream leaves a committed 200 with a truncated body that the error-page middleware can't fix. Consider buffering non-stream renders or documenting the contract.

12. **LOW / low — `pkg/auth/session.go:159-165`.** `Touch` failures only warn — sliding refresh silently stops working under store flakiness. Also `NeedsRehash` (`auth.go:113-119`) ignores `SaltLength`.

Clean: `pkg/ctx`, `pkg/htmx`, `pkg/server/layeredfs.go`, `pkg/respond/pagination.go`, `secure.go`, `rbac.go` (fails closed), Argon2id core (CSPRNG salt, constant-time compare), graceful shutdown in `server.go`.

---

## internal/devserver (core, excl. mocks and TUI)

1. **HIGH / high — `proxy.go:213-226`.** `injectReloadScript` silently truncates large chunked HTML. For `ContentLength == -1` responses larger than `maxInjectBody` it reads only `maxInjectBody+1` bytes, closes the original body, and serves just those bytes — the browser gets a truncated page with no error. Fix: `io.MultiReader(bytes.NewReader(body), resp.Body)` without closing the body.

2. **HIGH / medium-high — `devserver.go:265-289` + `process.go:191-252`.** Shutdown calls `cancel()` before `pm.StopAll()`. `exec.CommandContext`'s default Cancel SIGKILLs the `sh` immediately, so (a) the SIGINT + 2s grace logic never runs, and (b) for compound `run` commands only `sh` dies — the actual server is orphaned holding its port, and because the `cmd.Wait` goroutine deletes the `pm.procs` entry, the later `StopAll()` process-group kill is a no-op.

3. **MEDIUM / high — `process.go:296-313` vs `230-231`.** Concurrent double-`Wait` on the same process: `stopProcess` spawns `proc.Wait()` while `StartProcess`'s goroutine is blocked in `cmd.Wait()` on the same PID. One waiter reaps, the other gets ECHILD; the SIGKILL-escalation path can signal an already-reaped (reusable) PID.

4. **MEDIUM / high — `actions.go:79-106, 110-127`.** Manual runs (`POST /__hamr/rule/{name}/run`) and the `r` hotkey bypass the scheduler/graph entirely — no `MarkRunning`/`WaitForDeps`, no coordination. A file-save build and a manual build of the same rule run concurrently; two concurrent `StartProcess` calls can leave a loser process running untracked, fighting for the same port.

5. **MEDIUM / high — `actions.go:129-209`.** Destructive endpoints are CSRF-able: `POST /__hamr/docker/{name}/wipe` runs `docker compose down -v` with no auth, Origin/Host check, or CSRF token. A cross-origin form POST from any open webpage executes it (simple request, no preflight); SSE additionally sets `Access-Control-Allow-Origin: *` (`sse.go:122`), and `proxy.listen` on `0.0.0.0` makes it LAN-reachable. Verify Origin/Host like the console WS does.

6. **MEDIUM / medium-high — `process.go:152-187`.** `RunCommand` sets neither `Setpgid` nor `WaitDelay` (unlike `StartProcess` and compose invocations). On ctx cancel only `sh` is killed; children inheriting the pipes block `cmd.Run()` forever, wedging the scheduler/shutdown.

7. **MEDIUM / high — `devserver.go:305-319, 413-417, 764-768`.** Data race on `r.proxyURL` and `r.cfg.Proxy.*`: the startup hotkey-drain goroutine reads them while the main Run goroutine mutates them, no synchronization. Would trip `-race`.

8. **MEDIUM-LOW / high — `composeports.go:276-319`.** `walkComposeServices` doesn't reserve ports it just assigned (probe listener closed immediately, results not added to `owned`). Two services can be assigned the same port in one walk; `compose up` then fails on a collision hamr created.

9. **LOW / high — `config.go:591-605`.** `validateAddr` splits on the first `:`, rejecting all IPv6 addresses (`[::1]:3000` → "invalid port") even though `net.Listen` accepts them. Use `net.SplitHostPort` like `portwalk.go:122`.

10. **LOW / high — `watcher.go:135-138, 190-193`.** Channel-closed exits return without closing `w.done` or stopping timers — fired `time.AfterFunc` goroutines block forever on `w.events`. Latent today; a leak waiting on an fsnotify behavior change.

11. **LOW / high — `process.go:404-434`.** `prefixWriter` never flushes a trailing partial line — the final line of a crashing process (e.g. a panic without trailing newline) is missing from terminal/TUI and file log.

12. **LOW / medium — `actions.go:362-364`.** `dockerRestart`/`dockerWipe` flatten a safely-built argv into `sh -c "docker ..."` by joining with spaces — paths with spaces/metacharacters break or get shell-interpreted. Exec `docker` with an argv slice like the rest of the file.

13. **LOW / high — `actions_test.go:78-106`.** Wipe/restart tests run real `docker compose` commands in detached goroutines that outlive the test (up to 60/120s timeouts), spraying errors into output; in a cwd containing a real `docker-compose.yml` they would mutate real containers/volumes.

Clean: sse.go (broker locking), graph.go, errorstate.go, logbuffer.go, logwriter.go, console.go, filelog.go, envinject.go/walks.go, composeinspect.go, makefile.go, configwatch.go, hotkeys.go, versioncheck.go, errorpage.go.

---

## internal/devserver — Stripe/mail mocks

1. **HIGH / high — `stripemock_paymentintent.go:153` (also `:140`).** `serializePaymentIntent`'s contract requires holding `m.mu` (it indexes `m.charges`), but `retrievePaymentIntent` releases RLock before calling it while complete-handlers mutate the map under write lock. Concurrent map read+write is a **fatal runtime error** — an app polling `paymentintent.Get` while the dev clicks "Succeed" can kill the whole dev server.

2. **HIGH / high — systemic unlocked-read class.** Same family in: `stripemock.go:175`, `stripemock_account.go:82`, `stripemock_payout.go:79, 160-163`, all UI page handlers (`stripemock_page.go:81-99`, `stripemock_paymentintent_page.go:57-88`, `stripemock_payout_page.go:60-80`, `stripemock_account_page.go:46-59`), and `stripemock_dashboard.go:47-64` (whose "internally consistent snapshot" comment is false — snapshot slices hold live pointers read after RUnlock). Contrast mailmock, which deep-copies via `cloneMessage`. Fix the class, not the instances.

3. **MEDIUM / high — `stripemock.go:328-341` + `stripemock_refund.go:200-208`.** `getInt64` returns 0 for any non-pure-digit string, so the `amount < 0` guard is dead code and `amount == 0` means "refund everything" — `amount=-500` or any garbage silently executes a FULL refund. Also no overflow check on `n*10+digit`. Same hazard for `transfer_data[amount]`.

4. **MEDIUM / high — `stripemock.go:192` + checkout flow.** Checkout sessions carry a dangling `payment_intent` ID never registered in `m.paymentIntents` — apps following Stripe's documented pattern get a 404 for an ID the mock handed out. The "paid" outcome creates no Charge and fires no `payment_intent.succeeded`, so Checkout payments can never be refunded in the mock.

5. **MEDIUM / high — `mailmock_mbox.go:171-184` vs `:724-748`.** MBOXO escaping is asymmetric: writer escapes only `From `, reader strips `>From ` and `>>From `. Body lines starting `>From ` are corrupted on every reload (one `>` lost per restart). Escape `>*From ` on write.

6. **MEDIUM / high — `mailmock.go:485-501`.** The inline-attachment endpoint serves stored bytes with the stored attacker-controlled Content-Type, no CSP, no `Content-Disposition` — stored XSS on the dev-proxy origin (same origin as the app under development). `/__hamr/mail/ingest` is unauthenticated and not origin-checked, so a drive-by cross-origin POST can plant the payload. Bypasses the carefully-sandboxed HTML-frame path entirely.

7. **MEDIUM / medium — `mailmock_mbox.go:127-132`.** Header **names** are printed raw (`fmt.Fprintf("%s: %s")`); a key containing CRLF injects headers — including spoofing `X-Hamr-Status`/`X-Hamr-Id` (the reserved-name check at `:358` is exact-match, bypassed by `"Subject\r\nX-Hamr-Status"`) — or corrupts the entry so it's silently dropped at next load.

8. **LOW-MEDIUM / high — `stripemock_dashboard.go:134-137, 389-391`.** Resend treats any PI in `requires_payment_method` (the initial state) as "previously failed" and delivers a signed `payment_intent.payment_failed` for a payment never attempted.

9. **LOW / medium — `stripemock_page.go:46-51`.** Card-decline outcome marks the session `complete`, fires `async_payment_failed`, and 410s the session permanently — real Stripe keeps the session open for retry and fires no event on sync declines. The retry path apps ship can't be exercised.

10. **LOW / high — `stripemock_paymentintent.go:191-199, 254-277`.** Manual-capture is a dead end: confirm moves `capture_method=manual` PIs to `requires_capture` but there is no `/capture` endpoint and `amount_capturable` is hardcoded 0. Either reject manual capture at create or model capture.

11. **LOW / high — `stripemock_paymentintent.go:202-207`.** TOCTOU in confirm: lock released then RLock re-acquired to serialize, so the confirm response can report a state a concurrent dev-UI outcome produced, not the confirm's own result.

12. **LOW / high — vacuous tests.** `mailmock_mbox_test.go:407-409` asserts `NotContains "msg_ma"` but seeded IDs are `ma`…`me`, so the rewrite check can't fail. `stripemock_persist_test.go:103-114` checks an unrelated fresh temp dir is empty.

Clean: webhook signing (round-trips through real `webhook.ConstructEvent`), cascade ordering, double-submit/409 guards, `applyRefund` critical section, tmp+rename persistence, bracket-form decoder, mailmock clone-on-read and ring-buffer eviction.

---

## internal/devserver/tui

1. **HIGH / high — `model.go:866-876, 890-936`.** Duplicated auto-scroll tick chains during edge drag: `continueDrag` returns `maybeAutoScroll()` on every motion event and `tickDrag` schedules its own next tick, with no in-flight guard — N motion events past the edge spawn N concurrent self-perpetuating tick chains, multiplying scroll speed unboundedly. Needs a `ticking` flag cleared in `tickDrag`/`endDrag`.

2. **MEDIUM / high — `model.go:568-575` (`setDockerTabs`).** On config-reload reorder: (a) selection indices are never cleared, so stale selections reverse-video and `y`-copy lines from the wrong stack's buffer (same class of bug the name-keyed search fix addressed); (b) `refreshViewport()` only runs in the reset branch, so on pure reorder the title shows the new stack while the viewport shows the old one.

3. **MEDIUM / high — `model.go:705-717, 724-744`.** Search scroll-to-match passes a buffer-line index to `SetYOffset`, but the viewport is hard-wrapped so offsets are visual rows — with long lines the "centered" match can be entirely off-screen. The selection code already has `visualRowCount`; use it here (and in the filter branch).

4. **MEDIUM / medium — `selection.go:56-70` + `model.go:871`.** In filter view, drag/shift-click range selection fills every buffer index between anchor and endpoint — including hidden non-matching lines. `y` copies lines the user never saw; two adjacent filtered rows can copy hundreds of hidden lines.

5. **MEDIUM / medium — `search.go:153-171` + `model.go:519-531`.** At `maxLogs` cap every append shifts indices, but `recompute` re-anchors by `(line, start)` identity which no longer exists — cursor resets to match 0 on every incoming line (or pins to a different line). n/N is unusable on a full buffer with live logs.

6. **LOW-MEDIUM / high — `model.go:796-806`.** `c` (clear) with an active search never recomputes matches — stale `[k/n]` counter and phantom n/N targets until the next appended line.

7. **LOW / medium — `model.go:1472-1516, 1371-1418, 1518-1568`.** Status/search/hint bars have no overflow truncation on the unbounded left cluster (error lists, search query) — a bar wider than the terminal wraps and corrupts the whole frame layout.

8. **LOW / medium — `model.go:1241-1292, 1590-1599`.** Modal chrome budgets off by one: `modalTitle`'s `MarginBottom(1)` isn't counted in `fixedChrome`, so at tight heights the bottom border is cropped.

9. **LOW / medium — `search.go:207-231`.** Match offsets computed on `strings.ToLower(line)` are applied to the original line; Unicode case mappings that change byte length (İ, ẞ, Ⱥ) shift offsets — garbled highlights (clamps prevent panics).

10. **LOW / high — `model_scroll_test.go:124-151`.** `TestModel_FollowDoesNotResumeAfterEnterWhileSearchActive` has no assertions — both branches `t.Log`. Assert or delete.

11. **LOW / medium — `model.go:956-978`.** A plain click on empty rows below a short buffer clamps to the last line and creates a selection, contradicting `visualRowToLine`'s documented "-1 → ignore" contract (clamp is justified only for drag-past-end).

12. **LOW / medium — `model.go:1166-1185`.** `makePrefixWriter.Write` returns `(0, err)` after consuming all of `p`, and leaves the failed line in `buf` so retries duplicate. Unreachable today (Sink never errors); latent `io.Writer` contract violation.

Clean: sink.go, hotkeys.go, run.go state machine, wrap.go (`hardwrap`/`visualRowCount` consistent), help.go, concurrency discipline (all model mutation on the Update goroutine), terminal restore (bubbletea v1.3.10 handles panics).

---

## internal/cli, internal/scaffold, internal/browsercapture

1. **HIGH / high — `internal/cli/cmd/localegen.go:277, 296, 336`.** Interpolation names become Go parameter names with only a lower-case-first transform: `{{.Type}}` → parameter `type` (keyword) → `format.Source` fails opaquely. Same for `Range`/`Func`/`Map`/`Var`; `{{.T}}` collides with the receiver `t *T`. No hint which key is at fault.

2. **MEDIUM / high — `localegen.go:236-239`.** `Count` is unconditionally deleted from params, including for non-plural keys — a plain `"You have {{.Count}} credits"` generates a zero-arg accessor whose placeholder can never be filled. Silent wrong output, not caught by gen-time validation.

3. **MEDIUM / high — `localegen.go:316-334`.** Method names split keys only on `.`/`_`/`-`: `home.title` and `home_title` collide into duplicate methods; keys with spaces/punctuation produce invalid identifiers. Both surface only as a generic format failure.

4. **MEDIUM / high — `internal/cli/generator/rename.go:34-75`.** `RenameModule` rewrites files in-place during the walk with no rollback, and descends into `vendor/`, `node_modules/`, `.git/`, `testdata/`. A parse failure mid-walk aborts leaving the project half-renamed with the old module path still in go.mod (rewritten last) — guaranteed-broken build.

5. **MEDIUM / medium — `rename.go:137-139`.** `.templ` import rewrite is a whole-file text replace of `"oldModule` — string literals/attributes starting with the module path get silently rewritten too.

6. **MEDIUM / high — `internal/cli/cmd/new.go:315-337`.** Dev builds stamp the scaffolded project's baseline from `git describe --tags` run in the **user's cwd**, not the hamr repo — scaffolding inside any tagged repo bakes that repo's version in, and `ensureCLINotBehindScaffold` later wrongly blocks release CLIs.

7. **MEDIUM / high — `internal/scaffold/gitdiff.go:49-58` + `cmd/upgrade.go:75-80`.** `GitDiff` uses the raw metadata version as tag (`"v" + base`) with no pre-release stripping — projects scaffolded by dev builds have baseline `X.Y.Z-dev`, tag `vX.Y.Z-dev` never exists, upgrade is permanently broken without `--from`. `ParseVersion` knows how to strip `-dev`; this path doesn't use it.

8. **MEDIUM / high — `internal/cli/generator/vendor.go:166-174`.** Skip-if-vendored compares `existing.Version` against the **registry default**, not the locked version — pins (`hamr vendor alpine@3.15.0`) are silently re-downloaded at the default and the lock overwritten on the next `VendorAll` (which `hamr new` runs automatically). Pins are effectively meaningless.

9. **MEDIUM / medium — `internal/browsercapture/capture.go:239-263`.** The rod launcher is never cleaned up (`l.Cleanup()` absent) — every `hamr ai capture` leaks a multi-MB Chromium profile dir; if `browser.Connect()` fails after launch, the browser process isn't explicitly killed.

10. **LOW / high — `localegen.go:176`.** Error message claims "(raw source written for debugging)" but nothing is ever written — users are pointed at a file that doesn't exist.

11. **LOW / medium — `cmd/upgrade.go:57-63`.** `--applied` overwrites the baseline with the CLI's version unconditionally — including moving it backwards with an older CLI — and silently ignores `--from`/`--json`/`--dir`.

12. **LOW / medium — `cmd/upgrade.go:152-176`.** Report filename embeds the `--from` version sanitized only for `.`/`-`; a tag like `release/0.1.0` makes `os.WriteFile` fail after the expensive clone+diff succeeded. (Git refname rules block actual traversal.)

13. **LOW / medium — `cmd/sync.go:130-160`.** Hand-rolled `.env` parser doesn't handle `export KEY=value`, which godotenv (used by scaffolded apps) accepts — `hamr sync` can miss S3 credentials the app loads fine.

Clean: semver.go + tests, `UpdateVersion` (section-scoped with verify-and-restore), latest_release.go and gitdiff exec usage (no injection, bodies closed), remaining cmd files, generator/{generator,name,skill}.go, cmd/hamr/main.go.

---

## pkg/media, pkg/storage, pkg/validate, pkg/templint, pkg/i18n

1. **HIGH / high — `pkg/media/process.go:103-121` via `image_store.go:197`, `video_store.go:169`.** `detectMIME` does unbounded `io.ReadAll`; the real `MaxSize` check runs only after the full stream is buffered. The `size` parameter is advisory (client-controlled Content-Length). Memory-exhaustion DoS — wrap with `io.LimitReader(r, MaxSize+1)`.

2. **HIGH / high — `image_store.go:343-348`, `video_store.go:334-340` (`serveLocal`).** Missing files return 500, never 404: `os.IsNotExist` doesn't unwrap `fmt.Errorf`-wrapped errors, and the `strings.Contains(err.Error(), "not exist")` fallback never matches Linux's "no such file or directory". Use `errors.Is(err, fs.ErrNotExist)`.

3. **HIGH / high — `image_store.go:372-407`, `video_store.go:362-390` (`serveS3`).** (a) The "strip common prefixes" loop body is just `break` — a no-op — so the S3 key is the raw URL path; unless routes mirror bucket layout from root, everything 404s. (b) Neither handler scopes to the store's `Category` prefix — it proxies **any object in the bucket**, even when the store is configured signed-URL-only, silently defeating that access model. `serveLocal` is similarly unscoped across the storage root.

4. **MEDIUM / high — `pkg/media/media.go:332-369`.** `DetectType` falls back to the attacker-controlled multipart `Content-Type` header when sniffing fails; the real upload gate doesn't, so the two disagree — any caller using `DetectType` as a pre-check has a spoofing bypass. Drop the fallback or document it as untrusted.

5. **MEDIUM / high — `pkg/i18n/bundle.go:90-99`.** Only "interpolation mismatch" validation errors are promoted to hard failures (substring match on `ve.Message`); missing-key and extra-key errors are silently discarded — no error, no log. Typo'd translation keys fall through to default with zero diagnostics.

6. **MEDIUM / high — `pkg/i18n/translator.go:148-194` + bundle fallback.** Plural fallback is broken: `T` only consults the fallback when the key is entirely absent. A locale missing the CLDR category for a count (e.g. Polish `many`) picks `Other` then "any available category" via a **non-deterministic map range** — same count can render different strings run-to-run.

7. **MEDIUM / medium — `video_store.go:178` + `video.go:135`.** Input size check happens before transcode; `raw`, `encoded`, and `os.ReadFile(outPath)` can be resident simultaneously — memory amplification interacting with #1. (Output non-recheck is documented; flagged for the interaction.)

8. **MEDIUM / medium — `pkg/validate/validate.go:305, 348`.** `NormalizeURL` prepends `https://` only when `"://"` is absent, so `javascript:alert(1)` passes through, and `URL()` accepts `javascript://host/%0aalert(1)` — no scheme allowlist anywhere; XSS vector if validated URLs land in `href`. Also `emailRe` rejects valid addresses (IDN, `user@localhost`, IP literals).

9. **MEDIUM / medium — `pkg/templint/rules_control.go:6-8`.** Inline-if/for/switch regexes are greedy and naive — false positives on Go lines/struct literals containing `<`, false negatives on `{{` expr bodies — in Error-severity rules that gate CI.

10. **MEDIUM / medium — `pkg/templint/rules_a11y.go:65-131`.** Tag scanner treats the first `>` as tag end and doesn't track quote state across lines — attributes after a quoted `>` (`title="a > b"`) are invisible to img-alt/no-href checks. Silently wrong lint results.

11. **LOW / high — `pkg/storage/s3.go:100-131`.** `EnsureBucket` only treats typed `*s3types.NotFound` as "create it"; real S3/R2 often return a generic API error with code `NotFound` for HeadBucket — missing bucket becomes a hard error instead of creation, backend-dependent.

12. **LOW / medium — `pkg/media/video.go:46-60`.** Thumbnail `-ss 1` on clips shorter than 1s can yield empty stdout with exit 0 — a zero-byte thumbnail saved and reported as success (`video_store.go:240-243` checks only the Save error). Tests deliberately avoid <2s clips. Validate non-empty output.

13. **LOW / medium — `pkg/validate/validate.go:150-178`.** `MinAge`/`MaxAge`: `time.Now()` is local-zone vs UTC-parsed DOB (off-by-a-day near midnight), and `MaxAge`'s `-(maxAge+1)`+`Before` boundary disagrees subtly with `MinAge`'s `After` semantics.

14. **LOW / high — media serve handlers.** `WriteHeader(200)` then `io.Copy`: a mid-copy failure returns an error to Echo on a committed response with `immutable, max-age=31536000` already sent — truncated body the client may cache.

15. **LOW / medium — `pkg/i18n/json.go:107-120`.** Any nested object whose keys are all CLDR category words with string values is misclassified as a plural message rather than a namespace. Ambiguous by design; flagged for awareness.

Clean: `pkg/validate/rules.go`/`constructors.go`/`messages.go`, `pkg/i18n/direction.go`/`plural.go` tables, `pkg/storage/local.go` `resolve` (path traversal solid), atomic temp-file-then-rename Save.

---

## pkg/websocket, pkg/async, pkg/janitor, pkg/db, pkg/e2e, pkg/sync, test/

1. **HIGH / high — `pkg/db/migrate.go:91-112`.** All Migrate* helpers leak a dedicated pool connection: `postgres.WithInstance` (golang-migrate v4.19.1) checks out a `*sql.Conn` released only by `m.Close()`, which is never called. Typical startup (`MigrateVersion` + `Migrate`) permanently pins 2 of the default 10 conns. **Caution:** the sqlite twin must NOT blindly call `m.Close()` — golang-migrate's sqlite driver `Close()` closes the caller's shared `*sql.DB`.

2. **HIGH / high — `pkg/janitor/janitor.go:102-104, 126, 165`.** A panicking task crashes the process: cron chain is only `SkipIfStillRunning` (no `cron.Recover`), and `runTask`/the `WithRunImmediately` goroutine have no `recover()`. Every other goroutine helper in the repo (async.Fire, async.Group) recovers; the janitor doesn't.

3. **MEDIUM / medium — `pkg/websocket/hub.go:234, 383-393`.** `JoinRoom` accepts a raw `*Client` with no registered-check — a stale client (disconnected, `send` channel closed by `unregister`) re-inserted into `h.rooms` makes the next `SendToRoom`/`Broadcast` panic on send-to-closed-channel. Verify `h.clients[c.SessionID] == c` in `JoinRoom`/`LeaveRoom`.

4. **MEDIUM / medium — `pkg/websocket/hub.go:123 vs 139`.** `Handler`'s `wg.Add(1)` races `Close()`'s `wg.Wait()` (Add-when-zero concurrent with Wait violates the WaitGroup contract); post-Close registrations also repopulate the maps Close just cleared. No closed flag in `Handler`.

5. **MEDIUM / medium — `pkg/janitor/janitor.go:147-162`.** `Stop` discards `cron.Stop()`'s completion context and doesn't track immediate-run goroutines — `janitor.Stop(); db.Close()` lets tasks run against closed resources; `j.ctx` cancellation abandons in-flight work with no drain.

6. **MEDIUM / medium — `janitor.go:123-128`.** `WithRunImmediately` bypasses `SkipIfStillRunning` (raw `go j.runTask`) — a slow immediate run overlaps the first scheduled tick, violating the documented no-overlap guarantee.

7. **MEDIUM / high — `test/integration/scaffold_test.go:228-234, 243-245`.** Cleanup deadlock: `h.exited` is 1-buffered, never closed; `waitHealthy`'s early-exit branch consumes the one value, then cleanup blocks forever on `<-h.exited` — any "server exited before healthy" failure hangs until the 10-minute panic, burying the real output.

8. **LOW / high — `pkg/e2e/browser.go:247`.** `require.Fail(t, "...%s...%v", currentURL, timeout)` — testify Sprintf's the *URL* as the format string. Should be `require.Failf` (cf. correct usage at line 374).

9. **LOW / high — `pkg/websocket/hub_test.go:333-345`.** `waitFor` claims "up to 500ms" but only exits on `t.Context().Done()` — a never-true condition busy-spins a core until the global test timeout.

10. **LOW / high — `pkg/db/db_test.go:172`.** `StartKeepAlive(ctx, db, 1, 1)` passes 1 **nanosecond** — continuous ticker hammering `PingContext`; intended `time.Second`.

11. **LOW / medium — `pkg/db/db.go:147-179`.** No error classification: permanent failures (auth/DSN) get the full retry ladder (~15-30s); `pgx.ParseConfig("")` succeeds via env defaults so `Connect("")` is accepted and `TestConnectInvalidURL` burns ~15s of real sleep per run.

12. **LOW / medium — `pkg/sync/sync.go:83-96`.** `WatchAndSync` misses files inside newly created directories (only the dir gets a Create event) and uploads partial writes (no debounce on Write events).

13. **LOW / medium — `pkg/e2e/browser.go:162-186`.** Launcher never cleaned up (`l.Cleanup()` absent) — leaks a temp Chromium profile dir per test run; same pattern as browsercapture.

Clean: pkg/async (All/Settle/Map happens-before correct, Group sem/mutex/Close sound), websocket client.go/event.go/emitter.go (single close-point enforced by pointer-identity check), pkg/db/sqlite connect/DSN, pkg/config, pkg/logging, pkg/ptr, pkg/email, pkg/emailmock (body closed, LimitReader used), pkg/e2e assert.go/htmx.go.
