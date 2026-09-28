package devserver

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Subscriptions are created by completing a Checkout Session in
// mode=subscription; the mock has no POST /v1/subscriptions. From then on
// the mock clock drives them (see stripemock_clock.go): at each period end a
// renewal invoice is paid, or fails when "fail next renewal" is set, or the
// subscription ends when cancellation was scheduled. Every object here is
// stamped with the mock clock's time at the moment it would have happened.
//
// ponytail: no trials, proration, quantity changes or plan swaps. The
// subscription keeps the items it was created with.

// stripeSubscription is a Stripe Subscription. Status is one of
// active | past_due | canceled.
type stripeSubscription struct {
	ID                 string                      `json:"id"`
	CustomerID         string                      `json:"customer_id"`
	SessionID          string                      `json:"session_id,omitempty"` // the checkout session that created it
	Status             string                      `json:"status"`
	Currency           string                      `json:"currency"`
	Items              []stripeSubscriptionItem    `json:"items"`
	Cycles             int                         `json:"cycles"` // completed billing periods; period bounds derive from the anchor
	BillingCycleAnchor time.Time                   `json:"billing_cycle_anchor"`
	CurrentPeriodStart time.Time                   `json:"current_period_start"`
	CurrentPeriodEnd   time.Time                   `json:"current_period_end"`
	CancelAtPeriodEnd  bool                        `json:"cancel_at_period_end,omitempty"`
	CancelAt           time.Time                   `json:"cancel_at"`   // zero = none
	CanceledAt         time.Time                   `json:"canceled_at"` // zero = not canceled
	EndedAt            time.Time                   `json:"ended_at"`
	LatestInvoiceID    string                      `json:"latest_invoice_id,omitempty"`
	Discount           *stripeSubscriptionDiscount `json:"discount,omitempty"`
	FailNextRenewal    bool                        `json:"fail_next_renewal,omitempty"` // dashboard toggle: the next cycle's payment fails
	Description        string                      `json:"description,omitempty"`
	StartDate          time.Time                   `json:"start_date"`
	Created            time.Time                   `json:"created"`
	Metadata           map[string]string           `json:"metadata,omitempty"`
}

// stripeSubscriptionItem is one recurring price on the subscription. The
// Price and Product ids are synthesised from the checkout line item.
type stripeSubscriptionItem struct {
	ID            string `json:"id"`
	PriceID       string `json:"price_id"`
	ProductID     string `json:"product_id"`
	ProductName   string `json:"product_name"`
	UnitAmount    int64  `json:"unit_amount"`
	Quantity      int64  `json:"quantity"`
	Interval      string `json:"interval"` // day | week | month | year
	IntervalCount int64  `json:"interval_count"`
}

// stripeSubscriptionDiscount is the coupon snapshotted onto the subscription
// so a later coupon change or delete does not rewrite history. End is set
// for repeating coupons; a once coupon is removed after the first invoice.
type stripeSubscriptionDiscount struct {
	ID              string    `json:"id"`
	CouponID        string    `json:"coupon_id"`
	PromotionCodeID string    `json:"promotion_code_id,omitempty"`
	Duration        string    `json:"duration"`
	PercentOff      float64   `json:"percent_off,omitempty"`
	AmountOff       int64     `json:"amount_off,omitempty"`
	Start           time.Time `json:"start"`
	End             time.Time `json:"end"` // zero = no end
}

// stripeInvoice is a subscription invoice: paid, open after a failed
// renewal payment, or void once its subscription was canceled while open.
type stripeInvoice struct {
	ID              string              `json:"id"`
	Number          string              `json:"number"`
	CustomerID      string              `json:"customer_id"`
	SubscriptionID  string              `json:"subscription_id"`
	Status          string              `json:"status"`         // paid | open | void
	BillingReason   string              `json:"billing_reason"` // subscription_create | subscription_cycle
	Currency        string              `json:"currency"`
	Subtotal        int64               `json:"subtotal"`
	Discount        int64               `json:"discount,omitempty"`
	DiscountID      string              `json:"discount_id,omitempty"`
	PaymentIntentID string              `json:"payment_intent_id,omitempty"` // empty for a zero-total invoice
	PeriodStart     time.Time           `json:"period_start"`
	PeriodEnd       time.Time           `json:"period_end"`
	Lines           []stripeInvoiceLine `json:"lines"`
	AttemptCount    int                 `json:"attempt_count"`
	Created         time.Time           `json:"created"`
	PaidAt          time.Time           `json:"paid_at"`
	VoidedAt        time.Time           `json:"voided_at"`
	Metadata        map[string]string   `json:"metadata,omitempty"`
}

