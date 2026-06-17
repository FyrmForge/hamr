package devserver

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// stripePaymentIntent is the in-memory representation of a Stripe PaymentIntent
// — the resource that drives a single payment from creation through to a
// succeeded/failed terminal state. Mirrors the subset of fields apps inspect
// in webhook payloads and via paymentintent.Get; everything else is omitted
// and decodes as zero-valued in stripe-go's struct unmarshaler.
//
// Connect-aware fields:
//   - StripeAccount: the connected account the request was made AS (from
//     the Stripe-Account header). Direct-charge model.
//   - OnBehalfOf: the connected account the funds are intended FOR (from
//     the on_behalf_of body field). Used by both direct and destination charges.
//   - TransferDataDestination + TransferDataAmount: destination-charge model
//     — funds are transferred to this connected account on success.
//   - ApplicationFeeAmount: platform's commission, deducted from the payment.
type stripePaymentIntent struct {
	ID                      string            `json:"id"`
	Amount                  int64             `json:"amount"`
	Currency                string            `json:"currency"`
	Status                  string            `json:"status"`              // requires_payment_method | requires_confirmation | succeeded | canceled
	CaptureMethod           string            `json:"capture_method"`      // automatic | manual
	ConfirmationMethod      string            `json:"confirmation_method"` // automatic | manual
	ApplicationFeeAmount    int64             `json:"application_fee_amount,omitempty"`
	TransferDataDestination string            `json:"transfer_data_destination,omitempty"`
	TransferDataAmount      int64             `json:"transfer_data_amount,omitempty"` // 0 = entire amount minus app fee
	OnBehalfOf              string            `json:"on_behalf_of,omitempty"`
	StripeAccount           string            `json:"stripe_account,omitempty"` // request was made AS this connected account (header)
	LatestChargeID          string            `json:"latest_charge_id,omitempty"`
	TransferID              string            `json:"transfer_id,omitempty"`
	Description             string            `json:"description,omitempty"`
	StatementDescriptor     string            `json:"statement_descriptor,omitempty"`
	ReceiptEmail            string            `json:"receipt_email,omitempty"`
	Customer                string            `json:"customer,omitempty"`
	PaymentMethod           string            `json:"payment_method,omitempty"`
	ClientSecret            string            `json:"client_secret"`
	Created                 time.Time         `json:"created"`
	Metadata                map[string]string `json:"metadata,omitempty"`
	// Failed records that a confirmation attempt was declined (status falls back
	// to requires_payment_method, which is otherwise indistinguishable from a
	// freshly-created, never-attempted PI). The dashboard uses it to decide
	// whether resending payment_intent.payment_failed is meaningful.
	Failed bool `json:"failed,omitempty"`
	// AmountReceived is the amount actually captured. For a full capture it
	// equals Amount; a partial manual capture sets it lower. Zero until the PI
	// succeeds.
	AmountReceived int64 `json:"amount_received,omitempty"`
}

// registerPaymentIntentRoutes mounts PI endpoints. Called from
// RegisterAPIRoutes — kept private so callers go through one entry point.
func (m *StripeMock) registerPaymentIntentRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/v1/payment_intents", m.handlePaymentIntents)
	mux.HandleFunc("/v1/payment_intents/", m.handlePaymentIntentByID)
}

// handlePaymentIntents dispatches the collection endpoint. POST creates;
// list is not implemented.
func (m *StripeMock) handlePaymentIntents(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		m.createPaymentIntent(w, r)
	default:
		writeStripeError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
	}
}

// handlePaymentIntentByID dispatches /v1/payment_intents/{id}[/confirm].
// GET retrieves; POST on the bare ID updates (not yet mocked); POST on
// /confirm advances state from requires_confirmation to either succeeded
// (capture_method=automatic) or requires_capture (manual).
func (m *StripeMock) handlePaymentIntentByID(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/payment_intents/")
	if rest == "" {
		writeStripeError(w, http.StatusNotFound, "invalid_request_error", "payment_intent id required")
		return
	}

	id, action, _ := strings.Cut(rest, "/")
	if id == "" {
		writeStripeError(w, http.StatusNotFound, "invalid_request_error", "payment_intent id required")
		return
	}

	switch {
	case action == "" && r.Method == http.MethodGet:
		m.retrievePaymentIntent(w, id)
	case action == "confirm" && r.Method == http.MethodPost:
		m.confirmPaymentIntent(w, r, id)
	case action == "capture" && r.Method == http.MethodPost:
		m.capturePaymentIntent(w, r, id)
	default:
		writeStripeError(w, http.StatusMethodNotAllowed, "invalid_request_error",
			fmt.Sprintf("method %s not allowed on /v1/payment_intents/%s/%s", r.Method, id, action))
	}
}

