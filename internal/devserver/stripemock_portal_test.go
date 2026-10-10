package devserver

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	stripe "github.com/stripe/stripe-go/v86"
	portalconfig "github.com/stripe/stripe-go/v86/billingportal/configuration"
	portalsession "github.com/stripe/stripe-go/v86/billingportal/session"
)

// TestStripeMock_Portal_CancelAndKeep drives the hosted portal the way an
// app does: create a session through stripe-go, open the page, flip
// cancel_at_period_end from it, and watch the webhook.
func TestStripeMock_Portal_CancelAndKeep(t *testing.T) {
	mock, srv, _ := newFullStripeStack(t, "")
	sink := newWebhookSink(t, "whsec_test")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: sink.URL, Secret: "whsec_test"})
	withStripeBackend(t, srv.URL, func() {
		var se *stripe.Error
		_, err := portalsession.New(&stripe.BillingPortalSessionParams{Customer: stripe.String("cus_nope")})
		require.ErrorAs(t, err, &se)
		assert.Equal(t, stripe.ErrorCodeResourceMissing, se.Code)

		subID := createSubscription(t, mock, subscriptionSessionParams(2000, "month"))
		drainEvents(t, sink, 6)
		cusID := ensurePortalCustomer(t, mock, subID)

		ps, err := portalsession.New(&stripe.BillingPortalSessionParams{
			Customer:  stripe.String(cusID),
			ReturnURL: stripe.String("https://app.example/account"),
		})
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(ps.ID, "bps_test_"))
		assert.Contains(t, ps.URL, "/__hamr/stripe/portal?session="+ps.ID)
		assert.Equal(t, cusID, ps.Customer)

		body := getPortalPage(t, ps.URL, http.StatusOK)
		assert.Contains(t, body, "Customer portal")
		assert.Contains(t, body, "a@b.c")
		assert.Contains(t, body, shortID(subID))
		assert.Contains(t, body, "MOCK-0001")
		assert.Contains(t, body, "Cancel at period end")
		assert.NotContains(t, body, "Keep subscription")
		assert.Contains(t, body, `href="https://app.example/account"`)

		resp := postDashboardForm(t, mock, "/__hamr/stripe/portal/action",
			url.Values{"session": {ps.ID}, "subscription": {subID}, "action": {"cancel"}})
		assert.Equal(t, http.StatusSeeOther, resp.StatusCode)
		assert.Equal(t, "/__hamr/stripe/portal?session="+ps.ID, resp.Header.Get("Location"))
		mock.mu.RLock()
		assert.True(t, mock.subscriptions[subID].CancelAtPeriodEnd)
		mock.mu.RUnlock()
		evt := sink.Wait(t, 3*time.Second)
		assert.Equal(t, stripe.EventType("customer.subscription.updated"), evt.Type)
		assert.Equal(t, true, evt.Data.Object["cancel_at_period_end"])
		time.Sleep(100 * time.Millisecond)
		assert.Equal(t, 7, sink.Count(), "exactly one event per action")

		body = getPortalPage(t, ps.URL, http.StatusOK)
		assert.Contains(t, body, "Keep subscription")
		assert.NotContains(t, body, "Cancel at period end</button>")

		resp = postDashboardForm(t, mock, "/__hamr/stripe/portal/action",
			url.Values{"session": {ps.ID}, "subscription": {subID}, "action": {"keep"}})
		assert.Equal(t, http.StatusSeeOther, resp.StatusCode)
		mock.mu.RLock()
		assert.False(t, mock.subscriptions[subID].CancelAtPeriodEnd)
		mock.mu.RUnlock()
		evt = sink.Wait(t, 3*time.Second)
		assert.Equal(t, stripe.EventType("customer.subscription.updated"), evt.Type)
		assert.Equal(t, false, evt.Data.Object["cancel_at_period_end"])

		// Update card: no state change, no event, just a notice.
		resp = postDashboardForm(t, mock, "/__hamr/stripe/portal/action",
			url.Values{"session": {ps.ID}, "action": {"card"}})
		assert.Equal(t, http.StatusSeeOther, resp.StatusCode)
		body = getPortalPage(t, mock.baseURL+resp.Header.Get("Location"), http.StatusOK)
		assert.Contains(t, body, "Card updated (mock: nothing happened)")
		time.Sleep(100 * time.Millisecond)
		assert.Equal(t, 8, sink.Count())

		// A subscription of another customer is refused.
		mock.mu.Lock()
		mock.customers["cus_test_other"] = &stripeCustomer{ID: "cus_test_other", Created: mock.now()}
		mock.mu.Unlock()
		other, err := portalsession.New(&stripe.BillingPortalSessionParams{Customer: stripe.String("cus_test_other")})
		require.NoError(t, err)
		resp = postDashboardForm(t, mock, "/__hamr/stripe/portal/action",
			url.Values{"session": {other.ID}, "subscription": {subID}, "action": {"cancel"}})
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)

		// Unknown / missing sessions on the page.
		getPortalPage(t, mock.baseURL+"/__hamr/stripe/portal", http.StatusBadRequest)
		getPortalPage(t, mock.baseURL+"/__hamr/stripe/portal?session=bps_test_nope", http.StatusNotFound)
	})
}

