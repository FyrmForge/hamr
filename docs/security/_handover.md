# Handover notes

Notes for whoever picks this up in a new session.

## Audit workflow used

1. **Claude pass** — manual read of `pkg/*` and `internal/cli/generator/templates/new/*`
2. **Codex pass A** — `codex exec` with no priming, asked for a fresh review
3. **Codex pass B** — fed both lists back, asked codex to merge/dedupe and verify each claim against source. One false positive killed (CSRF cookie expiry — Echo's defaults set it)
4. **Codex pass C** — gap hunt against the consolidated list, steered at unexplored areas (i18n, locale, errors, cache, gzip, sqlite perms, Dockerfile, CLI file ops)
5. **Codex pass D** — adversarial CVSS scoring against Claude's draft

Raw pass outputs were in `/tmp/hamr-sec/` and are gone now. If you need to defend a specific score, the README's "Audit method" line + this file is the only record. Re-running pass A on the same code should reproduce most findings.

## Conventions

- One file per finding: `NN-CVSS-slug.md`. Two-digit prefix, CVSS v3.1 base score, kebab slug.
- Sorted by CVSS descending, ties broken by business-impact judgement.
- File contents: title, CVSS+vector, file:line refs, what's wrong, exploit, fix. No good-parts section, no padding.
- README is the index.

## Open decisions

- **Threshold for "real finding" vs "hardening debt"** — findings 26–30 are all ≤2.6 and are framework-debt more than active vulnerabilities. User asked about splitting them into a `docs/security/hardening-debt/` subfolder. Not done; pending decision.
- **Triage / fix order** — no plan written yet. The 4 High findings (01–04) are the obvious starting point but no implementation plan exists.
- **Disclosure** — these are framework-internal findings, no external disclosure decision needed unless HAMR is published more broadly.

## Codex CLI gotchas

- `codex exec` will hang reading stdin if the prompt has certain shell metachars even with `</dev/null`. Workaround: write the prompt to a file, pass via `"$(cat file)"`, redirect stdin from `/dev/null`, redirect output to a file.
- One pass-D run hung for >1h before being killed. Output to a real file (not via `| tail`) — pipes confuse the "completed vs reading stdin" state.
- `--sandbox read-only --skip-git-repo-check` is the right combo for running inside the repo.

## What's *not* covered

Pass C noted no findings in: `pkg/i18n`, `pkg/middleware/locale.go`, `pkg/ctx`, `pkg/fingerprint`, `pkg/janitor`, `pkg/async`, `pkg/sync`, `pkg/middleware/cors.go`, `pkg/middleware/rbac.go`, `pkg/middleware/subject.go`, `pkg/emailmock`, scaffold gitignore, scaffold workflows. "No findings" here means "codex looked and didn't see anything" — not "verified clean." Worth a second pass if something downstream depends on those areas.

The scaffold's `internal/devserver/*` was *not* audited in depth — only confirmed it's dev-only and doesn't ship in the user's binary. If `hamr dev` ever becomes deployable, that's a separate review.

## If continuing

Re-read the README first, then pick a thread:
- Implement fixes (start at 01, work down by CVSS).
- Split off hardening-debt (26–30) if that decision lands.
- Run pass C against the areas listed above as "not covered" to convert them from "codex glanced" to "verified clean."
- Add deferred items I dropped at consolidation: per-route `Cache-Control` audit, password breach-list check, signed flash cookie pattern.