type stripeInvoiceLine struct {
	ID                 string    `json:"id"`
	Description        string    `json:"description"`
	Amount             int64     `json:"amount"`
	UnitAmount         int64     `json:"unit_amount"`
	Quantity           int64     `json:"quantity"`
	PriceID            string    `json:"price_id"`
	ProductID          string    `json:"product_id"`
	SubscriptionItemID string    `json:"subscription_item_id"`
	PeriodStart        time.Time `json:"period_start"`
	PeriodEnd          time.Time `json:"period_end"`
}

// Total is what the invoice charges.
func (in *stripeInvoice) Total() int64 { return in.Subtotal - in.Discount }

// perPeriod is the undiscounted amount of one billing period.
func (s *stripeSubscription) perPeriod() int64 {
	var total int64
	for _, it := range s.Items {
		total += it.UnitAmount * it.Quantity
	}
	return total
}

// periodEnd is the end of the n-th period (0-based) counted from the anchor,
// with Stripe's month-end clamping: a Jan 31 anchor bills Feb 28, Mar 31.
func (s *stripeSubscription) periodEnd(n int) time.Time {
	it := s.Items[0]
	count := int(it.IntervalCount) * (n + 1)
	a := s.BillingCycleAnchor
	switch it.Interval {
	case "day":
		return a.AddDate(0, 0, count)
	case "week":
		return a.AddDate(0, 0, 7*count)
	case "year":
		return addMonthsClamped(a, 12*count)
	default:
		return addMonthsClamped(a, count)
	}
}

// addMonthsClamped adds months keeping the day of month, clamped to the last
// day of the target month.
func addMonthsClamped(t time.Time, months int) time.Time {
	y, m, d := t.Date()
	first := time.Date(y, m+time.Month(months), 1, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
	last := first.AddDate(0, 1, -1).Day()
	return first.AddDate(0, 0, min(d, last)-1)
}

// dueAt is when the clock next has to act on the subscription: the period
// end, or an earlier scheduled cancel_at.
func (s *stripeSubscription) dueAt() time.Time {
	if !s.CancelAt.IsZero() && s.CancelAt.Before(s.CurrentPeriodEnd) {
		return s.CancelAt
	}
	return s.CurrentPeriodEnd
}

// discountFor is the discount on an invoice for the period starting at `at`,
// 0 when the snapshotted coupon no longer applies.
func (s *stripeSubscription) discountFor(subtotal int64, at time.Time) int64 {
	d := s.Discount
	if d == nil || (!d.End.IsZero() && !at.Before(d.End)) {
		return 0
	}
	// Currency was checked when the session applied the coupon.
	amt, _ := (&stripeCoupon{PercentOff: d.PercentOff, AmountOff: d.AmountOff, Currency: s.Currency}).discountFor(subtotal, s.Currency)
	return amt
}

// registerSubscriptionRoutes mounts /v1/subscriptions and /v1/invoices.
func (m *StripeMock) registerSubscriptionRoutes(mux stripeRouter) {
	mux.HandleFunc("/v1/subscriptions", m.handleSubscriptions)
	mux.HandleFunc("/v1/subscriptions/", m.handleSubscriptionByID)
	mux.HandleFunc("/v1/invoices", m.handleInvoices)
	mux.HandleFunc("/v1/invoices/", m.handleInvoiceByID)
}

// handleSubscriptions serves GET (list). POST is refused with a pointer at
// Checkout, which is how the mock creates subscriptions.
func (m *StripeMock) handleSubscriptions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		m.listSubscriptions(w, r)
	case http.MethodPost:
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error",
			"POST /v1/subscriptions is not mocked: create subscriptions through a Checkout Session with mode=subscription")
	default:
		writeStripeError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
	}
}

