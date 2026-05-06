package devserver

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	stripe "github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/account"
	"github.com/stripe/stripe-go/v82/paymentintent"
	"github.com/stripe/stripe-go/v82/refund"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStripeMock_CreateRefund_FullByPaymentIntent exercises the most common
// app pattern: refund a PI by passing the PI ID. Mock resolves it to the
// underlying Charge, refunds the full amount, fires charge.refunded.
func TestStripeMock_CreateRefund_FullByPaymentIntent(t *testing.T) {
	const secret = "whsec_test_devmock"
	app := newOrderedWebhookSink(t, secret)

	mock, srv, _ := newFullStripeStack(t, "")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: app.URL, Secret: secret})

	withStripeBackend(t, srv.URL, func() {
		// Set up: create + succeed a PI so there's a Charge to refund.
		pi := createAndSucceedPI(t, mock, 2000, "")

		// Drain the PI cascade (pi.succeeded + charge.succeeded) before
		// firing the refund. The cascade fires in its own goroutine and
		// the refund fires in another; without this barrier, CI's slower
		// scheduler under -race lets charge.refunded interleave between
		// pi.succeeded and charge.succeeded, and the position-2 assertion
		// below sees charge.succeeded instead of charge.refunded.
		app.WaitFor(t, 2, 2*time.Second)

		rf, err := refund.New(&stripe.RefundParams{
			PaymentIntent: stripe.String(pi.ID),
		})
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(rf.ID, "re_test_"))
		assert.Equal(t, int64(2000), rf.Amount, "no amount = full refund")
		assert.Equal(t, stripe.RefundStatus("succeeded"), rf.Status)
		require.NotNil(t, rf.Charge)
		assert.Equal(t, pi.LatestCharge.ID, rf.Charge.ID)
	})

	// Total: 2 events from PI succeed (pi.succeeded + charge.succeeded) + 1
	// from refund (charge.refunded). orderedWebhookSink.WaitFor returns
	// events[:n] from the start so we ask for all 3 and check the last one.
	got := app.WaitFor(t, 3, 2*time.Second)
	assert.Equal(t, "charge.refunded", string(got[2].Type))

	var ch stripe.Charge
	require.NoError(t, json.Unmarshal(got[2].Data.Raw, &ch))
	assert.True(t, ch.Refunded)
	assert.Equal(t, int64(2000), ch.AmountRefunded)
}

// TestStripeMock_CreateRefund_PartialByCharge covers refunding a Charge
// directly (rather than through the PI) with a partial amount.
func TestStripeMock_CreateRefund_PartialByCharge(t *testing.T) {
	mock, srv, _ := newFullStripeStack(t, "")

	withStripeBackend(t, srv.URL, func() {
		pi := createAndSucceedPI(t, mock, 5000, "")
		require.NotNil(t, pi.LatestCharge)

		rf, err := refund.New(&stripe.RefundParams{
			Charge: stripe.String(pi.LatestCharge.ID),
			Amount: stripe.Int64(1500),
		})
		require.NoError(t, err)
		assert.Equal(t, int64(1500), rf.Amount)

		// Charge state shows partial refund.
		mock.mu.RLock()
		ch := mock.charges[pi.LatestCharge.ID]
		mock.mu.RUnlock()
		assert.Equal(t, int64(1500), ch.AmountRefunded)
		assert.False(t, ch.Refunded, "1500 < 5000 means not fully refunded")
	})
}

// TestStripeMock_CreateRefund_RejectsBothPIAndCharge mirrors Stripe's
// validation: caller must pick exactly one source.
func TestStripeMock_CreateRefund_RejectsBothPIAndCharge(t *testing.T) {
	mock, srv, _ := newFullStripeStack(t, "")
	withStripeBackend(t, srv.URL, func() {
		pi := createAndSucceedPI(t, mock, 1000, "")
		_, err := refund.New(&stripe.RefundParams{
			PaymentIntent: stripe.String(pi.ID),
			Charge:        stripe.String(pi.LatestCharge.ID),
		})
		require.Error(t, err)
	})
}

