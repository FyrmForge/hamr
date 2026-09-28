package devserver

import (
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	stripe "github.com/stripe/stripe-go/v86"
	"github.com/stripe/stripe-go/v86/checkout/session"
	"github.com/stripe/stripe-go/v86/coupon"
	"github.com/stripe/stripe-go/v86/invoice"
	"github.com/stripe/stripe-go/v86/subscription"
)

// TestStripeMock_SubscriptionCheckout_CreatesSubscriptionAndInvoice drives
// the buyer path for a mode=subscription session and checks what the app
// sees afterwards through stripe-go and through its webhook handler.
func TestStripeMock_SubscriptionCheckout_CreatesSubscriptionAndInvoice(t *testing.T) {
	mock, srv, _ := newFullStripeStack(t, "")
	sink := newWebhookSink(t, "whsec_test")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: sink.URL, Secret: "whsec_test"})
	withStripeBackend(t, srv.URL, func() {
		p := subscriptionSessionParams(2000, "month")
		p.SubscriptionData = &stripe.CheckoutSessionSubscriptionDataParams{Metadata: map[string]string{"user_id": "42"}}
		sess, err := session.New(p)
		require.NoError(t, err)
		assert.Equal(t, stripe.CheckoutSessionModeSubscription, sess.Mode)
		assert.Nil(t, sess.Subscription, "no subscription until paid")

		resp := postComplete(t, mock, sess.ID, "paid")
		require.Equal(t, http.StatusSeeOther, resp.StatusCode)
		var types []string
		for range 6 {
			types = append(types, string(sink.Wait(t, 3*time.Second).Type))
		}
		assert.Equal(t, []string{
			"checkout.session.completed", "customer.subscription.created", "invoice.paid",
			"invoice.payment_succeeded", "payment_intent.succeeded", "charge.succeeded",
		}, types)

		got, err := session.Get(sess.ID, nil)
		require.NoError(t, err)
		require.NotNil(t, got.Subscription)
		require.NotNil(t, got.Customer)
		assert.True(t, strings.HasPrefix(got.Subscription.ID, "sub_test_"))
		assert.True(t, strings.HasPrefix(got.Customer.ID, "cus_test_"))

		sub, err := subscription.Get(got.Subscription.ID, nil)
		require.NoError(t, err)
		assert.Equal(t, stripe.SubscriptionStatusActive, sub.Status)
		assert.Equal(t, got.Customer.ID, sub.Customer.ID)
		assert.Equal(t, "42", sub.Metadata["user_id"])
		require.Len(t, sub.Items.Data, 1)
		item := sub.Items.Data[0]
		assert.Equal(t, int64(2000), item.Price.UnitAmount)
		assert.Equal(t, stripe.PriceRecurringIntervalMonth, item.Price.Recurring.Interval)
		assert.Equal(t, int64(1), item.Quantity)
		assert.Equal(t, sub.Created, item.CurrentPeriodStart)
		assert.Equal(t, addMonthsClamped(time.Unix(sub.Created, 0), 1).Unix(), item.CurrentPeriodEnd)
		require.NotNil(t, sub.LatestInvoice)

		inv, err := invoice.Get(sub.LatestInvoice.ID, nil)
		require.NoError(t, err)
		assert.Equal(t, stripe.InvoiceStatusPaid, inv.Status)
		assert.Equal(t, stripe.InvoiceBillingReasonSubscriptionCreate, inv.BillingReason)
		assert.Equal(t, int64(2000), inv.Total)
		assert.Equal(t, int64(2000), inv.AmountPaid)
		require.NotNil(t, inv.Parent)
		require.NotNil(t, inv.Parent.SubscriptionDetails)
		assert.Equal(t, sub.ID, inv.Parent.SubscriptionDetails.Subscription.ID)
		require.Len(t, inv.Payments.Data, 1)
		assert.Nil(t, got.PaymentIntent, "a subscription session's payment lives on the invoice")
		assert.True(t, strings.HasPrefix(inv.Payments.Data[0].Payment.PaymentIntent.ID, "pi_test_"))
		require.Len(t, inv.Lines.Data, 1)
		assert.Equal(t, item.Price.ID, inv.Lines.Data[0].Pricing.PriceDetails.Price.ID)

		// Mode / recurring mismatches are 400s.
		var se *stripe.Error
		bad := sessionParams(2000)
		bad.Mode = stripe.String(string(stripe.CheckoutSessionModeSubscription))
		_, err = session.New(bad)
		require.ErrorAs(t, err, &se, "subscription mode needs recurring prices")
		bad = subscriptionSessionParams(2000, "month")
		bad.Mode = stripe.String(string(stripe.CheckoutSessionModePayment))
		_, err = session.New(bad)
		require.ErrorAs(t, err, &se, "payment mode refuses recurring prices")
		_, err = subscription.New(&stripe.SubscriptionParams{Customer: stripe.String(got.Customer.ID)})
		require.ErrorAs(t, err, &se, "direct create is not mocked")
	})
}

