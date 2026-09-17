package devserver

import (
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"
)

// Disputes and scheduled connected-account payouts are things Stripe does to
// an app, not calls the app makes, so the mock drives them from dashboard
// controls:
//
//	POST /__hamr/stripe/dispute          — open a dispute on a succeeded payment
//	POST /__hamr/stripe/dispute/close    — close it won or lost
//	POST /__hamr/stripe/payout/account   — pay out a connected account's balance

// stripeDispute is a chargeback against a charge.
type stripeDispute struct {
	ID              string    `json:"id"`
	ChargeID        string    `json:"charge_id"`
	PaymentIntentID string    `json:"payment_intent_id,omitempty"`
	Amount          int64     `json:"amount"`
	Currency        string    `json:"currency"`
	Reason          string    `json:"reason"`
	Status          string    `json:"status"` // needs_response | won | lost
	EvidenceDueBy   time.Time `json:"evidence_due_by"`
	Created         time.Time `json:"created"`
	ClosedAt        time.Time `json:"closed_at,omitzero"`
}

func (m *StripeMock) registerDisputeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/__hamr/stripe/dispute", guardUnsafe(m.handleDashboardDispute))
	mux.HandleFunc("/__hamr/stripe/dispute/close", guardUnsafe(m.handleDashboardDisputeClose))
	mux.HandleFunc("/__hamr/stripe/payout/account", guardUnsafe(m.handleDashboardAccountPayout))
}

func (m *StripeMock) handleDashboardDispute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, err := m.openDispute(strings.TrimSpace(r.FormValue("payment_intent"))); err != nil {
		writeStripeOpError(w, err)
		return
	}
	http.Redirect(w, r, "/__hamr/stripe", http.StatusSeeOther)
}

func (m *StripeMock) handleDashboardDisputeClose(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := m.closeDispute(strings.TrimSpace(r.FormValue("dispute")), r.FormValue("outcome")); err != nil {
		writeStripeOpError(w, err)
		return
	}
	http.Redirect(w, r, "/__hamr/stripe", http.StatusSeeOther)
}

// openDispute raises a chargeback for the unrefunded amount of a payment
// intent's charge and fires charge.dispute.created + charge.dispute.funds_withdrawn.
func (m *StripeMock) openDispute(piID string) (*stripeDispute, error) {
	m.mu.Lock()
	pi, ok := m.paymentIntents[piID]
	if !ok {
		m.mu.Unlock()
		return nil, stripeErr(http.StatusNotFound, "payment_intent not found")
	}
	ch, ok := m.charges[pi.LatestChargeID]
	if !ok || pi.Status != "succeeded" {
		m.mu.Unlock()
		return nil, stripeErr(http.StatusBadRequest, "payment_intent %s has no succeeded charge to dispute", piID)
	}
	if ch.DisputeID != "" {
		m.mu.Unlock()
		return nil, stripeErr(http.StatusConflict, "charge %s is already disputed", ch.ID)
	}
	// Refunded money is already back with the cardholder; only the rest can
	// be charged back.
	if ch.AmountCaptured-ch.AmountRefunded <= 0 {
		m.mu.Unlock()
		return nil, stripeErr(http.StatusBadRequest, "charge %s is fully refunded, nothing to dispute", ch.ID)
	}
	now := time.Now()
	dp := &stripeDispute{
		ID:              "dp_test_" + randomHex(24),
		ChargeID:        ch.ID,
		PaymentIntentID: pi.ID,
		Amount:          ch.AmountCaptured - ch.AmountRefunded,
		Currency:        ch.Currency,
		Reason:          "fraudulent",
		Status:          "needs_response",
		EvidenceDueBy:   now.Add(7 * 24 * time.Hour),
		Created:         now,
	}
	m.disputes[dp.ID] = dp
	ch.DisputeID = dp.ID
	obj := m.serializeDisputeLocked(dp)
	m.persist()
	m.mu.Unlock()

	m.fireEventsAsync([]webhookFire{
		{eventType: "charge.dispute.created", object: obj},
		{eventType: "charge.dispute.funds_withdrawn", object: obj},
	}, "dispute", dp.ID)
	return dp, nil
}

