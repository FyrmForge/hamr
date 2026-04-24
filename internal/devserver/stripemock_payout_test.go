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
	"github.com/stripe/stripe-go/v82/account"
	"github.com/stripe/stripe-go/v82/payout"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStripeMock_CreatePayout_RoundTrip verifies the create happy path:
// real stripe-go payout.New, decoded into stripe.Payout with the right
// initial state (status=pending, automatic=false).
func TestStripeMock_CreatePayout_RoundTrip(t *testing.T) {
	_, srv := newTestStripeServer(t)
	withStripeBackend(t, srv.URL, func() {
		params := &stripe.PayoutParams{
			Amount:   stripe.Int64(2500),
			Currency: stripe.String("gbp"),
			Method:   stripe.String("standard"),
		}
		params.AddMetadata("payout_run", "weekly_2026_w17")
		po, err := payout.New(params)
		require.NoError(t, err)

		assert.True(t, strings.HasPrefix(po.ID, "po_test_"))
		assert.Equal(t, int64(2500), po.Amount)
		assert.Equal(t, stripe.Currency("gbp"), po.Currency)
		assert.Equal(t, stripe.PayoutMethodType("standard"), po.Method)
		assert.Equal(t, stripe.PayoutStatus("pending"), po.Status)
		assert.False(t, po.Automatic, "manually-triggered payout should be automatic=false")
		assert.Greater(t, po.ArrivalDate, po.Created, "arrival_date should be after created for standard method")
		assert.Equal(t, "weekly_2026_w17", po.Metadata["payout_run"])
	})
}

// TestStripeMock_CreatePayout_OnConnectedAccount asserts the Stripe-Account
// header scopes the payout to a connected account — the canonical
// marketplace pattern for paying out a seller's accumulated Transfers.
func TestStripeMock_CreatePayout_OnConnectedAccount(t *testing.T) {
	mock, srv := newTestStripeServer(t)
	connectedAcct := seedConnectedAccount(t, mock)

	withStripeBackend(t, srv.URL, func() {
		params := &stripe.PayoutParams{
			Amount:   stripe.Int64(1800),
			Currency: stripe.String("gbp"),
		}
		params.SetStripeAccount(connectedAcct)
		po, err := payout.New(params)
		require.NoError(t, err)

		// Server-side: payout records the connected account it was created for.
		mock.mu.RLock()
		stored := mock.payouts[po.ID]
		mock.mu.RUnlock()
		require.NotNil(t, stored)
		assert.Equal(t, connectedAcct, stored.AccountID,
			"Stripe-Account header must be propagated to the stored payout")
	})
}

// TestStripeMock_CreatePayout_RejectsUnknownAccount mirrors Stripe's
// behavior: payouts for a connected account that doesn't exist 404.
func TestStripeMock_CreatePayout_RejectsUnknownAccount(t *testing.T) {
	_, srv := newTestStripeServer(t)
	withStripeBackend(t, srv.URL, func() {
		params := &stripe.PayoutParams{
			Amount:   stripe.Int64(1000),
			Currency: stripe.String("gbp"),
		}
		params.SetStripeAccount("acct_test_doesnotexist")
		_, err := payout.New(params)
		require.Error(t, err)
		var sErr *stripe.Error
		if assert.ErrorAs(t, err, &sErr) {
			assert.Equal(t, http.StatusNotFound, sErr.HTTPStatusCode)
		}
	})
}

// TestStripeMock_GetPayout_RoundTrip verifies retrieve.
func TestStripeMock_GetPayout_RoundTrip(t *testing.T) {
	_, srv := newTestStripeServer(t)
	withStripeBackend(t, srv.URL, func() {
		created, err := payout.New(&stripe.PayoutParams{
			Amount:   stripe.Int64(1000),
			Currency: stripe.String("usd"),
		})
		require.NoError(t, err)

		fetched, err := payout.Get(created.ID, nil)
		require.NoError(t, err)
		assert.Equal(t, created.ID, fetched.ID)
		assert.Equal(t, created.Amount, fetched.Amount)
		assert.Equal(t, created.Status, fetched.Status)
	})
}

