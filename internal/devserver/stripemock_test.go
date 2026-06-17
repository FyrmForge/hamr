package devserver

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	stripe "github.com/stripe/stripe-go/v82"
	checkoutsession "github.com/stripe/stripe-go/v82/checkout/session"
)

// TestStripeAPIVersionMatchesSDK guards against drift: hamr's pinned API
// version must equal whatever stripe-go/v82 ships with. If a stripe-go bump
// changes APIVersion, this test fires immediately and forces a paired bump
// of the constant in stripemock.go.
func TestStripeAPIVersionMatchesSDK(t *testing.T) {
	assert.Equal(t, stripe.APIVersion, stripeAPIVersion,
		"stripemock.go's stripeAPIVersion must match stripe-go's stripe.APIVersion")
}

// TestStripeMock_CreateAndRetrieveSession exercises the full HTTP path: a
// real stripe-go client encodes the request as bracket-form, POSTs it to the
// mock, parses the response back into stripe.CheckoutSession, then retrieves
// the same session by ID. Asserts the round-tripped fields match what the
// caller submitted.
func TestStripeMock_CreateAndRetrieveSession(t *testing.T) {
	mock, srv := newTestStripeServer(t)

	withStripeBackend(t, srv.URL, func() {
		params := &stripe.CheckoutSessionParams{
			Mode: stripe.String(string(stripe.CheckoutSessionModePayment)),
			LineItems: []*stripe.CheckoutSessionLineItemParams{
				{
					PriceData: &stripe.CheckoutSessionLineItemPriceDataParams{
						Currency: stripe.String("gbp"),
						ProductData: &stripe.CheckoutSessionLineItemPriceDataProductDataParams{
							Name: stripe.String("Pro plan"),
						},
						UnitAmount: stripe.Int64(2000),
					},
					Quantity: stripe.Int64(1),
				},
				{
					PriceData: &stripe.CheckoutSessionLineItemPriceDataParams{
						Currency: stripe.String("gbp"),
						ProductData: &stripe.CheckoutSessionLineItemPriceDataProductDataParams{
							Name: stripe.String("Add-on"),
						},
						UnitAmount: stripe.Int64(500),
					},
					Quantity: stripe.Int64(2),
				},
			},
			SuccessURL: stripe.String("https://app.example/checkout/success"),
			CancelURL:  stripe.String("https://app.example/checkout/cancel"),
		}
		params.AddMetadata("user_id", "42")
		params.AddMetadata("plan", "pro")

		created, err := checkoutsession.New(params)
		require.NoError(t, err, "session.New round-trip failed")

		require.True(t, strings.HasPrefix(created.ID, "cs_test_"), "id %q missing cs_test_ prefix", created.ID)
		assert.Equal(t, stripe.Currency("gbp"), created.Currency)
		assert.Equal(t, int64(3000), created.AmountTotal, "2000 + 2*500")
		assert.Equal(t, stripe.CheckoutSessionModePayment, created.Mode)
		assert.Equal(t, "https://app.example/checkout/success", created.SuccessURL)
		assert.Equal(t, "https://app.example/checkout/cancel", created.CancelURL)
		assert.Equal(t, stripe.CheckoutSessionStatus("open"), created.Status)
		assert.Equal(t, stripe.CheckoutSessionPaymentStatus("unpaid"), created.PaymentStatus)
		assert.False(t, created.Livemode)
		assert.Equal(t, "42", created.Metadata["user_id"])
		assert.Equal(t, "pro", created.Metadata["plan"])
		assert.NotNil(t, created.PaymentIntent, "payment_intent should be present (as ID-only ref)")
		if created.PaymentIntent != nil {
			assert.True(t, strings.HasPrefix(created.PaymentIntent.ID, "pi_test_"),
				"payment_intent.id %q missing pi_test_ prefix", created.PaymentIntent.ID)
		}
		assert.Contains(t, created.URL, "/__hamr/stripe/checkout?session="+created.ID)

		// Verify state persisted on the mock side.
		mock.mu.RLock()
		_, ok := mock.sessions[created.ID]
		mock.mu.RUnlock()
		require.True(t, ok, "session not stored in mock")

		// Retrieve by ID and check fields survive.
		retrieved, err := checkoutsession.Get(created.ID, nil)
		require.NoError(t, err)
		assert.Equal(t, created.ID, retrieved.ID)
		assert.Equal(t, created.AmountTotal, retrieved.AmountTotal)
		assert.Equal(t, created.Currency, retrieved.Currency)
	})
}

