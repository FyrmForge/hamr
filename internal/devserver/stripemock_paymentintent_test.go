package devserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	stripe "github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/account"
	"github.com/stripe/stripe-go/v82/paymentintent"
	"github.com/stripe/stripe-go/v82/webhook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStripeMock_CreatePaymentIntent_DestinationCharge verifies the create
// happy path for the marketplace pattern: PI with application_fee_amount +
// transfer_data.destination. Asserts the response decodes into stripe-go's
// struct shape with the Connect fields preserved.
func TestStripeMock_CreatePaymentIntent_DestinationCharge(t *testing.T) {
	mock, srv := newTestStripeServer(t)

	// Pre-create the connected account that funds will flow to — real
	// Stripe rejects PIs whose destination doesn't exist; the mock mirrors that.
	dest := seedConnectedAccount(t, mock)

	withStripeBackend(t, srv.URL, func() {
		params := &stripe.PaymentIntentParams{
			Amount:   stripe.Int64(2000),
			Currency: stripe.String("gbp"),
			TransferData: &stripe.PaymentIntentTransferDataParams{
				Destination: stripe.String(dest),
			},
			ApplicationFeeAmount: stripe.Int64(200),
			Description:          stripe.String("Order ord_42"),
		}
		params.AddMetadata("order_id", "ord_42")

		pi, err := paymentintent.New(params)
		require.NoError(t, err)

		assert.True(t, strings.HasPrefix(pi.ID, "pi_test_"))
		assert.Equal(t, int64(2000), pi.Amount)
		assert.Equal(t, stripe.Currency("gbp"), pi.Currency)
		assert.Equal(t, int64(200), pi.ApplicationFeeAmount)
		assert.Equal(t, stripe.PaymentIntentStatus("requires_payment_method"), pi.Status,
			"new PI without payment_method should be requires_payment_method")
		require.NotNil(t, pi.TransferData, "transfer_data must round-trip")
		require.NotNil(t, pi.TransferData.Destination)
		assert.Equal(t, dest, pi.TransferData.Destination.ID)
		assert.NotEmpty(t, pi.ClientSecret)
		assert.Equal(t, "ord_42", pi.Metadata["order_id"])
	})
}

// TestStripeMock_CreatePaymentIntent_DirectCharge_StripeAccountHeader covers
// the other Connect pattern: platform creates the PI AS the connected account
// via the Stripe-Account header.
func TestStripeMock_CreatePaymentIntent_DirectCharge_StripeAccountHeader(t *testing.T) {
	mock, srv := newTestStripeServer(t)
	connectedAcct := seedConnectedAccount(t, mock)

	withStripeBackend(t, srv.URL, func() {
		params := &stripe.PaymentIntentParams{
			Amount:               stripe.Int64(5000),
			Currency:             stripe.String("usd"),
			ApplicationFeeAmount: stripe.Int64(500),
		}
		params.SetStripeAccount(connectedAcct)

		pi, err := paymentintent.New(params)
		require.NoError(t, err)
		require.NotEmpty(t, pi.ID)

		// Server-side: PI should record the connected account it was created AS.
		mock.mu.RLock()
		stored := mock.paymentIntents[pi.ID]
		mock.mu.RUnlock()
		require.NotNil(t, stored)
		assert.Equal(t, connectedAcct, stored.StripeAccount,
			"Stripe-Account header must be propagated to the stored PI")
	})
}

// TestStripeMock_CreatePaymentIntent_RejectsUnknownDestination matches real
// Stripe's behavior: transfer_data.destination must reference an existing
// connected account.
func TestStripeMock_CreatePaymentIntent_RejectsUnknownDestination(t *testing.T) {
	_, srv := newTestStripeServer(t)
	withStripeBackend(t, srv.URL, func() {
		_, err := paymentintent.New(&stripe.PaymentIntentParams{
			Amount:   stripe.Int64(1000),
			Currency: stripe.String("gbp"),
			TransferData: &stripe.PaymentIntentTransferDataParams{
				Destination: stripe.String("acct_test_doesnotexist"),
			},
		})
		require.Error(t, err)
		var sErr *stripe.Error
		if assert.ErrorAs(t, err, &sErr) {
			assert.Equal(t, http.StatusBadRequest, sErr.HTTPStatusCode)
		}
	})
}

