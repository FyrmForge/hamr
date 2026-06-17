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

// completeCheckout applies an outcome (paid/failed/cancelled) to an open
// session, synthesizing the PaymentIntent + Charge for a paid outcome and
// firing the webhook fanout, exactly as the dashboard "complete" button does.
// Returns the redirect target the HTTP handler should send the buyer to;
// leaveOpen is true for a synchronous decline (no state change). The MCP
// stripe.complete tool ignores redirect.
func (m *StripeMock) completeCheckout(id, outcome string) (redirect string, leaveOpen bool, err error) {
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

	sess.Status = rule.status
	sess.PaymentStatus = rule.paymentStatus
	fires := []webhookFire{{rule.eventType, m.serializeSession(sess)}}

	if rule.createPayment {
		now := time.Now()
		ch := &stripeCharge{
			ID:              "ch_test_" + randomHex(24),
			Amount:          sess.AmountTotal,
			AmountCaptured:  sess.AmountTotal,
			Currency:        sess.Currency,
			Status:          "succeeded",
			Paid:            true,
			Captured:        true,
			PaymentIntentID: sess.PaymentIntentID,
			PaymentMethod:   "pm_card_visa",
			Created:         now,
			Metadata:        sess.Metadata,
		}
		pi := &stripePaymentIntent{
			ID:                 sess.PaymentIntentID,
			Amount:             sess.AmountTotal,
			Currency:           sess.Currency,
			Status:             "succeeded",
			CaptureMethod:      "automatic",
			ConfirmationMethod: "automatic",
			LatestChargeID:     ch.ID,
			PaymentMethod:      "pm_card_visa",
			ClientSecret:       sess.PaymentIntentID + "_secret_" + randomHex(12),
			Created:            now,
			Metadata:           sess.Metadata,
		}
		m.paymentIntents[pi.ID] = pi
		m.charges[ch.ID] = ch
		fires = append(fires,
			webhookFire{"payment_intent.succeeded", m.serializePaymentIntent(pi, ch)},
			webhookFire{"charge.succeeded", m.serializeCharge(ch)},
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
	rf, _, eventObject, err := m.applyRefund(refundInput{
		piID:            piID,
		amount:          amount,
		reverseTransfer: reverseTransfer,
		refundAppFee:    refundAppFee,
	})
	if err != nil {
		return nil, err
	}
	m.fireEventAsync("charge.refunded", eventObject, "refund", rf.ID)
	return rf, nil
}

// stateSummary returns a compact, read-only view of the mock's objects for the
// stripe.list MCP tool.
func (m *StripeMock) stateSummary() StripeStateSummary {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := StripeStateSummary{}
	for _, s := range m.sessions {
		items := make([]StripeLineItemSummary, 0, len(s.LineItems))
		for _, li := range s.LineItems {
			items = append(items, StripeLineItemSummary{Name: li.Name, UnitAmount: li.UnitAmount, Quantity: li.Quantity})
		}
		out.Sessions = append(out.Sessions, StripeSessionSummary{
			ID:        s.ID,
			Status:    s.Status,
			Amount:    s.AmountTotal,
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
	return out
}
