package devserver

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	stripe "github.com/stripe/stripe-go/v82"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStripeMock_Dashboard_RendersAllResources is the smoke test: seed
// every resource type, GET the dashboard, verify each table header + row
// count is present.
func TestStripeMock_Dashboard_RendersAllResources(t *testing.T) {
	mock, _, _ := newFullStripeStack(t, "")

	acctID := seedConnectedAccount(t, mock)
	piID := seedPaymentIntent(t, mock, paymentIntentSeed{
		Amount: 5000, Currency: "gbp", ApplicationFeeAmount: 500, TransferDestination: acctID,
	})
	poID := seedPayout(t, mock, acctID)
	sessID := seedCheckoutSession(t, mock)

	body := getDashboard(t, mock, http.StatusOK)

	// Section headers.
	assert.Contains(t, body, "Checkout Sessions")
	assert.Contains(t, body, "Connect Accounts")
	assert.Contains(t, body, "PaymentIntents")
	assert.Contains(t, body, "Refunds")
	assert.Contains(t, body, "Payouts")

	// Resource IDs (truncated form, since shortID renders 14 chars + ellipsis
	// for IDs longer than 16).
	for _, id := range []string{acctID, piID, poID, sessID} {
		short := id
		if len(short) > 16 {
			short = id[:14]
		}
		assert.Contains(t, body, short, "id %q (or short form) should appear in dashboard", id)
	}
}

// TestStripeMock_Dashboard_EmptyState renders the dashboard against an
// empty mock and verifies each section shows the "no X yet" empty message
// instead of a malformed table.
func TestStripeMock_Dashboard_EmptyState(t *testing.T) {
	mock, _, _ := newFullStripeStack(t, "")

	body := getDashboard(t, mock, http.StatusOK)
	for _, msg := range []string{
		"No checkout sessions captured yet",
		"No connected accounts captured yet",
		"No PaymentIntents captured yet",
		"No refunds captured yet",
		"No payouts captured yet",
	} {
		assert.Contains(t, body, msg)
	}
}

// TestStripeMock_Dashboard_Resend_PIFiresFullCascade asserts that
// resending a succeeded destination-charge PI re-fires all three events
// (pi.succeeded + charge.succeeded + transfer.created), preserving the
// original cascade order.
func TestStripeMock_Dashboard_Resend_PIFiresFullCascade(t *testing.T) {
	const secret = "whsec_test_devmock"
	app := newOrderedWebhookSink(t, secret)

	mock, _, _ := newFullStripeStack(t, "")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: app.URL, Secret: secret})

	dest := seedConnectedAccount(t, mock)
	piID := seedPaymentIntent(t, mock, paymentIntentSeed{
		Amount: 2000, Currency: "gbp", ApplicationFeeAmount: 200, TransferDestination: dest,
	})

	// Drive PI to success — fires the original 3-event cascade.
	resp := postPaymentIntentComplete(t, mock, piID, "succeed")
	resp.Body.Close() //nolint:errcheck
	app.WaitFor(t, 3, 2*time.Second)

	// Now resend via dashboard — should fire the same 3 events again.
	resp = postDashboardResend(t, mock, "payment_intent", piID)
	defer resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusSeeOther, resp.StatusCode)

	got := app.WaitFor(t, 6, 3*time.Second)
	assert.Equal(t, "payment_intent.succeeded", string(got[3].Type))
	assert.Equal(t, "charge.succeeded", string(got[4].Type))
	assert.Equal(t, "transfer.created", string(got[5].Type))
}

// TestStripeMock_Dashboard_Resend_RefundFiresChargeRefunded asserts the
// resend semantics for refunds use the post-refund Charge state, matching
// what the original fire would have sent.
func TestStripeMock_Dashboard_Resend_RefundFiresChargeRefunded(t *testing.T) {
	const secret = "whsec_test_devmock"
	app := newOrderedWebhookSink(t, secret)

	mock, _, _ := newFullStripeStack(t, "")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: app.URL, Secret: secret})

	piID := seedPaymentIntent(t, mock, paymentIntentSeed{Amount: 1000, Currency: "gbp"})
	resp := postPaymentIntentComplete(t, mock, piID, "succeed")
	resp.Body.Close() //nolint:errcheck
	app.WaitFor(t, 2, 2*time.Second) // pi.succeeded + charge.succeeded

	rf, _, _, err := mock.applyRefund(refundInput{piID: piID, amount: 500})
	require.NoError(t, err)

	// Resend it via dashboard.
	resp = postDashboardResend(t, mock, "refund", rf.ID)
	resp.Body.Close() //nolint:errcheck
	got := app.WaitFor(t, 3, 2*time.Second)
	assert.Equal(t, "charge.refunded", string(got[2].Type))

	var ch stripe.Charge
	require.NoError(t, json.Unmarshal(got[2].Data.Raw, &ch))
	assert.Equal(t, int64(500), ch.AmountRefunded, "resent webhook payload reflects post-refund Charge state")
}

// TestStripeMock_Dashboard_Resend_NothingToSendErrors covers the edge
// case: resending an open session (or a pre-onboarding account) returns
// 400 because there's no natural event for that state.
func TestStripeMock_Dashboard_Resend_NothingToSendErrors(t *testing.T) {
	mock, _, _ := newFullStripeStack(t, "")
	sessID := seedCheckoutSession(t, mock)
	resp := postDashboardResend(t, mock, "session", sessID)
	defer resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode,
		"resending an open session has no natural event — should error not silently succeed")
}

