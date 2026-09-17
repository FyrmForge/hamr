package devserver

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// registerTransferRoutes mounts the separate-charges-and-transfers endpoints.
//
//	POST /v1/transfers                  — send money to a connected account
//	GET  /v1/transfers/{id}             — retrieve
//	POST /v1/transfers/{id}/reversals   — pull some or all of it back
func (m *StripeMock) registerTransferRoutes(mux stripeRouter) {
	mux.HandleFunc("/v1/transfers", m.handleTransfers)
	mux.HandleFunc("/v1/transfers/", m.handleTransferByID)
}

func (m *StripeMock) handleTransfers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeStripeError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	m.createTransfer(w, r)
}

func (m *StripeMock) handleTransferByID(w http.ResponseWriter, r *http.Request) {
	id, action, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/v1/transfers/"), "/")
	switch {
	case id == "":
		writeStripeError(w, http.StatusNotFound, "invalid_request_error", "transfer id required")
	case action == "" && r.Method == http.MethodGet:
		m.mu.RLock()
		tr := cloneTransfer(m.transfers[id])
		m.mu.RUnlock()
		if tr == nil {
			writeStripeError(w, http.StatusNotFound, "invalid_request_error", fmt.Sprintf("No such transfer: '%s'", id))
			return
		}
		writeStripeJSON(w, http.StatusOK, m.serializeTransfer(tr))
	case action == "reversals" && r.Method == http.MethodPost:
		m.createTransferReversal(w, r, id)
	default:
		writeStripeError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
	}
}

// createTransfer moves money from the platform balance to a connected
// account. Like Stripe, a v2 account must have its recipient
// stripe_balance.stripe_transfers capability active first — the refusal an
// app sees when it pays a seller who has not finished onboarding.
func (m *StripeMock) createTransfer(w http.ResponseWriter, r *http.Request) {
	p, ok := readStripeForm(w, r)
	if !ok {
		return
	}
	amount, ok := getInt64(p, "amount")
	if !ok || amount <= 0 {
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error", "amount must be a positive integer")
		return
	}
	currency := strings.ToLower(getString(p, "currency"))
	dest := getString(p, "destination")
	if currency == "" || dest == "" {
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error", "currency and destination are required")
		return
	}

	m.mu.Lock()
	if msg, code := m.transferDestinationProblem(dest); msg != "" {
		m.mu.Unlock()
		writeStripeErrorCode(w, http.StatusBadRequest, "invalid_request_error", code, msg)
		return
	}
	src := getString(p, "source_transaction")
	if src != "" {
		ch, exists := m.charges[src]
		switch {
		case !exists:
			m.mu.Unlock()
			writeStripeError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("No such charge: '%s'", src))
			return
		case ch.Currency != currency:
			m.mu.Unlock()
			writeStripeError(w, http.StatusBadRequest, "invalid_request_error",
				fmt.Sprintf("currency %s does not match source_transaction currency %s", currency, ch.Currency))
			return
		case amount > m.chargeAvailableLocked(ch):
			m.mu.Unlock()
			writeStripeErrorCode(w, http.StatusBadRequest, "invalid_request_error", "balance_insufficient",
				fmt.Sprintf("transfer amount %d exceeds the %d available on source_transaction %s", amount, m.chargeAvailableLocked(ch), src))
			return
		}
	} else if have := balanceOf(m.platformLedger())[currency]; amount > have {
		m.mu.Unlock()
		writeStripeErrorCode(w, http.StatusBadRequest, "invalid_request_error", "balance_insufficient",
			fmt.Sprintf("transfer amount %d exceeds the platform's %d %s balance", amount, have, currency))
		return
	}
	tr := &stripeTransfer{
		ID:                  "tr_test_" + randomHex(24),
		Amount:              amount,
		Currency:            currency,
		Destination:         dest,
		SourceTransactionID: src,
		TransferGroup:       getString(p, "transfer_group"),
		Description:         getString(p, "description"),
		Created:             time.Now(),
		Metadata:            stringMap(p, "metadata"),
	}
	m.transfers[tr.ID] = tr
	obj := m.serializeTransfer(tr)
	m.persist()
	m.mu.Unlock()

	m.fireEventAsync("transfer.created", obj, "transfer", tr.ID)
	writeStripeJSON(w, http.StatusOK, obj)
}

