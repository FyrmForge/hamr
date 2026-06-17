package devserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// stripeRefund is the in-memory representation of a Stripe Refund.
//
// The mock uses Stripe's sync-success model that matches card refunds: the
// POST /v1/refunds call returns status="succeeded" and fires the
// charge.refunded webhook async. Async-pending refunds (ACH, etc.) are not
// modelled — apps that need to test pending→succeeded transitions can
// extend this when the use case lands.
type stripeRefund struct {
	ID                     string            `json:"id"`
	Amount                 int64             `json:"amount"`
	Currency               string            `json:"currency"`
	Status                 string            `json:"status"` // "succeeded" — failure path not modelled yet
	ChargeID               string            `json:"charge_id"`
	PaymentIntentID        string            `json:"payment_intent_id,omitempty"`
	Reason                 string            `json:"reason,omitempty"`
	ReceiptNumber          string            `json:"receipt_number,omitempty"`
	ReverseTransfer        bool              `json:"reverse_transfer,omitempty"`
	RefundApplicationFee   bool              `json:"refund_application_fee,omitempty"`
	SourceTransferReversal string            `json:"source_transfer_reversal,omitempty"` // ID of the auto-created transfer reversal (Connect destination charges)
	Created                time.Time         `json:"created"`
	Metadata               map[string]string `json:"metadata,omitempty"`
}

// registerRefundRoutes mounts /v1/refunds endpoints. Called from
// RegisterAPIRoutes — kept private so callers go through one entry point.
func (m *StripeMock) registerRefundRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/v1/refunds", m.handleRefunds)
	mux.HandleFunc("/v1/refunds/", m.handleRefundByID)
}

// handleRefunds dispatches the collection endpoint. POST creates;
// list/update endpoints are not yet mocked.
func (m *StripeMock) handleRefunds(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		m.createRefund(w, r)
	default:
		writeStripeError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
	}
}

// handleRefundByID dispatches GET /v1/refunds/{id}.
func (m *StripeMock) handleRefundByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/refunds/")
	if id == "" || strings.Contains(id, "/") {
		writeStripeError(w, http.StatusNotFound, "invalid_request_error", "refund id required")
		return
	}
	if r.Method != http.MethodGet {
		writeStripeError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	m.mu.RLock()
	rf, ok := m.refunds[id]
	m.mu.RUnlock()
	if !ok {
		writeStripeError(w, http.StatusNotFound, "invalid_request_error",
			fmt.Sprintf("No such refund: '%s'", id))
		return
	}
	m.mu.RLock()
	out := m.serializeRefund(rf)
	m.mu.RUnlock()
	writeStripeJSON(w, http.StatusOK, out)
}

// createRefund executes the synchronous-succeed refund flow:
//  1. Resolve the source: caller passes either `payment_intent` or `charge`;
//     PI is resolved to its latest_charge.
//  2. Validate the requested amount fits within the charge's unrefunded
//     balance.
//  3. Update Charge.amount_refunded (+ Charge.refunded if fully refunded).
//  4. If reverse_transfer + the charge has a transfer, increment
//     Transfer.amount_reversed and synthesise a transfer_reversal ID. (The
//     mock doesn't expose a /v1/transfer_reversals endpoint yet — the ID
//     is purely for round-trip presentation on the Refund.)
//  5. Fire charge.refunded with the *updated* Charge as the event payload.
//
// All validation runs while holding the lock so a concurrent refund call
// can't double-refund a charge.
func (m *StripeMock) createRefund(w http.ResponseWriter, r *http.Request) {
	parsed, ok := readStripeForm(w, r)
	if !ok {
		return
	}

	piID := getString(parsed, "payment_intent")
	chargeID := getString(parsed, "charge")
	if piID == "" && chargeID == "" {
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error",
			"one of payment_intent or charge is required")
		return
	}
	if piID != "" && chargeID != "" {
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error",
			"can only provide one of payment_intent or charge, not both")
		return
	}

	requestedAmount, ok := getInt64(parsed, "amount")
	if !ok {
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error",
			"amount must be an integer")
		return
	}
	reverseTransfer := getBool(parsed, "reverse_transfer")
	refundAppFee := getBool(parsed, "refund_application_fee")

	rf, ch, eventObject, err := m.applyRefund(refundInput{
		piID:            piID,
		chargeID:        chargeID,
		amount:          requestedAmount,
		reason:          getString(parsed, "reason"),
		reverseTransfer: reverseTransfer,
		refundAppFee:    refundAppFee,
		metadata:        stringMap(parsed, "metadata"),
	})
	if err != nil {
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	// Fire-and-forget the webhook with the updated Charge so the app sees
	// the new amount_refunded/refunded values.
	chargeID = ch.ID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := m.FireEvent(ctx, "charge.refunded", eventObject); err != nil {
			m.logger.Warn("webhook delivery failed",
				"refund", rf.ID,
				"charge", chargeID,
				"event_type", "charge.refunded",
				"err", err,
			)
		}
	}()

	m.mu.RLock()
	out := m.serializeRefund(rf)
	m.mu.RUnlock()
	writeStripeJSON(w, http.StatusOK, out)
}

