package devserver

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// stripeOpError carries an HTTP status alongside the message so the dashboard
// handlers (which extracted their bodies into the methods below) can preserve
// their original status codes while the MCP gateway just reports the message.
type stripeOpError struct {
	msg    string
	status int
}

func (e *stripeOpError) Error() string { return e.msg }
func stripeErr(status int, format string, a ...any) *stripeOpError {
	return &stripeOpError{msg: fmt.Sprintf(format, a...), status: status}
}

// errPromotionCode marks a rejected promotion code so the hosted page can
// show the message inline instead of failing the request.
type errPromotionCode struct{ error }

// completeCheckout applies an outcome (paid/failed/cancelled) to an open
// session, synthesizing the PaymentIntent + Charge for a paid outcome and
// firing the webhook fanout, exactly as the dashboard "complete" button does.
// promoCode is what the buyer typed on the hosted page ("" = none); it is
// only honoured on a paid outcome and rejected with an *errPromotionCode.
// Returns the redirect target the HTTP handler should send the buyer to;
// leaveOpen is true for a synchronous decline (no state change). The MCP
// stripe.complete tool ignores redirect.
func (m *StripeMock) completeCheckout(id, outcome, promoCode string) (redirect string, leaveOpen bool, err error) {
	rule, ok := stripeOutcomes[outcome]
	if !ok {
		return "", false, stripeErr(http.StatusBadRequest, "unknown outcome %q (allowed: paid, failed, cancelled)", outcome)
	}

	m.mu.Lock()
	sess, exists := m.sessions[id]
	if !exists {
		m.mu.Unlock()
		return "", false, stripeErr(http.StatusNotFound, "session not found")
	}
	if sess.Status != "open" {
		st := sess.Status
		m.mu.Unlock()
		return "", false, stripeErr(http.StatusConflict, "session already %s", st)
	}
	if rule.leaveOpen {
		m.mu.Unlock()
		return "", true, nil
	}
	// Remembered so a typed code that applies but then fails redemption is
	// rolled back: otherwise the session keeps the discount, the hosted page
	// hides the code input and the buyer can never pay.
	prevDiscounts, prevAmountDiscount := sess.Discounts, sess.AmountDiscount
	if rule.createPayment && promoCode != "" {
		if !sess.AllowPromotionCodes {
			m.mu.Unlock()
			return "", false, &errPromotionCode{stripeText("This session does not allow promotion codes")}
		}
		if err := m.applyDiscount(sess, "", "", promoCode); err != nil {
			m.mu.Unlock()
			return "", false, &errPromotionCode{err}
		}
	}

	if rule.createPayment {
		if err := m.redeemDiscount(sess); err != nil {
			if promoCode != "" {
				sess.Discounts, sess.AmountDiscount = prevDiscounts, prevAmountDiscount
			}
			m.mu.Unlock()
			return "", false, &errPromotionCode{err}
		}
	}

	now := m.now()
	sess.Status = rule.status
	sess.PaymentStatus = rule.paymentStatus
	var subFires []webhookFire
	var piID string
	if rule.createPayment {
		if sess.Total() == 0 {
			// A 100% coupon: Stripe completes the session without any payment
			// and never creates a PaymentIntent.
			sess.PaymentStatus = "no_payment_required"
			rule.createPayment = false
		} else {
			piID = "pi_test_" + randomHex(24)
		}
		if sess.Mode == "subscription" {
			// Checkout always creates a Customer for a subscription. The mock
			// keeps only the id: there is no Customer resource to fetch. The
			// PaymentIntent belongs to the invoice, so the session's stays null.
			sess.CustomerID = "cus_test_" + randomHex(14)
			sub, inv := m.createSubscriptionLocked(sess, piID, now)
			sess.SubscriptionID = sub.ID
			subFires = []webhookFire{
				{eventType: "customer.subscription.created", object: m.serializeSubscription(sub)},
				{eventType: "invoice.paid", object: m.serializeInvoice(inv)},
				{eventType: "invoice.payment_succeeded", object: m.serializeInvoice(inv)},
			}
		} else {
			sess.PaymentIntentID = piID
		}
	}
	fires := append([]webhookFire{{eventType: rule.eventType, object: m.serializeSession(sess)}}, subFires...)

	if rule.createPayment {
		piMeta := sess.PaymentIntentMetadata
		if piMeta == nil {
			piMeta = sess.Metadata
		}
		description := ""
		if sess.SubscriptionID != "" {
			description, piMeta = "Subscription creation", nil
		}
		ch := &stripeCharge{
			ID:              "ch_test_" + randomHex(24),
			Amount:          sess.Total(),
			AmountCaptured:  sess.Total(),
			Currency:        sess.Currency,
			Status:          "succeeded",
			Paid:            true,
			Captured:        true,
			PaymentIntentID: piID,
			PaymentMethod:   "pm_card_visa",
			ReceiptEmail:    sess.CustomerEmail,
			Customer:        sess.CustomerID,
			Description:     description,
			Created:         now,
			Metadata:        cloneStringMap(piMeta),
		}
		pi := &stripePaymentIntent{
			ID:                 piID,
			Amount:             sess.Total(),
			Currency:           sess.Currency,
			Status:             "succeeded",
			CaptureMethod:      "automatic",
			ConfirmationMethod: "automatic",
			LatestChargeID:     ch.ID,
			PaymentMethod:      "pm_card_visa",
			ClientSecret:       piID + "_secret_" + randomHex(12),
			ReceiptEmail:       sess.CustomerEmail,
			Customer:           sess.CustomerID,
			Description:        description,
			Created:            now,
			Metadata:           cloneStringMap(piMeta),
		}
		m.paymentIntents[pi.ID] = pi
		m.charges[ch.ID] = ch
		fires = append(fires,
			webhookFire{eventType: "payment_intent.succeeded", object: m.serializePaymentIntent(pi, ch)},
			webhookFire{eventType: "charge.succeeded", object: m.serializeCharge(ch)},
		)
	}

	redirect = sess.SuccessURL
	if !rule.useSuccessURL {
		redirect = sess.CancelURL
	}
	redirect = strings.ReplaceAll(redirect, "{CHECKOUT_SESSION_ID}", sess.ID)
	if redirect == "" {
		redirect = "/"
	}
	m.persist()
	m.mu.Unlock()

	m.fireEventsAsync(fires, "session", id)
	return redirect, false, nil
}