// handleSubscriptionByID serves GET (retrieve), POST (update: cancel_at_period_end,
// cancel_at, metadata, description) and DELETE (cancel now).
func (m *StripeMock) handleSubscriptionByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/subscriptions/")
	if id == "" || strings.Contains(id, "/") {
		writeStripeError(w, http.StatusNotFound, "invalid_request_error", "subscription id required")
		return
	}
	var p map[string]any
	switch r.Method {
	case http.MethodGet, http.MethodDelete:
	case http.MethodPost:
		var ok bool
		if p, ok = readStripeForm(w, r); !ok {
			return
		}
	default:
		writeStripeError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}

	m.mu.Lock()
	sub, ok := m.subscriptions[id]
	if !ok {
		m.mu.Unlock()
		writeStripeError(w, http.StatusNotFound, "invalid_request_error", fmt.Sprintf("No such subscription: '%s'", id))
		return
	}
	var fires []webhookFire
	switch r.Method {
	case http.MethodDelete:
		if sub.Status == "canceled" {
			m.mu.Unlock()
			writeStripeErrorCode(w, http.StatusBadRequest, "invalid_request_error", "resource_missing",
				fmt.Sprintf("No such subscription: '%s'", id)) // Stripe answers this for an already-canceled subscription
			return
		}
		fires = m.cancelSubscriptionLocked(sub, m.now())
	case http.MethodPost:
		if sub.Status == "canceled" {
			m.mu.Unlock()
			writeStripeError(w, http.StatusBadRequest, "invalid_request_error", "A canceled subscription can no longer be updated.")
			return
		}
		// Validate everything before touching the subscription, so a 400
		// leaves it exactly as it was.
		cancelAt, ok := getInt64(p, "cancel_at")
		if s := getString(p, "cancel_at"); s != "" && (!ok || cancelAt <= 0) {
			m.mu.Unlock()
			writeStripeError(w, http.StatusBadRequest, "invalid_request_error", "cancel_at must be a unix timestamp")
			return
		}
		if s := getString(p, "cancel_at_period_end"); s != "" {
			sub.CancelAtPeriodEnd = s == "true"
		}
		if getString(p, "cancel_at") != "" {
			sub.CancelAt = time.Unix(cancelAt, 0)
		} else if _, present := p["cancel_at"]; present {
			sub.CancelAt = time.Time{} // cancel_at= clears it
		}
		if md := stringMap(p, "metadata"); md != nil {
			sub.Metadata = md
		}
		if s := getString(p, "description"); s != "" {
			sub.Description = s
		}
		if !sub.CancelAt.IsZero() && !sub.CancelAt.After(m.now()) {
			fires = m.cancelSubscriptionLocked(sub, m.now()) // a cancel_at not in the future cancels now, as on Stripe
		} else {
			fires = []webhookFire{{eventType: "customer.subscription.updated", object: m.serializeSubscription(sub)}}
		}
	}
	if r.Method != http.MethodGet {
		m.persist()
	}
	out := m.serializeSubscription(sub)
	m.mu.Unlock()

	if len(fires) > 0 {
		m.fireEventsAsync(fires, "subscription", id)
	}
	writeStripeJSON(w, http.StatusOK, out)
}

// listSubscriptions serves GET /v1/subscriptions with the customer, status
// and price filters. Like Stripe, canceled subscriptions are left out unless
// status=canceled or status=all.
func (m *StripeMock) listSubscriptions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	customer, status, price := q.Get("customer"), q.Get("status"), q.Get("price")

	m.mu.RLock()
	var all []map[string]any
	subs := make([]*stripeSubscription, 0, len(m.subscriptions))
	for _, s := range m.subscriptions {
		subs = append(subs, s)
	}
	sortNewestFirst(subs, func(s *stripeSubscription) (time.Time, string) { return s.Created, s.ID })
	for _, s := range subs {
		if customer != "" && s.CustomerID != customer {
			continue
		}
		switch status {
		case "", "all":
			if status == "" && s.Status == "canceled" {
				continue
			}
		default:
			if s.Status != status {
				continue
			}
		}
		if price != "" && !s.hasPrice(price) {
			continue
		}
		all = append(all, m.serializeSubscription(s))
	}
	m.mu.RUnlock()

	page, more := pageAfter(all, q, func(o map[string]any) string { return o["id"].(string) })
	writeStripeJSON(w, http.StatusOK, map[string]any{
		"object": "list", "url": "/v1/subscriptions", "has_more": more, "data": page,
	})
}

