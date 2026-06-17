package devserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	stripe "github.com/stripe/stripe-go/v82"
	checkoutsession "github.com/stripe/stripe-go/v82/checkout/session"
	"github.com/stripe/stripe-go/v82/webhook"
)

// TestStripeMock_CheckoutPage_RendersOpenSession is a sanity check on the
// rendering: an open session shows the line items, total, and three outcome
// buttons (paid/failed/cancelled).
func TestStripeMock_CheckoutPage_RendersOpenSession(t *testing.T) {
	mock, _, _ := newFullStripeStack(t, "")
	sessID := createTestSession(t, mock, []stripe.LineItem{
		{Description: "Pro plan", AmountTotal: 2000, Quantity: 1, Currency: "gbp"},
		{Description: "Add-on", AmountTotal: 500, Quantity: 2, Currency: "gbp"},
	})

	body := getCheckoutPage(t, mock, sessID, http.StatusOK)
	assert.Contains(t, body, "Stripe Checkout")
	assert.Contains(t, body, "Pro plan")
	assert.Contains(t, body, "Add-on")
	assert.Contains(t, body, "£30.00") // 2000 + 2*500
	assert.Contains(t, body, `name="outcome" value="paid"`)
	assert.Contains(t, body, `name="outcome" value="failed"`)
	assert.Contains(t, body, `name="outcome" value="cancelled"`)
	assert.Contains(t, body, sessID)
}

// TestStripeMock_CheckoutPage_GoneAfterCompletion ensures a stale browser
// tab can't double-fire — once the session is no longer "open", the page
// returns 410 instead of re-rendering.
func TestStripeMock_CheckoutPage_GoneAfterCompletion(t *testing.T) {
	mock, _, _ := newFullStripeStack(t, "")
	sessID := createTestSession(t, mock, []stripe.LineItem{
		{Description: "Test", AmountTotal: 100, Quantity: 1, Currency: "gbp"},
	})
	mock.mu.Lock()
	mock.sessions[sessID].Status = "complete"
	mock.mu.Unlock()

	getCheckoutPage(t, mock, sessID, http.StatusGone)
}

// TestStripeMock_CheckoutPage_NotFound covers the unknown-session path.
func TestStripeMock_CheckoutPage_NotFound(t *testing.T) {
	mock, _, _ := newFullStripeStack(t, "")
	getCheckoutPage(t, mock, "cs_test_doesnotexist", http.StatusNotFound)
}

// TestStripeMock_Complete_PaidFiresWebhookAndRedirects exercises the happy
// path end-to-end: paid outcome flips status, fires checkout.session.completed
// (verified by the app handler using real webhook.ConstructEvent), and
// redirects to success_url.
func TestStripeMock_Complete_PaidFiresWebhookAndRedirects(t *testing.T) {
	const secret = "whsec_test_devmock"
	app := newWebhookSink(t, secret)

	mock, _, _ := newFullStripeStack(t, "")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: app.URL, Secret: secret})

	sessID := createTestSession(t, mock, []stripe.LineItem{
		{Description: "Item", AmountTotal: 1000, Quantity: 1, Currency: "gbp"},
	})

	resp := postComplete(t, mock, sessID, "paid")
	defer resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusSeeOther, resp.StatusCode)
	assert.Equal(t, "https://app.example/success", resp.Header.Get("Location"))

	evt := app.Wait(t, 2*time.Second)
	assert.Equal(t, "checkout.session.completed", string(evt.Type))
	assert.Equal(t, stripeAPIVersion, evt.APIVersion)

	var sess stripe.CheckoutSession
	require.NoError(t, json.Unmarshal(evt.Data.Raw, &sess))
	assert.Equal(t, sessID, sess.ID)
	assert.Equal(t, stripe.CheckoutSessionStatus("complete"), sess.Status)
	assert.Equal(t, stripe.CheckoutSessionPaymentStatus("paid"), sess.PaymentStatus)

	// Session state on the server should now be "complete".
	mock.mu.RLock()
	stored := mock.sessions[sessID]
	mock.mu.RUnlock()
	assert.Equal(t, "complete", stored.Status)
	assert.Equal(t, "paid", stored.PaymentStatus)
}