// TestStripeMock_Clock_AdvanceRenewsSubscriptions: advancing the mock clock
// runs every renewal that falls due, stamps each at its period end, and
// persists the offset.
func TestStripeMock_Clock_AdvanceRenewsSubscriptions(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	mock, srv, _ := newFullStripeStack(t, statePath)
	sink := newWebhookSink(t, "whsec_test")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: sink.URL, Secret: "whsec_test"})
	withStripeBackend(t, srv.URL, func() {
		subID := createSubscription(t, mock, subscriptionSessionParams(1500, "month"))
		drainEvents(t, sink, 6)
		mock.mu.RLock()
		start := mock.subscriptions[subID].BillingCycleAnchor // the anchor, not a fresh now(): a second may have ticked since create
		mock.mu.RUnlock()

		_, err := mock.advanceClock(start.AddDate(0, 0, -1))
		require.Error(t, err, "clock never runs backwards")

		cycled, err := mock.advanceClock(start.AddDate(0, 0, 35))
		require.NoError(t, err)
		assert.Equal(t, []string{subID}, cycled)
		var types []string
		for range 5 {
			types = append(types, string(sink.Wait(t, 3*time.Second).Type))
		}
		assert.Equal(t, []string{
			"invoice.paid", "invoice.payment_succeeded", "customer.subscription.updated",
			"payment_intent.succeeded", "charge.succeeded",
		}, types)

		sub, err := subscription.Get(subID, nil)
		require.NoError(t, err)
		firstEnd := addMonthsClamped(start, 1)
		assert.Equal(t, firstEnd.Unix(), sub.Items.Data[0].CurrentPeriodStart, "new period starts where the old one ended")
		assert.Equal(t, addMonthsClamped(start, 2).Unix(), sub.Items.Data[0].CurrentPeriodEnd)

		invs := listInvoices(t, subID)
		require.Len(t, invs, 2)
		assert.Equal(t, stripe.InvoiceBillingReasonSubscriptionCycle, invs[0].BillingReason)
		assert.Equal(t, firstEnd.Unix(), invs[0].Created, "renewal is stamped at the period end, not at the advance target")
		assert.Equal(t, int64(1500), invs[0].AmountPaid)

		// Three months in one call: three cycles, in order.
		cycled, err = mock.advanceClock(mock.now().AddDate(0, 3, 0))
		require.NoError(t, err)
		assert.Equal(t, []string{subID, subID, subID}, cycled)
		drainEvents(t, sink, 15)
		assert.Len(t, listInvoices(t, subID), 5)

		// The offset survives a restart.
		reloaded := NewStripeMock(StripeMockOptions{BaseURL: "http://x", PersistPath: statePath})
		assert.WithinDuration(t, mock.now(), reloaded.now(), 2*time.Second)
		assert.Len(t, reloaded.subscriptions, 1)
	})
}