func (s *stripeSubscription) hasPrice(priceID string) bool {
	for _, it := range s.Items {
		if it.PriceID == priceID {
			return true
		}
	}
	return false
}

// handleInvoices serves GET /v1/invoices with subscription, customer and
// status filters, newest first.
func (m *StripeMock) handleInvoices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeStripeError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	q := r.URL.Query()
	subID, customer, status := q.Get("subscription"), q.Get("customer"), q.Get("status")

	m.mu.RLock()
	invs := make([]*stripeInvoice, 0, len(m.invoices))
	for _, in := range m.invoices {
		if (subID != "" && in.SubscriptionID != subID) || (customer != "" && in.CustomerID != customer) || (status != "" && in.Status != status) {
			continue
		}
		invs = append(invs, in)
	}
	sortNewestFirst(invs, func(in *stripeInvoice) (time.Time, string) { return in.Created, in.ID })
	page, more := pageAfter(invs, q, func(in *stripeInvoice) string { return in.ID })
	out := make([]map[string]any, len(page))
	for i, in := range page {
		out[i] = m.serializeInvoice(in)
	}
	m.mu.RUnlock()

	writeStripeJSON(w, http.StatusOK, map[string]any{
		"object": "list", "url": "/v1/invoices", "has_more": more, "data": out,
	})
}

// handleInvoiceByID serves GET /v1/invoices/{id}.
func (m *StripeMock) handleInvoiceByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/invoices/")
	if id == "" || strings.Contains(id, "/") {
		writeStripeError(w, http.StatusNotFound, "invalid_request_error", "invoice id required")
		return
	}
	if r.Method != http.MethodGet {
		writeStripeError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	m.mu.RLock()
	in, ok := m.invoices[id]
	var out map[string]any
	if ok {
		out = m.serializeInvoice(in)
	}
	m.mu.RUnlock()
	if !ok {
		writeStripeError(w, http.StatusNotFound, "invalid_request_error", fmt.Sprintf("No such invoice: '%s'", id))
		return
	}
	writeStripeJSON(w, http.StatusOK, out)
}

// --- lifecycle (caller holds m.mu write) ---

// createSubscriptionLocked materialises the subscription and its first,
// paid invoice for a completed mode=subscription session. The caller
// creates the PaymentIntent piID and its Charge (empty for a zero total);
// the invoice points at it, the session does not.
func (m *StripeMock) createSubscriptionLocked(sess *stripeSession, piID string, at time.Time) (*stripeSubscription, *stripeInvoice) {
	sub := &stripeSubscription{
		ID:                 "sub_test_" + randomHex(24),
		CustomerID:         sess.CustomerID,
		SessionID:          sess.ID,
		Status:             "active",
		Currency:           sess.Currency,
		BillingCycleAnchor: at,
		StartDate:          at,
		Created:            at,
		Metadata:           cloneStringMap(sess.SubscriptionMetadata),
	}
	for _, li := range sess.LineItems {
		sub.Items = append(sub.Items, stripeSubscriptionItem{
			ID:            "si_test_" + randomHex(16),
			PriceID:       "price_test_" + randomHex(16),
			ProductID:     "prod_test_" + randomHex(16),
			ProductName:   li.Name,
			UnitAmount:    li.UnitAmount,
			Quantity:      li.Quantity,
			Interval:      li.Interval,
			IntervalCount: max(li.IntervalCount, 1),
		})
	}
	sub.CurrentPeriodStart = at
	sub.CurrentPeriodEnd = sub.periodEnd(0)
	if len(sess.Discounts) > 0 {
		d := sess.Discounts[0]
		disc := &stripeSubscriptionDiscount{
			ID: "di_test_" + randomHex(16), CouponID: d.CouponID, PromotionCodeID: d.PromotionCodeID,
			Duration: "once", Start: at,
		}
		if c, ok := m.coupons[d.CouponID]; ok {
			disc.Duration, disc.PercentOff, disc.AmountOff = c.Duration, c.PercentOff, c.AmountOff
			if c.Duration == "repeating" {
				disc.End = addMonthsClamped(at, int(c.DurationInMonths))
			}
		} else {
			// Coupon deleted between create and pay: the session already
			// snapshotted the amount, so treat it as a once-only amount.
			disc.AmountOff = sess.AmountDiscount
		}
		sub.Discount = disc
	}
	m.subscriptions[sub.ID] = sub

	inv := m.newInvoiceLocked(sub, "subscription_create", at)
	inv.Discount = sess.AmountDiscount // the session's snapshot, so what the buyer saw is what is billed
	inv.Status, inv.PaidAt, inv.AttemptCount = "paid", at, 1
	inv.PaymentIntentID = piID
	m.invoices[inv.ID] = inv
	sub.LatestInvoiceID = inv.ID
	if sub.Discount != nil && sub.Discount.Duration == "once" {
		sub.Discount = nil // Stripe removes a once coupon after its single invoice
	}
	return sub, inv
}

