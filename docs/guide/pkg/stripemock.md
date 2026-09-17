# Stripe Mock — Local Stripe Backend for Development

`hamr dev` runs a local Stripe-compatible HTTP backend that the official
`stripe-go` SDK talks to in development. Your app code uses real `stripe-go`
calls (`session.New(...)`, `webhook.ConstructEvent(...)`, etc.) — the *only*
dev/prod difference is which URL `stripe-go` is configured to hit.

> **Dev/staging.** Apps gate by leaving `STRIPE_MOCK` unset in production
> so `stripe-go` reaches the real `api.stripe.com`. The scaffold's `main.go`
> additionally refuses to start with `STRIPE_MOCK=true && DEV_MODE=false`,
> so a forgotten dev flag in a prod `.env` fails closed at startup rather
> than silently routing real payment calls at localhost. Staging
> environments that want fake payments must also set `DEV_MODE=true`.

## What changed from earlier hamr versions

Previous versions shipped `pkg/stripemock` — a custom client + dev-UI that
your handlers called directly via `deps.StripeMock.CreateCheckoutSession(...)`.
That package has been **removed**. The replacement is server-side only:
hamr's dev server hosts a Stripe-shaped HTTP backend, and your app uses the
real `stripe-go` SDK in both dev and production. No abstraction layer in
your handlers.

## Quick Start

`hamr new --stripe` wires everything for you. The two pieces that have to
match:

**`.env`** (scaffolded):
```
STRIPE_KEY=sk_test_dev_local
STRIPE_MOCK=true
STRIPE_WEBHOOK_SECRET=whsec_dev_<generated>
STRIPE_WEBHOOK_SECRET_V2=whsec_dev_<same>
```

**`hamr.toml`** (scaffolded):
```toml
[dev.stripe]
mode = "mock"
webhook_url = "http://localhost:8080/api/webhooks/stripe"
thin_webhook_url = "http://localhost:8080/api/webhooks/stripe/v2"
webhook_secret = "whsec_dev_<same-as-env>"
```

The `webhook_secret` values must agree — the mock signs with one, your app
verifies with the other. The mock signs v1 snapshot events and v2 thin
events with the same secret, like `stripe listen` does, so both
`STRIPE_WEBHOOK_SECRET` and `STRIPE_WEBHOOK_SECRET_V2` hold it locally. The
scaffold generates a single random value and writes it everywhere at
`hamr new` time.

**Generated `cmd/site/main.go`** wires `stripe-go`:
```go
stripe.Key = envStripeKey
if envStripeMock {
    if !envDevMode {
        log.Error("STRIPE_MOCK=true requires DEV_MODE=true (refusing to route Stripe calls to a local mock without dev mode)")
        os.Exit(1)
    }
    stripe.SetBackend(stripe.APIBackend, stripe.GetBackendWithConfig(
        stripe.APIBackend,
        &stripe.BackendConfig{URL: stripe.String(envHamrStripeMockURL)},
    ))
}
// Built after SetBackend so it inherits the mock backend.
stripeClient := stripe.NewClient(envStripeKey)
```

That's the whole wiring. In production leave `STRIPE_MOCK` unset and
`stripe-go` hits `api.stripe.com` directly. The mock URL is auto-injected
by `hamr dev` as `HAMR_STRIPE_MOCK_URL`, derived from `[proxy].listen` —
no hardcoded port to keep in sync.

**Production safety guard.** The `STRIPE_MOCK=true && !DEV_MODE` check is
deliberate. A leftover `STRIPE_MOCK=true` in a production `.env` would
otherwise route real payment calls to a non-existent localhost endpoint
and silently break checkouts; failing closed loud at startup is the only
acceptable behavior. Staging environments that want fake payments should
also set `DEV_MODE=true`.

## How a payment flows in dev

1. App handler calls `session.New(&stripe.CheckoutSessionParams{...})`.
2. `stripe-go` POSTs to `http://localhost:3000/v1/checkout/sessions` (the
   mock — same proxy mux as the `/__hamr/*` UI).
3. Mock returns a real `checkout.session`-shaped response with
   `URL = http://localhost:3000/__hamr/stripe/checkout?session=cs_test_...`.
4. App redirects the user to that URL.
5. Browser hits hamr's proxy at `/__hamr/stripe/checkout`, sees three
   outcome buttons (Pay / Card Declined / Cancel).