// TestStripeMock_CreatePaymentIntent_RejectsAppFeeExceedingAmount mirrors
// Stripe's validation that application_fee_amount cannot exceed amount.
func TestStripeMock_CreatePaymentIntent_RejectsAppFeeExceedingAmount(t *testing.T) {
	mock, srv := newTestStripeServer(t)
	dest := seedConnectedAccount(t, mock)
	withStripeBackend(t, srv.URL, func() {
		_, err := paymentintent.New(&stripe.PaymentIntentParams{
			Amount:               stripe.Int64(1000),
			Currency:             stripe.String("gbp"),
			ApplicationFeeAmount: stripe.Int64(2000),
			TransferData: &stripe.PaymentIntentTransferDataParams{
				Destination: stripe.String(dest),
			},
		})
		require.Error(t, err)
	})
}

// TestStripeMock_GetPaymentIntent round-trips a retrieve: the latest_charge
// field is null until the outcome runs.
func TestStripeMock_GetPaymentIntent(t *testing.T) {
	mock, srv := newTestStripeServer(t)
	dest := seedConnectedAccount(t, mock)
	withStripeBackend(t, srv.URL, func() {
		created, err := paymentintent.New(&stripe.PaymentIntentParams{
			Amount:   stripe.Int64(1000),
			Currency: stripe.String("gbp"),
			TransferData: &stripe.PaymentIntentTransferDataParams{
				Destination: stripe.String(dest),
			},
		})
		require.NoError(t, err)

		fetched, err := paymentintent.Get(created.ID, nil)
		require.NoError(t, err)
		assert.Equal(t, created.ID, fetched.ID)
		assert.Equal(t, created.Amount, fetched.Amount)
		assert.Nil(t, fetched.LatestCharge, "latest_charge should be nil pre-outcome")
	})
}

// TestStripeMock_PaymentIntentPage_RendersOpenPI is a sanity check on the
// dev page rendering for a destination-charge PI.
func TestStripeMock_PaymentIntentPage_RendersOpenPI(t *testing.T) {
	mock, _, _ := newFullStripeStack(t, "")
	dest := seedConnectedAccount(t, mock)
	piID := seedPaymentIntent(t, mock, paymentIntentSeed{
		Amount:               2000,
		Currency:             "gbp",
		ApplicationFeeAmount: 200,
		TransferDestination:  dest,
	})

	body := getPaymentIntentPage(t, mock, piID, http.StatusOK)
	assert.Contains(t, body, "PaymentIntent")
	assert.Contains(t, body, "£20.00")
	assert.Contains(t, body, "£2.00") // application fee
	assert.Contains(t, body, dest)
	assert.Contains(t, body, "destination charge")
	assert.Contains(t, body, `name="outcome" value="succeed"`)
	assert.Contains(t, body, `name="outcome" value="fail"`)
}

// TestStripeMock_PaymentIntentPage_GoneAfterTerminal asserts a stale tab
// hitting an already-succeeded PI gets 410.
func TestStripeMock_PaymentIntentPage_GoneAfterTerminal(t *testing.T) {
	mock, _, _ := newFullStripeStack(t, "")
	piID := seedPaymentIntent(t, mock, paymentIntentSeed{Amount: 1000, Currency: "gbp"})
	mock.mu.Lock()
	mock.paymentIntents[piID].Status = "succeeded"
	mock.mu.Unlock()
	getPaymentIntentPage(t, mock, piID, http.StatusGone)
}