// newInvoiceLocked builds the invoice for the subscription's current period.
// Not stored; the caller decides paid/open and stores it.
func (m *StripeMock) newInvoiceLocked(sub *stripeSubscription, reason string, at time.Time) *stripeInvoice {
	inv := &stripeInvoice{
		ID:             "in_test_" + randomHex(24),
		Number:         fmt.Sprintf("MOCK-%04d", len(m.invoices)+1),
		CustomerID:     sub.CustomerID,
		SubscriptionID: sub.ID,
		BillingReason:  reason,
		Currency:       sub.Currency,
		Subtotal:       sub.perPeriod(),
		PeriodStart:    sub.CurrentPeriodStart,
		PeriodEnd:      sub.CurrentPeriodEnd,
		Created:        at,
	}
	inv.Discount = sub.discountFor(inv.Subtotal, at)
	if sub.Discount != nil && inv.Discount > 0 {
		inv.DiscountID = sub.Discount.ID
	}
	for _, it := range sub.Items {
		inv.Lines = append(inv.Lines, stripeInvoiceLine{
			ID:                 "il_test_" + randomHex(16),
			Description:        fmt.Sprintf("%d × %s (at %s / %s)", it.Quantity, it.ProductName, formatStripeAmount(it.UnitAmount, sub.Currency), it.Interval),
			Amount:             it.UnitAmount * it.Quantity,
			UnitAmount:         it.UnitAmount,
			Quantity:           it.Quantity,
			PriceID:            it.PriceID,
			ProductID:          it.ProductID,
			SubscriptionItemID: it.ID,
			PeriodStart:        sub.CurrentPeriodStart,
			PeriodEnd:          sub.CurrentPeriodEnd,
		})
	}
	return inv
}

// cycleSubscriptionLocked is what the clock does when a subscription's due
// time arrives: end it if a cancellation was scheduled or the last renewal
// was never paid, else open the next period and try to collect it.
func (m *StripeMock) cycleSubscriptionLocked(sub *stripeSubscription, at time.Time) []webhookFire {
	if sub.CancelAtPeriodEnd || (!sub.CancelAt.IsZero() && !sub.CancelAt.After(at)) || sub.Status == "past_due" {
		// past_due at the next period end means dunning ran out; Stripe's
		// default is to cancel.
		return m.cancelSubscriptionLocked(sub, at)
	}
	if sub.Discount != nil && !sub.Discount.End.IsZero() && !at.Before(sub.Discount.End) {
		sub.Discount = nil
	}
	sub.Cycles++
	sub.CurrentPeriodStart = at
	sub.CurrentPeriodEnd = sub.periodEnd(sub.Cycles)
	inv := m.newInvoiceLocked(sub, "subscription_cycle", at)
	m.invoices[inv.ID] = inv
	sub.LatestInvoiceID = inv.ID

	// A zero-total invoice (100% coupon) has nothing to fail, as on Stripe.
	failNext := sub.FailNextRenewal && inv.Total() > 0
	sub.FailNextRenewal = false
	if failNext {
		sub.Status = "past_due"
		inv.Status, inv.AttemptCount = "open", 1
		pi := m.newInvoicePaymentIntentLocked(inv, at)
		pi.Status, pi.Failed = "requires_payment_method", true
		return []webhookFire{
			{eventType: "invoice.payment_failed", object: m.serializeInvoice(inv)},
			{eventType: "customer.subscription.updated", object: m.serializeSubscription(sub)},
			{eventType: "payment_intent.payment_failed", object: m.serializePaymentIntent(pi, nil)},
		}
	}
	fires := m.payInvoiceLocked(inv, at)
	return append([]webhookFire{
		{eventType: "invoice.paid", object: m.serializeInvoice(inv)},
		{eventType: "invoice.payment_succeeded", object: m.serializeInvoice(inv)},
		{eventType: "customer.subscription.updated", object: m.serializeSubscription(sub)},
	}, fires...)
}