// TestStripeMock_Subscription_CouponDurations: once applies to the first
// invoice only, repeating for its months, and the subscription drops the
// discount when it runs out.
func TestStripeMock_Subscription_CouponDurations(t *testing.T) {
	mock, srv, _ := newFullStripeStack(t, "")
	withStripeBackend(t, srv.URL, func() {
		_, err := coupon.New(&stripe.CouponParams{ID: stripe.String("HALF2"), PercentOff: stripe.Float64(50), Duration: stripe.String("repeating"), DurationInMonths: stripe.Int64(2)})
		require.NoError(t, err)
		_, err = coupon.New(&stripe.CouponParams{ID: stripe.String("ONCE"), PercentOff: stripe.Float64(10), Duration: stripe.String("once")})
		require.NoError(t, err)

		p := subscriptionSessionParams(1000, "month")
		p.Discounts = []*stripe.CheckoutSessionDiscountParams{{Coupon: stripe.String("HALF2")}}
		repeating := createSubscription(t, mock, p)
		p = subscriptionSessionParams(1000, "month")
		p.Discounts = []*stripe.CheckoutSessionDiscountParams{{Coupon: stripe.String("ONCE")}}
		once := createSubscription(t, mock, p)

		sub, err := subscription.Get(repeating, nil)
		require.NoError(t, err)
		require.Len(t, sub.Discounts, 1)
		assert.Equal(t, "HALF2", sub.Discounts[0].Source.Coupon.ID)
		assert.NotZero(t, sub.Discounts[0].End)
		sub, err = subscription.Get(once, nil)
		require.NoError(t, err)
		assert.Empty(t, sub.Discounts, "a once coupon is spent on the first invoice")

		for range 2 {
			_, err = mock.advanceClock(mock.now().AddDate(0, 1, 0))
			require.NoError(t, err)
		}
		totals := func(id string) []int64 {
			var out []int64
			for _, in := range listInvoices(t, id) { // newest first
				out = append(out, in.Total)
			}
			return out
		}
		assert.Equal(t, []int64{1000, 500, 500}, totals(repeating), "50% for two months, then full price")
		assert.Equal(t, []int64{1000, 1000, 900}, totals(once))
		sub, err = subscription.Get(repeating, nil)
		require.NoError(t, err)
		assert.Empty(t, sub.Discounts, "expired repeating discount is removed")
	})
}

// TestStripeMock_Subscription_FailedRenewalAndRetry covers the dunning
// path: a failed renewal, a retry that pays it, and cancellation when a
// past_due subscription reaches its next period end unpaid.
func TestStripeMock_Subscription_FailedRenewalAndRetry(t *testing.T) {
	mock, srv, _ := newFullStripeStack(t, "")
	sink := newWebhookSink(t, "whsec_test")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: sink.URL, Secret: "whsec_test"})
	withStripeBackend(t, srv.URL, func() {
		subID := createSubscription(t, mock, subscriptionSessionParams(1000, "month"))
		drainEvents(t, sink, 6)

		status, err := mock.subscriptionAction(subID, "fail_next")
		require.NoError(t, err)
		assert.Equal(t, "active", status)
		status, err = mock.subscriptionAction(subID, "next")
		require.NoError(t, err)
		assert.Equal(t, "past_due", status)
		var types []string
		for range 3 {
			types = append(types, string(sink.Wait(t, 3*time.Second).Type))
		}
		assert.Equal(t, []string{"invoice.payment_failed", "customer.subscription.updated", "payment_intent.payment_failed"}, types)
		invs := listInvoices(t, subID)
		require.Len(t, invs, 2)
		assert.Equal(t, stripe.InvoiceStatusOpen, invs[0].Status)
		assert.Equal(t, int64(0), invs[0].AmountPaid)
		assert.Zero(t, invs[0].NextPaymentAttempt, "no automatic retry is scheduled")

		status, err = mock.subscriptionAction(subID, "retry")
		require.NoError(t, err)
		assert.Equal(t, "active", status)
		types = nil
		for range 5 {
			types = append(types, string(sink.Wait(t, 3*time.Second).Type))
		}
		assert.Equal(t, []string{"invoice.paid", "invoice.payment_succeeded", "customer.subscription.updated", "payment_intent.succeeded", "charge.succeeded"}, types)
		inv, err := invoice.Get(invs[0].ID, nil)
		require.NoError(t, err)
		assert.Equal(t, stripe.InvoiceStatusPaid, inv.Status)
		assert.Equal(t, int64(2), inv.AttemptCount)
		_, err = mock.subscriptionAction(subID, "retry")
		require.Error(t, err, "nothing to retry on an active subscription")

		// Unpaid through a whole period: dunning gives up and cancels.
		_, err = mock.subscriptionAction(subID, "fail_next")
		require.NoError(t, err)
		_, err = mock.subscriptionAction(subID, "next")
		require.NoError(t, err)
		drainEvents(t, sink, 3)
		status, err = mock.subscriptionAction(subID, "next")
		require.NoError(t, err)
		assert.Equal(t, "canceled", status)
		assert.Equal(t, stripe.EventType("customer.subscription.deleted"), sink.Wait(t, 3*time.Second).Type)
		assert.Equal(t, stripe.EventType("invoice.voided"), sink.Wait(t, 3*time.Second).Type, "the unpaid renewal is voided on the way out")
		invs = listInvoices(t, subID)
		require.Len(t, invs, 3)
		assert.Equal(t, stripe.InvoiceStatusVoid, invs[0].Status)
		assert.NotZero(t, invs[0].StatusTransitions.VoidedAt)
		assert.Empty(t, listOpenInvoices(t), "a voided invoice is no longer open")
	})
}