// TestStripeMock_PaymentIntentComplete_DestinationChargeCascade is the
// headline marketplace test. Outcome=succeed on a PI with transfer_data
// fires THREE webhooks in order: payment_intent.succeeded, charge.succeeded,
// transfer.created. App-side stripe-go decodes each into the right struct
// type and the cascade fields line up.
func TestStripeMock_PaymentIntentComplete_DestinationChargeCascade(t *testing.T) {
	const secret = "whsec_test_devmock"
	app := newOrderedWebhookSink(t, secret)

	mock, _, _ := newFullStripeStack(t, "")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: app.URL, Secret: secret})

	dest := seedConnectedAccount(t, mock)
	piID := seedPaymentIntent(t, mock, paymentIntentSeed{
		Amount:               2000,
		Currency:             "gbp",
		ApplicationFeeAmount: 200,
		TransferDestination:  dest,
	})

	resp := postPaymentIntentComplete(t, mock, piID, "succeed")
	defer resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	got := app.WaitFor(t, 3, 3*time.Second)

	assert.Equal(t, "payment_intent.succeeded", string(got[0].Type))
	assert.Equal(t, "charge.succeeded", string(got[1].Type))
	assert.Equal(t, "transfer.created", string(got[2].Type))

	var pi stripe.PaymentIntent
	require.NoError(t, json.Unmarshal(got[0].Data.Raw, &pi))
	assert.Equal(t, piID, pi.ID)
	assert.Equal(t, stripe.PaymentIntentStatus("succeeded"), pi.Status)
	assert.Equal(t, int64(2000), pi.AmountReceived)

	var ch stripe.Charge
	require.NoError(t, json.Unmarshal(got[1].Data.Raw, &ch))
	assert.Equal(t, piID, ch.PaymentIntent.ID)
	assert.Equal(t, int64(2000), ch.Amount)
	assert.True(t, ch.Paid)
	assert.True(t, ch.Captured)
	assert.Equal(t, int64(200), ch.ApplicationFeeAmount)

	var tr stripe.Transfer
	require.NoError(t, json.Unmarshal(got[2].Data.Raw, &tr))
	assert.Equal(t, int64(1800), tr.Amount, "amount=2000 - app_fee=200 = 1800 to destination")
	assert.Equal(t, stripe.Currency("gbp"), tr.Currency)
	require.NotNil(t, tr.Destination)
	assert.Equal(t, dest, tr.Destination.ID)
	assert.Equal(t, ch.ID, tr.SourceTransaction.ID)
}

// TestStripeMock_PaymentIntentComplete_NoDestinationFiresTwoEvents covers
// the non-Connect path: succeed fires payment_intent.succeeded +
// charge.succeeded but NOT transfer.created.
func TestStripeMock_PaymentIntentComplete_NoDestinationFiresTwoEvents(t *testing.T) {
	const secret = "whsec_test_devmock"
	app := newOrderedWebhookSink(t, secret)

	mock, _, _ := newFullStripeStack(t, "")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: app.URL, Secret: secret})

	piID := seedPaymentIntent(t, mock, paymentIntentSeed{Amount: 1000, Currency: "usd"})

	resp := postPaymentIntentComplete(t, mock, piID, "succeed")
	defer resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	got := app.WaitFor(t, 2, 2*time.Second)
	assert.Equal(t, "payment_intent.succeeded", string(got[0].Type))
	assert.Equal(t, "charge.succeeded", string(got[1].Type))

	// Make sure no rogue transfer.created arrives in the next 100ms.
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, 2, app.Count(), "no transfer should fire when transfer_data is unset")
}

