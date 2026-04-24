package devserver

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// stripeAccount is the in-memory representation of a Connect connected
// account. Only fields the mock tracks are populated; the rest is omitted
// from responses (stripe-go ignores unknown fields, missing fields default
// to zero values).
type stripeAccount struct {
	ID               string            `json:"id"`
	Type             string            `json:"type"`          // "express" | "standard" | "custom"
	BusinessType     string            `json:"business_type"` // "individual" | "company" | etc.
	Country          string            `json:"country"`
	Email            string            `json:"email"`
	DefaultCurrency  string            `json:"default_currency"`
	ChargesEnabled   bool              `json:"charges_enabled"`
	PayoutsEnabled   bool              `json:"payouts_enabled"`
	DetailsSubmitted bool              `json:"details_submitted"`
	CurrentlyDue     []string          `json:"currently_due,omitempty"`
	DisabledReason   string            `json:"disabled_reason,omitempty"`
	Created          time.Time         `json:"created"`
	Metadata         map[string]string `json:"metadata,omitempty"`
}

// stripeAccountLink is the in-memory representation of an onboarding link.
// expires_at is a real value so dev callers can observe Stripe-shaped TTLs;
// the mock doesn't actually expire links (the underlying account stays valid).
type stripeAccountLink struct {
	URL       string    `json:"url"`
	Created   time.Time `json:"created"`
	ExpiresAt time.Time `json:"expires_at"`
}

// registerAccountRoutes mounts the Account + AccountLink endpoints.
// Called from RegisterAPIRoutes — kept private so callers go through the
// single entry point.
func (m *StripeMock) registerAccountRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/v1/accounts", m.handleAccounts)
	mux.HandleFunc("/v1/accounts/", m.handleAccountByID)
	mux.HandleFunc("/v1/account_links", m.handleAccountLinks)
}

// handleAccounts dispatches the collection endpoint. POST creates; list is
// not implemented (the dev UI shows accounts via the in-memory map, not via
// the public API).
func (m *StripeMock) handleAccounts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		m.createAccount(w, r)
	default:
		writeStripeError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
	}
}

// handleAccountByID handles GET /v1/accounts/{id}. Account update (POST
// with the same path) is not yet mocked — dev UI handles state changes.
func (m *StripeMock) handleAccountByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/accounts/")
	if id == "" || strings.Contains(id, "/") {
		writeStripeError(w, http.StatusNotFound, "invalid_request_error", "account id required")
		return
	}
	if r.Method != http.MethodGet {
		writeStripeError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}

	m.mu.RLock()
	acct, ok := m.accounts[id]
	m.mu.RUnlock()
	if !ok {
		writeStripeError(w, http.StatusNotFound, "invalid_request_error",
			fmt.Sprintf("No such account: '%s'", id))
		return
	}
	writeStripeJSON(w, http.StatusOK, m.serializeAccount(acct))
}

// createAccount parses the form-encoded POST body and stores a new
// connected account in the "needs onboarding" state: all enabled flags
// false, requirements.currently_due populated. Mirrors what real Stripe
// does for a freshly created Express account before onboarding.
func (m *StripeMock) createAccount(w http.ResponseWriter, r *http.Request) {
	parsed, ok := readStripeForm(w, r)
	if !ok {
		return
	}

	acct := &stripeAccount{
		ID:           "acct_test_" + randomHex(16),
		Type:         orDefault(getString(parsed, "type"), "express"),
		BusinessType: getString(parsed, "business_type"),
		Country:      orDefault(getString(parsed, "country"), "US"),
		Email:        getString(parsed, "email"),
		Created:      time.Now(),
		Metadata:     stringMap(parsed, "metadata"),
		// Pre-onboarding defaults: nothing enabled, several fields outstanding.
		ChargesEnabled:   false,
		PayoutsEnabled:   false,
		DetailsSubmitted: false,
		CurrentlyDue: []string{
			"business_type",
			"external_account",
			"tos_acceptance.date",
			"tos_acceptance.ip",
		},
		DisabledReason: "requirements.past_due",
	}
	if acct.DefaultCurrency == "" {
		// Country-driven default; close enough for dev.
		switch strings.ToUpper(acct.Country) {
		case "GB":
			acct.DefaultCurrency = "gbp"
		case "EU", "DE", "FR", "IT", "ES", "NL":
			acct.DefaultCurrency = "eur"
		case "JP":
			acct.DefaultCurrency = "jpy"
		default:
			acct.DefaultCurrency = "usd"
		}
	}

	m.mu.Lock()
	m.accounts[acct.ID] = acct
	m.persist()
	m.mu.Unlock()

	writeStripeJSON(w, http.StatusOK, m.serializeAccount(acct))
}