// TestStripeMock_Complete_PaidCreatesRetrievablePaymentIntentAndCharge guards
// the dangling-checkout-PI fix: a paid session must materialise the
// PaymentIntent (whose id the session advertised) plus its Charge, and fire
// payment_intent.succeeded + charge.succeeded — so the app can retrieve the PI
// and refund the checkout payment, not just receive checkout.session.completed.
func TestStripeMock_Complete_PaidCreatesRetrievablePaymentIntentAndCharge(t *testing.T) {
	const secret = "whsec_test_devmock"
	app := newWebhookSink(t, secret)

	mock, _, _ := newFullStripeStack(t, "")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: app.URL, Secret: secret})

	sessID := createTestSession(t, mock, []stripe.LineItem{
		{Description: "Item", AmountTotal: 1500, Quantity: 1, Currency: "gbp"},
	})

	mock.mu.RLock()
	piID := mock.sessions[sessID].PaymentIntentID
	mock.mu.RUnlock()
	require.NotEmpty(t, piID)

	resp := postComplete(t, mock, sessID, "paid")
	resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusSeeOther, resp.StatusCode)

	// Events fire in order: completed, payment_intent.succeeded, charge.succeeded.
	e1 := app.Wait(t, 2*time.Second)
	e2 := app.Wait(t, 2*time.Second)
	e3 := app.Wait(t, 2*time.Second)
	assert.Equal(t, "checkout.session.completed", string(e1.Type))
	assert.Equal(t, "payment_intent.succeeded", string(e2.Type))
	assert.Equal(t, "charge.succeeded", string(e3.Type))

	// The PaymentIntent + Charge now exist and are retrievable.
	mock.mu.RLock()
	pi, okPI := mock.paymentIntents[piID]
	var ch *stripeCharge
	if okPI {
		ch = mock.charges[pi.LatestChargeID]
	}
	mock.mu.RUnlock()
	require.True(t, okPI, "checkout paid must create the PaymentIntent the session advertised")
	assert.Equal(t, "succeeded", pi.Status)
	assert.Equal(t, int64(1500), pi.Amount)
	require.NotNil(t, ch, "checkout paid must create a Charge so the payment can be refunded")
	assert.Equal(t, int64(1500), ch.Amount)
	assert.Equal(t, piID, ch.PaymentIntentID)
}

// TestStripeMock_Complete_CancelledFiresExpiredAndRedirectsToCancelURL
// asserts the cancel button's mapping: status=expired, event=expired,
// redirect to cancel_url.
func TestStripeMock_Complete_CancelledFiresExpiredAndRedirectsToCancelURL(t *testing.T) {
	const secret = "whsec_test_devmock"
	app := newWebhookSink(t, secret)

	mock, _, _ := newFullStripeStack(t, "")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: app.URL, Secret: secret})
	sessID := createTestSession(t, mock, []stripe.LineItem{
		{Description: "Item", AmountTotal: 1000, Quantity: 1, Currency: "gbp"},
	})

	resp := postComplete(t, mock, sessID, "cancelled")
	defer resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusSeeOther, resp.StatusCode)
	assert.Equal(t, "https://app.example/cancel", resp.Header.Get("Location"))

	evt := app.Wait(t, 2*time.Second)
	assert.Equal(t, "checkout.session.expired", string(evt.Type))
}