// retrySubscriptionLocked pays the open invoice of a past_due subscription,
// the mock's stand-in for a successful Stripe retry.
func (m *StripeMock) retrySubscriptionLocked(sub *stripeSubscription, at time.Time) ([]webhookFire, error) {
	inv, ok := m.invoices[sub.LatestInvoiceID]
	if sub.Status != "past_due" || !ok || inv.Status != "open" {
		return nil, stripeErr(http.StatusConflict, "subscription %s is %s, nothing to retry", sub.ID, sub.Status)
	}
	inv.AttemptCount++
	sub.Status = "active"
	fires := m.payInvoiceLocked(inv, at)
	return append([]webhookFire{
		{eventType: "invoice.paid", object: m.serializeInvoice(inv)},
		{eventType: "invoice.payment_succeeded", object: m.serializeInvoice(inv)},
		{eventType: "customer.subscription.updated", object: m.serializeSubscription(sub)},
	}, fires...), nil
}

// cancelSubscriptionLocked ends the subscription at `at` and fires
// customer.subscription.deleted. Any open invoice it still has is voided
// (invoice.voided), as Stripe does, so it stops showing up as collectable.
func (m *StripeMock) cancelSubscriptionLocked(sub *stripeSubscription, at time.Time) []webhookFire {
	sub.Status = "canceled"
	sub.CanceledAt, sub.EndedAt = at, at
	sub.CancelAtPeriodEnd = false
	fires := []webhookFire{{eventType: "customer.subscription.deleted", object: m.serializeSubscription(sub)}}
	for _, inv := range m.invoices {
		if inv.SubscriptionID == sub.ID && inv.Status == "open" {
			inv.Status, inv.VoidedAt = "void", at
			fires = append(fires, webhookFire{eventType: "invoice.voided", object: m.serializeInvoice(inv)})
		}
	}
	return fires
}

// payInvoiceLocked marks an invoice paid and settles its PaymentIntent and
// Charge (creating them when the invoice has none yet), returning the
// payment events. A zero-total invoice is paid with no payment.
func (m *StripeMock) payInvoiceLocked(inv *stripeInvoice, at time.Time) []webhookFire {
	inv.Status, inv.PaidAt = "paid", at
	if inv.AttemptCount == 0 {
		inv.AttemptCount = 1
	}
	if inv.Total() == 0 {
		return nil
	}
	pi, ok := m.paymentIntents[inv.PaymentIntentID]
	if !ok {
		pi = m.newInvoicePaymentIntentLocked(inv, at)
	}
	ch := &stripeCharge{
		ID:              "ch_test_" + randomHex(24),
		Amount:          pi.Amount,
		AmountCaptured:  pi.Amount,
		Currency:        pi.Currency,
		Status:          "succeeded",
		Paid:            true,
		Captured:        true,
		PaymentIntentID: pi.ID,
		PaymentMethod:   "pm_card_visa",
		Description:     pi.Description,
		Customer:        pi.Customer,
		Created:         at,
		Metadata:        cloneStringMap(pi.Metadata),
	}
	m.charges[ch.ID] = ch
	pi.Status, pi.Failed, pi.LatestChargeID, pi.AmountReceived = "succeeded", false, ch.ID, pi.Amount
	return []webhookFire{
		{eventType: "payment_intent.succeeded", object: m.serializePaymentIntent(pi, ch)},
		{eventType: "charge.succeeded", object: m.serializeCharge(ch)},
	}
}

// newInvoicePaymentIntentLocked creates and stores the PaymentIntent that
// collects an invoice, in requires_payment_method until paid.
func (m *StripeMock) newInvoicePaymentIntentLocked(inv *stripeInvoice, at time.Time) *stripePaymentIntent {
	pi := &stripePaymentIntent{
		ID:                 "pi_test_" + randomHex(24),
		Amount:             inv.Total(),
		Currency:           inv.Currency,
		Status:             "requires_payment_method",
		CaptureMethod:      "automatic",
		ConfirmationMethod: "automatic",
		Customer:           inv.CustomerID,
		PaymentMethod:      "pm_card_visa",
		Description:        "Invoice " + inv.Number,
		Created:            at,
	}
	pi.ClientSecret = pi.ID + "_secret_" + randomHex(12)
	m.paymentIntents[pi.ID] = pi
	inv.PaymentIntentID = pi.ID
	return pi
}

