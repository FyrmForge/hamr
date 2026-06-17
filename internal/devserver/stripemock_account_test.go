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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	stripe "github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/account"
	"github.com/stripe/stripe-go/v82/accountlink"
	"github.com/stripe/stripe-go/v82/webhook"
)

// TestStripeMock_CreateAccount_RoundTripsThroughStripeGo asserts the create
// endpoint produces a Stripe-shaped account object that stripe-go decodes
// cleanly: ID prefix, type, country, default currency, and the
// pre-onboarding state (charges_enabled=false, requirements present).
func TestStripeMock_CreateAccount_RoundTripsThroughStripeGo(t *testing.T) {
	_, srv := newTestStripeServer(t)
	withStripeBackend(t, srv.URL, func() {
		params := &stripe.AccountParams{
			Type:    stripe.String("express"),
			Country: stripe.String("GB"),
			Email:   stripe.String("seller@example.com"),
		}
		params.AddMetadata("seller_id", "seller_42")
		acct, err := account.New(params)
		require.NoError(t, err)

		assert.True(t, strings.HasPrefix(acct.ID, "acct_test_"), "id %q missing acct_test_ prefix", acct.ID)
		assert.Equal(t, stripe.AccountType("express"), acct.Type)
		assert.Equal(t, "GB", acct.Country)
		assert.Equal(t, "seller@example.com", acct.Email)
		assert.Equal(t, stripe.Currency("gbp"), acct.DefaultCurrency)
		assert.False(t, acct.ChargesEnabled, "fresh account should not be charges-enabled")
		assert.False(t, acct.PayoutsEnabled, "fresh account should not be payouts-enabled")
		assert.False(t, acct.DetailsSubmitted, "fresh account should not have details_submitted")
		require.NotNil(t, acct.Requirements)
		assert.NotEmpty(t, acct.Requirements.CurrentlyDue, "fresh account should have outstanding requirements")
		assert.Equal(t, "seller_42", acct.Metadata["seller_id"])
	})
}

// TestStripeMock_GetAccount_RoundTrip asserts retrieve returns the same
// state we stored, including newly-created onboarding requirements.
func TestStripeMock_GetAccount_RoundTrip(t *testing.T) {
	_, srv := newTestStripeServer(t)
	withStripeBackend(t, srv.URL, func() {
		created, err := account.New(&stripe.AccountParams{
			Type:    stripe.String("express"),
			Country: stripe.String("US"),
		})
		require.NoError(t, err)

		fetched, err := account.GetByID(created.ID, nil)
		require.NoError(t, err)
		assert.Equal(t, created.ID, fetched.ID)
		assert.Equal(t, created.Country, fetched.Country)
		assert.Equal(t, stripe.Currency("usd"), fetched.DefaultCurrency)
		assert.False(t, fetched.ChargesEnabled)
	})
}

// TestStripeMock_GetAccount_NotFound surfaces 404s as a *stripe.Error.
func TestStripeMock_GetAccount_NotFound(t *testing.T) {
	_, srv := newTestStripeServer(t)
	withStripeBackend(t, srv.URL, func() {
		_, err := account.GetByID("acct_test_nonexistent", nil)
		require.Error(t, err)
		var sErr *stripe.Error
		if assert.ErrorAs(t, err, &sErr) {
			assert.Equal(t, http.StatusNotFound, sErr.HTTPStatusCode)
		}
	})
}

// TestStripeMock_CreateAccountLink_RoundTrip exercises the onboarding-link
// creation: stripe-go calls accountlink.New, the mock returns an
// account_link object whose URL points at the dev onboarding page.
func TestStripeMock_CreateAccountLink_RoundTrip(t *testing.T) {
	_, srv := newTestStripeServer(t)
	withStripeBackend(t, srv.URL, func() {
		acct, err := account.New(&stripe.AccountParams{
			Type:    stripe.String("express"),
			Country: stripe.String("GB"),
		})
		require.NoError(t, err)

		link, err := accountlink.New(&stripe.AccountLinkParams{
			Account:    stripe.String(acct.ID),
			RefreshURL: stripe.String("https://app.example/refresh"),
			ReturnURL:  stripe.String("https://app.example/return"),
			Type:       stripe.String("account_onboarding"),
		})
		require.NoError(t, err)

		assert.Contains(t, link.URL, "/__hamr/stripe/onboarding?account=")
		assert.Contains(t, link.URL, acct.ID)
		assert.Greater(t, link.ExpiresAt, link.Created, "expires_at should be after created")
	})
}

// TestStripeMock_CreateAccountLink_UnknownAccount asserts the link endpoint
// 404s on missing accounts (mirroring real Stripe).
func TestStripeMock_CreateAccountLink_UnknownAccount(t *testing.T) {
	_, srv := newTestStripeServer(t)
	withStripeBackend(t, srv.URL, func() {
		_, err := accountlink.New(&stripe.AccountLinkParams{
			Account:    stripe.String("acct_test_nonexistent"),
			RefreshURL: stripe.String("https://app.example/refresh"),
			ReturnURL:  stripe.String("https://app.example/return"),
			Type:       stripe.String("account_onboarding"),
		})
		require.Error(t, err)
	})
}