// TestStripeMock_Subscription_CancelVoidsOpenInvoice: DELETE on a past_due
// subscription voids its open renewal invoice, as Stripe does, so
// invoice.List(status=open) stops returning it.
func TestStripeMock_Subscription_CancelVoidsOpenInvoice(t *testing.T) {
	mock, srv, _ := newFullStripeStack(t, "")
	sink := newWebhookSink(t, "whsec_test")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: sink.URL, Secret: "whsec_test"})
	withStripeBackend(t, srv.URL, func() {
		subID := createSubscription(t, mock, subscriptionSessionParams(1000, "month"))
		drainEvents(t, sink, 6)
		_, err := mock.subscriptionAction(subID, "fail_next")
		require.NoError(t, err)
		_, err = mock.subscriptionAction(subID, "next")
		require.NoError(t, err)
		drainEvents(t, sink, 3)
		require.Len(t, listOpenInvoices(t), 1)

		got, err := subscription.Cancel(subID, nil)
		require.NoError(t, err)
		assert.Equal(t, stripe.SubscriptionStatusCanceled, got.Status)
		assert.Equal(t, stripe.EventType("customer.subscription.deleted"), sink.Wait(t, 3*time.Second).Type)
		evt := sink.Wait(t, 3*time.Second)
		assert.Equal(t, stripe.EventType("invoice.voided"), evt.Type)

		var inv stripe.Invoice
		require.NoError(t, json.Unmarshal(evt.Data.Raw, &inv))
		assert.Equal(t, stripe.InvoiceStatusVoid, inv.Status)
		assert.Equal(t, got.CanceledAt, inv.StatusTransitions.VoidedAt)
		require.Len(t, inv.Payments.Data, 1)
		assert.Equal(t, "canceled", string(inv.Payments.Data[0].Status))
		assert.Empty(t, listOpenInvoices(t))
		assert.Equal(t, stripe.InvoiceStatusPaid, listInvoices(t, subID)[1].Status, "the paid first invoice is untouched")
	})
}

// TestStripeMock_Clock_InterleavedRenewals: one advance covering several
// subscriptions runs their cycles in time order, not per subscription.
func TestStripeMock_Clock_InterleavedRenewals(t *testing.T) {
	mock, srv, _ := newFullStripeStack(t, "")
	withStripeBackend(t, srv.URL, func() {
		every := func(days int64) *stripe.CheckoutSessionParams {
			p := subscriptionSessionParams(1000, "day")
			p.LineItems[0].PriceData.Recurring.IntervalCount = stripe.Int64(days)
			return p
		}
		three := createSubscription(t, mock, every(3))
		two := createSubscription(t, mock, every(2))

		// Due at days 2 (two), 3 (three), 4 (two): three is created first
		// but must not cycle first.
		cycled, err := mock.advanceClock(mock.now().AddDate(0, 0, 5))
		require.NoError(t, err)
		assert.Equal(t, []string{two, three, two}, cycled)

		var created []int64
		iter := invoice.List(&stripe.InvoiceListParams{})
		for iter.Next() {
			if iter.Invoice().BillingReason == stripe.InvoiceBillingReasonSubscriptionCycle {
				created = append(created, iter.Invoice().Created)
			}
		}
		require.NoError(t, iter.Err())
		require.Len(t, created, 3)
		assert.True(t, created[0] > created[1] && created[1] > created[2], "renewal invoices are stamped in time order (newest first): %v", created)
	})
}