// TestStripeMock_CreateRefund_RejectsNeither covers the inverse: must
// provide one of the two.
func TestStripeMock_CreateRefund_RejectsNeither(t *testing.T) {
	_, srv := newTestStripeServer(t)
	withStripeBackend(t, srv.URL, func() {
		_, err := refund.New(&stripe.RefundParams{})
		require.Error(t, err)
	})
}

// TestStripeMock_CreateRefund_RejectsExcessAmount asserts you can't refund
// more than the charge's remaining balance — neither in one go nor across
// multiple calls.
func TestStripeMock_CreateRefund_RejectsExcessAmount(t *testing.T) {
	mock, srv, _ := newFullStripeStack(t, "")
	withStripeBackend(t, srv.URL, func() {
		pi := createAndSucceedPI(t, mock, 1000, "")

		// Single excess.
		_, err := refund.New(&stripe.RefundParams{
			Charge: stripe.String(pi.LatestCharge.ID),
			Amount: stripe.Int64(1500),
		})
		require.Error(t, err)

		// Partial then excess remaining.
		_, err = refund.New(&stripe.RefundParams{
			Charge: stripe.String(pi.LatestCharge.ID),
			Amount: stripe.Int64(800),
		})
		require.NoError(t, err)
		_, err = refund.New(&stripe.RefundParams{
			Charge: stripe.String(pi.LatestCharge.ID),
			Amount: stripe.Int64(300), // 200 remaining → 300 should fail
		})
		require.Error(t, err)
	})
}

// TestStripeMock_CreateRefund_ReverseTransfer is the marketplace-critical
// path: refunding a destination-charge PI with reverse_transfer=true also
// reverses the corresponding Transfer to the connected account, so the
// platform's books re-balance correctly.
func TestStripeMock_CreateRefund_ReverseTransfer(t *testing.T) {
	const secret = "whsec_test_devmock"
	app := newOrderedWebhookSink(t, secret)

	mock, srv, _ := newFullStripeStack(t, "")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: app.URL, Secret: secret})

	withStripeBackend(t, srv.URL, func() {
		// Set up: connected account + destination-charge PI succeed.
		acct, err := account.New(&stripe.AccountParams{Type: stripe.String("express"), Country: stripe.String("GB")})
		require.NoError(t, err)
		pi := createAndSucceedPI(t, mock, 2000, acct.ID)
		require.NotNil(t, pi.LatestCharge)

		// Wait for the PI cascade (3 events) before refunding so the
		// downstream assertion only depends on the refund's own webhook.
		app.WaitFor(t, 3, 3*time.Second)

		rf, err := refund.New(&stripe.RefundParams{
			PaymentIntent:        stripe.String(pi.ID),
			ReverseTransfer:      stripe.Bool(true),
			RefundApplicationFee: stripe.Bool(true),
		})
		require.NoError(t, err)
		assert.NotEmpty(t, rf.SourceTransferReversal, "reverse_transfer=true should populate source_transfer_reversal id")

		// Mock-side: Transfer.amount_reversed should be incremented to the
		// post-refund amount. PI was 2000 with app_fee 200, so transfer
		// was 1800; refund here is 2000 (full) so reverse is capped at 1800.
		mock.mu.RLock()
		ch := mock.charges[pi.LatestCharge.ID]
		tr := mock.transfers[ch.TransferID]
		mock.mu.RUnlock()
		require.NotNil(t, tr)
		assert.Equal(t, int64(1800), tr.AmountReversed,
			"refund of 2000 against transfer of 1800 should reverse the full 1800")
	})

	// Cascade was 3 events; refund adds a 4th. Inspect the 4th.
	got := app.WaitFor(t, 4, 2*time.Second)
	assert.Equal(t, "charge.refunded", string(got[3].Type))
}

// TestStripeMock_GetRefund_RoundTrip verifies retrieve returns the stored
// refund with the same fields the create response had.
func TestStripeMock_GetRefund_RoundTrip(t *testing.T) {
	mock, srv, _ := newFullStripeStack(t, "")
	withStripeBackend(t, srv.URL, func() {
		pi := createAndSucceedPI(t, mock, 1000, "")
		created, err := refund.New(&stripe.RefundParams{PaymentIntent: stripe.String(pi.ID)})
		require.NoError(t, err)

		fetched, err := refund.Get(created.ID, nil)
		require.NoError(t, err)
		assert.Equal(t, created.ID, fetched.ID)
		assert.Equal(t, created.Amount, fetched.Amount)
	})
}