// TestStripeMock_PaymentIntentComplete_FailFiresPaymentFailed verifies the
// fail outcome only fires payment_intent.payment_failed (no charge, no
// transfer).
func TestStripeMock_PaymentIntentComplete_FailFiresPaymentFailed(t *testing.T) {
	const secret = "whsec_test_devmock"
	app := newOrderedWebhookSink(t, secret)

	mock, _, _ := newFullStripeStack(t, "")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: app.URL, Secret: secret})

	dest := seedConnectedAccount(t, mock)
	piID := seedPaymentIntent(t, mock, paymentIntentSeed{
		Amount:               1500,
		Currency:             "gbp",
		ApplicationFeeAmount: 150,
		TransferDestination:  dest,
	})

	resp := postPaymentIntentComplete(t, mock, piID, "fail")
	resp.Body.Close() //nolint:errcheck

	got := app.WaitFor(t, 1, 2*time.Second)
	assert.Equal(t, "payment_intent.payment_failed", string(got[0].Type))

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, 1, app.Count(), "fail outcome must not fire charge or transfer events")
}

// TestStripeMock_PaymentIntentComplete_DefaultsPaymentMethodWhenAbsent
// regression-tests the implicit-confirm fallback. A dev creating a PI
// without a payment method and clicking Succeed must not produce a
// Charge with payment_method="" — the mock fills in pm_card_visa so the
// synthesised Charge looks like a real captured card payment.
func TestStripeMock_PaymentIntentComplete_DefaultsPaymentMethodWhenAbsent(t *testing.T) {
	const secret = "whsec_test_devmock"
	app := newOrderedWebhookSink(t, secret)

	mock, _, _ := newFullStripeStack(t, "")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: app.URL, Secret: secret})

	// Seed a PI in the requires_payment_method state — no PaymentMethod set.
	piID := "pi_test_" + randomHex(16)
	mock.mu.Lock()
	mock.paymentIntents[piID] = &stripePaymentIntent{
		ID:                 piID,
		Amount:             1000,
		Currency:           "gbp",
		Status:             "requires_payment_method",
		CaptureMethod:      "automatic",
		ConfirmationMethod: "automatic",
		ClientSecret:       piID + "_secret_" + randomHex(8),
		Created:            time.Now(),
	}
	mock.mu.Unlock()

	resp := postPaymentIntentComplete(t, mock, piID, "succeed")
	resp.Body.Close() //nolint:errcheck
	require.Equal(t, http.StatusOK, resp.StatusCode)

	got := app.WaitFor(t, 2, 2*time.Second)
	assert.Equal(t, "charge.succeeded", string(got[1].Type))

	var ch stripe.Charge
	require.NoError(t, json.Unmarshal(got[1].Data.Raw, &ch))
	assert.Equal(t, "pm_card_visa", ch.PaymentMethod,
		"Charge.payment_method must be defaulted (not empty) when PI was succeeded without explicit confirm")
}

// seedCapturablePI inserts a manual-capture PI already authorized and awaiting
// capture (the state confirm() leaves a capture_method=manual PI in).
func seedCapturablePI(t *testing.T, mock *StripeMock, amount int64) string {
	t.Helper()
	id := "pi_test_" + randomHex(16)
	mock.mu.Lock()
	mock.paymentIntents[id] = &stripePaymentIntent{
		ID: id, Amount: amount, Currency: "gbp",
		Status: "requires_capture", CaptureMethod: "manual",
		ConfirmationMethod: "automatic", PaymentMethod: "pm_card_visa",
		ClientSecret: id + "_secret_" + randomHex(8), Created: time.Now(),
	}
	mock.mu.Unlock()
	return id
}