// TestStripeMock_OnboardingPage_RendersFreshAccount asserts the dev page
// renders the requirements + Complete button for a brand-new account.
func TestStripeMock_OnboardingPage_RendersFreshAccount(t *testing.T) {
	mock, _, _ := newFullStripeStack(t, "")
	acctID := createTestAccount(t, mock)

	body := getOnboardingPage(t, mock, acctID, http.StatusOK)
	assert.Contains(t, body, "Stripe Connect Onboarding")
	assert.Contains(t, body, acctID)
	assert.Contains(t, body, "tos_acceptance.date") // currently_due item
	assert.Contains(t, body, "Complete Onboarding")
}

// TestStripeMock_OnboardingPage_RendersOnboardedBanner asserts an already-
// onboarded account shows the success banner and DOES NOT show the Complete
// button (so a stale tab can't double-fire).
func TestStripeMock_OnboardingPage_RendersOnboardedBanner(t *testing.T) {
	mock, _, _ := newFullStripeStack(t, "")
	acctID := createTestAccount(t, mock)

	mock.mu.Lock()
	a := mock.accounts[acctID]
	a.ChargesEnabled = true
	a.PayoutsEnabled = true
	a.DetailsSubmitted = true
	a.CurrentlyDue = nil
	mock.mu.Unlock()

	body := getOnboardingPage(t, mock, acctID, http.StatusOK)
	assert.Contains(t, body, "fully onboarded")
	assert.NotContains(t, body, "Complete Onboarding")
}

// TestStripeMock_OnboardingPage_NotFound covers the missing-account path.
func TestStripeMock_OnboardingPage_NotFound(t *testing.T) {
	mock, _, _ := newFullStripeStack(t, "")
	getOnboardingPage(t, mock, "acct_test_nope", http.StatusNotFound)
}

// TestStripeMock_AccountComplete_FlipsStateAndFiresWebhook is the full
// happy path: clicking Complete flips charges/payouts/details_submitted and
// fires a real signed account.updated webhook the app verifies.
func TestStripeMock_AccountComplete_FlipsStateAndFiresWebhook(t *testing.T) {
	const secret = "whsec_test_devmock"
	app := newWebhookSink(t, secret)

	mock, _, _ := newFullStripeStack(t, "")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: app.URL, Secret: secret})

	acctID := createTestAccount(t, mock)

	resp := postAccountComplete(t, mock, acctID)
	defer resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	evt := app.Wait(t, 2*time.Second)
	assert.Equal(t, "account.updated", string(evt.Type))
	assert.Equal(t, stripeAPIVersion, evt.APIVersion)

	var got stripe.Account
	require.NoError(t, json.Unmarshal(evt.Data.Raw, &got))
	assert.Equal(t, acctID, got.ID)
	assert.True(t, got.ChargesEnabled)
	assert.True(t, got.PayoutsEnabled)
	assert.True(t, got.DetailsSubmitted)

	// Server-side state matches what we sent in the webhook.
	mock.mu.RLock()
	stored := mock.accounts[acctID]
	mock.mu.RUnlock()
	assert.True(t, stored.ChargesEnabled)
	assert.True(t, stored.PayoutsEnabled)
	assert.True(t, stored.DetailsSubmitted)
	assert.Empty(t, stored.CurrentlyDue)
	assert.Empty(t, stored.DisabledReason)
}

// TestStripeMock_AccountComplete_DoubleSubmitConflict asserts a stale tab
// can't spam webhooks: the second POST returns 409 and no second event fires.
func TestStripeMock_AccountComplete_DoubleSubmitConflict(t *testing.T) {
	const secret = "whsec_test_devmock"
	app := newWebhookSink(t, secret)

	mock, _, _ := newFullStripeStack(t, "")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: app.URL, Secret: secret})
	acctID := createTestAccount(t, mock)

	resp1 := postAccountComplete(t, mock, acctID)
	resp1.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusOK, resp1.StatusCode)
	app.Wait(t, 2*time.Second)

	resp2 := postAccountComplete(t, mock, acctID)
	defer resp2.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusConflict, resp2.StatusCode)

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, 1, app.Count(), "second submit must not fire a duplicate webhook")
}

// TestStripeMock_AccountComplete_RejectsCrossOrigin verifies the same-origin
// CSRF guard applies to the account-complete endpoint as well.
func TestStripeMock_AccountComplete_RejectsCrossOrigin(t *testing.T) {
	mock, _, _ := newFullStripeStack(t, "")
	acctID := createTestAccount(t, mock)

	uiSrv := newUIOnlyServer(t, mock)
	form := url.Values{"account": {acctID}}
	req, err := http.NewRequest(http.MethodPost, uiSrv.URL+"/__hamr/stripe/account/complete", strings.NewReader(form.Encode()))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://evil.example")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)

	mock.mu.RLock()
	stored := mock.accounts[acctID]
	mock.mu.RUnlock()
	assert.False(t, stored.ChargesEnabled, "CSRF-rejected request must not mutate state")
}