// TestStripeMock_FullDestinationChargeRefundLoop_ViaRealStripeGoClient is
// the marketplace headline: real stripe-go does account → PI succeed →
// refund with reverse_transfer → app verifies Charge is now fully refunded
// and the transfer reversal is reflected.
func TestStripeMock_FullDestinationChargeRefundLoop_ViaRealStripeGoClient(t *testing.T) {
	const secret = "whsec_test_devmock"
	app := newOrderedWebhookSink(t, secret)

	mock, srv, _ := newFullStripeStack(t, "")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: app.URL, Secret: secret})

	withStripeBackend(t, srv.URL, func() {
		acct, err := account.New(&stripe.AccountParams{Type: stripe.String("express"), Country: stripe.String("GB")})
		require.NoError(t, err)

		pi, err := paymentintent.New(&stripe.PaymentIntentParams{
			Amount:               stripe.Int64(5000),
			Currency:             stripe.String("gbp"),
			ApplicationFeeAmount: stripe.Int64(500),
			TransferData: &stripe.PaymentIntentTransferDataParams{
				Destination: stripe.String(acct.ID),
			},
		})
		require.NoError(t, err)

		// Succeed the PI via the dev UI complete endpoint.
		resp := postPaymentIntentComplete(t, mock, pi.ID, "succeed")
		resp.Body.Close() //nolint:errcheck
		require.Equal(t, http.StatusOK, resp.StatusCode)

		// Drain the PI cascade webhooks (3 events).
		app.WaitFor(t, 3, 3*time.Second)

		// Issue a refund with reverse_transfer.
		rf, err := refund.New(&stripe.RefundParams{
			PaymentIntent:        stripe.String(pi.ID),
			ReverseTransfer:      stripe.Bool(true),
			RefundApplicationFee: stripe.Bool(true),
		})
		require.NoError(t, err)
		assert.Equal(t, int64(5000), rf.Amount, "no amount = full refund")
		assert.Equal(t, stripe.RefundStatus("succeeded"), rf.Status)
		assert.NotEmpty(t, rf.SourceTransferReversal)

		// charge.refunded webhook arrives with the post-refund Charge.
		got := app.WaitFor(t, 4, 3*time.Second)
		assert.Equal(t, "charge.refunded", string(got[3].Type))

		var ch stripe.Charge
		require.NoError(t, json.Unmarshal(got[3].Data.Raw, &ch))
		assert.True(t, ch.Refunded)
		assert.Equal(t, int64(5000), ch.AmountRefunded)

		// PI.Get reflects the post-refund Charge state.
		fetched, err := paymentintent.Get(pi.ID, nil)
		require.NoError(t, err)
		require.NotNil(t, fetched.LatestCharge)
		assert.True(t, fetched.LatestCharge.Refunded)
		assert.Equal(t, int64(5000), fetched.LatestCharge.AmountRefunded)
	})
}

// --- helpers ---

// createAndSucceedPI is a one-call setup that creates a PI via the API
// and immediately succeeds it via the dev UI complete handler so a
// Charge exists for refund tests. dest may be empty for non-Connect PIs.
// Returns the Stripe-go-decoded PI (post-create, pre-success) so tests
// have access to LatestCharge after refunding.
func createAndSucceedPI(t *testing.T, mock *StripeMock, amount int64, dest string) *stripe.PaymentIntent {
	t.Helper()
	params := &stripe.PaymentIntentParams{
		Amount:   stripe.Int64(amount),
		Currency: stripe.String("gbp"),
	}
	if dest != "" {
		params.ApplicationFeeAmount = stripe.Int64(amount / 10)
		params.TransferData = &stripe.PaymentIntentTransferDataParams{Destination: stripe.String(dest)}
	}
	pi, err := paymentintent.New(params)
	require.NoError(t, err)

	resp := postPaymentIntentComplete(t, mock, pi.ID, "succeed")
	resp.Body.Close() //nolint:errcheck
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Re-fetch so caller has the post-success state with LatestCharge populated.
	updated, err := paymentintent.Get(pi.ID, nil)
	require.NoError(t, err)
	return updated
}
