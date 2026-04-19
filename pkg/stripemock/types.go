// Package stripemock is a local, in-memory Stripe mock intended for development.
//
// It exposes a client with a small subset of the Stripe checkout surface
// (create session, read result) plus a browser-rendered dev page where the
// developer chooses the outcome of each session. The package is deliberately
// dev-only: it performs no real charges and has no safety guards against being
// mounted in production — the caller is responsible for gating it (e.g. behind
// a STRIPE_MOCK env var in main.go).
//
// Currency is configured on the client (stripemock.New(baseURL, "GBP")) and
// amounts are passed in the smallest unit of that currency (pence for GBP,
// cents for USD, whole yen for JPY).
package stripemock

// CheckoutRequest holds the parameters for creating a mock checkout session.
type CheckoutRequest struct {
	LineItems  []LineItem
	SuccessURL string
	CancelURL  string
	Metadata   map[string]string
}

// LineItem represents a single item in a checkout session.
type LineItem struct {
	Name     string
	Amount   int64 // smallest currency unit (pence for GBP, cents for USD, whole yen for JPY)
	Quantity int64
}

// CheckoutSession is the result of creating a checkout session.
type CheckoutSession struct {
	ID              string
	URL             string
	PaymentIntentID string
}

// PaymentStatus represents the outcome of a mocked payment.
type PaymentStatus string

const (
	StatusPaid           PaymentStatus = "paid"
	StatusFailed         PaymentStatus = "failed"
	StatusCancelled      PaymentStatus = "cancelled"
	StatusRequiresAction PaymentStatus = "requires_action"
)

// PaymentResult is returned when querying a session's outcome.
type PaymentResult struct {
	SessionID       string
	PaymentIntentID string
	Status          PaymentStatus
	Metadata        map[string]string
}
