# Proposed: Stripe mock on stripe-go v86, Connect v2 catch-up, fake dashboards

Status: **steps 1–4 implemented (unreleased)** — see the changelog entry and
`docs/guide/pkg/stripemock.md`.

## Why

sportfreelancers.com (`~/SportFreelancers/sportfreelancers.com`) turned the
hamr Stripe mock off (`hamr.toml` `[dev.stripe] enabled = false`) and now
develops against a Stripe sandbox plus `stripe listen`. Their write-up is in
`docs/notes-and-plans/96-accounts-v2-plan.md` over there. Three reasons:

1. Stripe refuses v1 `type: express` account creation for new platforms.
   The app moved to Accounts v2. The mock only has v1 accounts.
2. Their sandbox stamps events `dahlia`. The mock stamps `2025-08-27.basil`
   (`internal/devserver/stripemock.go:66`). stripe-go's webhook check
   compares the train name, so a `dahlia` app rejects every mock event.
3. v2 account changes arrive as thin event notifications on a second
   webhook. The mock never sends any.

What the app calls vs what the mock serves:

| App call (stripe-go client) | Mock route |
|---|---|
| `V2CoreAccounts.Create/Retrieve` | none |
| `V2CoreAccountLinks.Create` | v1 `/v1/account_links` only |
| `V1Transfers.Create/Retrieve`, `V1TransferReversals.Create` | none |
| `V1BalanceSettings.Update`, `V1BalanceTransactions.Retrieve` | none |
| `V1Refunds.Create/List/Retrieve` | yes |
| `V1CheckoutSessions.Create/Expire` | yes |

## SDK version research (2026-09-16)

- Newest stripe-go: **v86.4.2**, pinned to **`2026-08-26.dahlia`**, which is
  also Stripe's newest stable API version. No v87.
- Per-major pins: v82 basil, v83 `2025-09-30.clover`, v84 `2025-11-17.clover`,
  v85 `2026-03-25.dahlia`, v86 `2026-05-27.dahlia` → `2026-08-26.dahlia` at 86.4.0.
- `webhook.ConstructEvent` only compares the train (`dahlia`), not the date.
  A mock tagged `2026-08-26.dahlia` is accepted by v85 and v86 apps alike.

Breaking changes v82 → v86 that touch hamr:

- v83: thin-event parsing rewritten (`ParseThinEvent` → `ParseEventNotification`,
  typed notifications). New work for step 2, nothing to fix in step 1.
- v84: v2 requests serialise lists as `include[0]=a&include[1]=b`. The new v2
  mock routes must read that form.
- v85: SDK errors when a webhook is parsed with the wrong method (snapshot vs
  thin). The scaffolded handler must route by endpoint.
- v85: `List`/`Search` on `stripe.Client` need `.All(ctx)`. Templates have no
  `List` calls; nothing to change.
- v86.4.0: thin events now exist for v1 objects too; new
  `EventNotificationHandler`. One thin-event format for the mock to emit.
- v86.4.2: SDK rejects empty webhook secrets. `hamr new` already generates one
  (`internal/cli/generator/project.go`).

Not touching us: coupon/discount, issuing, tax, billing, `tax_details`
type change, Go < 1.22 drop (hamr is on 1.25).

v85 vs v86: no reason to stop at v85. sportfreelancers moving v85 → v86 is an
import-path change; they use none of the v86-breaking fields.

## Agreed

- Target **stripe-go v86.4.2**, mock stamps `2026-08-26.dahlia`.
- One jump v82 → v86, no stepping through majors.
- Order: (1) SDK upgrade, (2) mock catch-up, (3) fake platform dashboard,
  (4) fake Express dashboard. Each its own PR-sized chunk.
- Accounts default to v2 everywhere; payments stay v1 (Stripe has no v2 for
  them). The mock keeps its v1 account routes for older apps. New projects
  scaffold a second thin-event webhook route, v2 account event handlers in
  place of the `account.updated` stub, and a second secret line in
  `.env.example`. One signing secret covers both webhooks under the mock,
  same as `stripe listen`.
- Fake dashboards copy Stripe's layout (nav sections, table columns) but keep
  hamr's dark dev styling and a "hamr mock" badge on every page. No Stripe
  logo or branding.
- Steps 2–4 run back to back: `make ai` + stale-reference grep after each,
  docs/llmsdocs/changelog kept current, stop only for a decision the repos
  can't answer, finish with a scratchpad run of sportfreelancers against the
  mock. No commits. `[dev.stripe]` thin-event webhook URL is optional; unset
  means no thin events fire, so existing configs still validate.
- The current `/__hamr/stripe` page keeps the mock *controls* (resend,
  expire, refund, resolve). The new dashboards are read-only records, shaped
  like what Stripe actually shows.

## Step 1 — SDK upgrade v82 → v86

Mechanical. ~15 files. No behaviour change except the version stamp.

**Dependency**
- `go.mod`: `stripe-go/v82 v82.5.1` → `stripe-go/v86 v86.4.2`; tidy.