// TestStripeMock_Dashboard_Refund issues a full refund via the dashboard
// form and asserts both Charge state and the webhook fire correctly.
// Equivalent to a refund.New call but routed through the dashboard endpoint.
func TestStripeMock_Dashboard_Refund(t *testing.T) {
	const secret = "whsec_test_devmock"
	app := newOrderedWebhookSink(t, secret)

	mock, _, _ := newFullStripeStack(t, "")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: app.URL, Secret: secret})

	piID := seedPaymentIntent(t, mock, paymentIntentSeed{Amount: 3000, Currency: "gbp"})
	resp := postPaymentIntentComplete(t, mock, piID, "succeed")
	resp.Body.Close() //nolint:errcheck
	app.WaitFor(t, 2, 2*time.Second)

	// POST a partial refund of 1000 from the dashboard.
	form := url.Values{
		"payment_intent": {piID},
		"amount":         {"1000"},
	}
	req, err := http.NewRequest(http.MethodPost, mock.baseURL+"/__hamr/stripe/refund", strings.NewReader(form.Encode()))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err = client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusSeeOther, resp.StatusCode)

	got := app.WaitFor(t, 3, 2*time.Second)
	assert.Equal(t, "charge.refunded", string(got[2].Type))

	mock.mu.RLock()
	pi := mock.paymentIntents[piID]
	ch := mock.charges[pi.LatestChargeID]
	mock.mu.RUnlock()
	assert.Equal(t, int64(1000), ch.AmountRefunded)
	assert.False(t, ch.Refunded, "1000/3000 = partial; refunded flag stays false")
}

// TestStripeMock_Dashboard_Expire takes an open session, expires it from
// the dashboard, verifies the session.expired webhook fires.
func TestStripeMock_Dashboard_Expire(t *testing.T) {
	const secret = "whsec_test_devmock"
	app := newOrderedWebhookSink(t, secret)

	mock, _, _ := newFullStripeStack(t, "")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: app.URL, Secret: secret})

	sessID := seedCheckoutSession(t, mock)

	form := url.Values{"session": {sessID}}
	req, err := http.NewRequest(http.MethodPost, mock.baseURL+"/__hamr/stripe/expire", strings.NewReader(form.Encode()))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusSeeOther, resp.StatusCode)

	got := app.WaitFor(t, 1, 2*time.Second)
	assert.Equal(t, "checkout.session.expired", string(got[0].Type))

	mock.mu.RLock()
	stored := mock.sessions[sessID]
	mock.mu.RUnlock()
	assert.Equal(t, "expired", stored.Status)
}

// TestStripeMock_Dashboard_Expire_AlreadyTerminal asserts a stale tab
// can't double-expire — second call returns 409.
func TestStripeMock_Dashboard_Expire_AlreadyTerminal(t *testing.T) {
	mock, _, _ := newFullStripeStack(t, "")
	sessID := seedCheckoutSession(t, mock)
	mock.mu.Lock()
	mock.sessions[sessID].Status = "complete"
	mock.mu.Unlock()

	form := url.Values{"session": {sessID}}
	req, err := http.NewRequest(http.MethodPost, mock.baseURL+"/__hamr/stripe/expire", strings.NewReader(form.Encode()))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
}

// TestStripeMock_Dashboard_RejectsCrossOrigin verifies the same-origin
// CSRF guard applies to all the new dashboard mutators (resend, refund, expire).
func TestStripeMock_Dashboard_RejectsCrossOrigin(t *testing.T) {
	mock, _, _ := newFullStripeStack(t, "")
	piID := seedPaymentIntent(t, mock, paymentIntentSeed{Amount: 1000, Currency: "gbp"})

	for _, target := range []struct {
		path string
		form url.Values
	}{
		{"/__hamr/stripe/resend", url.Values{"resource": {"payment_intent"}, "id": {piID}}},
		{"/__hamr/stripe/refund", url.Values{"payment_intent": {piID}}},
		{"/__hamr/stripe/expire", url.Values{"session": {"cs_test_x"}}},
	} {
		req, err := http.NewRequest(http.MethodPost, mock.baseURL+target.path, strings.NewReader(target.form.Encode()))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", "http://evil.example")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		resp.Body.Close() //nolint:errcheck
		assert.Equal(t, http.StatusForbidden, resp.StatusCode, "path %s missing CSRF guard", target.path)
	}
}

// --- helpers ---

func getDashboard(t *testing.T, mock *StripeMock, wantStatus int) string {
	t.Helper()
	resp, err := http.Get(mock.baseURL + "/__hamr/stripe")
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	body, _ := io.ReadAll(resp.Body)
	require.Equal(t, wantStatus, resp.StatusCode, "body=%s", string(body))
	return string(body)
}

func postDashboardResend(t *testing.T, mock *StripeMock, resource, id string) *http.Response {
	t.Helper()
	form := url.Values{"resource": {resource}, "id": {id}}
	req, err := http.NewRequest(http.MethodPost, mock.baseURL+"/__hamr/stripe/resend", strings.NewReader(form.Encode()))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	require.NoError(t, err)
	return resp
}