// TestStripeMock_Complete_FailedIsSyncDeclineLeavesSessionOpen covers the
// "Card Declined" button. A synchronous decline leaves the session open (so the
// buyer can retry), fires NO webhook, and redirects back to the checkout page.
func TestStripeMock_Complete_FailedIsSyncDeclineLeavesSessionOpen(t *testing.T) {
	const secret = "whsec_test_devmock"
	app := newWebhookSink(t, secret)

	mock, _, _ := newFullStripeStack(t, "")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: app.URL, Secret: secret})
	sessID := createTestSession(t, mock, []stripe.LineItem{
		{Description: "Item", AmountTotal: 1000, Quantity: 1, Currency: "gbp"},
	})

	resp := postComplete(t, mock, sessID, "failed")
	defer resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusSeeOther, resp.StatusCode)
	assert.Equal(t, "/__hamr/stripe/checkout?session="+sessID, resp.Header.Get("Location"))

	// Session must remain open for a retry.
	mock.mu.RLock()
	stored := mock.sessions[sessID]
	mock.mu.RUnlock()
	assert.Equal(t, "open", stored.Status)
	assert.Equal(t, "unpaid", stored.PaymentStatus)

	// No webhook should fire for a synchronous decline.
	time.Sleep(150 * time.Millisecond)
	assert.Equal(t, 0, app.Count(), "sync card decline must not fire a webhook")
}

// TestStripeMock_Complete_DoubleSubmitGuardedByConflict ensures clicking
// "Pay" twice in quick succession can only fire one webhook. The second
// post returns 409 (session already complete) and does not redeliver.
func TestStripeMock_Complete_DoubleSubmitGuardedByConflict(t *testing.T) {
	const secret = "whsec_test_devmock"
	app := newWebhookSink(t, secret)

	mock, _, _ := newFullStripeStack(t, "")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: app.URL, Secret: secret})
	sessID := createTestSession(t, mock, []stripe.LineItem{
		{Description: "Item", AmountTotal: 1000, Quantity: 1, Currency: "gbp"},
	})

	resp1 := postComplete(t, mock, sessID, "paid")
	resp1.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusSeeOther, resp1.StatusCode)

	// A paid session fires three events: checkout.session.completed,
	// payment_intent.succeeded, charge.succeeded.
	app.Wait(t, 2*time.Second)
	app.Wait(t, 2*time.Second)
	app.Wait(t, 2*time.Second)
	time.Sleep(100 * time.Millisecond)
	firstCount := app.Count()
	assert.Equal(t, 3, firstCount, "paid fires session.completed + payment_intent.succeeded + charge.succeeded")

	resp2 := postComplete(t, mock, sessID, "paid")
	defer resp2.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusConflict, resp2.StatusCode)

	// Brief settle window: a duplicate delivery would push the count past the
	// three events from the first submit.
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, firstCount, app.Count(), "second submit must not fire duplicate webhooks")
}

// TestStripeMock_Complete_RejectsCrossOriginPOST verifies the CSRF guard:
// a POST with a foreign Origin header is refused before any state mutation.
func TestStripeMock_Complete_RejectsCrossOriginPOST(t *testing.T) {
	mock, _, _ := newFullStripeStack(t, "")
	sessID := createTestSession(t, mock, []stripe.LineItem{
		{Description: "Item", AmountTotal: 1000, Quantity: 1, Currency: "gbp"},
	})

	uiSrv := newUIOnlyServer(t, mock)
	form := url.Values{"session": {sessID}, "outcome": {"paid"}}
	req, err := http.NewRequest(http.MethodPost, uiSrv.URL+"/__hamr/stripe/complete", strings.NewReader(form.Encode()))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://evil.example")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)

	// Session should still be open.
	mock.mu.RLock()
	stored := mock.sessions[sessID]
	mock.mu.RUnlock()
	assert.Equal(t, "open", stored.Status)
}

