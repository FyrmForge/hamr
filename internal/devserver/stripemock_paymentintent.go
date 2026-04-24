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
	m.persist()
	m.mu.Unlock()

	writeStripeJSON(w, http.StatusOK, m.serializePaymentIntent(pi))
}

// retrievePaymentIntent serves GET /v1/payment_intents/{id}.
func (m *StripeMock) retrievePaymentIntent(w http.ResponseWriter, id string) {
	m.mu.RLock()
	pi, ok := m.paymentIntents[id]
	m.mu.RUnlock()
	if !ok {
		writeStripeError(w, http.StatusNotFound, "invalid_request_error",
			fmt.Sprintf("No such payment_intent: '%s'", id))
		return
	}
	writeStripeJSON(w, http.StatusOK, m.serializePaymentIntent(pi))
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
	m.persist()
	m.mu.Unlock()

	m.mu.RLock()
	out := m.serializePaymentIntent(pi)
	m.mu.RUnlock()
	writeStripeJSON(w, http.StatusOK, out)
}

// buildPaymentIntentFromParams projects the decoded params into the mock's
// internal shape with Stripe-equivalent validation.
func buildPaymentIntentFromParams(p map[string]any) (*stripePaymentIntent, error) {
	amount := getInt64(p, "amount")
	if amount <= 0 {
		return nil, errors.New("amount must be a positive integer")
	}
	currency := strings.ToLower(getString(p, "currency"))
	if currency == "" {
		return nil, errors.New("currency is required")
	}
	pi := &stripePaymentIntent{
		Amount:               amount,
		Currency:             currency,
		ApplicationFeeAmount: getInt64(p, "application_fee_amount"),
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
		pi.TransferDataAmount = getInt64(td, "amount")
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
// LatestCharge is rendered as an inline charge object when available so a
// single PI retrieve gives the app everything it needs to update its state.
//
// Caller must hold m.mu (RLock or Lock) when LatestChargeID is non-empty,
// because this method calls into m.charges.
func (m *StripeMock) serializePaymentIntent(pi *stripePaymentIntent) map[string]any {
	out := map[string]any{
		"id":                     pi.ID,
		"object":                 "payment_intent",
		"amount":                 pi.Amount,
		"amount_capturable":      int64(0),
		"amount_received":        int64(0),
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
	if pi.Status == "succeeded" {
		out["amount_received"] = pi.Amount
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
		if ch, ok := m.charges[pi.LatestChargeID]; ok {
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