// TestStripeMock_Subscription_RetryAfterRestart: a past_due subscription and
// its open invoice survive a restart from state.json, and the retry still
// pays it.
func TestStripeMock_Subscription_RetryAfterRestart(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	mock, srv, _ := newFullStripeStack(t, statePath)
	var subID string
	withStripeBackend(t, srv.URL, func() {
		subID = createSubscription(t, mock, subscriptionSessionParams(1000, "month"))
	})
	_, err := mock.subscriptionAction(subID, "fail_next")
	require.NoError(t, err)
	status, err := mock.subscriptionAction(subID, "next")
	require.NoError(t, err)
	require.Equal(t, "past_due", status)

	reloaded := NewStripeMock(StripeMockOptions{BaseURL: "http://x", PersistPath: statePath})
	reloaded.mu.RLock()
	sub := reloaded.subscriptions[subID]
	require.Equal(t, "past_due", sub.Status)
	require.Equal(t, "open", reloaded.invoices[sub.LatestInvoiceID].Status)
	reloaded.mu.RUnlock()

	status, err = reloaded.subscriptionAction(subID, "retry")
	require.NoError(t, err)
	assert.Equal(t, "active", status)
	reloaded.mu.RLock()
	defer reloaded.mu.RUnlock()
	inv := reloaded.invoices[sub.LatestInvoiceID]
	assert.Equal(t, "paid", inv.Status)
	assert.Equal(t, 2, inv.AttemptCount)
	assert.Equal(t, "succeeded", reloaded.paymentIntents[inv.PaymentIntentID].Status)
}

// TestStripeMock_MCP_AdvanceAndSubscription drives the stripe.advance and
// stripe.subscription tools through the gateway's dispatch, the way the MCP
// server calls them.
func TestStripeMock_MCP_AdvanceAndSubscription(t *testing.T) {
	mock, srv, _ := newFullStripeStack(t, "")
	var subID string
	withStripeBackend(t, srv.URL, func() {
		subID = createSubscription(t, mock, subscriptionSessionParams(1000, "month"))
	})
	g := &mcpGateway{stripeMock: mock}
	status := func(err error) int {
		var oe *stripeOpError
		require.ErrorAs(t, err, &oe)
		return oe.status
	}
	subAction := func(action string) (any, error) {
		return g.dispatch("stripe.subscription", []byte(`{"subscription":"`+subID+`","action":"`+action+`"}`))
	}

	// stripe.advance: by, to, and the 400s.
	got, err := g.dispatch("stripe.advance", []byte(`{"by":"1d"}`))
	require.NoError(t, err)
	res := got.(stripeAdvanceResult)
	assert.Empty(t, res.Cycled, "nothing due within a day")
	assert.NotNil(t, res.Cycled, "cycled is [] not null")
	assert.WithinDuration(t, mock.now(), mustParseRFC3339(t, res.Now), 2*time.Second)
	got, err = g.dispatch("stripe.advance", []byte(`{"to":"`+mock.now().AddDate(0, 0, 35).Format(time.RFC3339)+`"}`))
	require.NoError(t, err)
	assert.Equal(t, []string{subID}, got.(stripeAdvanceResult).Cycled)
	_, err = g.dispatch("stripe.advance", []byte(`{"by":"1d","to":"2030-01-01T00:00:00Z"}`))
	assert.Equal(t, http.StatusBadRequest, status(err), "both")
	_, err = g.dispatch("stripe.advance", []byte(`{}`))
	assert.Equal(t, http.StatusBadRequest, status(err), "neither")
	_, err = g.dispatch("stripe.advance", []byte(`{"to":"2000-01-01T00:00:00Z"}`))
	assert.Equal(t, http.StatusBadRequest, status(err), "backwards")

	// stripe.subscription: fail_next, next, retry, next, cancel.
	got, err = subAction("fail_next")
	require.NoError(t, err)
	assert.Equal(t, stripeSubscriptionResult{ID: subID, Status: "active"}, got)
	got, err = subAction("next")
	require.NoError(t, err)
	assert.Equal(t, "past_due", got.(stripeSubscriptionResult).Status)
	got, err = subAction("retry")
	require.NoError(t, err)
	assert.Equal(t, "active", got.(stripeSubscriptionResult).Status)
	_, err = subAction("retry")
	assert.Equal(t, http.StatusConflict, status(err), "nothing to retry")
	got, err = subAction("next")
	require.NoError(t, err)
	assert.Equal(t, "active", got.(stripeSubscriptionResult).Status)
	got, err = subAction("cancel")
	require.NoError(t, err)
	assert.Equal(t, "canceled", got.(stripeSubscriptionResult).Status)
	_, err = subAction("cancel")
	assert.Equal(t, http.StatusConflict, status(err), "already canceled")
	_, err = subAction("next")
	assert.Equal(t, http.StatusConflict, status(err), "no next period once canceled")
	_, err = subAction("bogus")
	assert.Equal(t, http.StatusBadRequest, status(err))
	_, err = g.dispatch("stripe.subscription", []byte(`{"subscription":"sub_nope","action":"next"}`))
	assert.Equal(t, http.StatusNotFound, status(err))
	_, err = g.dispatch("stripe.subscription", []byte(`{"action":"next"}`))
	assert.Equal(t, http.StatusBadRequest, status(err), "subscription is required")
}