// TestStripeMock_CapturePaymentIntent_FullCapture guards the manual-capture
// fix: a requires_capture PI exposes amount_capturable, and POST /capture
// creates the Charge, advances to succeeded, and fires the payment events.
func TestStripeMock_CapturePaymentIntent_FullCapture(t *testing.T) {
	const secret = "whsec_test_devmock"
	app := newOrderedWebhookSink(t, secret)
	mock, srv, _ := newFullStripeStack(t, "")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: app.URL, Secret: secret})

	piID := seedCapturablePI(t, mock, 2000)

	// Before capture: the authorized amount is capturable.
	getResp, err := http.Get(srv.URL + "/v1/payment_intents/" + piID)
	require.NoError(t, err)
	var before map[string]any
	require.NoError(t, json.NewDecoder(getResp.Body).Decode(&before))
	getResp.Body.Close() //nolint:errcheck
	assert.EqualValues(t, 2000, before["amount_capturable"])

	resp, err := http.Post(srv.URL+"/v1/payment_intents/"+piID+"/capture",
		"application/x-www-form-urlencoded", nil)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	got := app.WaitFor(t, 2, 2*time.Second)
	assert.Equal(t, "payment_intent.succeeded", string(got[0].Type))
	assert.Equal(t, "charge.succeeded", string(got[1].Type))

	mock.mu.RLock()
	pi := mock.paymentIntents[piID]
	ch := mock.charges[pi.LatestChargeID]
	mock.mu.RUnlock()
	assert.Equal(t, "succeeded", pi.Status)
	assert.Equal(t, int64(2000), pi.AmountReceived)
	require.NotNil(t, ch, "capture must create a Charge")
	assert.Equal(t, int64(2000), ch.AmountCaptured)
}

// TestStripeMock_CapturePaymentIntent_PartialCapture captures less than the
// authorized amount via amount_to_capture.
func TestStripeMock_CapturePaymentIntent_PartialCapture(t *testing.T) {
	mock, srv, _ := newFullStripeStack(t, "")
	piID := seedCapturablePI(t, mock, 2000)

	form := url.Values{"amount_to_capture": {"800"}}
	resp, err := http.Post(srv.URL+"/v1/payment_intents/"+piID+"/capture",
		"application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "succeeded", body["status"])
	assert.EqualValues(t, 800, body["amount_received"])

	mock.mu.RLock()
	ch := mock.charges[mock.paymentIntents[piID].LatestChargeID]
	mock.mu.RUnlock()
	require.NotNil(t, ch)
	assert.Equal(t, int64(800), ch.AmountCaptured)
}

// TestStripeMock_CapturePaymentIntent_RejectsNonCapturable rejects a capture on
// a PI that isn't awaiting capture.
func TestStripeMock_CapturePaymentIntent_RejectsNonCapturable(t *testing.T) {
	mock, srv, _ := newFullStripeStack(t, "")
	piID := seedPaymentIntent(t, mock, paymentIntentSeed{Amount: 1000, Currency: "gbp"}) // requires_confirmation

	resp, err := http.Post(srv.URL+"/v1/payment_intents/"+piID+"/capture",
		"application/x-www-form-urlencoded", nil)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TestStripeMock_PaymentIntentComplete_DoubleSubmit is the same race-safety
// guarantee as the other resources.
func TestStripeMock_PaymentIntentComplete_DoubleSubmit(t *testing.T) {
	const secret = "whsec_test_devmock"
	app := newOrderedWebhookSink(t, secret)

	mock, _, _ := newFullStripeStack(t, "")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: app.URL, Secret: secret})

	piID := seedPaymentIntent(t, mock, paymentIntentSeed{Amount: 1000, Currency: "gbp"})

	resp1 := postPaymentIntentComplete(t, mock, piID, "succeed")
	resp1.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusOK, resp1.StatusCode)
	app.WaitFor(t, 2, 2*time.Second)

	resp2 := postPaymentIntentComplete(t, mock, piID, "succeed")
	defer resp2.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusConflict, resp2.StatusCode)

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, 2, app.Count(), "double-submit must not fire duplicate webhooks")
}