// TestStripeMock_Complete_SubstitutesCheckoutSessionIDPlaceholder is a
// regression test for the {CHECKOUT_SESSION_ID} substitution Stripe
// performs in success_url before redirect. Apps lift the documented
// pattern straight from Stripe's docs (`?session_id={CHECKOUT_SESSION_ID}`)
// and call session.Get on the success page; without substitution they
// land on a literal "{CHECKOUT_SESSION_ID}" string.
func TestStripeMock_Complete_SubstitutesCheckoutSessionIDPlaceholder(t *testing.T) {
	mock, _, _ := newFullStripeStack(t, "")

	// Seed a session whose success_url uses the documented placeholder.
	id := "cs_test_" + randomHex(16)
	mock.mu.Lock()
	mock.sessions[id] = &stripeSession{
		ID:              id,
		PaymentIntentID: "pi_test_" + randomHex(16),
		Mode:            "payment",
		Currency:        "gbp",
		AmountTotal:     1000,
		LineItems:       []stripeLineItem{{Name: "x", UnitAmount: 1000, Quantity: 1, Currency: "gbp"}},
		SuccessURL:      "https://app.example/checkout/success?session_id={CHECKOUT_SESSION_ID}",
		CancelURL:       "https://app.example/checkout/cancel",
		Status:          "open",
		PaymentStatus:   "unpaid",
	}
	mock.mu.Unlock()

	resp := postComplete(t, mock, id, "paid")
	defer resp.Body.Close() //nolint:errcheck
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	got := resp.Header.Get("Location")
	assert.Equal(t,
		"https://app.example/checkout/success?session_id="+id,
		got,
		"{CHECKOUT_SESSION_ID} placeholder must be substituted with the actual session ID",
	)
	assert.NotContains(t, got, "{CHECKOUT_SESSION_ID}",
		"placeholder must not survive the redirect — apps would land on a literal {CHECKOUT_SESSION_ID} string")
}

// --- helpers ---

// newFullStripeStack constructs a StripeMock plus an httptest.Server hosting
// both the API and UI routes on one mux. Returns the mock and the test
// server (mock.baseURL is patched to srv.URL so generated checkout URLs
// resolve in tests).
//
// persistPath enables JSON-file persistence; pass "" to keep state in memory.
// (Production sets persistence via [dev.stripe].persist + persist_path.)
func newFullStripeStack(t *testing.T, persistPath string) (*StripeMock, *httptest.Server, *http.ServeMux) {
	t.Helper()
	mock := NewStripeMock(StripeMockOptions{
		BaseURL:     "http://stripe-mock.test",
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		PersistPath: persistPath,
		OnPersistError: func(err error) {
			t.Errorf("unexpected persist error: %v", err)
		},
	})
	mux := http.NewServeMux()
	mock.RegisterAPIRoutes(mux)
	mock.RegisterUIRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	// Patch baseURL so checkout URLs in any later inspection point at our test server.
	mock.baseURL = srv.URL
	return mock, srv, mux
}

// newUIOnlyServer mounts only the UI routes — used for the CSRF-guard test
// where we want to send a same-host request with a foreign Origin and need
// the host to be the test server's, not stripe-mock.test.
func newUIOnlyServer(t *testing.T, mock *StripeMock) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mock.RegisterUIRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// createTestSession bypasses the public Stripe API and seeds a session
// directly so the test stays focused on UI/outcome behavior.
func createTestSession(t *testing.T, mock *StripeMock, items []stripe.LineItem) string {
	t.Helper()
	id := "cs_test_" + randomHex(16)
	pi := "pi_test_" + randomHex(16)
	currency := ""
	var total int64
	var lines []stripeLineItem
	for _, it := range items {
		if currency == "" {
			currency = string(it.Currency)
		}
		lines = append(lines, stripeLineItem{
			Name:       it.Description,
			UnitAmount: it.AmountTotal,
			Quantity:   it.Quantity,
			Currency:   string(it.Currency),
		})
		total += it.AmountTotal * it.Quantity
	}
	mock.mu.Lock()
	mock.sessions[id] = &stripeSession{
		ID:              id,
		PaymentIntentID: pi,
		Created:         time.Now(),
		Mode:            "payment",
		Currency:        currency,
		AmountTotal:     total,
		LineItems:       lines,
		SuccessURL:      "https://app.example/success",
		CancelURL:       "https://app.example/cancel",
		Status:          "open",
		PaymentStatus:   "unpaid",
	}
	mock.mu.Unlock()
	return id
}