// TestStripeMock_RejectsMixedCurrencies asserts the mock errors when line
// items disagree on currency, mirroring real Stripe's validation.
func TestStripeMock_RejectsMixedCurrencies(t *testing.T) {
	_, srv := newTestStripeServer(t)

	withStripeBackend(t, srv.URL, func() {
		params := &stripe.CheckoutSessionParams{
			Mode: stripe.String(string(stripe.CheckoutSessionModePayment)),
			LineItems: []*stripe.CheckoutSessionLineItemParams{
				{
					PriceData: &stripe.CheckoutSessionLineItemPriceDataParams{
						Currency: stripe.String("gbp"),
						ProductData: &stripe.CheckoutSessionLineItemPriceDataProductDataParams{
							Name: stripe.String("In GBP"),
						},
						UnitAmount: stripe.Int64(1000),
					},
					Quantity: stripe.Int64(1),
				},
				{
					PriceData: &stripe.CheckoutSessionLineItemPriceDataParams{
						Currency: stripe.String("usd"),
						ProductData: &stripe.CheckoutSessionLineItemPriceDataProductDataParams{
							Name: stripe.String("In USD"),
						},
						UnitAmount: stripe.Int64(1000),
					},
					Quantity: stripe.Int64(1),
				},
			},
			SuccessURL: stripe.String("https://app.example/ok"),
			CancelURL:  stripe.String("https://app.example/no"),
		}
		_, err := checkoutsession.New(params)
		require.Error(t, err, "expected mixed-currency rejection")
		assert.Contains(t, err.Error(), "does not match earlier currency")
	})
}

// TestStripeMock_RetrieveUnknownSession asserts the mock returns Stripe's
// canonical 404 shape so stripe-go raises a *stripe.Error.
func TestStripeMock_RetrieveUnknownSession(t *testing.T) {
	_, srv := newTestStripeServer(t)

	withStripeBackend(t, srv.URL, func() {
		_, err := checkoutsession.Get("cs_test_nonexistent", nil)
		require.Error(t, err)
		var stripeErr *stripe.Error
		if assert.ErrorAs(t, err, &stripeErr) {
			assert.Equal(t, http.StatusNotFound, stripeErr.HTTPStatusCode)
		}
	})
}

// TestDecodeBracketForm covers the parser in isolation against the wire shape
// stripe-go's form encoder produces.
func TestDecodeBracketForm(t *testing.T) {
	values := url.Values{
		"mode":                                   {"payment"},
		"success_url":                            {"https://x"},
		"line_items[0][price_data][currency]":    {"gbp"},
		"line_items[0][price_data][unit_amount]": {"2000"},
		"line_items[0][price_data][product_data][name]": {"Pro"},
		"line_items[0][quantity]":                       {"1"},
		"line_items[1][price_data][currency]":           {"gbp"},
		"line_items[1][quantity]":                       {"3"},
		"metadata[user_id]":                             {"42"},
	}
	out, err := decodeBracketForm(values)
	require.NoError(t, err)

	assert.Equal(t, "payment", out["mode"])
	items, ok := out["line_items"].([]any)
	require.True(t, ok, "line_items should decode to []any")
	require.Len(t, items, 2)

	first, ok := items[0].(map[string]any)
	require.True(t, ok)
	priceData, ok := first["price_data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "gbp", priceData["currency"])
	assert.Equal(t, "2000", priceData["unit_amount"])
	assert.Equal(t, "1", first["quantity"])

	productData, ok := priceData["product_data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Pro", productData["name"])

	meta, ok := out["metadata"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "42", meta["user_id"])
}

// --- helpers ---

func newTestStripeServer(t *testing.T) (*StripeMock, *httptest.Server) {
	t.Helper()
	mux := http.NewServeMux()
	mock := NewStripeMock(StripeMockOptions{BaseURL: "http://stripe-mock.test"})
	mock.RegisterAPIRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return mock, srv
}

// withStripeBackend points stripe-go at the given URL for the duration of
// fn, restoring the previous backend after. Necessary because stripe-go's
// SetBackend is process-global state.
func withStripeBackend(t *testing.T, url string, fn func()) {
	t.Helper()
	prev := stripe.GetBackend(stripe.APIBackend)
	stripe.Key = "sk_test_mock"
	stripe.SetBackend(stripe.APIBackend, stripe.GetBackendWithConfig(
		stripe.APIBackend,
		&stripe.BackendConfig{URL: stripe.String(url)},
	))
	t.Cleanup(func() {
		stripe.SetBackend(stripe.APIBackend, prev)
	})
	fn()
}