// TestStripeMock_FullDestinationChargeLoop_ViaRealStripeGoClient is the
// integration: real stripe-go paymentintent.New → mock UI succeed →
// three real signed webhooks decoded by the app → real paymentintent.Get
// confirms latest_charge is now populated.
func TestStripeMock_FullDestinationChargeLoop_ViaRealStripeGoClient(t *testing.T) {
	const secret = "whsec_test_devmock"
	app := newOrderedWebhookSink(t, secret)

	mock, srv, _ := newFullStripeStack(t, "")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: app.URL, Secret: secret})

	withStripeBackend(t, srv.URL, func() {
		// Step 1: create connected account via stripe-go.
		acct, err := account.New(&stripe.AccountParams{
			Type:    stripe.String("express"),
			Country: stripe.String("GB"),
		})
		require.NoError(t, err)

		// Step 2: create the PI with application_fee_amount + transfer_data.
		pi, err := paymentintent.New(&stripe.PaymentIntentParams{
			Amount:               stripe.Int64(5000),
			Currency:             stripe.String("gbp"),
			ApplicationFeeAmount: stripe.Int64(500),
			TransferData: &stripe.PaymentIntentTransferDataParams{
				Destination: stripe.String(acct.ID),
			},
		})
		require.NoError(t, err)

		// Step 3: user clicks Succeed on the dev UI.
		resp := postPaymentIntentComplete(t, mock, pi.ID, "succeed")
		resp.Body.Close() //nolint:errcheck
		require.Equal(t, http.StatusOK, resp.StatusCode)

		// Step 4: app receives all three signed webhooks.
		evts := app.WaitFor(t, 3, 3*time.Second)
		assert.Equal(t, "payment_intent.succeeded", string(evts[0].Type))
		assert.Equal(t, "charge.succeeded", string(evts[1].Type))
		assert.Equal(t, "transfer.created", string(evts[2].Type))

		// Step 5: app retrieves the PI; latest_charge is now populated and
		// app can read the resulting Charge directly off the PI.
		fetched, err := paymentintent.Get(pi.ID, nil)
		require.NoError(t, err)
		assert.Equal(t, stripe.PaymentIntentStatus("succeeded"), fetched.Status)
		require.NotNil(t, fetched.LatestCharge)
		assert.True(t, fetched.LatestCharge.Paid)
		assert.Equal(t, int64(500), fetched.LatestCharge.ApplicationFeeAmount)
	})
}

// --- helpers ---

type paymentIntentSeed struct {
	Amount               int64
	Currency             string
	ApplicationFeeAmount int64
	TransferDestination  string // empty = no destination charge
}

// seedPaymentIntent inserts a PI directly into the mock so PI-page/outcome
// tests don't have to round-trip through the API for setup.
func seedPaymentIntent(t *testing.T, mock *StripeMock, s paymentIntentSeed) string {
	t.Helper()
	id := "pi_test_" + randomHex(16)
	mock.mu.Lock()
	mock.paymentIntents[id] = &stripePaymentIntent{
		ID:                      id,
		Amount:                  s.Amount,
		Currency:                s.Currency,
		Status:                  "requires_confirmation",
		CaptureMethod:           "automatic",
		ConfirmationMethod:      "automatic",
		ApplicationFeeAmount:    s.ApplicationFeeAmount,
		TransferDataDestination: s.TransferDestination,
		ClientSecret:            id + "_secret_" + randomHex(8),
		Created:                 time.Now(),
	}
	mock.persist()
	mock.mu.Unlock()
	return id
}

// seedConnectedAccount creates an onboarded connected account directly so
// PI tests can reference it without going through the account API.
func seedConnectedAccount(t *testing.T, mock *StripeMock) string {
	t.Helper()
	id := "acct_test_" + randomHex(16)
	mock.mu.Lock()
	mock.accounts[id] = &stripeAccount{
		ID:               id,
		Type:             "express",
		Country:          "GB",
		DefaultCurrency:  "gbp",
		ChargesEnabled:   true,
		PayoutsEnabled:   true,
		DetailsSubmitted: true,
		Created:          time.Now(),
	}
	mock.persist()
	mock.mu.Unlock()
	return id
}