**Mock (`internal/devserver/`)**
- `stripemock.go:66` const → `2026-08-26.dahlia`; fix the "v82" comment.
- `stripemock_webhook.go:103` example comment shows `basil`; update.
- Import path `v82` → `v86` in `stripemock.go` and the 8 `stripemock_*_test.go`
  files. Symbols used (`stripe.Event`, `stripe.APIBackend`, `stripe.String`,
  `SetBackend`, `GetBackendWithConfig`, `BackendConfig`, `Key`,
  `webhook.ConstructEvent`) all exist unchanged in v86.
- `stripemock_test.go:21` already asserts the const equals `stripe.APIVersion`;
  it guards the bump.

**Scaffold templates (`internal/cli/generator/templates/new/`)**
- `cmd/site/main.go.tmpl`, `internal/api/handler/stripe/handler.go.tmpl`:
  import path → `v86`.
- `root/go.mod.tmpl` does not pin stripe-go; `hamr new` runs `go get ./...`
  so new projects resolve v86 on their own.
- `internal/cli/generator/project_test.go`: check no assertion greps for `v82`.

**Docs**
- `docs/guide/pkg/stripemock.md:211`, `llmsdocs/llms.txt:453-454`,
  `llmsdocs/llms-full.txt:2495`: version + SDK references.
- `docs/changelog.md` Unreleased entry.

**Verify**
- `make ai`.
- `hamr new` a throwaway project with stripe on (scratchpad), confirm it
  builds against v86.
- Repo-wide grep for `v82` and `basil` before done.

## Step 2 — mock catch-up (shape agreed, details open)

Add what sportfreelancers needs to turn the mock back on:

- Accounts v2: `POST/GET /v2/core/accounts[/{id}]`, `POST /v2/core/account_links`.
  Read v2 query lists in indexed form. v2 requests are JSON bodies, v1 are
  form-encoded — separate decode path. The v1 `/v1/accounts` and
  `/v1/account_links` routes stay; existing hamr apps use them.
- Transfers + reversals: `POST/GET /v1/transfers[/{id}]`,
  `POST /v1/transfers/{id}/reversals`. The mock already stores transfers
  (`stripeTransfer` in `internal/devserver/stripemock_charge.go`, reversals
  tracked via `AmountReversed`) and fires `transfer.created`; only the HTTP
  routes are missing.
- Balance: `/v1/balance_settings` (update), `/v1/balance_transactions/{id}`.
  A per-account ledger the dashboards in steps 3–4 read from.
- Thin events: second webhook target (`[dev.stripe] thin_webhook_url` next to
  `webhook_url` in `internal/devserver/config.go`), signed with the same or a
  second secret. Match the `stripe listen --forward-thin-to` shape. The two
  events sportfreelancers subscribes to, and the minimum the mock must emit:
  - `v2.core.account[configuration.recipient].capability_status_updated`
  - `v2.core.account[requirements].updated`
- Event log: store every emitted event (snapshot + thin) so the platform
  dashboard can show it and "resend" can replay by id.

Found while reading sportfreelancers' app code (not in the first draft):

- `POST /v1/checkout/sessions/{id}/expire` — the SDK route. The mock only had
  the dashboard button.
- `GET /v1/refunds` list with `charge=` filter and cursor paging (`.All(ctx)`).
- `balance_transaction` on every charge, plus
  `GET /v1/balance_transactions/{id}` returning the Stripe fee. Without it the
  app never records a fee.
- `payment_method_details.card` on charges (brand, last4).
- Connected-account payouts: the app never calls the payouts API; Stripe pays
  out on a schedule and the only signal is `payout.paid`/`payout.failed` with
  the event's top-level `account` set. Needs a dashboard control and the
  `account` field on the event envelope.
- Disputes: `charge.dispute.created/updated/closed` with
  `balance_transactions[].fee`. Needs a dashboard control.
- Idempotency keys: the app relies on a replayed key returning the first
  result (account create, transfers, refunds).

Sub-order: v1 gaps → v2 accounts + links → thin events → event log.
Settled from the repos: one signing secret for both webhooks (same HMAC scheme
in stripe-go), v2 accounts stored in their own map/type, v2 errors use the v2
error shape, v2 timestamps are RFC 3339.

## Step 3 — fake platform dashboard

`/__hamr/stripe/dashboard`, read-only. Payments, Connect accounts, balance,
transfers/payouts, events log. Layout follows the real Stripe dashboard's
nav so QA reads it the same way. Spec after step 2 lands.

## Step 4 — fake Express dashboard

`/__hamr/stripe/express/{acct}`, read-only. Balance, payouts, account
status for one connected account. Reached from the account row on the
platform dashboard. sportfreelancers sets `dashboard: express` on its v2
accounts but never calls login links, so no login-link route is planned;
add one if an app needs it. Spec after step 2 lands.

## Out of scope

- Deployed environments for sportfreelancers; local only.
- Embedded onboarding components; hosted account links stay.