// handleAccountLinks creates an onboarding link for an existing account.
// The returned URL points at the dev onboarding page; "expires_at" is set
// to mirror Stripe's 5-minute TTL for visibility, though the mock doesn't
// enforce it (the underlying account stays valid for the dev session).
func (m *StripeMock) handleAccountLinks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeStripeError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	parsed, ok := readStripeForm(w, r)
	if !ok {
		return
	}
	acctID := getString(parsed, "account")
	if acctID == "" {
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error", "account is required")
		return
	}

	m.mu.RLock()
	_, exists := m.accounts[acctID]
	m.mu.RUnlock()
	if !exists {
		writeStripeError(w, http.StatusNotFound, "invalid_request_error",
			fmt.Sprintf("No such account: '%s'", acctID))
		return
	}

	link := &stripeAccountLink{
		URL:       m.baseURL + "/__hamr/stripe/onboarding?account=" + url.QueryEscape(acctID),
		Created:   time.Now(),
		ExpiresAt: time.Now().Add(5 * time.Minute),
	}

	writeStripeJSON(w, http.StatusOK, map[string]any{
		"object":     "account_link",
		"created":    link.Created.Unix(),
		"expires_at": link.ExpiresAt.Unix(),
		"url":        link.URL,
	})
}

// serializeAccount renders the JSON wire shape matching Stripe's account
// object. Nested capabilities/business_profile etc. are omitted — stripe-go
// unmarshals missing fields as nil/zero, which is fine for dev.
func (m *StripeMock) serializeAccount(a *stripeAccount) map[string]any {
	out := map[string]any{
		"id":                a.ID,
		"object":            "account",
		"type":              a.Type,
		"business_type":     a.BusinessType,
		"country":           a.Country,
		"email":             a.Email,
		"default_currency":  a.DefaultCurrency,
		"charges_enabled":   a.ChargesEnabled,
		"payouts_enabled":   a.PayoutsEnabled,
		"details_submitted": a.DetailsSubmitted,
		"created":           a.Created.Unix(),
		"requirements": map[string]any{
			"currently_due":   a.CurrentlyDue,
			"disabled_reason": a.DisabledReason,
			// Empty slices for fields we don't model — stripe-go expects []
			// not null on these so iterations don't NPE.
			"alternatives":         []any{},
			"errors":               []any{},
			"eventually_due":       []any{},
			"past_due":             []any{},
			"pending_verification": []any{},
		},
	}
	if a.Metadata != nil {
		out["metadata"] = a.Metadata
	} else {
		out["metadata"] = map[string]string{}
	}
	return out
}

// readStripeForm reads + parses + bracket-decodes a Stripe form body. On
// any error it writes a Stripe-shaped 400 and returns ok=false so the
// handler can early-return without further work.
func readStripeForm(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	body, err := readLimitedBody(r)
	if err != nil {
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error", "read body: "+err.Error())
		return nil, false
	}
	values, err := url.ParseQuery(string(body))
	if err != nil {
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error", "parse form: "+err.Error())
		return nil, false
	}
	parsed, err := decodeBracketForm(values)
	if err != nil {
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, false
	}
	return parsed, true
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