func mustParseRFC3339(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	require.NoError(t, err)
	return ts
}

// listOpenInvoices returns every invoice with status=open.
func listOpenInvoices(t *testing.T) []*stripe.Invoice {
	t.Helper()
	var out []*stripe.Invoice
	iter := invoice.List(&stripe.InvoiceListParams{Status: stripe.String("open")})
	for iter.Next() {
		out = append(out, iter.Invoice())
	}
	require.NoError(t, iter.Err())
	return out
}

// TestStripeMock_Subscription_CancelFlows: cancel_at_period_end through the
// API ends the subscription at the next cycle; DELETE ends it now; lists
// hide canceled subscriptions unless asked.
func TestStripeMock_Subscription_CancelFlows(t *testing.T) {
	mock, srv, _ := newFullStripeStack(t, "")
	sink := newWebhookSink(t, "whsec_test")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: sink.URL, Secret: "whsec_test"})
	withStripeBackend(t, srv.URL, func() {
		scheduled := createSubscription(t, mock, subscriptionSessionParams(1000, "month"))
		immediate := createSubscription(t, mock, subscriptionSessionParams(1000, "year")) // not due within the month the other one cycles
		pastCancel := createSubscription(t, mock, subscriptionSessionParams(1000, "year"))
		drainEvents(t, sink, 18)
		customer := func(id string) string {
			s, err := subscription.Get(id, nil)
			require.NoError(t, err)
			return s.Customer.ID
		}

		upd, err := subscription.Update(scheduled, &stripe.SubscriptionParams{CancelAtPeriodEnd: stripe.Bool(true)})
		require.NoError(t, err)
		assert.True(t, upd.CancelAtPeriodEnd)
		assert.Equal(t, stripe.SubscriptionStatusActive, upd.Status)
		assert.Equal(t, stripe.EventType("customer.subscription.updated"), sink.Wait(t, 3*time.Second).Type)

		status, err := mock.subscriptionAction(scheduled, "next")
		require.NoError(t, err)
		assert.Equal(t, "canceled", status)
		evt := sink.Wait(t, 3*time.Second)
		assert.Equal(t, stripe.EventType("customer.subscription.deleted"), evt.Type)
		assert.Len(t, listInvoices(t, scheduled), 1, "no renewal invoice on the way out")
		got, err := subscription.Get(scheduled, nil)
		require.NoError(t, err)
		assert.Equal(t, stripe.SubscriptionStatusCanceled, got.Status)
		assert.NotZero(t, got.EndedAt)
		assert.False(t, got.CancelAtPeriodEnd)

		// The yearly one was not due yet, so it is still active.
		got, err = subscription.Get(immediate, nil)
		require.NoError(t, err)
		assert.Equal(t, stripe.SubscriptionStatusActive, got.Status)

		got, err = subscription.Cancel(immediate, nil)
		require.NoError(t, err)
		assert.Equal(t, stripe.SubscriptionStatusCanceled, got.Status)
		assert.Equal(t, got.CanceledAt, got.EndedAt)
		assert.Equal(t, stripe.EventType("customer.subscription.deleted"), sink.Wait(t, 3*time.Second).Type)
		var se *stripe.Error
		_, err = subscription.Cancel(immediate, nil)
		require.ErrorAs(t, err, &se)
		_, err = subscription.Update(immediate, &stripe.SubscriptionParams{CancelAtPeriodEnd: stripe.Bool(false)})
		require.ErrorAs(t, err, &se)

		// A cancel_at not in the future cancels immediately, as on Stripe.
		got, err = subscription.Update(pastCancel, &stripe.SubscriptionParams{CancelAt: stripe.Int64(mock.now().Unix())})
		require.NoError(t, err)
		assert.Equal(t, stripe.SubscriptionStatusCanceled, got.Status)
		assert.Equal(t, stripe.EventType("customer.subscription.deleted"), sink.Wait(t, 3*time.Second).Type)

		cus := customer(immediate)
		iter := subscription.List(&stripe.SubscriptionListParams{Customer: stripe.String(cus)})
		assert.False(t, iter.Next(), "canceled subscriptions are hidden by default")
		require.NoError(t, iter.Err())
		iter = subscription.List(&stripe.SubscriptionListParams{Customer: stripe.String(cus), Status: stripe.String("all")})
		require.True(t, iter.Next())
		assert.Equal(t, immediate, iter.Subscription().ID)
	})
}