// TestStripeMock_FullOnboardingLoop_ViaRealStripeGoClient is the headline
// integration: real stripe-go account.New → accountlink.New → user clicks
// Complete on the dev page → real signed account.updated webhook arrives
// → real stripe-go account.Get sees the post-onboarding state.
func TestStripeMock_FullOnboardingLoop_ViaRealStripeGoClient(t *testing.T) {
	const secret = "whsec_test_devmock"
	app := newAccountWebhookSink(t, secret)

	mock, srv, _ := newFullStripeStack(t, "")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: app.URL, Secret: secret})

	withStripeBackend(t, srv.URL, func() {
		// Step 1: app creates a Connect account via real stripe-go.
		acct, err := account.New(&stripe.AccountParams{
			Type:    stripe.String("express"),
			Country: stripe.String("GB"),
			Email:   stripe.String("seller@example.com"),
		})
		require.NoError(t, err)
		require.False(t, acct.ChargesEnabled)

		// Step 2: app generates an onboarding link.
		link, err := accountlink.New(&stripe.AccountLinkParams{
			Account:    stripe.String(acct.ID),
			RefreshURL: stripe.String("https://app.example/refresh"),
			ReturnURL:  stripe.String("https://app.example/return"),
			Type:       stripe.String("account_onboarding"),
		})
		require.NoError(t, err)
		assert.Contains(t, link.URL, "/__hamr/stripe/onboarding?account="+acct.ID)

		// Step 3: user clicks Complete on the dev onboarding page.
		resp := postAccountComplete(t, mock, acct.ID)
		resp.Body.Close() //nolint:errcheck
		require.Equal(t, http.StatusOK, resp.StatusCode)

		// Step 4: app's webhook handler verifies the signed event.
		evt := app.WaitFor(t, "account.updated", 2*time.Second)
		var got stripe.Account
		require.NoError(t, json.Unmarshal(evt.Data.Raw, &got))
		assert.Equal(t, acct.ID, got.ID)
		assert.True(t, got.ChargesEnabled)

		// Step 5: app retrieves the account via real stripe-go and sees the
		// post-onboarding state — this is the path most marketplace code
		// uses to gate "can this seller list products yet".
		fetched, err := account.GetByID(acct.ID, nil)
		require.NoError(t, err)
		assert.True(t, fetched.ChargesEnabled)
		assert.True(t, fetched.PayoutsEnabled)
		assert.True(t, fetched.DetailsSubmitted)
		require.NotNil(t, fetched.Requirements)
		assert.Empty(t, fetched.Requirements.CurrentlyDue)
	})
}

// --- helpers ---

func createTestAccount(t *testing.T, mock *StripeMock) string {
	t.Helper()
	id := "acct_test_" + randomHex(16)
	mock.mu.Lock()
	mock.accounts[id] = &stripeAccount{
		ID:              id,
		Type:            "express",
		Country:         "GB",
		DefaultCurrency: "gbp",
		Created:         time.Now(),
		CurrentlyDue:    []string{"tos_acceptance.date", "external_account"},
		DisabledReason:  "requirements.past_due",
	}
	mock.mu.Unlock()
	return id
}

func getOnboardingPage(t *testing.T, mock *StripeMock, acctID string, wantStatus int) string {
	t.Helper()
	resp, err := http.Get(mock.baseURL + "/__hamr/stripe/onboarding?account=" + url.QueryEscape(acctID))
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	body, _ := io.ReadAll(resp.Body)
	require.Equal(t, wantStatus, resp.StatusCode, "body=%s", string(body))
	return string(body)
}

func postAccountComplete(t *testing.T, mock *StripeMock, acctID string) *http.Response {
	t.Helper()
	form := url.Values{"account": {acctID}}
	req, err := http.NewRequest(http.MethodPost, mock.baseURL+"/__hamr/stripe/account/complete", strings.NewReader(form.Encode()))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

// accountWebhookSink is webhookSink with a per-event-type Wait helper. The
// full-loop test fires both account.updated AND (in future slices) other
// events, so we need to filter by type.
type accountWebhookSink struct {
	URL    string
	mu     sync.Mutex
	events []stripe.Event
	got    chan stripe.Event
}

func newAccountWebhookSink(t *testing.T, secret string) *accountWebhookSink {
	t.Helper()
	s := &accountWebhookSink{got: make(chan stripe.Event, 16)}
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
		s.got <- evt
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	s.URL = srv.URL
	return s
}

func (s *accountWebhookSink) WaitFor(t *testing.T, evtType string, d time.Duration) stripe.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), d)
	defer cancel()
	for {
		select {
		case evt := <-s.got:
			if string(evt.Type) == evtType {
				return evt
			}
		case <-ctx.Done():
			t.Fatalf("timed out waiting for event %q", evtType)
			return stripe.Event{}
		}
	}
}