// createPaymentIntent parses the form body and stores a new PI. New PIs
// start in `requires_payment_method` (no payment method supplied) or
// `requires_confirmation` (payment method supplied, awaiting client confirm).
// Real Stripe also auto-confirms when `confirm=true` is sent — we treat
// that as "ready for the dev UI outcome" and store as requires_confirmation.
func (m *StripeMock) createPaymentIntent(w http.ResponseWriter, r *http.Request) {
	parsed, ok := readStripeForm(w, r)
	if !ok {
		return
	}

	pi, err := buildPaymentIntentFromParams(parsed)
	if err != nil {
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	pi.ID = "pi_test_" + randomHex(24)
	pi.ClientSecret = pi.ID + "_secret_" + randomHex(16)
	pi.Created = time.Now()
	pi.StripeAccount = strings.TrimSpace(r.Header.Get("Stripe-Account"))
	if pi.PaymentMethod != "" {
		pi.Status = "requires_confirmation"
	} else {
		pi.Status = "requires_payment_method"
	}

	// Validate the destination connected account exists, mirroring real
	// Stripe's behavior — sending transfer_data.destination = unknown acct
	// is an instant 400.
	if pi.TransferDataDestination != "" {
		m.mu.RLock()
		_, exists := m.accounts[pi.TransferDataDestination]
		m.mu.RUnlock()
		if !exists {
			writeStripeError(w, http.StatusBadRequest, "invalid_request_error",
				fmt.Sprintf("No such destination account: '%s'", pi.TransferDataDestination))
			return
		}
	}

	m.mu.Lock()
	m.paymentIntents[pi.ID] = pi
	piCopy := clonePaymentIntent(pi)
	m.persist()
	m.mu.Unlock()

	// New PI has no charge yet (LatestChargeID empty), so ch is nil.
	writeStripeJSON(w, http.StatusOK, m.serializePaymentIntent(piCopy, nil))
}

// retrievePaymentIntent serves GET /v1/payment_intents/{id}.
func (m *StripeMock) retrievePaymentIntent(w http.ResponseWriter, id string) {
	m.mu.RLock()
	pi, ok := m.paymentIntents[id]
	var piCopy *stripePaymentIntent
	var chCopy *stripeCharge
	if ok {
		piCopy = clonePaymentIntent(pi)
		if pi.LatestChargeID != "" {
			if ch, ok := m.charges[pi.LatestChargeID]; ok {
				chCopy = cloneCharge(ch)
			}
		}
	}
	m.mu.RUnlock()
	if !ok {
		writeStripeError(w, http.StatusNotFound, "invalid_request_error",
			fmt.Sprintf("No such payment_intent: '%s'", id))
		return
	}
	writeStripeJSON(w, http.StatusOK, m.serializePaymentIntent(piCopy, chCopy))
}

// confirmPaymentIntent moves the PI from requires_confirmation → either
// succeeded (capture_method=automatic) or requires_capture (manual). The
// dev UI outcome handler then drives the transition all the way to success
// or failure with webhook fanout.
//
// In real Stripe this is what client-side stripe.confirmPayment(...) calls
// after collecting card details. The mock skips the payment-method-data
// dance and just advances the state.
func (m *StripeMock) confirmPaymentIntent(w http.ResponseWriter, r *http.Request, id string) {
	parsed, ok := readStripeForm(w, r)
	if !ok {
		return
	}

	m.mu.Lock()
	pi, exists := m.paymentIntents[id]
	if !exists {
		m.mu.Unlock()
		writeStripeError(w, http.StatusNotFound, "invalid_request_error",
			fmt.Sprintf("No such payment_intent: '%s'", id))
		return
	}
	if pi.Status != "requires_payment_method" && pi.Status != "requires_confirmation" {
		m.mu.Unlock()
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error",
			fmt.Sprintf("PaymentIntent status is %s; cannot confirm", pi.Status))
		return
	}
	if pm := getString(parsed, "payment_method"); pm != "" {
		pi.PaymentMethod = pm
	}
	if pi.PaymentMethod == "" {
		// Default to a placeholder so the rest of the flow doesn't error.
		pi.PaymentMethod = "pm_card_visa"
	}
	if pi.CaptureMethod == "manual" {
		pi.Status = "requires_capture"
	} else {
		// Real Stripe goes requires_confirmation → processing → succeeded
		// for an automatic-capture confirm with no SCA challenge.
		// `requires_action` is reserved for 3DS/SCA flows the mock doesn't
		// model — using it here would mislead app code that branches on
		// `pi.Status == "requires_action"` to show an SCA modal.
		pi.Status = "processing"
	}
	piCopy := clonePaymentIntent(pi)
	m.persist()
	m.mu.Unlock()

	// Confirm never creates a charge (LatestChargeID still empty here), and
	// cloning under the lock above closes the previous TOCTOU window between
	// the mutation and the response serialization.
	writeStripeJSON(w, http.StatusOK, m.serializePaymentIntent(piCopy, nil))
}