// TestStripeMock_Portal_ConfigurationDisablesCancel: a configuration with
// subscription_cancel disabled hides the cancel button and refuses the action.
func TestStripeMock_Portal_ConfigurationDisablesCancel(t *testing.T) {
	mock, srv, _ := newFullStripeStack(t, "")
	withStripeBackend(t, srv.URL, func() {
		subID := createSubscription(t, mock, subscriptionSessionParams(2000, "month"))
		cusID := ensurePortalCustomer(t, mock, subID)

		iter := portalconfig.List(&stripe.BillingPortalConfigurationListParams{})
		assert.False(t, iter.Next(), "no configuration until one is created")
		require.NoError(t, iter.Err())

		cfg, err := portalconfig.New(&stripe.BillingPortalConfigurationParams{
			Features: &stripe.BillingPortalConfigurationFeaturesParams{
				SubscriptionCancel: &stripe.BillingPortalConfigurationFeaturesSubscriptionCancelParams{Enabled: new(false)},
			},
		})
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(cfg.ID, "bpc_test_"))
		assert.False(t, cfg.Features.SubscriptionCancel.Enabled)
		iter = portalconfig.List(&stripe.BillingPortalConfigurationListParams{})
		require.True(t, iter.Next())
		assert.Equal(t, cfg.ID, iter.BillingPortalConfiguration().ID)

		ps, err := portalsession.New(&stripe.BillingPortalSessionParams{Customer: stripe.String(cusID)})
		require.NoError(t, err)
		body := getPortalPage(t, ps.URL, http.StatusOK)
		assert.Contains(t, body, shortID(subID))
		assert.NotContains(t, body, "Cancel at period end</button>")
		assert.Contains(t, body, `href="/"`, "no return_url falls back to /")

		resp := postDashboardForm(t, mock, "/__hamr/stripe/portal/action",
			url.Values{"session": {ps.ID}, "subscription": {subID}, "action": {"cancel"}})
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})
}

// --- helpers ---

// ensurePortalCustomer returns the subscription's customer id, inserting the
// customer record when the checkout flow did not create one.
func ensurePortalCustomer(t *testing.T, mock *StripeMock, subID string) string {
	t.Helper()
	mock.mu.Lock()
	defer mock.mu.Unlock()
	cusID := mock.subscriptions[subID].CustomerID
	require.NotEmpty(t, cusID)
	if _, ok := mock.customers[cusID]; !ok {
		mock.customers[cusID] = &stripeCustomer{ID: cusID, Email: "a@b.c", Created: mock.now()}
	} else {
		mock.customers[cusID].Email = "a@b.c"
	}
	return cusID
}

// getPortalPage GETs a full portal URL and asserts the status code.
func getPortalPage(t *testing.T, fullURL string, wantStatus int) string {
	t.Helper()
	resp, err := http.Get(fullURL)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	body, _ := io.ReadAll(resp.Body)
	require.Equal(t, wantStatus, resp.StatusCode, "body=%s", string(body))
	return string(body)
}