// TestStripeMock_ListPayouts_FiltersByStripeAccount is the marketplace
// dashboard query: list payouts for a specific connected account.
func TestStripeMock_ListPayouts_FiltersByStripeAccount(t *testing.T) {
	mock, srv := newTestStripeServer(t)
	acctA := seedConnectedAccount(t, mock)
	acctB := seedConnectedAccount(t, mock)

	withStripeBackend(t, srv.URL, func() {
		// Two payouts for acctA.
		for range 2 {
			p := &stripe.PayoutParams{Amount: stripe.Int64(1000), Currency: stripe.String("gbp")}
			p.SetStripeAccount(acctA)
			_, err := payout.New(p)
			require.NoError(t, err)
		}
		// One for acctB.
		p := &stripe.PayoutParams{Amount: stripe.Int64(2000), Currency: stripe.String("gbp")}
		p.SetStripeAccount(acctB)
		_, err := payout.New(p)
		require.NoError(t, err)

		// List under acctA → should only see 2.
		listParams := &stripe.PayoutListParams{}
		listParams.SetStripeAccount(acctA)
		iter := payout.List(listParams)
		var aPayouts []*stripe.Payout
		for iter.Next() {
			aPayouts = append(aPayouts, iter.Payout())
		}
		require.NoError(t, iter.Err())
		assert.Len(t, aPayouts, 2, "list should be scoped to acctA")

		// List under acctB → should only see 1.
		listParamsB := &stripe.PayoutListParams{}
		listParamsB.SetStripeAccount(acctB)
		iter = payout.List(listParamsB)
		var bPayouts []*stripe.Payout
		for iter.Next() {
			bPayouts = append(bPayouts, iter.Payout())
		}
		require.NoError(t, iter.Err())
		assert.Len(t, bPayouts, 1)
	})
}

// TestStripeMock_PayoutPage_RendersPending checks the dev page renders the
// outcome buttons for a pending payout.
func TestStripeMock_PayoutPage_RendersPending(t *testing.T) {
	mock, _, _ := newFullStripeStack(t, "")
	poID := seedPayout(t, mock, "")

	body := getPayoutPage(t, mock, poID, http.StatusOK)
	assert.Contains(t, body, "Payout")
	assert.Contains(t, body, poID)
	assert.Contains(t, body, "£10.00")
	assert.Contains(t, body, `name="outcome" value="paid"`)
	assert.Contains(t, body, `name="outcome" value="fail"`)
}

// TestStripeMock_PayoutPage_GoneAfterTerminal asserts a stale tab against
// an already-paid payout returns 410.
func TestStripeMock_PayoutPage_GoneAfterTerminal(t *testing.T) {
	mock, _, _ := newFullStripeStack(t, "")
	poID := seedPayout(t, mock, "")
	mock.mu.Lock()
	mock.payouts[poID].Status = "paid"
	mock.mu.Unlock()
	getPayoutPage(t, mock, poID, http.StatusGone)
}

// TestStripeMock_PayoutComplete_PaidFiresWebhook verifies the happy path:
// Mark paid → status=paid → payout.paid webhook arrives.
func TestStripeMock_PayoutComplete_PaidFiresWebhook(t *testing.T) {
	const secret = "whsec_test_devmock"
	app := newOrderedWebhookSink(t, secret)

	mock, _, _ := newFullStripeStack(t, "")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: app.URL, Secret: secret})

	poID := seedPayout(t, mock, "")
	resp := postPayoutComplete(t, mock, poID, "paid")
	defer resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	got := app.WaitFor(t, 1, 2*time.Second)
	assert.Equal(t, "payout.paid", string(got[0].Type))

	var po stripe.Payout
	require.NoError(t, json.Unmarshal(got[0].Data.Raw, &po))
	assert.Equal(t, poID, po.ID)
	assert.Equal(t, stripe.PayoutStatus("paid"), po.Status)
}

// TestStripeMock_PayoutComplete_FailedFiresWebhookWithFailureReason covers
// the failure path: payout.failed event includes failure_code + message
// so the app can show the user something actionable.
func TestStripeMock_PayoutComplete_FailedFiresWebhookWithFailureReason(t *testing.T) {
	const secret = "whsec_test_devmock"
	app := newOrderedWebhookSink(t, secret)

	mock, _, _ := newFullStripeStack(t, "")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: app.URL, Secret: secret})

	poID := seedPayout(t, mock, "")
	resp := postPayoutComplete(t, mock, poID, "fail")
	defer resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	got := app.WaitFor(t, 1, 2*time.Second)
	assert.Equal(t, "payout.failed", string(got[0].Type))

	var po stripe.Payout
	require.NoError(t, json.Unmarshal(got[0].Data.Raw, &po))
	assert.Equal(t, stripe.PayoutStatus("failed"), po.Status)
	assert.NotEmpty(t, po.FailureCode, "failure_code must be set so apps can dispatch on it")
	assert.NotEmpty(t, po.FailureMessage, "failure_message must be set for end-user display")
}

