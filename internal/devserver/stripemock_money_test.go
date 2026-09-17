package devserver

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	stripe "github.com/stripe/stripe-go/v86"
)

// seedPlatformCharge drops a succeeded gbp charge straight into the store,
// no events: the cheapest way to give the platform (or, with acct, a
// connected account via a direct charge) a balance to move.
func seedPlatformCharge(t *testing.T, mock *StripeMock, amount int64) string {
	t.Helper()
	return seedDirectCharge(t, mock, amount, "", 0)
}

func seedDirectCharge(t *testing.T, mock *StripeMock, amount int64, acct string, appFee int64) (piID string) {
	t.Helper()
	piID = "pi_test_" + randomHex(16)
	chID := "ch_test_" + randomHex(16)
	mock.mu.Lock()
	mock.paymentIntents[piID] = &stripePaymentIntent{
		ID: piID, Amount: amount, Currency: "gbp", Status: "succeeded", LatestChargeID: chID,
		StripeAccount: acct, ApplicationFeeAmount: appFee, Created: time.Now(),
	}
	mock.charges[chID] = &stripeCharge{
		ID: chID, Amount: amount, AmountCaptured: amount, Currency: "gbp", Status: "succeeded",
		Paid: true, Captured: true, PaymentIntentID: piID, ApplicationFeeAmount: appFee, Created: time.Now(),
	}
	mock.mu.Unlock()
	return piID
}

func platformBalance(mock *StripeMock) int64 {
	mock.mu.RLock()
	defer mock.mu.RUnlock()
	return balanceOf(mock.platformLedger())["gbp"]
}

func TestStripeMock_Transfer_NeedsBalance(t *testing.T) {
	mock, srv, _ := newFullStripeStack(t, "")
	c := newMockClient(srv.URL)
	ctx := context.Background()
	acctID := seedConnectedAccount(t, mock)
	transfer := func(amount int64, src string) error {
		p := &stripe.TransferCreateParams{Amount: stripe.Int64(amount), Currency: stripe.String("gbp"), Destination: stripe.String(acctID)}
		if src != "" {
			p.SourceTransaction = stripe.String(src)
		}
		_, err := c.V1Transfers.Create(ctx, p)
		return err
	}

	// Nothing in the platform balance yet.
	assert.ErrorContains(t, transfer(1, ""), "balance_insufficient")

	piID := seedPlatformCharge(t, mock, 1000)
	mock.mu.RLock()
	chargeID := mock.paymentIntents[piID].LatestChargeID
	mock.mu.RUnlock()

	// source_transaction draws the gross charge once; Stripe's fee stays
	// on the platform, so the balance ends slightly negative, like Stripe.
	require.NoError(t, transfer(600, chargeID))
	assert.ErrorContains(t, transfer(600, chargeID), "balance_insufficient")
	require.NoError(t, transfer(400, chargeID))
	assert.Equal(t, -stripeFee(1000, "gbp"), platformBalance(mock))
	assert.ErrorContains(t, transfer(1, ""), "balance_insufficient")
}

func TestStripeMock_Refund_CapsAtCapturedAmount(t *testing.T) {
	mock, srv, _ := newFullStripeStack(t, "")
	piID := seedCapturablePI(t, mock, 1000)
	post := func(path string, form url.Values) *http.Response {
		resp, err := http.Post(srv.URL+path, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
		require.NoError(t, err)
		t.Cleanup(func() { resp.Body.Close() }) //nolint:errcheck
		return resp
	}
	require.Equal(t, http.StatusOK, post("/v1/payment_intents/"+piID+"/capture", url.Values{"amount_to_capture": {"600"}}).StatusCode)

	rf, _, _, _, err := mock.applyRefund(refundInput{piID: piID})
	require.NoError(t, err)
	assert.Equal(t, int64(600), rf.Amount, "a full refund returns what was captured, not what was authorised")
	_, _, _, _, err = mock.applyRefund(refundInput{piID: piID, amount: 1})
	assert.ErrorIs(t, err, errChargeAlreadyRefunded)
	assert.Equal(t, int64(0), platformBalance(mock)+stripeFee(600, "gbp"))
}

func TestStripeMock_RefundAndDispute_Exclusive(t *testing.T) {
	mock, _, _ := newFullStripeStack(t, "")

	// Disputed money is frozen.
	disputed := seedPlatformCharge(t, mock, 1000)
	_, err := mock.openDispute(disputed)
	require.NoError(t, err)
	_, _, _, _, err = mock.applyRefund(refundInput{piID: disputed})
	assert.ErrorIs(t, err, errChargeDisputed)

	// Only the unrefunded part can be charged back.
	partial := seedPlatformCharge(t, mock, 1000)
	_, _, _, _, err = mock.applyRefund(refundInput{piID: partial, amount: 400})
	require.NoError(t, err)
	dp, err := mock.openDispute(partial)
	require.NoError(t, err)
	assert.Equal(t, int64(600), dp.Amount)

	// Nothing left after a full refund.
	refunded := seedPlatformCharge(t, mock, 1000)
	_, _, _, _, err = mock.applyRefund(refundInput{piID: refunded})
	require.NoError(t, err)
	_, err = mock.openDispute(refunded)
	assert.ErrorContains(t, err, "fully refunded")
	assert.NotContains(t, getDashboard(t, mock, http.StatusOK), `name="payment_intent" value="`+refunded+`"><button class="action danger">Dispute`)
}

func TestStripeMock_Ledger_DirectChargeDisputeAndFeeRefund(t *testing.T) {
	mock, _, _ := newFullStripeStack(t, "")
	acctID := seedConnectedAccount(t, mock)
	fee := stripeFee(5000, "gbp")

	// A direct charge lands on the connected account; the platform keeps
	// its application fee.
	disputedPI := seedDirectCharge(t, mock, 5000, acctID, 500)
	assert.Equal(t, int64(500), platformBalance(mock))
	connected := func() int64 {
		mock.mu.RLock()
		defer mock.mu.RUnlock()
		return mock.connectedBalance(acctID)["gbp"]
	}
	assert.Equal(t, 5000-fee-500, connected())

	// A dispute hits whoever holds the charge, not the platform.
	_, err := mock.openDispute(disputedPI)
	require.NoError(t, err)
	assert.Equal(t, int64(500), platformBalance(mock))
	assert.Equal(t, 5000-fee-500-5000-disputeFee, connected())

	// refund_application_fee hands the fee back pro rata.
	feePI := seedDirectCharge(t, mock, 5000, acctID, 500)
	_, _, _, _, err = mock.applyRefund(refundInput{piID: feePI, amount: 2500, refundAppFee: true})
	require.NoError(t, err)
	assert.Equal(t, int64(500+500-250), platformBalance(mock))
	assert.Equal(t, (5000-fee-500-5000-disputeFee)+(5000-fee-500)-2500+250, connected())
}