// refundInput packages the parameters from createRefund so applyRefund can
// be tested independently and stays focused on state transitions.
type refundInput struct {
	piID            string
	chargeID        string
	amount          int64
	reason          string
	reverseTransfer bool
	refundAppFee    bool
	metadata        map[string]string
}

// applyRefund mutates Charge + Transfer state in one critical section and
// returns the new Refund + the updated Charge (cached as a serialized
// snapshot so the webhook fires the post-mutation state without holding
// the lock).
func (m *StripeMock) applyRefund(in refundInput) (*stripeRefund, *stripeCharge, map[string]any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Resolve the source charge.
	var ch *stripeCharge
	switch {
	case in.chargeID != "":
		c, ok := m.charges[in.chargeID]
		if !ok {
			return nil, nil, nil, fmt.Errorf("no such charge: '%s'", in.chargeID)
		}
		ch = c
	default:
		pi, ok := m.paymentIntents[in.piID]
		if !ok {
			return nil, nil, nil, fmt.Errorf("no such payment_intent: '%s'", in.piID)
		}
		if pi.LatestChargeID == "" {
			return nil, nil, nil, fmt.Errorf("payment_intent '%s' has no charge to refund (status=%s)", pi.ID, pi.Status)
		}
		c, ok := m.charges[pi.LatestChargeID]
		if !ok {
			return nil, nil, nil, fmt.Errorf("internal: charge %s for PI %s missing", pi.LatestChargeID, pi.ID)
		}
		ch = c
	}

	// Validate amount.
	remaining := ch.Amount - ch.AmountRefunded
	if remaining <= 0 {
		return nil, nil, nil, errors.New("charge has been fully refunded")
	}
	amount := in.amount
	if amount == 0 {
		amount = remaining // default to remaining (full refund)
	}
	if amount < 0 {
		return nil, nil, nil, errors.New("amount must be a positive integer")
	}
	if amount > remaining {
		return nil, nil, nil, fmt.Errorf("refund amount %d exceeds remaining refundable amount %d", amount, remaining)
	}

	// Apply mutations.
	ch.AmountRefunded += amount
	if ch.AmountRefunded >= ch.Amount {
		ch.Refunded = true
	}

	rf := &stripeRefund{
		ID:                   "re_test_" + randomHex(24),
		Amount:               amount,
		Currency:             ch.Currency,
		Status:               "succeeded",
		ChargeID:             ch.ID,
		PaymentIntentID:      ch.PaymentIntentID,
		Reason:               in.reason,
		ReverseTransfer:      in.reverseTransfer,
		RefundApplicationFee: in.refundAppFee,
		Created:              time.Now(),
		Metadata:             in.metadata,
	}

	// Reverse the transfer for destination charges. Real Stripe creates a
	// TransferReversal resource here; we synthesise an ID and increment
	// the Transfer's amount_reversed counter so the next transfer.Get
	// would reflect it (when we mock that endpoint).
	if in.reverseTransfer && ch.TransferID != "" {
		if tr, ok := m.transfers[ch.TransferID]; ok {
			rf.SourceTransferReversal = "trr_test_" + randomHex(24)
			// Reverse the same amount as the refund, capped at the
			// transfer's remaining unreversed balance. Real Stripe scales
			// the reversal proportionally to the application fee split,
			// but for dev the simpler 1:1 model is good enough.
			reverseAmt := min(amount, tr.Amount-tr.AmountReversed)
			tr.AmountReversed += reverseAmt
		}
	}

	m.refunds[rf.ID] = rf

	// Pre-serialize the updated Charge for the webhook payload — we have
	// the lock; serializeCharge reads from the (now-updated) struct.
	eventObject := m.serializeCharge(ch)
	m.persist()
	return rf, ch, eventObject, nil
}

// serializeRefund renders the JSON wire shape stripe-go expects.
func (m *StripeMock) serializeRefund(r *stripeRefund) map[string]any {
	out := map[string]any{
		"id":                       r.ID,
		"object":                   "refund",
		"amount":                   r.Amount,
		"created":                  r.Created.Unix(),
		"currency":                 r.Currency,
		"charge":                   r.ChargeID,
		"payment_intent":           nullableString(r.PaymentIntentID),
		"reason":                   nullableString(r.Reason),
		"receipt_number":           nullableString(r.ReceiptNumber),
		"status":                   r.Status,
		"source_transfer_reversal": nullableString(r.SourceTransferReversal),
	}
	if r.Metadata != nil {
		out["metadata"] = r.Metadata
	} else {
		out["metadata"] = map[string]string{}
	}
	return out
}

// getBool reads a "true"/"false" leaf at key, returning false for missing
// or unparseable values. Stripe-go encodes bools as the strings "true"/"false".
func getBool(m map[string]any, key string) bool {
	return getString(m, key) == "true"
}
