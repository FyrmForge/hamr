package devserver

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	stripe "github.com/stripe/stripe-go/v86"
)

func getPage(t *testing.T, url string, wantStatus int) string {
	t.Helper()
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	body, _ := io.ReadAll(resp.Body)
	require.Equal(t, wantStatus, resp.StatusCode, "body=%s", string(body))
	return string(body)
}

// seedMarketplace builds one of everything the fake dashboards show: an
// onboarded v2 seller, a paid checkout, a transfer with a partial reversal, a
// monthly payout schedule, a paid payout, and a won dispute.
func seedMarketplace(t *testing.T, mock *StripeMock, srvURL string) (acctID, piID string) {
	t.Helper()
	c := newMockClient(srvURL)
	ctx := context.Background()

	acct, err := c.V2CoreAccounts.Create(ctx, recipientAccountParams("seller@example.com"))
	require.NoError(t, err)
	_, err = mock.completeV2Onboarding(acct.ID)
	require.NoError(t, err)

	sessID := createTestSession(t, mock, []stripe.LineItem{{Description: "Lesson", Quantity: 1, AmountTotal: 10000, Currency: "gbp"}})
	resp := postComplete(t, mock, sessID, "paid")
	resp.Body.Close() //nolint:errcheck
	mock.mu.RLock()
	piID = mock.sessions[sessID].PaymentIntentID
	chargeID := mock.paymentIntents[piID].LatestChargeID
	mock.mu.RUnlock()

	tr, err := c.V1Transfers.Create(ctx, &stripe.TransferCreateParams{
		Amount: stripe.Int64(8000), Currency: stripe.String("gbp"), Destination: stripe.String(acct.ID),
		SourceTransaction: stripe.String(chargeID), Description: stripe.String("Order 42"),
	})
	require.NoError(t, err)
	_, err = c.V1TransferReversals.Create(ctx, &stripe.TransferReversalCreateParams{ID: stripe.String(tr.ID), Amount: stripe.Int64(1000)})
	require.NoError(t, err)

	bs := &stripe.BalanceSettingsUpdateParams{Payments: &stripe.BalanceSettingsUpdatePaymentsParams{
		Payouts: &stripe.BalanceSettingsUpdatePaymentsPayoutsParams{Schedule: &stripe.BalanceSettingsUpdatePaymentsPayoutsScheduleParams{
			Interval: stripe.String("monthly"), MonthlyPayoutDays: stripe.Int64Slice([]int64{31}),
		}},
	}}
	bs.StripeAccount = stripe.String(acct.ID)
	_, err = c.V1BalanceSettings.Update(ctx, bs)
	require.NoError(t, err)

	po, err := mock.payoutAccountBalance(acct.ID)
	require.NoError(t, err)
	resp = postPayoutComplete(t, mock, po.ID, "paid")
	resp.Body.Close() //nolint:errcheck

	dp, err := mock.openDispute(piID)
	require.NoError(t, err)
	require.NoError(t, mock.closeDispute(dp.ID, "won"))
	return acct.ID, piID
}

func TestStripeMock_Ledger_Balances(t *testing.T) {
	mock, srv, _ := newFullStripeStack(t, "")
	acctID, _ := seedMarketplace(t, mock, srv.URL)

	mock.mu.RLock()
	defer mock.mu.RUnlock()
	fee := stripeFee(10000, "gbp")
	// Platform: charge 10000 - fee, transfer -8000, reversal +1000, dispute
	// withdrawn and returned (won) costs only the dispute fee.
	assert.Equal(t, map[string]int64{"gbp": 10000 - fee - 8000 + 1000 - disputeFee}, balanceOf(mock.platformLedger()))
	// Seller: 8000 in, 1000 reversed, 7000 paid out.
	assert.Equal(t, map[string]int64{"gbp": 0}, mock.connectedBalance(acctID))
}

func TestStripeMock_PlatformView_RendersEverySection(t *testing.T) {
	mock, srv, _ := newFullStripeStack(t, "")
	acctID, piID := seedMarketplace(t, mock, srv.URL)
	require.Eventually(t, func() bool { // thin events are recorded from a goroutine
		mock.mu.RLock()
		defer mock.mu.RUnlock()
		return len(mock.events) > 0
	}, 3*time.Second, 10*time.Millisecond)

	home := getPage(t, srv.URL+"/__hamr/stripe/dashboard", http.StatusOK)
	assert.Contains(t, home, "hamr mock")
	assert.Contains(t, home, "Recent payments")
	assert.NotContains(t, home, "<form", "the fake dashboards are read-only")

	payments := getPage(t, srv.URL+"/__hamr/stripe/dashboard/payments", http.StatusOK)
	assert.Contains(t, payments, "Disputed")
	assert.Contains(t, payments, "visa •••• 4242")

	balances := getPage(t, srv.URL+"/__hamr/stripe/dashboard/balances", http.StatusOK)
	assert.Contains(t, balances, "Transfer reversal")
	assert.Contains(t, balances, "Chargeback reversal")

	connect := getPage(t, srv.URL+"/__hamr/stripe/dashboard/connect", http.StatusOK)
	assert.Contains(t, connect, "seller@example.com")
	assert.Contains(t, connect, "/__hamr/stripe/express/"+acctID)

	transfers := getPage(t, srv.URL+"/__hamr/stripe/dashboard/transfers", http.StatusOK)
	assert.Contains(t, transfers, "Order 42")

	getPage(t, srv.URL+"/__hamr/stripe/dashboard/payouts", http.StatusOK)

	disputes := getPage(t, srv.URL+"/__hamr/stripe/dashboard/disputes", http.StatusOK)
	assert.Contains(t, disputes, piID)
	assert.Contains(t, disputes, "Won")

	events := getPage(t, srv.URL+"/__hamr/stripe/dashboard/events", http.StatusOK)
	assert.Contains(t, events, "v2.core.account[requirements].updated")
	assert.Contains(t, events, "Not sent", "no webhook endpoint configured in this test")

	getPage(t, srv.URL+"/__hamr/stripe/dashboard/nope", http.StatusNotFound)

	resp, err := http.Post(srv.URL+"/__hamr/stripe/dashboard", "text/plain", strings.NewReader(""))
	require.NoError(t, err)
	resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

func TestStripeMock_ExpressView(t *testing.T) {
	mock, srv, _ := newFullStripeStack(t, "")
	acctID, _ := seedMarketplace(t, mock, srv.URL)

	page := getPage(t, srv.URL+"/__hamr/stripe/express/"+acctID, http.StatusOK)
	assert.Contains(t, page, "seller@example.com")
	assert.Contains(t, page, "Monthly, on day 31")
	assert.Contains(t, page, "Order 42")
	assert.Contains(t, page, "£70.00", "the paid payout")
	assert.Contains(t, page, "recipient.stripe_balance.stripe_transfers: active")
	assert.NotContains(t, page, "<form")

	v1 := seedConnectedAccount(t, mock)
	getPage(t, srv.URL+"/__hamr/stripe/express/"+v1, http.StatusOK)
	getPage(t, srv.URL+"/__hamr/stripe/express/acct_test_missing", http.StatusNotFound)
}
