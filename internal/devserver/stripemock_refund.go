package devserver

import (
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
func (m *StripeMock) registerRefundRoutes(mux stripeRouter) {
	mux.HandleFunc("/v1/refunds", m.handleRefunds)
	mux.HandleFunc("/v1/refunds/", m.handleRefundByID)
}

// handleRefunds dispatches the collection endpoint. POST creates; GET lists.
func (m *StripeMock) handleRefunds(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		m.createRefund(w, r)
	case http.MethodGet:
		m.listRefunds(w, r)
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

// listRefunds serves GET /v1/refunds, filtered by charge and/or
// payment_intent, newest first, with limit + starting_after cursor paging so
// stripe-go's List(...).All(ctx) iterator walks every page.
func (m *StripeMock) listRefunds(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	chargeID, piID := q.Get("charge"), q.Get("payment_intent")

	m.mu.RLock()
	var matching []*stripeRefund
	for _, rf := range m.refunds {
		if (chargeID == "" || rf.ChargeID == chargeID) && (piID == "" || rf.PaymentIntentID == piID) {
			matching = append(matching, cloneRefund(rf))
		}
	}
	m.mu.RUnlock()

	sortNewestFirst(matching, func(rf *stripeRefund) (time.Time, string) { return rf.Created, rf.ID })
	page, hasMore := pageAfter(matching, q, func(rf *stripeRefund) string { return rf.ID })

	data := make([]map[string]any, len(page))
	for i, rf := range page {
		data[i] = m.serializeRefund(rf)
	}
	writeStripeJSON(w, http.StatusOK, map[string]any{
		"object":   "list",
		"url":      "/v1/refunds",
		"has_more": hasMore,
		"data":     data,
	})
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

	rf, ch, eventObject, reversed, err := m.applyRefund(refundInput{
		piID:            piID,
		chargeID:        chargeID,
		amount:          requestedAmount,
		reason:          getString(parsed, "reason"),
		reverseTransfer: reverseTransfer,
		refundAppFee:    refundAppFee,
		metadata:        stringMap(parsed, "metadata"),
	})
	if err != nil {
		code := ""
		if errors.Is(err, errChargeAlreadyRefunded) {
			code = "charge_already_refunded"
		}
		writeStripeErrorCode(w, http.StatusBadRequest, "invalid_request_error", code, err.Error())
		return
	}

	// Fire-and-forget the webhook with the updated Charge so the app sees
	// the new amount_refunded/refunded values.
	m.fireEventsAsync(refundFires(eventObject, reversed), "refund", rf.ID, "charge", ch.ID)

	m.mu.RLock()
	out := m.serializeRefund(rf)
	m.mu.RUnlock()
	writeStripeJSON(w, http.StatusOK, out)
}

// errChargeAlreadyRefunded marks the "nothing left to refund" refusal so the
// API can attach Stripe's charge_already_refunded code.
var errChargeAlreadyRefunded = errors.New("charge_already_refunded")

// errChargeDisputed marks the "money is frozen under a dispute" refusal.
var errChargeDisputed = errors.New("charge_disputed")

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
func (m *StripeMock) applyRefund(in refundInput) (rf *stripeRefund, ch *stripeCharge, eventObject map[string]any, reversedTransfer map[string]any, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Resolve the source charge.
	switch {
	case in.chargeID != "":
		c, ok := m.charges[in.chargeID]
		if !ok {
			return nil, nil, nil, nil, fmt.Errorf("no such charge: '%s'", in.chargeID)
		}
		ch = c
	default:
		pi, ok := m.paymentIntents[in.piID]
		if !ok {
			return nil, nil, nil, nil, fmt.Errorf("no such payment_intent: '%s'", in.piID)
		}
		if pi.LatestChargeID == "" {
			return nil, nil, nil, nil, fmt.Errorf("payment_intent '%s' has no charge to refund (status=%s)", pi.ID, pi.Status)
		}
		c, ok := m.charges[pi.LatestChargeID]
		if !ok {
			return nil, nil, nil, nil, fmt.Errorf("internal: charge %s for PI %s missing", pi.LatestChargeID, pi.ID)
		}
		ch = c
	}

	// Validate amount. Disputed money is frozen: Stripe refuses with
	// charge_disputed, and the mock's own dispute says is_charge_refundable=false.
	if ch.DisputeID != "" {
		return nil, nil, nil, nil, fmt.Errorf("charge %s is disputed and cannot be refunded: %w", ch.ID, errChargeDisputed)
	}
	remaining := ch.AmountCaptured - ch.AmountRefunded
	if remaining <= 0 {
		return nil, nil, nil, nil, fmt.Errorf("charge %s has already been fully refunded: %w", ch.ID, errChargeAlreadyRefunded)
	}
	amount := in.amount
	if amount == 0 {
		amount = remaining // default to remaining (full refund)
	}
	if amount < 0 {
		return nil, nil, nil, nil, errors.New("amount must be a positive integer")
	}
	if amount > remaining {
		return nil, nil, nil, nil, fmt.Errorf("refund amount %d exceeds remaining refundable amount %d", amount, remaining)
	}

	// Apply mutations.
	ch.AmountRefunded += amount
	if ch.AmountRefunded >= ch.AmountCaptured {
		ch.Refunded = true
	}

	rf = &stripeRefund{
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

	// Reverse the transfer for destination charges, recording a real
	// reversal on it. Reverses the same amount as the refund, capped at the
	// transfer's unreversed balance. ponytail: real Stripe scales the reversal
	// by the application-fee split; 1:1 is close enough for dev.
	if in.reverseTransfer && ch.TransferID != "" {
		if tr, ok := m.transfers[ch.TransferID]; ok {
			if reverseAmt := min(amount, tr.Amount-tr.AmountReversed); reverseAmt > 0 {
				rv := m.reverseTransferLocked(tr, reverseAmt, rf.ID, nil)
				rf.SourceTransferReversal = rv.ID
				reversedTransfer = m.serializeTransfer(tr)
			}
		}
	}

	m.refunds[rf.ID] = rf

	// Pre-serialize the updated Charge for the webhook payload — we have
	// the lock; serializeCharge reads from the (now-updated) struct.
	eventObject = m.serializeCharge(ch)
	m.persist()
	return rf, ch, eventObject, reversedTransfer, nil
}

// refundFires is charge.refunded, then transfer.reversed when the refund
// pulled money back off a destination transfer.
func refundFires(charge, reversedTransfer map[string]any) []webhookFire {
	fires := []webhookFire{{eventType: "charge.refunded", object: charge}}
	if reversedTransfer != nil {
		fires = append(fires, webhookFire{eventType: "transfer.reversed", object: reversedTransfer})
	}
	return fires
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
