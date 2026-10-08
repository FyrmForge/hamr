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

**Pay Successfully** also creates the `PaymentIntent` and its `Charge` at
that moment; the app learns the PI id from `checkout.session.completed` (or
the invoice, for a subscription) and can then retrieve it and refund the
checkout payment — mirroring real Stripe, which fires the payment events
alongside `checkout.session.completed`.

**Card Declined** models a *synchronous* decline: the session stays `open`, no
webhook fires, and the buyer is sent back to the checkout page to retry with
another card — exactly as real Stripe behaves (the decline is surfaced inline,
not as an event; `async_payment_failed` is only for delayed/async methods).

For a `mode=subscription` session, **Pay Successfully** also creates the
Subscription and its first paid Invoice and fires, between
`checkout.session.completed` and the payment events,
`customer.subscription.created`, `invoice.paid` and
`invoice.payment_succeeded`. A session whose total is zero after a 100%
coupon completes with `payment_status=no_payment_required` and no
PaymentIntent. As on Stripe, `payment_intent` on the session is null until
the buyer pays, and stays null for a subscription, whose PaymentIntent
hangs off the invoice.

## Coupons and promotion codes

The app creates coupons and codes the way it would against Stripe:

```go
coupon.New(&stripe.CouponParams{ID: stripe.String("SPRING25"), PercentOff: stripe.Float64(25),
    Duration: stripe.String("repeating"), DurationInMonths: stripe.Int64(3)})
promotioncode.New(&stripe.PromotionCodeParams{Code: stripe.String("SPRING"),
    Promotion: &stripe.PromotionCodePromotionParams{Type: stripe.String("coupon"), Coupon: stripe.String("SPRING25")}})
```

A discount reaches a checkout session one of two ways:

- The app passes `Discounts: [{Coupon: ...}]` or `[{PromotionCode: ...}]`
  when creating the session. An unknown, expired or exhausted coupon fails
  the create with a 400, like Stripe.
- The app sets `AllowPromotionCodes: true` and the buyer types a code on the
  mock's hosted page. A rejected code (unknown, inactive, expired, maxed
  out) re-renders the page with Stripe's inline message and leaves the
  session open. Codes match case-insensitively.

Either way the session reports `amount_subtotal` (gross), `amount_total`
(after discount), `total_details.amount_discount` and `discounts[]`. The
PaymentIntent and Charge are for the discounted total. `times_redeemed` on
the coupon and the code is counted when the session completes, not when it
is created. Percent discounts round to the nearest minor unit; an
`amount_off` coupon must be in the session's currency.

## Subscriptions and the mock clock

A subscription starts from a checkout session in `mode=subscription` whose
line items carry `price_data.recurring` or reference a recurring Price by
`price=<id>` (all items must share one interval). Paying it creates:

- a Subscription (`sub_test_…`, `status=active`, or `trialing` with `subscription_data[trial_end]`), with one item per line
  item — the referenced Price and Product ids, or synthesised ones for
  inline `price_data` — and `current_period_start/end` on the item;
- a Customer (`cus_test_…`) on the session and subscription, carrying the
  session's `customer_email`, unless the session was created with
  `customer=<id>`, which is kept;
- the first Invoice (`in_test_…`, `billing_reason=subscription_create`,
  `status=paid`), linked to the session's PaymentIntent through
  `payments[]`.

From then on the **mock clock** drives it. The clock is real time plus a
persisted offset, the mock's stand-in for Stripe's test clocks. Advance it
from the bar at the top of `/__hamr/stripe` (+1 day / +1 week / +1 month /
to a date), from the subscription row ("Next period" advances to that
subscription's period end, or just runs what is already due once real
time has passed it), or from an agent with `stripe.advance`. Every
renewal that falls due in between runs in time order, each stamped at its
own period end, so a three-month advance on a monthly plan produces three
cycles. Month and year periods keep the anchor day and clamp to month end
(a Jan 31 anchor bills Feb 28, then Mar 31); "+1 month" on the clock
clamps the same way. Webhook signatures stay on real time so
`webhook.ConstructEvent` keeps passing its tolerance check; every object's
`created` and period timestamps, and each event's `created`, come from the
mock clock. The clock never runs backwards and one advance covers at most
five years; the offset lives in the state file, so delete it to reset.

What a cycle does:

| Situation at period end | Result | Events |
|---|---|---|
| trialing, at `trial_end` | `active`, first real invoice (`subscription_cycle`) for the period starting at `trial_end`; paid or failed like a renewal, and the `once` coupon applies here | `customer.subscription.updated` (`trialing` to `active`), then the paid or failed events below |
| active | new period, paid renewal invoice (`subscription_cycle`) | `invoice.paid`, `invoice.payment_succeeded`, `customer.subscription.updated`, `payment_intent.succeeded`, `charge.succeeded` |
| active, "Fail next renewal" set | new period, invoice `open`, subscription `past_due` (a zero-total invoice cannot fail: it pays as above) | `invoice.payment_failed`, `customer.subscription.updated`, `payment_intent.payment_failed` |
| `cancel_at_period_end` (or `cancel_at` reached) | subscription `canceled` | `customer.subscription.deleted` |
| still `past_due` from the last cycle | dunning gives up: `canceled`, open invoice `void` | `customer.subscription.deleted`, `invoice.voided` |

**Trials.** A session in `mode=subscription` may carry
`subscription_data[trial_end]` (unix seconds). It must be at least 48 hours
after the mock clock, as on Stripe, and is refused with a 400 on
`mode=payment`. Mock sessions never expire, so the rule is checked again
at pay time: a session whose `trial_end` went stale is refused and stays
open. Paying such a session creates the subscription as
`trialing` (`trial_start` now, `trial_end` and `current_period_end` at
`trial_end`, `billing_cycle_anchor` = `trial_end`) with a £0 paid first
invoice and `payment_status=no_payment_required`: no PaymentIntent, no
Charge. The session's `amount_total` still shows the plan price; it is not
money received. The hosted checkout page shows £0 due today, then the
plan price from `trial_end`. Events: `checkout.session.completed`,
`customer.subscription.created`, `invoice.paid`,
`invoice.payment_succeeded`. A `once` coupon is not used up by the £0
invoice; it applies to the first real one. A `repeating` coupon's months
count from checkout, so the trial uses part of them, as on Stripe. "Fail next renewal" is allowed
while trialing and makes the first real invoice fail (`past_due`).
Cancelling during the trial (now, or `cancel_at_period_end`) never charges.
`trial_start` and `trial_end` stay on the subscription after the trial.
`trial_period_days`, `trial_settings` and trials without a card are not
modelled.

"Retry payment" on a `past_due` row pays the open invoice and returns the
subscription to `active` with the paid events. A coupon on the session is
snapshotted onto the subscription: `once` covers the first invoice only,
`repeating` the first N months, `forever` every invoice; the discount is
removed from the subscription when it runs out, as Stripe does.

The app manages the subscription through the usual calls:
`subscription.Get`, `subscription.Update` (`cancel_at_period_end`,
`cancel_at`, `metadata`, `description`; fires
`customer.subscription.updated`), `subscription.Cancel` (ends it now, fires
`customer.subscription.deleted`), `subscription.List` (by `customer`,
`status`, `price`; canceled ones hidden unless `status=all` or `canceled`),
`invoice.Get` and `invoice.List` (by `subscription`, `customer`,
`status`). `subscription.New` is refused with a message pointing at
Checkout.

Gate access on the subscription `status` from these events, not on a
wall-clock comparison with `current_period_end`: after an advance the
mock's timestamps sit ahead of the app's clock, exactly as under a Stripe
test clock.

## Customers, products and prices

The mock stores Customers, Products and Prices, so an app can set them up
the way it does against Stripe and reference them from Checkout:

```go
cust, _ := customer.New(&stripe.CustomerParams{
    Email: stripe.String("ada@example.com"),
})
price, _ := price.New(&stripe.PriceParams{
    Currency: stripe.String("gbp"), UnitAmount: stripe.Int64(2000),
    Recurring: &stripe.PriceRecurringParams{Interval: stripe.String("month")},
    ProductData: &stripe.PriceProductDataParams{Name: stripe.String("Pro")},
    LookupKey: stripe.String("pro_monthly"),
})
session.New(&stripe.CheckoutSessionParams{
    Mode: stripe.String("subscription"), Customer: stripe.String(cust.ID),
    LineItems: []*stripe.CheckoutSessionLineItemParams{
        {Price: stripe.String(price.ID), Quantity: stripe.Int64(1)},
    },
    SuccessURL: ..., CancelURL: ...,
})
```

