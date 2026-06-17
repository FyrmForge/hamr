package devserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	stripe "github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/webhook"
)

// TestStripeMock_FireEvent_RoundTripsThroughConstructEvent proves the mock's
// webhook delivery is byte-identical to what stripe-go expects: the signature
// scheme, the api_version field, and the event envelope all clear
// webhook.ConstructEvent on the receiving side.
//
// This is the webhook-side counterpart to TestStripeMock_CreateAndRetrieveSession:
// together they cover both halves of the channel a real Stripe integration uses.
func TestStripeMock_FireEvent_RoundTripsThroughConstructEvent(t *testing.T) {
	const secret = "whsec_test_devmock"

	var (
		mu             sync.Mutex
		receivedEvent  stripe.Event
		receivedHeader string
		receivedRaw    []byte
		receivedErr    error
	)
	appHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		evt, err := webhook.ConstructEvent(body, r.Header.Get("Stripe-Signature"), secret)

		mu.Lock()
		receivedEvent = evt
		receivedHeader = r.Header.Get("Stripe-Signature")
		receivedRaw = body
		receivedErr = err
		mu.Unlock()

		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	appServer := httptest.NewServer(appHandler)
	t.Cleanup(appServer.Close)

	mock := NewStripeMock(StripeMockOptions{BaseURL: "http://stripe-mock.test"})
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: appServer.URL, Secret: secret})

	// Build a session-shaped object. Mirrors what serializeSession would emit
	// when an outcome button is clicked — kept inline here so the test
	// doesn't depend on session-creation working first.
	sessionObj := map[string]any{
		"id":             "cs_test_evt_target",
		"object":         "checkout.session",
		"payment_status": "paid",
		"status":         "complete",
		"amount_total":   1234,
		"currency":       "gbp",
		"metadata":       map[string]string{"order_id": "ord_xyz"},
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	require.NoError(t, mock.FireEvent(ctx, "checkout.session.completed", sessionObj))

	mu.Lock()
	defer mu.Unlock()
	require.NoError(t, receivedErr, "webhook.ConstructEvent rejected mock-signed event (header=%q payload=%q)", receivedHeader, string(receivedRaw))

	assert.Equal(t, "checkout.session.completed", string(receivedEvent.Type))
	assert.Equal(t, stripeAPIVersion, receivedEvent.APIVersion)
	assert.False(t, receivedEvent.Livemode)
	assert.NotEmpty(t, receivedEvent.ID)

	// data.object should round-trip into a CheckoutSession via the standard
	// envelope.Data.Raw → json.Unmarshal pattern apps use.
	require.NotNil(t, receivedEvent.Data, "event.Data must be present")
	var got stripe.CheckoutSession
	require.NoError(t, json.Unmarshal(receivedEvent.Data.Raw, &got))
	assert.Equal(t, "cs_test_evt_target", got.ID)
	assert.Equal(t, int64(1234), got.AmountTotal)
	assert.Equal(t, stripe.Currency("gbp"), got.Currency)
	assert.Equal(t, "ord_xyz", got.Metadata["order_id"])
}

// TestStripeMock_FireEvent_NoEndpoint asserts firing without an endpoint is
// a silent no-op so callers don't have to gate every fire.
func TestStripeMock_FireEvent_NoEndpoint(t *testing.T) {
	mock := NewStripeMock(StripeMockOptions{BaseURL: "http://stripe-mock.test"})
	err := mock.FireEvent(t.Context(), "checkout.session.completed", map[string]any{"id": "cs_test_x"})
	assert.NoError(t, err)
}

// TestStripeMock_FireEvent_NonOKResponse surfaces a non-2xx app response as
// an error rather than silently succeeding.
func TestStripeMock_FireEvent_NonOKResponse(t *testing.T) {
	const secret = "whsec_test"
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(failing.Close)

	mock := NewStripeMock(StripeMockOptions{BaseURL: "http://stripe-mock.test"})
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: failing.URL, Secret: secret})

	err := mock.FireEvent(t.Context(), "checkout.session.completed", map[string]any{"id": "cs_test_x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "returned 500")
}

// TestStripeMock_FireEvent_RejectedByConstructOnTamperedPayload sanity-checks
// that webhook.ConstructEvent really would reject a tampered payload — guards
// against accidentally writing a test that always passes.
func TestStripeMock_FireEvent_RejectedByConstructOnTamperedPayload(t *testing.T) {
	const secret = "whsec_test"
	var (
		mu     sync.Mutex
		errOut error
	)
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		// Tamper: append a byte before verification.
		body = append(body, ' ')
		_, err := webhook.ConstructEvent(body, r.Header.Get("Stripe-Signature"), secret)
		mu.Lock()
		errOut = err
		mu.Unlock()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(app.Close)

	mock := NewStripeMock(StripeMockOptions{BaseURL: "http://stripe-mock.test"})
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: app.URL, Secret: secret})

	// FireEvent will return an error because the tamper-then-verify produces
	// a 400 response. We don't care about that; we care that the verification
	// caught it.
	_ = mock.FireEvent(t.Context(), "checkout.session.completed", map[string]any{"id": "cs_test_x"})
	mu.Lock()
	defer mu.Unlock()
	require.Error(t, errOut, "ConstructEvent should reject tampered payload")
}
