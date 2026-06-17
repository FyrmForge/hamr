package devserver

import (
	"time"
)

// stripeCharge is the in-memory representation of a Stripe Charge —
// auto-created when a PaymentIntent transitions to succeeded. Apps usually
// don't create Charges directly anymore (the modern surface is PaymentIntent
// + Charge-as-side-effect), so the mock doesn't expose POST /v1/charges.
//
// Connect-aware fields:
//   - Destination: the connected account funds were transferred to (set when
//     the source PaymentIntent had transfer_data.destination)
//   - ApplicationFeeAmount: platform commission collected from this charge
//   - TransferID: the auto-created Transfer's ID (destination-charge model)
type stripeCharge struct {
	ID                   string            `json:"id"`
	Amount               int64             `json:"amount"`
	AmountCaptured       int64             `json:"amount_captured"`
	AmountRefunded       int64             `json:"amount_refunded"`
	Currency             string            `json:"currency"`
	Status               string            `json:"status"` // succeeded | pending | failed
	Paid                 bool              `json:"paid"`
	Captured             bool              `json:"captured"`
	Refunded             bool              `json:"refunded"`
	PaymentIntentID      string            `json:"payment_intent_id,omitempty"`
	PaymentMethod        string            `json:"payment_method,omitempty"`
	ApplicationFeeAmount int64             `json:"application_fee_amount,omitempty"`
	Destination          string            `json:"destination,omitempty"` // connected account funds went to (transfer_data.destination)
	TransferID           string            `json:"transfer_id,omitempty"` // auto-created Transfer for destination charges
	Description          string            `json:"description,omitempty"`
	ReceiptEmail         string            `json:"receipt_email,omitempty"`
	Customer             string            `json:"customer,omitempty"`
	Created              time.Time         `json:"created"`
	Metadata             map[string]string `json:"metadata,omitempty"`
}

// stripeTransfer is the in-memory representation of a Stripe Transfer —
// the Connect resource that moves money from the platform's balance to a
// connected account. Auto-created when a PaymentIntent with
// transfer_data.destination succeeds; the destination receives
// (Amount - ApplicationFeeAmount) by default.
type stripeTransfer struct {
	ID                  string            `json:"id"`
	Amount              int64             `json:"amount"`
	AmountReversed      int64             `json:"amount_reversed,omitempty"` // accumulated reversals from refunds with reverse_transfer=true
	Currency            string            `json:"currency"`
	Destination         string            `json:"destination"`           // connected account ID (acct_xxx)
	SourceTransactionID string            `json:"source_transaction_id"` // the Charge ID that triggered this transfer
	Created             time.Time         `json:"created"`
	Metadata            map[string]string `json:"metadata,omitempty"`
}

// serializeCharge renders the JSON wire shape stripe-go expects for a
// Charge. Contract: c must be lock-stable — either hold m.mu across the call
// or pass a clone (see stripemock_clone.go). This method reads only c.
func (m *StripeMock) serializeCharge(c *stripeCharge) map[string]any {
	out := map[string]any{
		"id":                     c.ID,
		"object":                 "charge",
		"amount":                 c.Amount,
		"amount_captured":        c.AmountCaptured,
		"amount_refunded":        c.AmountRefunded,
		"application_fee_amount": c.ApplicationFeeAmount,
		"captured":               c.Captured,
		"created":                c.Created.Unix(),
		"currency":               c.Currency,
		"customer":               nullableString(c.Customer),
		"description":            nullableString(c.Description),
		"livemode":               false,
		"paid":                   c.Paid,
		"payment_intent":         c.PaymentIntentID,
		"payment_method":         nullableString(c.PaymentMethod),
		"receipt_email":          nullableString(c.ReceiptEmail),
		"refunded":               c.Refunded,
		"status":                 c.Status,
	}
	if c.Metadata != nil {
		out["metadata"] = c.Metadata
	} else {
		out["metadata"] = map[string]string{}
	}
	if c.Destination != "" {
		out["destination"] = c.Destination
	}
	if c.TransferID != "" {
		out["transfer"] = c.TransferID
	}
	return out
}

// serializeTransfer renders a Transfer object. Used as the data.object on
// transfer.created webhook events.
func (m *StripeMock) serializeTransfer(t *stripeTransfer) map[string]any {
	out := map[string]any{
		"id":                 t.ID,
		"object":             "transfer",
		"amount":             t.Amount,
		"amount_reversed":    t.AmountReversed,
		"created":            t.Created.Unix(),
		"currency":           t.Currency,
		"destination":        t.Destination,
		"livemode":           false,
		"reversed":           t.AmountReversed >= t.Amount,
		"source_transaction": t.SourceTransactionID,
	}
	if t.Metadata != nil {
		out["metadata"] = t.Metadata
	} else {
		out["metadata"] = map[string]string{}
	}
	return out
}