// capturePaymentIntent captures a manual-capture PI sitting in
// requires_capture: it creates the Charge (and destination Transfer, if any),
// advances the PI to succeeded, and fires payment_intent.succeeded +
// charge.succeeded (+ transfer.created). Supports partial capture via
// amount_to_capture. Mirrors POST /v1/payment_intents/{id}/capture, which an
// app calls after authorizing now and capturing later.
func (m *StripeMock) capturePaymentIntent(w http.ResponseWriter, r *http.Request, id string) {
	parsed, ok := readStripeForm(w, r)
	if !ok {
		return
	}

	m.mu.Lock()
	pi, exists := m.paymentIntents[id]
	if !exists {
		m.mu.Unlock()
		writeStripeError(w, http.StatusNotFound, "invalid_request_error",
			fmt.Sprintf("No such payment_intent: '%s'", id))
		return
	}
	if pi.Status != "requires_capture" {
		m.mu.Unlock()
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error",
			fmt.Sprintf("PaymentIntent status is %s; only requires_capture can be captured", pi.Status))
		return
	}

	captureAmount := pi.Amount
	if v, ok := getInt64(parsed, "amount_to_capture"); ok && v > 0 {
		if v > pi.Amount {
			m.mu.Unlock()
			writeStripeError(w, http.StatusBadRequest, "invalid_request_error",
				fmt.Sprintf("amount_to_capture (%d) cannot exceed the authorized amount (%d)", v, pi.Amount))
			return
		}
		captureAmount = v
	}

	now := time.Now()
	ch := &stripeCharge{
		ID:                   "ch_test_" + randomHex(24),
		Amount:               pi.Amount,
		AmountCaptured:       captureAmount,
		Currency:             pi.Currency,
		Status:               "succeeded",
		Paid:                 true,
		Captured:             true,
		PaymentIntentID:      pi.ID,
		PaymentMethod:        pi.PaymentMethod,
		ApplicationFeeAmount: pi.ApplicationFeeAmount,
		Destination:          pi.TransferDataDestination,
		Description:          pi.Description,
		ReceiptEmail:         pi.ReceiptEmail,
		Customer:             pi.Customer,
		Created:              now,
		Metadata:             pi.Metadata,
	}
	m.charges[ch.ID] = ch
	pi.Status = "succeeded"
	pi.LatestChargeID = ch.ID
	pi.AmountReceived = captureAmount

	fires := []webhookFire{
		{"payment_intent.succeeded", m.serializePaymentIntent(pi, ch)},
		{"charge.succeeded", m.serializeCharge(ch)},
	}
	// Destination-charge cascade: transfer the captured amount (minus the
	// application fee) to the connected account, unless transfer_data.amount
	// overrides it.
	if pi.TransferDataDestination != "" {
		amt := pi.TransferDataAmount
		if amt == 0 {
			// A partial capture can be smaller than the application fee, which
			// would make the default transfer negative — clamp to 0 (real Stripe
			// never emits a negative transfer).
			amt = max(captureAmount-pi.ApplicationFeeAmount, 0)
		}
		tr := &stripeTransfer{
			ID:                  "tr_test_" + randomHex(24),
			Amount:              amt,
			Currency:            pi.Currency,
			Destination:         pi.TransferDataDestination,
			SourceTransactionID: ch.ID,
			Created:             now,
		}
		m.transfers[tr.ID] = tr
		pi.TransferID = tr.ID
		ch.TransferID = tr.ID
		fires = append(fires, webhookFire{"transfer.created", m.serializeTransfer(tr)})
	}

	piCopy := clonePaymentIntent(pi)
	chCopy := cloneCharge(ch)
	m.persist()
	m.mu.Unlock()

	m.fireEventsAsync(fires, "payment_intent", id)

	writeStripeJSON(w, http.StatusOK, m.serializePaymentIntent(piCopy, chCopy))
}