// TestStripeMock_PayoutComplete_DoubleSubmit asserts race-safety: a
// second POST returns 409 and does not fire a duplicate webhook.
func TestStripeMock_PayoutComplete_DoubleSubmit(t *testing.T) {
	const secret = "whsec_test_devmock"
	app := newOrderedWebhookSink(t, secret)

	mock, _, _ := newFullStripeStack(t, "")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: app.URL, Secret: secret})
	poID := seedPayout(t, mock, "")

	resp1 := postPayoutComplete(t, mock, poID, "paid")
	resp1.Body.Close() //nolint:errcheck
	app.WaitFor(t, 1, 2*time.Second)

	resp2 := postPayoutComplete(t, mock, poID, "paid")
	defer resp2.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusConflict, resp2.StatusCode)

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, 1, app.Count(), "double-submit must not fire a duplicate")
}

// TestStripeMock_FullPayoutLoop_ViaRealStripeGoClient is the marketplace
// payout headline: real stripe-go account.New → payout.New on that account
// → user clicks Mark paid → real signed payout.paid webhook arrives →
// real payout.Get sees status=paid.
func TestStripeMock_FullPayoutLoop_ViaRealStripeGoClient(t *testing.T) {
	const secret = "whsec_test_devmock"
	app := newOrderedWebhookSink(t, secret)

	mock, srv, _ := newFullStripeStack(t, "")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: app.URL, Secret: secret})

	withStripeBackend(t, srv.URL, func() {
		acct, err := account.New(&stripe.AccountParams{Type: stripe.String("express"), Country: stripe.String("GB")})
		require.NoError(t, err)

		params := &stripe.PayoutParams{
			Amount:   stripe.Int64(15000),
			Currency: stripe.String("gbp"),
		}
		params.SetStripeAccount(acct.ID)
		po, err := payout.New(params)
		require.NoError(t, err)
		assert.Equal(t, stripe.PayoutStatus("pending"), po.Status)

		// User clicks Mark paid.
		resp := postPayoutComplete(t, mock, po.ID, "paid")
		resp.Body.Close() //nolint:errcheck
		require.Equal(t, http.StatusOK, resp.StatusCode)

		// payout.paid webhook arrives.
		evt := app.WaitFor(t, 1, 2*time.Second)
		assert.Equal(t, "payout.paid", string(evt[0].Type))

		// App retrieves the payout and confirms status=paid.
		fetched, err := payout.Get(po.ID, nil)
		require.NoError(t, err)
		assert.Equal(t, stripe.PayoutStatus("paid"), fetched.Status)
	})
}

// --- helpers ---

// seedPayout inserts a pending payout directly so UI tests don't need to
// round-trip through the API for setup. accountID="" = platform balance.
func seedPayout(t *testing.T, mock *StripeMock, accountID string) string {
	t.Helper()
	id := "po_test_" + randomHex(16)
	now := time.Now()
	mock.mu.Lock()
	mock.payouts[id] = &stripePayout{
		ID:          id,
		Amount:      1000,
		Currency:    "gbp",
		Status:      "pending",
		Method:      "standard",
		SourceType:  "bank_account",
		Created:     now,
		ArrivalDate: now.Add(48 * time.Hour),
		AccountID:   accountID,
	}
	mock.persist()
	mock.mu.Unlock()
	return id
}

func getPayoutPage(t *testing.T, mock *StripeMock, poID string, wantStatus int) string {
	t.Helper()
	resp, err := http.Get(mock.baseURL + "/__hamr/stripe/payout?id=" + url.QueryEscape(poID))
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	body, _ := io.ReadAll(resp.Body)
	require.Equal(t, wantStatus, resp.StatusCode, "body=%s", string(body))
	return string(body)
}

func postPayoutComplete(t *testing.T, mock *StripeMock, poID, outcome string) *http.Response {
	t.Helper()
	form := url.Values{"id": {poID}, "outcome": {outcome}}
	req, err := http.NewRequest(http.MethodPost, mock.baseURL+"/__hamr/stripe/payout/complete", strings.NewReader(form.Encode()))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}