func getPaymentIntentPage(t *testing.T, mock *StripeMock, piID string, wantStatus int) string {
	t.Helper()
	resp, err := http.Get(mock.baseURL + "/__hamr/stripe/payment_intent?id=" + url.QueryEscape(piID))
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	body, _ := io.ReadAll(resp.Body)
	require.Equal(t, wantStatus, resp.StatusCode, "body=%s", string(body))
	return string(body)
}

func postPaymentIntentComplete(t *testing.T, mock *StripeMock, piID, outcome string) *http.Response {
	t.Helper()
	form := url.Values{"id": {piID}, "outcome": {outcome}}
	req, err := http.NewRequest(http.MethodPost, mock.baseURL+"/__hamr/stripe/payment_intent/complete", strings.NewReader(form.Encode()))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

// orderedWebhookSink collects events in arrival order. WaitFor blocks
// until N events arrive and returns them sorted by arrival; useful for
// asserting the cascade order (PI → Charge → Transfer).
type orderedWebhookSink struct {
	URL    string
	mu     sync.Mutex
	events []stripe.Event
	got    chan struct{}
}

func newOrderedWebhookSink(t *testing.T, secret string) *orderedWebhookSink {
	t.Helper()
	s := &orderedWebhookSink{got: make(chan struct{}, 16)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		evt, err := webhook.ConstructEvent(body, r.Header.Get("Stripe-Signature"), secret)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.events = append(s.events, evt)
		s.mu.Unlock()
		s.got <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	s.URL = srv.URL
	return s
}

func (s *orderedWebhookSink) WaitFor(t *testing.T, n int, d time.Duration) []stripe.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), d)
	defer cancel()
	for {
		s.mu.Lock()
		got := len(s.events)
		s.mu.Unlock()
		if got >= n {
			s.mu.Lock()
			out := append([]stripe.Event(nil), s.events[:n]...)
			s.mu.Unlock()
			return out
		}
		select {
		case <-s.got:
		case <-ctx.Done():
			s.mu.Lock()
			haveTypes := make([]string, len(s.events))
			for i, e := range s.events {
				haveTypes[i] = string(e.Type)
			}
			s.mu.Unlock()
			t.Fatalf("timed out waiting for %d events, got %d: %v", n, got, haveTypes)
			return nil
		}
	}
}

func (s *orderedWebhookSink) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}

// TestStripeMock_CapturePaymentIntent_PartialBelowFeeClampsTransfer guards
// against a negative destination transfer: a partial capture smaller than the
// application fee must clamp the transfer amount to 0, not go negative.
func TestStripeMock_CapturePaymentIntent_PartialBelowFeeClampsTransfer(t *testing.T) {
	mock, srv, _ := newFullStripeStack(t, "")

	piID := "pi_test_" + randomHex(16)
	mock.mu.Lock()
	mock.paymentIntents[piID] = &stripePaymentIntent{
		ID: piID, Amount: 1000, Currency: "gbp",
		Status: "requires_capture", CaptureMethod: "manual", PaymentMethod: "pm_card_visa",
		ApplicationFeeAmount:    600,
		TransferDataDestination: "acct_test_dest",
		ClientSecret:            piID + "_secret", Created: time.Now(),
	}
	mock.mu.Unlock()

	form := url.Values{"amount_to_capture": {"300"}} // below the 600 fee
	resp, err := http.Post(srv.URL+"/v1/payment_intents/"+piID+"/capture",
		"application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	require.Equal(t, http.StatusOK, resp.StatusCode)

	mock.mu.RLock()
	pi := mock.paymentIntents[piID]
	tr := mock.transfers[pi.TransferID]
	mock.mu.RUnlock()
	require.NotNil(t, tr, "a transfer should have been created")
	assert.GreaterOrEqual(t, tr.Amount, int64(0), "transfer amount must not be negative")
	assert.Equal(t, int64(0), tr.Amount, "capture below the fee clamps the transfer to 0")
}