6. User clicks an outcome. The mock (except for a synchronous Card Declined,
   which leaves the session open and fires nothing — see the table below):
   - Updates the stored session's `status` and `payment_status`.
   - Fires real signed webhook(s) (HMAC-SHA256, `Stripe-Signature: t=...,v1=...`)
     to `webhook_url`.
   - Substitutes `{CHECKOUT_SESSION_ID}` in `success_url` / `cancel_url`
     (Stripe's documented placeholder pattern) before redirecting.
   - Redirects the user to the resulting URL.
7. App's success page can call `session.Get(session_id)` (using the
   substituted ID) to confirm the payment status before showing "thanks".
   App's existing `webhook.ConstructEvent(body, sig, secret)` verifies the
   signature and decodes the event — the same code path as production.

## Outcome → event mapping

| Button | Webhook event(s) | Status | Redirect |
|---|---|---|---|
| Pay Successfully | `checkout.session.completed`, `payment_intent.succeeded`, `charge.succeeded` | complete + paid | success_url |
| Card Declined | *(none)* | stays open | back to checkout page |
| Cancel Payment | `checkout.session.expired` | expired | cancel_url |

**Pay Successfully** also materialises the `PaymentIntent` (the id the session
advertised) and its `Charge`, so the app can retrieve the PI and refund the
checkout payment — mirroring real Stripe, which fires the payment events
alongside `checkout.session.completed`.

**Card Declined** models a *synchronous* decline: the session stays `open`, no
webhook fires, and the buyer is sent back to the checkout page to retry with
another card — exactly as real Stripe behaves (the decline is surfaced inline,
not as an event; `async_payment_failed` is only for delayed/async methods).

## Connect with Accounts v2

Stripe no longer lets new platforms create v1 Express accounts, so new
projects use Accounts v2. The mock serves both; the scaffold uses v2 for
accounts and v1 for payments (Stripe has no v2 payments API).

1. App calls `client.V2CoreAccounts.Create(...)` with a `recipient`
   configuration requesting `stripe_balance.stripe_transfers`. The mock
   returns the account with that capability (and `stripe_balance.payouts`,
   which Stripe grants alongside it) `pending`, plus requirements entries.
2. App calls `client.V2CoreAccountLinks.Create(...)`. The URL points at
   `/__hamr/stripe/onboarding?account=<id>`.
3. Until onboarding finishes, `client.V1Transfers.Create` to the account
   fails with `insufficient_capabilities_for_transfer`, like Stripe.
4. The user clicks **Complete Onboarding**. Every requested capability
   becomes `active`, and the mock fires two thin events to
   `thin_webhook_url`: `v2.core.account[requirements].updated` and
   `v2.core.account[configuration.recipient].capability_status_updated`.
   The browser is sent to the link's `return_url`.
5. The app's thin route verifies with `client.ParseEventNotification`,
   fetches the account, and sees `stripe_transfers` active. Transfers now
   succeed.

Thin events carry no object, only `related_object`. They are signed with
the same secret as snapshot events. With no `thin_webhook_url` they are
still recorded in the event log, but not sent.

## What the mock implements today

**Checkout sessions**
- `POST /v1/checkout/sessions` — create. `payment_intent_data.metadata`
  lands on the PaymentIntent and Charge; without it the session metadata
  is used. `customer_email` is echoed.
- `GET /v1/checkout/sessions/{id}` — retrieve
- `POST /v1/checkout/sessions/{id}/expire` — expire an open session, fires
  `checkout.session.expired`; 400 if the session is not open
- Same-currency-per-session validation
- Dev UI at `/__hamr/stripe/checkout` with three outcome buttons

**Connect accounts v2**
- `POST /v2/core/accounts` — create (JSON body). Requested capabilities
  under any configuration start `pending`.
- `GET /v2/core/accounts/{id}` — retrieve. `include[]` is accepted;
  configuration and requirements are always returned.
- `POST /v2/core/account_links` — onboarding or update link; remembers
  `return_url` for the onboarding page's redirect.
- v2 errors use the v2 error shape; timestamps are RFC 3339.
- Onboarding completion fires the thin events listed above.

**Connect accounts v1 (onboarding)**
- `POST /v1/accounts` — create connected account in pre-onboarding state
- `GET /v1/accounts/{id}` — retrieve current state (charges_enabled, payouts_enabled, requirements)
- `POST /v1/account_links` — generate hosted onboarding URL
- Dev UI at `/__hamr/stripe/onboarding?account=<id>` with Complete button
- Outcome fires real signed `account.updated` webhook

