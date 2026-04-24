package devserver

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// stripePayout is the in-memory representation of a Stripe Payout — funds
// leaving Stripe to a connected account's bank (or the platform's). New
// payouts start in `pending`; the dev UI advances them to `paid` or `failed`.
//
// AccountID identifies which connected account the payout belongs to (read
// from the `Stripe-Account` request header at create time). Empty means the
// payout is on the platform's own balance.
type stripePayout struct {
	ID                  string            `json:"id"`
	Amount              int64             `json:"amount"`
	Currency            string            `json:"currency"`
	Status              string            `json:"status"`      // pending | in_transit | paid | failed | canceled
	Method              string            `json:"method"`      // standard | instant
	SourceType          string            `json:"source_type"` // bank_account | card | fpx
	Description         string            `json:"description,omitempty"`
	StatementDescriptor string            `json:"statement_descriptor,omitempty"`
	FailureCode         string            `json:"failure_code,omitempty"`
	FailureMessage      string            `json:"failure_message,omitempty"`
	Automatic           bool              `json:"automatic"`
	ArrivalDate         time.Time         `json:"arrival_date"`
	Created             time.Time         `json:"created"`
	AccountID           string            `json:"account_id,omitempty"` // Stripe-Account header at create time (empty = platform)
	Metadata            map[string]string `json:"metadata,omitempty"`
}

// registerPayoutRoutes mounts /v1/payouts endpoints. Called from
// RegisterAPIRoutes — kept private so callers go through one entry point.
func (m *StripeMock) registerPayoutRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/v1/payouts", m.handlePayouts)
	mux.HandleFunc("/v1/payouts/", m.handlePayoutByID)
}

// handlePayouts dispatches the collection endpoint. POST creates; GET lists.
// The list endpoint matters for marketplace dashboards showing payouts to a
// connected account: the Stripe-Account request header scopes the result.
func (m *StripeMock) handlePayouts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		m.createPayout(w, r)
	case http.MethodGet:
		m.listPayouts(w, r)
	default:
		writeStripeError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
	}
}

// handlePayoutByID dispatches GET /v1/payouts/{id}. Cancel + reverse are
// not yet mocked.
func (m *StripeMock) handlePayoutByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/payouts/")
	if id == "" || strings.Contains(id, "/") {
		writeStripeError(w, http.StatusNotFound, "invalid_request_error", "payout id required")
		return
	}
	if r.Method != http.MethodGet {
		writeStripeError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	m.mu.RLock()
	po, ok := m.payouts[id]
	m.mu.RUnlock()
	if !ok {
		writeStripeError(w, http.StatusNotFound, "invalid_request_error",
			fmt.Sprintf("No such payout: '%s'", id))
		return
	}
	writeStripeJSON(w, http.StatusOK, m.serializePayout(po))
}

// createPayout parses the form body and stores a new manually-triggered
// payout in `pending`. Real Stripe also creates payouts automatically on
// a schedule — that's not modelled; the dev triggers payouts via the API.
func (m *StripeMock) createPayout(w http.ResponseWriter, r *http.Request) {
	parsed, ok := readStripeForm(w, r)
	if !ok {
		return
	}

	po, err := buildPayoutFromParams(parsed)
	if err != nil {
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	po.ID = "po_test_" + randomHex(24)
	po.Created = time.Now()
	po.Status = "pending"
	po.Automatic = false
	// Real Stripe sets arrival_date based on the bank's clearing window —
	// 1-2 business days for standard, immediate for instant. Approximate.
	if po.Method == "instant" {
		po.ArrivalDate = po.Created
	} else {
		po.ArrivalDate = po.Created.Add(48 * time.Hour)
	}
	po.AccountID = strings.TrimSpace(r.Header.Get("Stripe-Account"))

	// If the payout claims to be from a connected account that doesn't
	// exist, mirror Stripe's 404 — protects against typos in dev.
	if po.AccountID != "" {
		m.mu.RLock()
		_, exists := m.accounts[po.AccountID]
		m.mu.RUnlock()
		if !exists {
			writeStripeError(w, http.StatusNotFound, "invalid_request_error",
				fmt.Sprintf("No such account: '%s'", po.AccountID))
			return
		}
	}

	m.mu.Lock()
	m.payouts[po.ID] = po
	m.persist()
	m.mu.Unlock()

	writeStripeJSON(w, http.StatusOK, m.serializePayout(po))
}

// listPayouts returns payouts scoped by Stripe-Account header. With no
// header, returns platform-balance payouts. Sorted newest-first like Stripe.
//
// Pagination params (limit, starting_after) are accepted but only `limit`
// is honored — full cursor pagination is overkill for a dev mock.
func (m *StripeMock) listPayouts(w http.ResponseWriter, r *http.Request) {
	scope := strings.TrimSpace(r.Header.Get("Stripe-Account"))
	limit := 10
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}

	m.mu.RLock()
	var matching []*stripePayout
	for _, po := range m.payouts {
		if po.AccountID == scope {
			matching = append(matching, po)
		}
	}
	m.mu.RUnlock()

	sort.Slice(matching, func(i, j int) bool {
		return matching[i].Created.After(matching[j].Created)
	})
	if len(matching) > limit {
		matching = matching[:limit]
	}

	out := make([]map[string]any, len(matching))
	for i, po := range matching {
		out[i] = m.serializePayout(po)
	}
	writeStripeJSON(w, http.StatusOK, map[string]any{
		"object":   "list",
		"url":      "/v1/payouts",
		"has_more": false,
		"data":     out,
	})
}

// buildPayoutFromParams projects the decoded form params into the mock's
// internal shape. Mirrors Stripe's required-field validation.
func buildPayoutFromParams(p map[string]any) (*stripePayout, error) {
	amount := getInt64(p, "amount")
	if amount <= 0 {
		return nil, errors.New("amount must be a positive integer")
	}
	currency := strings.ToLower(getString(p, "currency"))
	if currency == "" {
		return nil, errors.New("currency is required")
	}
	method := strings.ToLower(orDefault(getString(p, "method"), "standard"))
	if method != "standard" && method != "instant" {
		return nil, fmt.Errorf("invalid method %q (must be standard or instant)", method)
	}
	return &stripePayout{
		Amount:              amount,
		Currency:            currency,
		Method:              method,
		SourceType:          orDefault(getString(p, "source_type"), "bank_account"),
		Description:         getString(p, "description"),
		StatementDescriptor: getString(p, "statement_descriptor"),
		Metadata:            stringMap(p, "metadata"),
	}, nil
}

// serializePayout renders the JSON wire shape stripe-go expects.
func (m *StripeMock) serializePayout(p *stripePayout) map[string]any {
	out := map[string]any{
		"id":                   p.ID,
		"object":               "payout",
		"amount":               p.Amount,
		"arrival_date":         p.ArrivalDate.Unix(),
		"automatic":            p.Automatic,
		"created":              p.Created.Unix(),
		"currency":             p.Currency,
		"description":          nullableString(p.Description),
		"failure_code":         nullableString(p.FailureCode),
		"failure_message":      nullableString(p.FailureMessage),
		"livemode":             false,
		"method":               p.Method,
		"source_type":          p.SourceType,
		"statement_descriptor": nullableString(p.StatementDescriptor),
		"status":               p.Status,
		"type":                 "bank_account",
	}
	if p.Metadata != nil {
		out["metadata"] = p.Metadata
	} else {
		out["metadata"] = map[string]string{}
	}
	return out
}