**Customers.** `customer.New` (`email`, `name`, `description`, metadata),
`customer.Get`, `customer.Update` (same fields) and `customer.List`
(`email` filter, exact and case-insensitive; paged). A checkout session
created with `customer=<id>` must name a stored customer (unknown → 400
`resource_missing`) and cannot also pass `customer_email`; the session's
`customer_details {email, name}` is filled from the Customer from the
start. Without `customer=`, paying a `mode=subscription` session mints a
Customer carrying the session's `customer_email` and stores it, so
`customer.Get` on the id from `checkout.session.completed` works.
`customer_details` is null while a session is open with no customer
attached. Invoices carry `customer_email` and `customer_name` from the
Customer.

**Products and prices.** `product.New` (`name`, `description`, metadata,
`active`), `product.Get`, `product.List` (`active`). `price.New`
(`currency`, `unit_amount`, `recurring[interval]` +
`recurring[interval_count]` for a recurring price, `product=<id>` or
`product_data[name]` to create the product inline, `nickname`,
`lookup_key` with `transfer_lookup_key`, metadata, `active`), `price.Get`,
`price.Update` (`active`, metadata, `nickname`, `lookup_key`), `price.List`
(`active`, `product`, `type`, `lookup_keys[]`). A checkout line item can be
`price=<id>` instead of `price_data` (not both): a recurring price needs
`mode=subscription`, a one-off price works in `mode=payment`; unknown →
400 `resource_missing`, inactive → 400. A subscription created from
`price=` carries the real price and product ids on its item and serializes
the stored Price; inline `price_data` still gets synthesised ids.

Not mocked: Customer delete, product update, metered and tiered prices,
`currency_options`.

**Upgrading.** Subscriptions paid before this change hold a customer id
with no Customer record behind it, so `customer.Get` on that id is a 404
and a portal session for it a 400. `rm .hamr/stripe/state.json` if you
need those subscriptions to have Customers.

## Billing portal

`client.V1BillingPortalSessions.Create(ctx,
&stripe.BillingPortalSessionCreateParams{Customer: ..., ReturnURL: ...})`
(`customer` required and must exist; unknown → 400 `resource_missing`)
returns a session whose `url` is `/__hamr/stripe/portal?session=<id>` on
the proxy — a dev page standing in for Stripe's hosted customer portal.
It lists the customer's subscriptions (plan, status, period end,
cancel-at-period-end) and invoices, with:

- **Cancel at period end** / **Keep subscription** — flips
  `cancel_at_period_end` and fires `customer.subscription.updated`, the
  same as `subscription.Update`;
- **Update card** — a no-op that only shows a notice;
- **Back** — the session's `return_url`.

`client.V1BillingPortalConfigurations.Create` / `.List` keep one global
configuration; only `features[subscription_cancel][enabled]` is honoured
(default true). Set it to false and the cancel button disappears; the
action then answers 403. Not mocked: plan switching, payment-method
collection, everything else on the configuration.

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
  is used. `customer_email` is echoed; `customer=<id>` attaches a stored
  Customer instead (unknown → 400 `resource_missing`; not with
  `customer_email`). Line items are inline `price_data` or `price=<id>`
  of a stored Price (not both; unknown or inactive → 400). `mode` is
  `payment` (default) or `subscription`; subscription mode needs a
  recurring price on every line item, payment mode refuses one.
  `subscription_data.metadata` lands on the subscription.
  `discounts[0].coupon` / `.promotion_code` and `allow_promotion_codes`
  (not both) apply a discount; the response carries `amount_subtotal`,
  `amount_total`, `total_details.amount_discount`, `discounts[]`,
  `customer_details` (null while open with no customer), and after a
  subscription completes, `customer` and `subscription`. `payment_intent`
  is null until paid (always for a subscription).
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

**Coupons and promotion codes**
- `POST /v1/coupons` — create: `id` (optional, else generated), `name`,
  exactly one of `percent_off` / `amount_off` + `currency`, `duration`
  (`once` default, `repeating` + `duration_in_months`, `forever`),
  `max_redemptions`, `redeem_by`, metadata. A repeated `id` fails with
  `resource_already_exists`.
- `GET /v1/coupons` (paged) and `GET /v1/coupons/{id}`; `DELETE
  /v1/coupons/{id}` — sessions and subscriptions that already carry the
  coupon keep their snapshotted amounts.
- `POST /v1/promotion_codes` — `promotion[type]=coupon` +
  `promotion[coupon]`, `code` (generated when omitted; an active duplicate
  is a 400), `active`, `max_redemptions`, `expires_at`, metadata. The
  coupon is inlined under `promotion.coupon`.
