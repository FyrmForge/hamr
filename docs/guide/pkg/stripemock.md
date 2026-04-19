# Stripemock — Local Stripe Mock for Development

`hamr/pkg/stripemock` is an in-memory Stripe mock for local development. It
replaces the hosted Stripe checkout with a browser page where you pick the
outcome of each payment (`paid`, `failed`, `cancelled`).

Use it when you want to exercise your checkout code paths without real Stripe
keys, network calls, or test cards.

> **Dev only.** The package has no production safety guards. Mount it only
> when an env flag (e.g. `STRIPE_MOCK=true`) says to.

## Quick Start

```go
import "github.com/FyrmForge/hamr/pkg/stripemock"

client := stripemock.New(baseOrigin, "GBP") // ISO 4217 currency
stripemock.Mount(e, client)                  // registers /dev/stripe routes
```

From a handler that needs to create a checkout:

```go
session, err := client.CreateCheckoutSession(stripemock.CheckoutRequest{
    LineItems: []stripemock.LineItem{
        {Name: "Pro plan", Amount: 2000, Quantity: 1}, // £20.00
    },
    SuccessURL: "/purchase/success",
    CancelURL:  "/purchase/cancel",
    Metadata:   map[string]string{"user_id": "42"},
})
if err != nil {
    return err
}
return c.Redirect(http.StatusSeeOther, session.URL)
```

The `session.URL` points at the local dev page. The developer clicks an
outcome; `stripemock` flips the session status and redirects to your success
or cancel URL.

Later, when your code needs the outcome:

```go
result, err := client.GetSessionResult(sessionID)
// result.Status == stripemock.StatusPaid (or Failed / Cancelled)
```

## Routes

`Mount` registers two routes on the provided Echo instance:

| Method | Path                   | Purpose                              |
|--------|------------------------|--------------------------------------|
| GET    | `/dev/stripe`          | Renders the mock checkout page       |
| POST   | `/dev/stripe/complete` | Records the chosen outcome, redirects |

## Scaffold Integration

`hamr new --stripe` wires this in automatically. Generated `main.go`:

```go
envStripeMock     = config.GetEnvOrDefaultBool("STRIPE_MOCK", false)
envStripeCurrency = config.GetEnvOrDefault("STRIPE_CURRENCY", "GBP")
// ...
var stripeMock *stripemock.Client
if envStripeMock {
    stripeMock = stripemock.New(baseOrigin, envStripeCurrency)
    stripemock.Mount(srv.Echo(), stripeMock)
}
```

`stripeMock` is passed into `web.Deps.StripeMock` for handlers to use.

Set `STRIPE_MOCK=true` in `.env` for dev; leave unset (or `false`) in
production. `STRIPE_CURRENCY` defaults to `GBP`.

## Notes & Limits

- **Currency is per-client.** Configured at `stripemock.New(baseURL, code)`.
  Built-in symbols: GBP → `£`, USD → `$`, EUR → `€`, JPY → `¥`. Unknown codes
  render as `<CODE> x.xx` with 2 decimals.
- **Amounts are in the smallest currency unit.** Pence for GBP, cents for USD,
  whole yen for JPY.
- **Checkout sessions only.** Subscriptions, refunds, and webhook events are
  not yet supported.
- **In-memory.** Sessions are lost when the process exits.
- **No interface.** There is no real Stripe client in hamr yet, so no
  `stripe.Client` interface. Introduce one in your app when you add a real
  client.

## API Reference

```go
// Construction & mounting
func New(baseURL, currency string) *Client
func Mount(e *echo.Echo, client *Client)

// Client methods
func (c *Client) CreateCheckoutSession(req CheckoutRequest) (*CheckoutSession, error)
func (c *Client) GetSessionResult(sessionID string) (*PaymentResult, error)
func (c *Client) SetOutcome(sessionID string, status PaymentStatus) error
func (c *Client) GetRequest(sessionID string) (*CheckoutRequest, error)
func (c *Client) SuccessURL(sessionID string) string
func (c *Client) CancelURL(sessionID string) string
func (c *Client) Currency() string

// Types
type CheckoutRequest struct { LineItems []LineItem; SuccessURL, CancelURL string; Metadata map[string]string }
type LineItem        struct { Name string; Amount, Quantity int64 } // Amount is the smallest currency unit
type CheckoutSession struct { ID, URL, PaymentIntentID string }
type PaymentResult   struct { SessionID, PaymentIntentID string; Status PaymentStatus; Metadata map[string]string }

type PaymentStatus string
const (
    StatusPaid           PaymentStatus = "paid"
    StatusFailed         PaymentStatus = "failed"
    StatusCancelled      PaymentStatus = "cancelled"
    StatusRequiresAction PaymentStatus = "requires_action"
)
```