// TestStripeMock_Dashboard_ClockAndSubscriptions: the control page shows
// the clock and subscription rows, and its forms drive them.
func TestStripeMock_Dashboard_ClockAndSubscriptions(t *testing.T) {
	mock, srv, _ := newFullStripeStack(t, "")
	withStripeBackend(t, srv.URL, func() {
		subID := createSubscription(t, mock, subscriptionSessionParams(1000, "month"))
		body := getDashboard(t, mock, http.StatusOK)
		assert.Contains(t, body, "Mock clock")
		assert.Contains(t, body, shortID(subID))
		assert.Contains(t, body, "MOCK-0001")
		assert.Contains(t, body, "Fail next renewal")

		resp := postDashboardForm(t, mock, "/__hamr/stripe/clock", url.Values{"by": {"1m"}})
		assert.Equal(t, http.StatusSeeOther, resp.StatusCode)
		assert.Len(t, listInvoices(t, subID), 2)
		body = getDashboard(t, mock, http.StatusOK)
		assert.Contains(t, body, "ahead")

		resp = postDashboardForm(t, mock, "/__hamr/stripe/subscription", url.Values{"subscription": {subID}, "action": {"cancel"}})
		assert.Equal(t, http.StatusSeeOther, resp.StatusCode)
		s, err := subscription.Get(subID, nil)
		require.NoError(t, err)
		assert.Equal(t, stripe.SubscriptionStatusCanceled, s.Status)

		resp = postDashboardForm(t, mock, "/__hamr/stripe/clock", url.Values{"to": {"2000-01-01T00:00:00Z"}})
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "backwards is refused")
	})
}

// --- helpers ---

// subscriptionSessionParams is a one-item gbp subscription session.
func subscriptionSessionParams(unitAmount int64, interval string) *stripe.CheckoutSessionParams {
	p := sessionParams(unitAmount)
	p.Mode = stripe.String(string(stripe.CheckoutSessionModeSubscription))
	p.LineItems[0].PriceData.Recurring = &stripe.CheckoutSessionLineItemPriceDataRecurringParams{Interval: stripe.String(interval)}
	return p
}

// createSubscription creates the session through stripe-go and pays it,
// returning the subscription id. The backend must already point at the mock.
func createSubscription(t *testing.T, mock *StripeMock, p *stripe.CheckoutSessionParams) string {
	t.Helper()
	sess, err := session.New(p)
	require.NoError(t, err)
	_, _, err = mock.completeCheckout(sess.ID, "paid", "")
	require.NoError(t, err)
	mock.mu.RLock()
	defer mock.mu.RUnlock()
	return mock.sessions[sess.ID].SubscriptionID
}