**PaymentIntents (Connect-aware)**
- `POST /v1/payment_intents` — create. Supports `application_fee_amount`,
  `transfer_data[destination]`, `on_behalf_of`, and the `Stripe-Account`
  request header (direct-charge model).
- `GET /v1/payment_intents/{id}` — retrieve. `latest_charge` is populated
  inline once the outcome runs.
- `POST /v1/payment_intents/{id}/confirm` — advance from
  `requires_payment_method` to `processing` for auto-capture (mirrors
  real Stripe's sync card flow), or to `requires_capture` if
  `capture_method=manual`. The dev UI drives the rest.
- `POST /v1/payment_intents/{id}/capture` — capture a `requires_capture`
  (manual-capture) PI: creates the Charge, advances to `succeeded`, and fires
  `payment_intent.succeeded` + `charge.succeeded` (+ `transfer.created` for
  destination charges). Supports partial capture via `amount_to_capture`.
  While in `requires_capture`, `amount_capturable` reports the authorized
  amount; after capture, `amount_received` reflects the captured amount.
- Validates `transfer_data.destination` references an existing connected
  account; rejects `application_fee_amount > amount`.
- Dev UI at `/__hamr/stripe/payment_intent?id=<pi_id>` with Succeed/Fail
  buttons. Renders application fee, transfer destination, and a
  Connect-pattern badge (destination charge vs direct charge).
- **Outcome cascade on succeed (destination charge)**: fires three signed
  webhooks in order — `payment_intent.succeeded`, `charge.succeeded`,
  `transfer.created`. Auto-creates an in-memory Charge and a Transfer
  whose amount is `amount - application_fee_amount` (or
  `transfer_data.amount` if explicitly set).
- **Outcome cascade on succeed (no destination)**: fires
  `payment_intent.succeeded` then `charge.succeeded`. No transfer.
- **Outcome cascade on fail**: fires `payment_intent.payment_failed` only.
  No charge is synthesised (mirrors real Stripe's sync card-decline path).

**Refunds (Connect-aware)**
- `POST /v1/refunds` — sync-success model (matches card refunds). Caller
  passes either `payment_intent` or `charge` (exactly one). Optional
  `amount` defaults to the charge's remaining unrefunded balance (captured
  minus refunded, so a partial capture caps the refund). Optional
  `reverse_transfer` and `refund_application_fee` mirror the real flags;
  the latter hands the application fee back to the connected account pro
  rata on direct charges.
- `GET /v1/refunds/{id}` — retrieve.
- Validates: source exists, amount fits within remaining balance, can't
  refund a fully-refunded charge.
- `GET /v1/refunds` — list, filtered by `charge` and/or `payment_intent`,
  newest first, with `limit` + `starting_after` paging so
  `List(...).All(ctx)` walks every page.
- A refund against a fully refunded charge fails with code
  `charge_already_refunded`; a disputed charge fails with `charge_disputed`.
- Cascade: updates `Charge.amount_refunded` and `Charge.refunded`; if
  `reverse_transfer=true` records a reversal on the transfer (1:1 with the
  refund, capped at what is left) and sets
  `Refund.source_transfer_reversal` to its id. Fires `charge.refunded` with
  the post-refund Charge, then `transfer.reversed` when a reversal happened.

**Transfers (separate charges and transfers)**
- `POST /v1/transfers` — send money to a connected account. v2 accounts
  must have `recipient.stripe_balance.stripe_transfers` active. An optional
  `source_transaction` must be an existing charge in the same currency with
  enough left on it after refunds and earlier transfers from it; without
  one the platform balance must cover the amount. Either shortfall is a 400
  with code `balance_insufficient`, so fund the platform with a payment
  first. Fires `transfer.created`.
- `GET /v1/transfers/{id}` — retrieve, reversals included.
- `POST /v1/transfers/{id}/reversals` — reverse some (`amount`) or all of a
  transfer. Fires `transfer.reversed`.

**Balance**
- Every charge carries `balance_transaction` and
  `payment_method_details.card` (visa, 4242).
- `GET /v1/balance_transactions/{id}` — amount, `fee`, `net` for a charge
  or a dispute movement. The fee is a flat standard rate: 1.5% + 20 for gbp
  and eur, 2.9% + 30 otherwise.
- `GET` / `POST /v1/balance_settings` — payout schedule, `delay_days` and
  `debit_negative_balances`, scoped by the `Stripe-Account` header.

**Disputes** (dashboard-driven; apps never create them)
- **Dispute** on a succeeded PaymentIntent opens a `needs_response`
  dispute for the unrefunded part of the charge and fires
  `charge.dispute.created` + `charge.dispute.funds_withdrawn`. A fully
  refunded charge cannot be disputed and a disputed charge cannot be
  refunded. The withdrawal lands on whoever holds the charge: the
  connected account for a direct charge, else the platform.
- **Close: won** fires `charge.dispute.closed` +
  `charge.dispute.funds_reinstated`; **Close: lost** fires
  `charge.dispute.closed`.
- `balance_transactions` on the dispute carry the fee: 2000 withdrawn. A won
  dispute adds a second entry returning the money with fee 0 — the dispute
  fee is never returned, as in real Stripe.

**Payouts (Connect-aware)**
- `POST /v1/payouts` — create a manually-triggered payout in `pending`
  state. Reads the `Stripe-Account` request header to scope the payout to
  a connected account; without the header, payout is on the platform's
  own balance.
- `GET /v1/payouts/{id}` — retrieve.
- `GET /v1/payouts` — list, scoped by `Stripe-Account` header (the
  marketplace dashboard query). Honors `?limit=`. Sorted newest-first.
- Validates: amount > 0, currency required, method ∈ {standard, instant};
  if Stripe-Account is set the connected account must exist.
- Dev UI at `/__hamr/stripe/payout?id=<po_id>` with Mark paid / Mark
  failed buttons.
- **Outcome on Mark paid**: status → `paid`, fires `payout.paid`.
- **Outcome on Mark failed**: status → `failed`, populates
  `failure_code="account_closed"` + `failure_message`, fires
  `payout.failed`.
- Events for a payout on a connected account set the event's top-level
  `account`, which is how an app ties the payout to a seller.
- **Pay out balance** on a connected account row stands in for Stripe's
  scheduled payout: it creates a pending automatic payout for the account's
  whole balance, then opens the payout page.

**Cross-cutting**
- Bracket-form decoding for `stripe-go`'s v1 nested params; JSON bodies for v2
- Real signed webhook delivery (HMAC-SHA256, `Stripe-Signature: t=...,v1=...`)
  for snapshot and thin events
- Idempotency: a POST that repeats an `Idempotency-Key` (same path and
  `Stripe-Account`) gets the first response replayed, with
  `Idempotent-Replayed: true`. A concurrent repeat waits for the first.
- Event log: every emitted event, with its delivery result, is kept (last
  200) so the dashboard can show and resend it with the same event id
- Stripe-shaped 4xx error responses surface as `*stripe.Error` to callers
- Same-origin guard on every state-mutating UI POST
- 409 Conflict on double-submit; 410 Gone on stale completed-session reload

**Not yet mocked.** Customer, Price, Subscription, Invoice, list endpoints
for transfers and reversals, v2 account update/close, dispute evidence, and
GET endpoints for Charge / ApplicationFee. The patterns from the existing
resources transfer directly — add as needed.

## API version pinning

The mock pins to a specific Stripe API version via a constant in
`internal/devserver/stripemock.go`. A test asserts it equals
`stripe.APIVersion` from the SDK in `go.mod`, so a `stripe-go` bump fails CI
unless the constant is bumped in lockstep.

Current pinned version: **`2026-08-26.dahlia`** (matches `stripe-go/v86`).

## Config

```toml
[dev.stripe]
mode                     = "mock"                                           # "off" (default) | "mock" | "listen"
webhook_url              = "http://localhost:8080/api/webhooks/stripe"      # required unless off
thin_webhook_url         = "http://localhost:8080/api/webhooks/stripe/v2"   # optional; v2 thin events
connect_webhook_url      = ""                                               # optional; connected-account events (empty = webhook_url)
thin_connect_webhook_url = ""                                               # optional; connected-account thin events (empty = thin_webhook_url)
webhook_secret           = "whsec_dev_..."                                  # required unless off; signs every event
persist                  = true                                             # default; set false for in-memory only
persist_path             = ".hamr/stripe/state.json"                        # default
```

The URLs' ports follow the app when `hamr dev` walks to a free one. Events
that carry a connected `account` go to the connect URLs when set.

Any mode but `off` requires both `webhook_url` and `webhook_secret` —
`hamr dev` refuses to start otherwise with an explicit error. It also
requires a `[proxy]` section, since the mock lives on the proxy mux.

`hamr dev` injects `STRIPE_MOCK=true` and `webhook_secret` as every
`STRIPE_WEBHOOK_SECRET*` var into your app, overriding `.env`.

## Listen mode

`mode = "listen"` (or `S` in the TUI, or the `stripe.mode` MCP tool) swaps
the mock for `stripe listen` against your real sandbox: hamr runs the Stripe
CLI with `STRIPE_KEY` from `.env`, forwards to the same URLs, injects
`STRIPE_MOCK=false` and the secret the CLI prints, and restarts your app.
The mock's dashboards and state stay up but it stops delivering webhooks.
See [`[dev.stripe]`](../hamr-toml.md) for the full behaviour.

## Persistence

State is persisted to a single JSON file at `.hamr/stripe/state.json`
(default), atomically rewritten on every mutation. On `hamr dev` restart
the file is loaded so sessions, PaymentIntents, v1 and v2 accounts,
transfers, refunds, payouts, balance settings and disputes all survive — useful for long-running dev sessions and for
LLM-driven workflows that need to read prior state across restarts.

The event log and idempotency keys are kept in memory only; a restart
clears them.

Corrupt or missing files are tolerated: missing = first-run, corrupt =
log a warning via `hamr dev` and start with empty state. Set
`persist = false` for ephemeral in-memory-only state.

The state schema is forward-compatible: unknown JSON fields are ignored,
missing fields stay zero. Adding fields to the in-memory structs is safe;
removing or renaming a field requires `rm .hamr/stripe/state.json`.

## Architecture: one mux

The Stripe API surface (`/v1/*`, `/v2/*`) and the dev UI (`/__hamr/stripe/*`) both
mount on hamr's proxy mux. Apps point `stripe-go` at the proxy URL
(`http://localhost:3000` by default) via `STRIPE_MOCK=true`.

`stripe-go` validates `req.URL.Path` starts with `/v1` after every request
— that's satisfied as long as the path begins with `/v1` regardless of
the listener. Earlier hamr versions used a dedicated `:3001` listener; the
single-mux model removes that port and the `api_listen` config knob.

**Path conflict with apps that serve their own `/v1/*`.** Because the mock
mounts real Stripe paths on the same mux as the reverse proxy, `ServeMux`
matches these before the catch-all and the request never reaches your app.
The mock claims:

- `/v1/checkout/sessions{,/}`
- `/v1/payment_intents{,/}`
- `/v1/accounts{,/}`, `/v1/account_links`
- `/v1/refunds{,/}`
- `/v1/payouts{,/}`
- `/v1/transfers{,/}`
- `/v1/balance_transactions/`, `/v1/balance_settings`
- `/v2/core/accounts{,/}`, `/v2/core/account_links`

If your own REST API is versioned at `/v1/*` and overlaps any of these,
the mock will eat those requests while `[dev.stripe]` is on. The
cleanest workaround is to serve your app's API under a different prefix
(e.g. `/api/v1/*`) — most hamr projects already do this. The mock has no
way to know which `/v1/*` paths are yours vs Stripe's, and `stripe-go`'s
`/v1`-prefix validation prevents moving the mock itself.

## Dashboard

Open `http://<proxy>/__hamr/stripe` to see every resource the mock has
captured: checkout sessions, v1 accounts, v2 accounts, PaymentIntents,
refunds, payouts, disputes and the event log, newest-first, capped at 25
rows per table. Connected account rows show the account's balance. The `hamr dev` panel shows an
"Open Stripe mock" shortcut that links here.

Per-row actions:
- **All terminal resources**: "Resend webhook" — re-fires the natural
  event(s) for the current state. For a succeeded destination-charge PI,
  this re-fires the full cascade (`payment_intent.succeeded`,
  `charge.succeeded`, `transfer.created`) in order.
- **Sessions (open)**: "Expire" — flips status to `expired` and fires
  `checkout.session.expired`.
- **PaymentIntents (succeeded)**: inline refund form — empty amount =
  full refund, set amount for partial. The "reverse" checkbox toggles
  `reverse_transfer` for destination charges. Calls the same internal
  `applyRefund` path as `refund.New`, so all the validation, charge
  mutation, and webhook delivery semantics are identical.
- **PaymentIntents (succeeded)**: "Dispute" — opens a dispute (see
  Disputes above).
- **Disputes (needs_response)**: "Close: won" / "Close: lost".
- **Connected accounts with a balance**: "Pay out balance".
- **v2 accounts not yet onboarded**: "Onboard" — the onboarding page.
- **Events**: "Resend" — redelivers that exact event (same id) to its
  webhook URL. The row shows delivered / failed (hover for the error) /
  not sent (no URL configured for that kind).
- **Pending PIs / Open sessions / Pending payouts**: "Resolve" — link to
  the existing per-resource outcome page where you pick the result.

## Read-only dashboards

Two more pages show the same records laid out like Stripe's own
dashboards, so QA can read them the way they read Stripe. They use hamr's
dark styling and a "hamr mock" badge, with no Stripe branding. Nothing on
them changes state; the controls stay on `/__hamr/stripe`.

**Platform dashboard** at `/__hamr/stripe/dashboard`. A side nav with:

| Section | Shows |
|---|---|
| Home | platform balance, recent payments and payouts |
| Payments | every PaymentIntent: amount, status (succeeded, refunded, partially refunded, disputed, failed, incomplete, uncaptured), card, customer, fee, metadata |
| Balances | balance per currency and every balance movement: charges with fees, refunds, transfers, reversals, dispute withdrawals and reversals, payouts |
| Connected accounts | v1 and v2 accounts with status, dashboard type, country, balance, and a link to each Express view |
| Transfers | amount, amount reversed, destination, source charge |
| Payouts | platform payouts |
| Disputes | amount, status, reason, evidence due date |
| Events | the event log with delivery result and the full payload |

**Express dashboard** at `/__hamr/stripe/express/<account>`. What one
connected account sees: balance, payout schedule (from balance settings),
payouts, activity (transfers in, reversals, direct charges) and account
details with capabilities and what is still needed.

Balances are worked out from the stored objects on every page load. There
is no pending/available split and no currency conversion.

## Logging

Mock log lines are tagged `[hamr:stripe]` (rendered alongside the standard
`[hamr dev]` lines from the proxy/file-watcher). The tag is plumbed via a
`component=stripe` slog attribute that hamr's `devHandler` interprets and
strips before formatting, so attrs in the log line itself are unaffected.
Filtering live logs is a `grep '\[hamr:stripe\]'` away.

Mock-emitted lines you'll see most:

- `webhook delivery failed` — your app server is down or returned non-2xx
- `dashboard resend failed` / `dashboard refund webhook delivery failed`
  / `dashboard expire webhook delivery failed` — same, fired from a
  dashboard action
- `mock enabled` (one-shot at boot, includes the proxy URL the mock is
  reachable on)

## Failure simulation

Three outcome buttons cover the common flows. For more exotic scenarios:

- **Webhook delivery failure**: stop your app server before clicking an
  outcome — the mock logs the delivery error.
- **Signature mismatch**: change `webhook_secret` in `hamr.toml` without
  also updating `.env`. The app's `webhook.ConstructEvent` will reject the
  event.
- **Network errors during create**: stop `hamr dev`. `stripe-go` reports a
  connection-refused error (the same error class your prod app would see if
  Stripe was unreachable).

For deterministic outcome testing in unit/integration tests, instantiate
the mock directly and seed sessions:

```go
mock := devserver.NewStripeMock(devserver.StripeMockOptions{...})
mock.SetWebhookEndpoint(devserver.WebhookEndpoint{URL: ..., Secret: ...})
// ... use httptest.Server to host mock.RegisterAPIRoutes(mux) ...
```

See `internal/devserver/stripemock_*_test.go` for the canonical patterns.

## Production path

Production wiring is the same code, with three env differences:

1. `STRIPE_MOCK` unset (or `false`) → `stripe-go` uses `api.stripe.com`.
2. `STRIPE_KEY` set to a real `sk_live_...` (or `sk_test_...` for Stripe's
   own test mode).
3. `STRIPE_WEBHOOK_SECRET` set to the secret from your Stripe dashboard
   webhook endpoint config, and `STRIPE_WEBHOOK_SECRET_V2` to the secret of
   the event destination that receives thin events (it is a different
   secret in production).

`hamr.toml`'s `[dev.stripe]` block is `mode = "off"` (or absent) in
production deployments — the mock never starts.

## See Also

- [emailmock](emailmock.md) — Same `hamr dev`-hosted mock pattern, applied to email
- [dev](dev.md) — `hamr dev` overview
- [mock-serve](mock-serve.md) — Running the mocks standalone in a container
- [stripe-go SDK reference](https://stripe.com/docs/api?lang=go)
- [Stripe webhook signing](https://stripe.com/docs/webhooks#signatures)