// closeDispute settles an open dispute. Won returns the money and the fee
// (charge.dispute.funds_reinstated); lost keeps both withdrawn.
func (m *StripeMock) closeDispute(id, outcome string) error {
	if outcome != "won" && outcome != "lost" {
		return stripeErr(http.StatusBadRequest, "unknown outcome %q (allowed: won, lost)", outcome)
	}
	m.mu.Lock()
	dp, ok := m.disputes[id]
	if !ok {
		m.mu.Unlock()
		return stripeErr(http.StatusNotFound, "dispute not found")
	}
	if dp.Status != "needs_response" {
		st := dp.Status
		m.mu.Unlock()
		return stripeErr(http.StatusConflict, "dispute already %s", st)
	}
	dp.Status = outcome
	dp.ClosedAt = time.Now()
	obj := m.serializeDisputeLocked(dp)
	m.persist()
	m.mu.Unlock()

	fires := []webhookFire{{eventType: "charge.dispute.closed", object: obj}}
	if outcome == "won" {
		fires = append(fires, webhookFire{eventType: "charge.dispute.funds_reinstated", object: obj})
	}
	m.fireEventsAsync(fires, "dispute", id)
	return nil
}

// serializeDisputeLocked renders a dispute with its balance transactions
// inline, which is where apps read the dispute fee. Caller holds m.mu.
func (m *StripeMock) serializeDisputeLocked(dp *stripeDispute) map[string]any {
	bts := []any{m.balanceTransactionLocked(disputeWithdrawalTxnID(dp.ID))}
	if dp.Status == "won" {
		bts = append(bts, m.balanceTransactionLocked(disputeReinstateTxnID(dp.ID)))
	}
	return map[string]any{
		"id":                   dp.ID,
		"object":               "dispute",
		"amount":               dp.Amount,
		"currency":             dp.Currency,
		"charge":               dp.ChargeID,
		"payment_intent":       nullableString(dp.PaymentIntentID),
		"reason":               dp.Reason,
		"status":               dp.Status,
		"created":              dp.Created.Unix(),
		"livemode":             false,
		"is_charge_refundable": false,
		"metadata":             map[string]string{},
		"balance_transactions": bts,
		"evidence_details": map[string]any{
			"due_by":           dp.EvidenceDueBy.Unix(),
			"has_evidence":     false,
			"past_due":         false,
			"submission_count": 0,
		},
	}
}

// handleDashboardAccountPayout pays out a connected account's whole balance,
// standing in for the payout Stripe runs on the account's schedule. The
// payout starts pending; the payout page marks it paid or failed.
func (m *StripeMock) handleDashboardAccountPayout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	po, err := m.payoutAccountBalance(strings.TrimSpace(r.FormValue("account")))
	if err != nil {
		writeStripeOpError(w, err)
		return
	}
	http.Redirect(w, r, "/__hamr/stripe/payout?id="+po.ID, http.StatusSeeOther)
}

// payoutAccountBalance creates a pending automatic payout for the first
// currency the account holds a positive balance in.
func (m *StripeMock) payoutAccountBalance(acct string) (*stripePayout, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if acct == "" || !m.accountExists(acct) {
		return nil, stripeErr(http.StatusNotFound, "account not found")
	}
	bal := m.connectedBalance(acct)
	for _, currency := range slices.Sorted(maps.Keys(bal)) {
		amount := bal[currency]
		if amount <= 0 {
			continue
		}
		now := time.Now()
		po := &stripePayout{
			ID:          "po_test_" + randomHex(24),
			Amount:      amount,
			Currency:    currency,
			Status:      "pending",
			Method:      "standard",
			SourceType:  "card",
			Automatic:   true,
			ArrivalDate: now.Add(48 * time.Hour),
			Created:     now,
			AccountID:   acct,
		}
		m.payouts[po.ID] = po
		m.persist()
		return clonePayout(po), nil
	}
	return nil, stripeErr(http.StatusBadRequest, "account %s has no balance to pay out", acct)
}