// listInvoices returns a subscription's invoices newest first.
func listInvoices(t *testing.T, subID string) []*stripe.Invoice {
	t.Helper()
	var out []*stripe.Invoice
	iter := invoice.List(&stripe.InvoiceListParams{Subscription: stripe.String(subID)})
	for iter.Next() {
		out = append(out, iter.Invoice())
	}
	require.NoError(t, iter.Err())
	return out
}

// drainEvents waits for n webhook deliveries and discards them.
func drainEvents(t *testing.T, sink *webhookSink, n int) {
	t.Helper()
	for range n {
		sink.Wait(t, 3*time.Second)
	}
}

func shortID(id string) string {
	return dashboardFuncs["shortID"].(func(string) string)(id)
}

// postDashboardForm posts a same-origin form to a dashboard route without
// following the redirect.
func postDashboardForm(t *testing.T, mock *StripeMock, path string, form url.Values) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, mock.baseURL+path, strings.NewReader(form.Encode()))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	require.NoError(t, err)
	resp.Body.Close() //nolint:errcheck
	return resp
}

// TestStripeMock_Clock_ParseAndCap pins the input rules: by= is matched
// whole ("1m30s" is a Go duration, not one month), months clamp like
// subscription periods, to= must be RFC 3339 or all digits, and one advance
// is capped so a century of daily renewals cannot run under the lock.
func TestStripeMock_Clock_ParseAndCap(t *testing.T) {
	mock, _, _ := newFullStripeStack(t, "")
	now := mock.now()

	// parseClockTarget reads now() itself, so a second may tick between
	// our read and its read; compare with a tolerance.
	got, err := mock.parseClockTarget("1m30s", "")
	require.NoError(t, err)
	assert.WithinDuration(t, now.Add(90*time.Second), got, 2*time.Second)
	got, err = mock.parseClockTarget("2w", "")
	require.NoError(t, err)
	assert.WithinDuration(t, now.AddDate(0, 0, 14), got, 2*time.Second)
	got, err = mock.parseClockTarget("1m", "")
	require.NoError(t, err)
	assert.WithinDuration(t, addMonthsClamped(now, 1), got, 2*time.Second)
	for _, bad := range []string{"10min", "0d", "abc", "-1d"} {
		_, err = mock.parseClockTarget(bad, "")
		assert.Error(t, err, bad)
	}
	_, err = mock.parseClockTarget("", "2027-01-01")
	assert.Error(t, err, "a bare date is not unix seconds")
	got, err = mock.parseClockTarget("", "1900000000")
	require.NoError(t, err)
	assert.Equal(t, time.Unix(1900000000, 0), got)

	_, err = mock.advanceClock(now.AddDate(6, 0, 0))
	require.Error(t, err, "one advance is capped at five years")
	cycled, err := mock.advanceClock(time.Time{})
	require.NoError(t, err, "zero target is now under the lock: only runs what is already due")
	assert.Empty(t, cycled)
}

// TestStripeMock_Clock_OverdueNextPeriod: once real time has passed a period
// end, nextPeriodTarget returns the zero time and advanceClock reads it as
// now under its lock, so a wall-clock tick between the two calls cannot turn
// "Next period" into a backwards move.
func TestStripeMock_Clock_OverdueNextPeriod(t *testing.T) {
	mock, srv, _ := newFullStripeStack(t, "")
	withStripeBackend(t, srv.URL, func() {
		subID := createSubscription(t, mock, subscriptionSessionParams(1500, "month"))
		mock.mu.Lock()
		mock.subscriptions[subID].CurrentPeriodEnd = mock.now().Add(-time.Hour)
		mock.mu.Unlock()
		before := mock.now()

		to, err := mock.nextPeriodTarget(subID)
		require.NoError(t, err)
		assert.True(t, to.IsZero(), "overdue target is the zero time, not a now() read outside the lock")

		cycled, err := mock.advanceClock(to)
		require.NoError(t, err)
		assert.Equal(t, []string{subID}, cycled)
		assert.False(t, mock.now().Before(before), "clock did not move backwards")
	})
}