// --- serialization (caller holds m.mu or passes clones) ---

// serializeSubscription renders the subscription object stripe-go decodes.
// current_period_* live on the items on this API version.
func (m *StripeMock) serializeSubscription(s *stripeSubscription) map[string]any {
	items := make([]map[string]any, len(s.Items))
	for i, it := range s.Items {
		items[i] = map[string]any{
			"id":                   it.ID,
			"object":               "subscription_item",
			"created":              s.Created.Unix(),
			"current_period_start": s.CurrentPeriodStart.Unix(),
			"current_period_end":   s.CurrentPeriodEnd.Unix(),
			"subscription":         s.ID,
			"quantity":             it.Quantity,
			"metadata":             map[string]string{},
			"discounts":            []any{},
			"price": map[string]any{
				"id":                  it.PriceID,
				"object":              "price",
				"active":              true,
				"billing_scheme":      "per_unit",
				"created":             s.Created.Unix(),
				"currency":            s.Currency,
				"livemode":            false,
				"metadata":            map[string]string{},
				"nickname":            nullableString(it.ProductName),
				"product":             it.ProductID,
				"type":                "recurring",
				"unit_amount":         it.UnitAmount,
				"unit_amount_decimal": fmt.Sprint(it.UnitAmount),
				"recurring": map[string]any{
					"interval": it.Interval, "interval_count": it.IntervalCount,
					"usage_type": "licensed", "meter": nil, "trial_period_days": nil,
				},
			},
		}
	}
	out := map[string]any{
		"id":                     s.ID,
		"object":                 "subscription",
		"customer":               s.CustomerID,
		"status":                 s.Status,
		"currency":               s.Currency,
		"created":                s.Created.Unix(),
		"start_date":             s.StartDate.Unix(),
		"billing_cycle_anchor":   s.BillingCycleAnchor.Unix(),
		"cancel_at":              nullableUnix(s.CancelAt),
		"cancel_at_period_end":   s.CancelAtPeriodEnd,
		"canceled_at":            nullableUnix(s.CanceledAt),
		"ended_at":               nullableUnix(s.EndedAt),
		"collection_method":      "charge_automatically",
		"default_payment_method": "pm_card_visa",
		"description":            nullableString(s.Description),
		"latest_invoice":         nullableString(s.LatestInvoiceID),
		"livemode":               false,
		"trial_start":            nil,
		"trial_end":              nil,
		"items": map[string]any{
			"object": "list", "url": "/v1/subscription_items?subscription=" + s.ID,
			"has_more": false, "total_count": len(items), "data": items,
		},
		"discounts": []any{},
	}
	if s.Discount != nil {
		out["discounts"] = []any{m.serializeDiscount(s.Discount, s)}
	}
	if s.Metadata != nil {
		out["metadata"] = s.Metadata
	} else {
		out["metadata"] = map[string]string{}
	}
	return out
}

// serializeDiscount renders a Discount object. On this API version the
// coupon sits under source; it is inlined when it still exists, else its id.
func (m *StripeMock) serializeDiscount(d *stripeSubscriptionDiscount, s *stripeSubscription) map[string]any {
	var coupon any = d.CouponID
	if c, ok := m.coupons[d.CouponID]; ok {
		coupon = m.serializeCoupon(c)
	}
	return map[string]any{
		"id":               d.ID,
		"object":           "discount",
		"source":           map[string]any{"type": "coupon", "coupon": coupon},
		"promotion_code":   nullableString(d.PromotionCodeID),
		"customer":         s.CustomerID,
		"subscription":     s.ID,
		"start":            d.Start.Unix(),
		"end":              nullableUnix(d.End),
		"checkout_session": nullableString(s.SessionID),
		"invoice":          nil,
		"invoice_item":     nil,
	}
}