// chargeAvailableLocked is what is left of a charge to fund transfers:
// captured, minus refunds, minus transfers already drawn from it (net of
// their reversals). Caller holds m.mu.
func (m *StripeMock) chargeAvailableLocked(ch *stripeCharge) int64 {
	left := ch.AmountCaptured - ch.AmountRefunded
	for _, tr := range m.transfers {
		if tr.SourceTransactionID == ch.ID {
			left -= tr.Amount - tr.AmountReversed
		}
	}
	return left
}

// transferDestinationProblem returns Stripe's refusal for an account that
// cannot receive transfers, or "" when it can. Caller holds m.mu.
// ponytail: v1 accounts are not capability-checked; the mock never modelled
// their capabilities.
func (m *StripeMock) transferDestinationProblem(dest string) (msg, code string) {
	if _, ok := m.accounts[dest]; ok {
		return "", ""
	}
	acct, ok := m.v2Accounts[dest]
	if !ok {
		return fmt.Sprintf("No such destination: '%s'", dest), "resource_missing"
	}
	if acct.Capabilities[capRecipientTransfers] != "active" {
		return "Your destination account needs to have at least one of the following capabilities enabled: transfers, crypto_transfers, legacy_payments",
			"insufficient_capabilities_for_transfer"
	}
	return "", ""
}

// createTransferReversal reverses some (amount) or all of a transfer and
// fires transfer.reversed with the updated transfer, reversals included.
func (m *StripeMock) createTransferReversal(w http.ResponseWriter, r *http.Request, id string) {
	p, ok := readStripeForm(w, r)
	if !ok {
		return
	}
	amount, ok := getInt64(p, "amount")
	if !ok || amount < 0 {
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error", "amount must be a positive integer")
		return
	}

	m.mu.Lock()
	tr, exists := m.transfers[id]
	if !exists {
		m.mu.Unlock()
		writeStripeError(w, http.StatusNotFound, "invalid_request_error", fmt.Sprintf("No such transfer: '%s'", id))
		return
	}
	remaining := tr.Amount - tr.AmountReversed
	if amount == 0 {
		amount = remaining
	}
	if remaining <= 0 || amount > remaining {
		m.mu.Unlock()
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error",
			fmt.Sprintf("reversal amount %d exceeds the %d left on transfer %s", amount, remaining, id))
		return
	}
	rv := m.reverseTransferLocked(tr, amount, "", stringMap(p, "metadata"))
	out := serializeTransferReversal(tr, &rv)
	obj := m.serializeTransfer(tr)
	m.persist()
	m.mu.Unlock()

	m.fireEventAsync("transfer.reversed", obj, "transfer", id)
	writeStripeJSON(w, http.StatusOK, out)
}

// reverseTransferLocked records a reversal on tr. Caller holds m.mu and has
// checked amount fits.
func (m *StripeMock) reverseTransferLocked(tr *stripeTransfer, amount int64, refundID string, meta map[string]string) stripeTransferReversal {
	rv := stripeTransferReversal{
		ID:       "trr_test_" + randomHex(24),
		Amount:   amount,
		RefundID: refundID,
		Created:  time.Now(),
		Metadata: meta,
	}
	tr.AmountReversed += amount
	tr.Reversals = append(tr.Reversals, rv)
	return rv
}

func serializeTransferReversal(tr *stripeTransfer, rv *stripeTransferReversal) map[string]any {
	meta := rv.Metadata
	if meta == nil {
		meta = map[string]string{}
	}
	return map[string]any{
		"id":            rv.ID,
		"object":        "transfer_reversal",
		"amount":        rv.Amount,
		"created":       rv.Created.Unix(),
		"currency":      tr.Currency,
		"transfer":      tr.ID,
		"source_refund": nullableString(rv.RefundID),
		"metadata":      meta,
	}
}