// buildPaymentIntentFromParams projects the decoded params into the mock's
// internal shape with Stripe-equivalent validation.
func buildPaymentIntentFromParams(p map[string]any) (*stripePaymentIntent, error) {
	amount, ok := getInt64(p, "amount")
	if !ok || amount <= 0 {
		return nil, errors.New("amount must be a positive integer")
	}
	currency := strings.ToLower(getString(p, "currency"))
	if currency == "" {
		return nil, errors.New("currency is required")
	}
	appFee, ok := getInt64(p, "application_fee_amount")
	if !ok {
		return nil, errors.New("application_fee_amount must be an integer")
	}
	pi := &stripePaymentIntent{
		Amount:               amount,
		Currency:             currency,
		ApplicationFeeAmount: appFee,
		OnBehalfOf:           getString(p, "on_behalf_of"),
		Description:          getString(p, "description"),
		StatementDescriptor:  getString(p, "statement_descriptor"),
		ReceiptEmail:         getString(p, "receipt_email"),
		Customer:             getString(p, "customer"),
		PaymentMethod:        getString(p, "payment_method"),
		Metadata:             stringMap(p, "metadata"),
		CaptureMethod:        orDefault(getString(p, "capture_method"), "automatic"),
		ConfirmationMethod:   orDefault(getString(p, "confirmation_method"), "automatic"),
	}
	if td, ok := p["transfer_data"].(map[string]any); ok {
		pi.TransferDataDestination = getString(td, "destination")
		// Invalid or negative transfer_data.amount falls through to 0 ("entire
		// amount minus fee" default) rather than erroring — it's a best-effort dev
		// mock field. The < 0 guard mirrors application_fee_amount; without it a
		// negative value would bypass the capture-time max(…,0) clamp (which only
		// runs on the amount==0 default path) and emit a negative transfer.created.
		if v, ok := getInt64(td, "amount"); ok && v >= 0 {
			pi.TransferDataAmount = v
		}
	}
	if pi.ApplicationFeeAmount < 0 {
		return nil, errors.New("application_fee_amount must be non-negative")
	}
	if pi.ApplicationFeeAmount > pi.Amount {
		return nil, errors.New("application_fee_amount cannot exceed amount")
	}
	return pi, nil
}

// serializePaymentIntent renders the JSON wire shape stripe-go expects.
// LatestCharge is rendered as an inline charge object when ch is non-nil so a
// single PI retrieve gives the app everything it needs to update its state.
//
// Contract: pi (and ch) must be lock-stable — either hold m.mu across the
// call or pass a clone (see stripemock_clone.go). This method reads only its
// arguments; the caller is responsible for resolving the charge from m.charges
// under the lock and passing it in (nil when there is no charge).
func (m *StripeMock) serializePaymentIntent(pi *stripePaymentIntent, ch *stripeCharge) map[string]any {
	out := map[string]any{
		"id":                     pi.ID,
		"object":                 "payment_intent",
		"amount":                 pi.Amount,
		"amount_capturable":      int64(0),
		"amount_received":        pi.AmountReceived,
		"application_fee_amount": pi.ApplicationFeeAmount,
		"capture_method":         pi.CaptureMethod,
		"client_secret":          pi.ClientSecret,
		"confirmation_method":    pi.ConfirmationMethod,
		"created":                pi.Created.Unix(),
		"currency":               pi.Currency,
		"customer":               nullableString(pi.Customer),
		"description":            nullableString(pi.Description),
		"livemode":               false,
		"on_behalf_of":           nullableString(pi.OnBehalfOf),
		"payment_method":         nullableString(pi.PaymentMethod),
		"receipt_email":          nullableString(pi.ReceiptEmail),
		"statement_descriptor":   nullableString(pi.StatementDescriptor),
		"status":                 pi.Status,
	}
	if pi.Status == "requires_capture" {
		// The full authorized amount is available to capture.
		out["amount_capturable"] = pi.Amount
	}
	if pi.Status == "succeeded" {
		// Captured amount: AmountReceived for an explicit (possibly partial)
		// capture, else the full amount for an auto-captured success.
		if pi.AmountReceived > 0 {
			out["amount_received"] = pi.AmountReceived
		} else {
			out["amount_received"] = pi.Amount
		}
	}
	if pi.Metadata != nil {
		out["metadata"] = pi.Metadata
	} else {
		out["metadata"] = map[string]string{}
	}
	if pi.TransferDataDestination != "" {
		td := map[string]any{
			"destination": pi.TransferDataDestination,
		}
		if pi.TransferDataAmount > 0 {
			td["amount"] = pi.TransferDataAmount
		}
		out["transfer_data"] = td
	}
	if pi.LatestChargeID != "" {
		if ch != nil {
			out["latest_charge"] = m.serializeCharge(ch)
		} else {
			out["latest_charge"] = pi.LatestChargeID
		}
	}
	return out
}

// nullableString returns nil for empty strings so the JSON renders `null`
// instead of `""` — matches Stripe's actual response shape.
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