// serializeInvoice renders the invoice object on this API version: the
// subscription hangs off parent.subscription_details and the PaymentIntent
// off payments[].
func (m *StripeMock) serializeInvoice(in *stripeInvoice) map[string]any {
	lines := make([]map[string]any, len(in.Lines))
	for i, l := range in.Lines {
		lines[i] = map[string]any{
			"id":           l.ID,
			"object":       "line_item",
			"amount":       l.Amount,
			"currency":     in.Currency,
			"description":  l.Description,
			"discountable": true,
			"invoice":      in.ID,
			"livemode":     false,
			"metadata":     map[string]string{},
			"period":       map[string]any{"start": l.PeriodStart.Unix(), "end": l.PeriodEnd.Unix()},
			"quantity":     l.Quantity,
			"pricing": map[string]any{
				"type":                "price_details",
				"price_details":       map[string]any{"price": l.PriceID, "product": l.ProductID},
				"unit_amount_decimal": fmt.Sprint(l.UnitAmount),
			},
			"parent": map[string]any{
				"type": "subscription_item_details",
				"subscription_item_details": map[string]any{
					"subscription": in.SubscriptionID, "subscription_item": l.SubscriptionItemID,
					"proration": false, "invoice_item": nil,
				},
				"invoice_item_details": nil,
			},
		}
	}
	paid := in.Status == "paid"
	amountPaid := int64(0)
	if paid {
		amountPaid = in.Total()
	}
	var payments []map[string]any
	if in.PaymentIntentID != "" {
		status := "open"
		switch in.Status {
		case "paid":
			status = "paid"
		case "void":
			status = "canceled"
		}
		payments = append(payments, map[string]any{
			"id":               "inpay_" + strings.TrimPrefix(in.ID, "in_"),
			"object":           "invoice_payment",
			"amount_paid":      amountPaid,
			"amount_requested": in.Total(),
			"created":          in.Created.Unix(),
			"currency":         in.Currency,
			"invoice":          in.ID,
			"is_default":       true,
			"livemode":         false,
			"status":           status,
			"payment":          map[string]any{"type": "payment_intent", "payment_intent": in.PaymentIntentID},
		})
	}
	if payments == nil {
		payments = []map[string]any{}
	}
	discounts, discountAmounts := []any{}, []any{}
	if in.DiscountID != "" {
		discounts = []any{in.DiscountID}
		discountAmounts = []any{map[string]any{"amount": in.Discount, "discount": in.DiscountID}}
	}
	out := map[string]any{
		"id":                     in.ID,
		"object":                 "invoice",
		"number":                 in.Number,
		"customer":               in.CustomerID,
		"status":                 in.Status,
		"billing_reason":         in.BillingReason,
		"collection_method":      "charge_automatically",
		"currency":               in.Currency,
		"amount_due":             in.Total(),
		"amount_paid":            amountPaid,
		"amount_remaining":       in.Total() - amountPaid,
		"subtotal":               in.Subtotal,
		"total":                  in.Total(),
		"total_discount_amounts": discountAmounts,
		"discounts":              discounts,
		"attempt_count":          in.AttemptCount,
		"attempted":              in.AttemptCount > 0,
		"created":                in.Created.Unix(),
		"period_start":           in.PeriodStart.Unix(),
		"period_end":             in.PeriodEnd.Unix(),
		"due_date":               nil,
		"description":            nil,
		"livemode":               false,
		"next_payment_attempt":   nil, // the mock never retries on its own; the "retry" action does
		"parent": map[string]any{
			"type":                 "subscription_details",
			"subscription_details": map[string]any{"subscription": in.SubscriptionID, "metadata": map[string]string{}},
			"quote_details":        nil,
		},
		"payments": map[string]any{"object": "list", "url": "/v1/invoices/" + in.ID + "/payments", "has_more": false, "data": payments},
		"lines":    map[string]any{"object": "list", "url": "/v1/invoices/" + in.ID + "/lines", "has_more": false, "data": lines},
		"status_transitions": map[string]any{
			"finalized_at": in.Created.Unix(), "paid_at": nullableUnix(in.PaidAt),
			"marked_uncollectible_at": nil, "voided_at": nullableUnix(in.VoidedAt),
		},
	}
	if in.Metadata != nil {
		out["metadata"] = in.Metadata
	} else {
		out["metadata"] = map[string]string{}
	}
	return out
}

// nullableUnix renders a zero time as null, else unix seconds.
func nullableUnix(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.Unix()
}