- `GET /v1/promotion_codes` filtered by `code` (case-insensitive),
  `coupon`, `active`, paged; `GET` and `POST /v1/promotion_codes/{id}`
  (update `active`, metadata).
- Redemption checks: coupon `redeem_by` and `max_redemptions`, code
  `active`, `expires_at`, `max_redemptions`, run when the discount is
  attached and again when the session is paid, so a limit reached or a
  code deactivated while the session sat open stops the payment with the
  same inline message. `times_redeemed` and `valid` are kept current. A
  code's text can be reused once the old code is inactive; the active one
  is the one a buyer gets. Not modelled: minimum amount, first-purchase-only,
  customer and product restrictions, currency options.

**Subscriptions and invoices**
- Created only by completing a `mode=subscription` checkout session.
  `POST /v1/subscriptions` answers 400 with a message saying so.
- `GET /v1/subscriptions/{id}` — items with price (`recurring.interval`,
  `interval_count`, `unit_amount`), `current_period_start/end` on each
  item, `latest_invoice`, `discounts[]` (coupon under `source.coupon`),
  `cancel_at_period_end`, `cancel_at`, `canceled_at`, `ended_at`.
- `POST /v1/subscriptions/{id}` — `cancel_at_period_end`, `cancel_at`
  (empty clears), `metadata`, `description`; fires
  `customer.subscription.updated`. 400 on a canceled subscription.
- `DELETE /v1/subscriptions/{id}` — cancel now; fires
  `customer.subscription.deleted`. An open renewal invoice is voided
  (`invoice.voided`), as on Stripe.
- `GET /v1/subscriptions` — filters `customer`, `status` (default hides
  `canceled`; `all` shows everything), `price`; paged.
- `GET /v1/invoices/{id}` and `GET /v1/invoices` (filters `subscription`,
  `customer`, `status`; paged) — `parent.subscription_details.subscription`,
  `payments[]` with the PaymentIntent, `lines[]` with `pricing.price_details`
  and `parent.subscription_item_details`, `total_discount_amounts`,
  `status_transitions`, `customer_email` and `customer_name` from the
  Customer. `next_payment_attempt` is always null: the mock never retries
  on its own, only through the "Retry payment" action.
- Renewal invoices create their own PaymentIntent and Charge, so balances
  and the payments views follow.
- Not modelled: `trial_period_days`, proration, quantity or plan changes, pausing,
  invoice items.

**Customers**
- `POST /v1/customers` — create: `email`, `name`, `description`, metadata.
- `GET /v1/customers/{id}`; `POST /v1/customers/{id}` — update the same
  fields. Unknown id → 404 `resource_missing`.
- `GET /v1/customers` — `email` filter (exact, case-insensitive); paged.
- A paid `mode=subscription` session without `customer=` mints and stores
  one with the session's `customer_email`.
- Not modelled: delete, payment methods, tax ids.

**Products and prices**
- `POST /v1/products` — `name` (required), `description`, metadata,
  `active`. `GET /v1/products/{id}`; `GET /v1/products` (`active`; paged).
  No update.
- `POST /v1/prices` — `currency`, `unit_amount` (both required),
  `recurring[interval]` (`day|week|month|year`) + `recurring[interval_count]`
  (default 1) for a recurring price, else one-time; exactly one of
  `product=<id>` (unknown → 400 `resource_missing`) or `product_data[name]`;
  `nickname`, `lookup_key` (a key held by another price is a 400 unless
  `transfer_lookup_key=true` moves it), metadata, `active`.
- `GET /v1/prices/{id}`; `POST /v1/prices/{id}` — update `active`,
  metadata, `nickname`, `lookup_key` (+ `transfer_lookup_key`).
- `GET /v1/prices` — filters `active`, `product`, `type`
  (`one_time|recurring`), `lookup_keys[]`; paged.
- Prices are `per_unit`; not modelled: tiers, metered usage,
  `currency_options`.

**Billing portal**
- `POST /v1/billing_portal/sessions` — `customer` (required, must exist:
  unknown → 400 `resource_missing`), `return_url`. `url` is
  `/__hamr/stripe/portal?session=<id>`.
- `POST /v1/billing_portal/configurations` — creates or replaces the one
  global configuration; only `features[subscription_cancel][enabled]` is
  read (default true). `GET /v1/billing_portal/configurations` lists it.