// getCheckoutPage GETs /__hamr/stripe/checkout?session=<id> off the mock's
// own httptest server (we tracked it in baseURL during setup) and asserts
// the status code, returning the body for further checks.
func getCheckoutPage(t *testing.T, mock *StripeMock, sessID string, wantStatus int) string {
	t.Helper()
	resp, err := http.Get(mock.baseURL + "/__hamr/stripe/checkout?session=" + sessID)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	body, _ := io.ReadAll(resp.Body)
	require.Equal(t, wantStatus, resp.StatusCode, "body=%s", string(body))
	return string(body)
}

// postComplete posts an outcome to the mock's complete endpoint, NOT
// following redirects (so the test can inspect the 303 + Location header).
func postComplete(t *testing.T, mock *StripeMock, sessID, outcome string) *http.Response {
	t.Helper()
	form := url.Values{"session": {sessID}, "outcome": {outcome}}
	req, err := http.NewRequest(http.MethodPost, mock.baseURL+"/__hamr/stripe/complete", strings.NewReader(form.Encode()))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	require.NoError(t, err)
	return resp
}

// webhookSink is a minimal httptest.Server that verifies incoming Stripe
// webhooks with the given secret and exposes them via Wait/Count.
type webhookSink struct {
	URL    string
	mu     sync.Mutex
	events []stripe.Event
	got    chan stripe.Event
	errs   []error
}

func newWebhookSink(t *testing.T, secret string) *webhookSink {
	t.Helper()
	s := &webhookSink{got: make(chan stripe.Event, 8)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		evt, err := webhook.ConstructEvent(body, r.Header.Get("Stripe-Signature"), secret)
		s.mu.Lock()
		if err != nil {
			s.errs = append(s.errs, err)
		} else {
			s.events = append(s.events, evt)
			s.got <- evt
		}
		s.mu.Unlock()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	s.URL = srv.URL
	return s
}

func (s *webhookSink) Wait(t *testing.T, d time.Duration) stripe.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), d)
	defer cancel()
	select {
	case evt := <-s.got:
		return evt
	case <-ctx.Done():
		s.mu.Lock()
		errs := s.errs
		s.mu.Unlock()
		t.Fatalf("timed out waiting for webhook delivery (errs=%v)", errs)
		return stripe.Event{}
	}
}

func (s *webhookSink) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}

// TestStripeMock_FullLoop_ViaRealStripeGoClient is the headline integration
// test: a real stripe-go session.New() against the API endpoint, then the
// dev UI complete flow firing a real signed webhook, all observed by the
// app via real webhook.ConstructEvent. This is the demo path.
func TestStripeMock_FullLoop_ViaRealStripeGoClient(t *testing.T) {
	const secret = "whsec_test_devmock"
	app := newWebhookSink(t, secret)

	mock, srv, _ := newFullStripeStack(t, "")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: app.URL, Secret: secret})

	withStripeBackend(t, srv.URL, func() {
		// Step 1: app creates a checkout session via real stripe-go.
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
			},
			SuccessURL: stripe.String("https://app.example/success"),
			CancelURL:  stripe.String("https://app.example/cancel"),
		}
		params.AddMetadata("order_id", "ord_full_loop")
		created, err := checkoutsession.New(params)
		require.NoError(t, err)

		// Step 2: user clicks "Pay" on the dev UI.
		resp := postComplete(t, mock, created.ID, "paid")
		defer resp.Body.Close() //nolint:errcheck
		require.Equal(t, http.StatusSeeOther, resp.StatusCode)
		assert.Equal(t, "https://app.example/success", resp.Header.Get("Location"))

		// Step 3: app's webhook handler verifies the signed event.
		evt := app.Wait(t, 2*time.Second)
		assert.Equal(t, "checkout.session.completed", string(evt.Type))

		var sess stripe.CheckoutSession
		require.NoError(t, json.Unmarshal(evt.Data.Raw, &sess))
		assert.Equal(t, created.ID, sess.ID)
		assert.Equal(t, "ord_full_loop", sess.Metadata["order_id"])
		assert.Equal(t, stripe.CheckoutSessionPaymentStatus("paid"), sess.PaymentStatus)
	})
}
