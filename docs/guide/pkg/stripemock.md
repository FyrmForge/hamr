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
```

**`hamr.toml`** (scaffolded):
```toml
[dev.stripe]
enabled = true
webhook_url = "http://localhost:8080/api/webhooks/stripe"
webhook_secret = "whsec_dev_<same-as-env>"
```

Both `webhook_secret` values must agree — the mock signs with one, your app
verifies with the other. The scaffold generates a single random value and
writes it to both files at `hamr new` time.

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
6. User clicks an outcome. The mock:
   - Updates the stored session's `status` and `payment_status`.
   - Fires a real signed webhook (HMAC-SHA256, `Stripe-Signature: t=...,v1=...`)
     to `webhook_url`.
   - Substitutes `{CHECKOUT_SESSION_ID}` in `success_url` / `cancel_url`
     (Stripe's documented placeholder pattern) before redirecting.
   - Redirects the user to the resulting URL.
7. App's success page can call `session.Get(session_id)` (using the
   substituted ID) to confirm the payment status before showing "thanks".
   App's existing `webhook.ConstructEvent(body, sig, secret)` verifies the
   signature and decodes the event — the same code path as production.

## Outcome → event mapping

| Button | Webhook event | Status | Redirect |
|---|---|---|---|
| Pay Successfully | `checkout.session.completed` | complete + paid | success_url |
| Card Declined | `checkout.session.async_payment_failed` | complete + unpaid | cancel_url |
| Cancel Payment | `checkout.session.expired` | expired | cancel_url |

## What the mock implements today

**Checkout sessions**
- `POST /v1/checkout/sessions` — create
- `GET /v1/checkout/sessions/{id}` — retrieve
- Same-currency-per-session validation
- Dev UI at `/__hamr/stripe/checkout` with three outcome buttons

**Connect accounts (onboarding)**
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
  `amount` defaults to the charge's remaining unrefunded balance. Optional
  `reverse_transfer` and `refund_application_fee` mirror the real flags.
- `GET /v1/refunds/{id}` — retrieve.
- Validates: source exists, amount fits within remaining balance, can't
  refund a fully-refunded charge.
- Cascade: updates `Charge.amount_refunded` and `Charge.refunded`; if
  `reverse_transfer=true` increments `Transfer.amount_reversed` and
  populates `Refund.source_transfer_reversal` with a synthesised ID.
  Fires `charge.refunded` with the post-refund Charge as the payload.

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

**Cross-cutting**
- Bracket-form decoding for `stripe-go`'s nested params
- Real signed webhook delivery (HMAC-SHA256, `Stripe-Signature: t=...,v1=...`)
- Stripe-shaped 4xx error responses surface as `*stripe.Error` to callers
- Same-origin guard on every state-mutating UI POST
- 409 Conflict on double-submit; 410 Gone on stale completed-session reload

**Not yet mocked.** Standalone Transfer create, Customer, Price,
Subscription, Invoice, Dispute, and GET endpoints for Charge / Transfer /
ApplicationFee. The patterns from the existing resources transfer
directly — add as needed.

## API version pinning

The mock pins to a specific Stripe API version via a constant in
`internal/devserver/stripemock.go`. A test asserts it equals
`stripe.APIVersion` from the SDK in `go.mod`, so a `stripe-go` bump fails CI
unless the constant is bumped in lockstep.

Current pinned version: **`2025-08-27.basil`** (matches `stripe-go/v82`).

## Config

```toml
[dev.stripe]
enabled        = true                                          # opt-in; default false
webhook_url    = "http://localhost:8080/api/webhooks/stripe"   # required when enabled
webhook_secret = "whsec_dev_..."                               # required when enabled
persist        = true                                          # default; set false for in-memory only
persist_path   = ".hamr/stripe/state.json"                     # default
```

`enabled = true` requires both `webhook_url` and `webhook_secret` —
`hamr dev` refuses to start otherwise with an explicit error. It also
requires a `[proxy]` section, since the mock lives on the proxy mux.

## Persistence

State is persisted to a single JSON file at `.hamr/stripe/state.json`
(default), atomically rewritten on every mutation. On `hamr dev` restart
the file is loaded so sessions, PaymentIntents, accounts, refunds, and
payouts all survive — useful for long-running dev sessions and for
LLM-driven workflows that need to read prior state across restarts.

Corrupt or missing files are tolerated: missing = first-run, corrupt =
log a warning via `hamr dev` and start with empty state. Set
`persist = false` for ephemeral in-memory-only state.

The state schema is forward-compatible: unknown JSON fields are ignored,
missing fields stay zero. Adding fields to the in-memory structs is safe;
removing or renaming a field requires `rm .hamr/stripe/state.json`.

## Architecture: one mux

The Stripe API surface (`/v1/*`) and the dev UI (`/__hamr/stripe/*`) both
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

If your own REST API is versioned at `/v1/*` and overlaps any of these,
the mock will eat those requests while `[dev.stripe].enabled = true`. The
cleanest workaround is to serve your app's API under a different prefix
(e.g. `/api/v1/*`) — most hamr projects already do this. The mock has no
way to know which `/v1/*` paths are yours vs Stripe's, and `stripe-go`'s
`/v1`-prefix validation prevents moving the mock itself.

## Dashboard

Open `http://<proxy>/__hamr/stripe` to see every resource the mock has
captured: 5 tables (sessions, accounts, PaymentIntents, refunds, payouts),
newest-first, capped at 25 rows per table. The `hamr dev` panel shows an
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
- **Pending PIs / Open sessions / Pending payouts**: "Resolve" — link to
  the existing per-resource outcome page where you pick the result.

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
   webhook endpoint config.

`hamr.toml`'s `[dev.stripe]` block is `enabled = false` (or absent) in
production deployments — the mock never starts.

## See Also

- [emailmock](emailmock.md) — Same `hamr dev`-hosted mock pattern, applied to email
- [dev](dev.md) — `hamr dev` overview
- [stripe-go SDK reference](https://stripe.com/docs/api?lang=go)
- [Stripe webhook signing](https://stripe.com/docs/webhooks#signatures)