- Dev UI at `/__hamr/stripe/portal?session=<id>`: the customer's
  subscriptions and invoices; "Cancel at period end" / "Keep subscription"
  (fires `customer.subscription.updated`; 403 when cancellation is
  disabled), "Update card" (no-op with a notice), "Back" to `return_url`.
- Not modelled: plan switching, payment-method collection.

**Mock clock**
- `now()` is real time plus a persisted offset, second granularity. Every
  object and event the mock creates is stamped with it; webhook signatures
  use real time.
- Advance from the dashboard bar (`POST /__hamr/stripe/clock` with `by=`
  `<n>d|<n>w|<n>m|<n>y` (months and years clamp to month end) or a Go
  duration such as `90m`, or `to=<RFC 3339 | unix seconds>`), a
  subscription row ("Next period": to its due time, or now if that has
  passed), or `stripe.advance`. Never backwards, at most five years per
  call. Runs every due cycle in time order.

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

**Not yet mocked.** `POST /v1/subscriptions`, `trial_period_days`, proration, plan
changes, Customer delete, product update, metered and tiered prices,
portal plan switching and payment-method collection, Stripe's
`/v1/test_helpers/test_clocks` API (the mock has one global clock
instead), list endpoints for transfers and reversals, v2 account
update/close, dispute evidence, and GET endpoints for Charge /
ApplicationFee. The patterns from the existing resources transfer
directly — add as needed.

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
transfers, refunds, payouts, balance settings, disputes, coupons,
promotion codes, subscriptions, invoices, customers, products, prices,
portal sessions, the portal configuration and the mock clock's offset all
survive — useful for long-running dev sessions and for LLM-driven
workflows that need to read prior state across restarts.

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
- `/v1/coupons{,/}`, `/v1/promotion_codes{,/}`
- `/v1/subscriptions{,/}`, `/v1/invoices{,/}`
- `/v1/customers{,/}`, `/v1/products{,/}`, `/v1/prices{,/}`
- `/v1/billing_portal/sessions`, `/v1/billing_portal/configurations`
- `/v2/core/accounts{,/}`, `/v2/core/account_links`

If your own REST API is versioned at `/v1/*` and overlaps any of these,
the mock will eat those requests while `[dev.stripe]` is on. The
cleanest workaround is to serve your app's API under a different prefix
(e.g. `/api/v1/*`) — most hamr projects already do this. The mock has no
way to know which `/v1/*` paths are yours vs Stripe's, and `stripe-go`'s
`/v1`-prefix validation prevents moving the mock itself.

## Dashboard

Open `http://<proxy>/__hamr/stripe` to see every resource the mock has
captured: checkout sessions, customers (email, name, subscription count),
prices (product, amount and interval, lookup key, active), subscriptions,
invoices, v1 accounts, v2 accounts, PaymentIntents, refunds, payouts,
disputes, coupons (with their promotion codes and redemption counts) and
the event log, newest-first,
capped at 25 rows per table. Connected account rows show the account's
balance. The `hamr dev` panel shows an "Open Stripe mock" shortcut that
links here. The `stripe.list` MCP tool returns the same snapshot for an
agent, customers and prices included.

A bar at the top shows the mock clock and how far ahead of real time it
runs, with **+1 day**, **+1 week**, **+1 month** and **Advance to** a date.
Advancing runs every subscription renewal that falls due (see
"Subscriptions and the mock clock").

Per-row actions:
- **All terminal resources**: "Resend webhook" — re-fires the natural
  event(s) for the current state. For a succeeded destination-charge PI,
  this re-fires the full cascade (`payment_intent.succeeded`,
  `charge.succeeded`, `transfer.created`) in order.
- **Sessions (open)**: "Expire" — flips status to `expired` and fires
  `checkout.session.expired`.
- **Subscriptions**: "Next period" advances the clock to the row's period
  end (or runs the overdue renewal at the current time); "Fail next renewal" toggles the next cycle's payment failing
  (`past_due`); "Retry payment" on a `past_due` row pays the open invoice;
  "Cancel" ends it now; "Resend" re-fires `customer.subscription.updated`
  (or `.deleted` once canceled).
- **Invoices**: "Resend" re-fires `invoice.paid` + `invoice.payment_succeeded`
  for a paid invoice, `invoice.payment_failed` for an open one,
  `invoice.voided` for a void one.
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