// expireSession marks an open session expired and fires
// checkout.session.expired.
func (m *StripeMock) expireSession(id string) error {
	m.mu.Lock()
	sess, ok := m.sessions[id]
	if !ok {
		m.mu.Unlock()
		return stripeErr(http.StatusNotFound, "session not found")
	}
	if sess.Status != "open" {
		st := sess.Status
		m.mu.Unlock()
		return stripeErr(http.StatusConflict, "session already %s", st)
	}
	sess.Status = "expired"
	sess.PaymentStatus = "unpaid"
	dataObject := m.serializeSession(sess)
	m.persist()
	m.mu.Unlock()

	m.fireEventAsync("checkout.session.expired", dataObject, "session", id)
	return nil
}

// refundPayment refunds a payment intent and fires charge.refunded, mirroring
// the dashboard refund button. amount is in the smallest currency unit.
func (m *StripeMock) refundPayment(piID string, amount int64, reverseTransfer, refundAppFee bool) (*stripeRefund, error) {
	rf, _, eventObject, reversed, err := m.applyRefund(refundInput{
		piID:            piID,
		amount:          amount,
		reverseTransfer: reverseTransfer,
		refundAppFee:    refundAppFee,
	})
	if err != nil {
		return nil, err
	}
	m.fireEventsAsync(refundFires(eventObject, reversed), "refund", rf.ID)
	return rf, nil
}

// stateSummary returns a compact, read-only view of the mock's objects for the
// stripe.list MCP tool.
func (m *StripeMock) stateSummary() StripeStateSummary {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Seed every slice so an empty mock marshals to [] not null — matching the
	// other read tools and keeping the agent-facing JSON shape stable.
	out := StripeStateSummary{
		Clock:          m.now().Format(time.RFC3339),
		Sessions:       []StripeSessionSummary{},
		Subscriptions:  []StripeSubscriptionSummary{},
		PaymentIntents: []StripeObjectSummary{},
		Payouts:        []StripeObjectSummary{},
		Refunds:        []StripeObjectSummary{},
		Accounts:       []StripeAccountSummary{},
		Coupons:        []StripeCouponSummary{},
	}
	for _, s := range m.sessions {
		items := make([]StripeLineItemSummary, 0, len(s.LineItems))
		for _, li := range s.LineItems {
			items = append(items, StripeLineItemSummary{Name: li.Name, UnitAmount: li.UnitAmount, Quantity: li.Quantity})
		}
		out.Sessions = append(out.Sessions, StripeSessionSummary{
			ID:        s.ID,
			Status:    s.Status,
			Amount:    s.Total(),
			Currency:  s.Currency,
			URL:       m.baseURL + "/__hamr/stripe/checkout?session=" + url.QueryEscape(s.ID),
			LineItems: items,
		})
	}
	for _, pi := range m.paymentIntents {
		out.PaymentIntents = append(out.PaymentIntents, StripeObjectSummary{ID: pi.ID, Status: pi.Status, Amount: pi.Amount, Currency: pi.Currency})
	}
	for _, p := range m.payouts {
		out.Payouts = append(out.Payouts, StripeObjectSummary{ID: p.ID, Status: p.Status, Amount: p.Amount, Currency: p.Currency})
	}
	for _, rf := range m.refunds {
		out.Refunds = append(out.Refunds, StripeObjectSummary{ID: rf.ID, Status: rf.Status, Amount: rf.Amount, Currency: rf.Currency})
	}
	for _, a := range m.accounts {
		out.Accounts = append(out.Accounts, StripeAccountSummary{ID: a.ID, Email: a.Email, ChargesEnabled: a.ChargesEnabled})
	}
	for _, a := range m.v2Accounts {
		out.Accounts = append(out.Accounts, StripeAccountSummary{ID: a.ID, Email: a.ContactEmail, V2: true, Onboarded: a.onboarded()})
	}
	for _, s := range m.subscriptions {
		out.Subscriptions = append(out.Subscriptions, StripeSubscriptionSummary{
			ID: s.ID, Status: s.Status, Customer: s.CustomerID, Amount: s.perPeriod(), Currency: s.Currency,
			Interval: s.Items[0].Interval, PeriodEnd: s.CurrentPeriodEnd.Format(time.RFC3339),
			CancelAtPeriodEnd: s.CancelAtPeriodEnd, FailNextRenewal: s.FailNextRenewal,
		})
	}
	for _, c := range m.coupons {
		cs := StripeCouponSummary{ID: c.ID, Name: c.Name, PercentOff: c.PercentOff, AmountOff: c.AmountOff,
			Currency: c.Currency, Duration: c.Duration, TimesRedeemed: c.TimesRedeemed, Codes: []string{}}
		for _, pc := range m.promotionCodes {
			if pc.CouponID == c.ID && pc.Active {
				cs.Codes = append(cs.Codes, pc.Code)
			}
		}
		out.Coupons = append(out.Coupons, cs)
	}
	return out
}
