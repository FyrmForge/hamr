// Package devserver — Stripe mock backend.
//
// This file implements just enough of Stripe's HTTP API to round-trip cleanly
// through the official stripe-go SDK. Apps point stripe-go at the dev proxy
// via stripe.SetBackend(...) with BackendConfig.URL = "<HAMR_DEV_URL>/__hamr/stripe",
// and stripe-go appends paths like /v1/checkout/sessions to that base.
//
// The mock is dev-only and has no production safeguards beyond the gating
// done by hamr.toml [dev.stripe].
package devserver

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// stripeAPIVersion is the Stripe API version this mock emits responses for.
// MUST match stripe-go/v82's stripe.APIVersion constant. CI test enforces
// the link so a stripe-go bump cannot land without bumping this string.
const stripeAPIVersion = "2025-08-27.basil"

// StripeMock is a dev-only in-memory Stripe backend. Routes implement enough
// of /v1/* for stripe-go to round-trip CheckoutSession create/retrieve.
type StripeMock struct {
	baseURL     string // origin used to build user-facing checkout URLs (e.g. "http://localhost:3000")
	logger      *slog.Logger
	persistPath string         // empty = in-memory only
	persistErr  func(error)    // callback for persist errors; nil = silent

	mu             sync.RWMutex
	sessions       map[string]*stripeSession
	accounts       map[string]*stripeAccount
	paymentIntents map[string]*stripePaymentIntent
	charges        map[string]*stripeCharge
	transfers      map[string]*stripeTransfer
	refunds        map[string]*stripeRefund
	payouts        map[string]*stripePayout
	webhookEP      WebhookEndpoint // destination + secret for outbound signed events; zero value disables firing
	whClient       *http.Client    // lazy-initialized in webhookHTTP()
}

// StripeMockOptions configures a StripeMock at construction.
type StripeMockOptions struct {
	// BaseURL is the proxy origin (scheme + host + port) used to build the
	// hosted-checkout URL returned in CheckoutSession.URL. Required.
	BaseURL string

	// Logger receives errors from async webhook fanout. Defaults to slog.Default().
	Logger *slog.Logger

	// PersistPath enables JSON-file persistence of all in-memory state.
	// When set, state is loaded on construction (corrupt/missing files are
	// silently tolerated) and the entire state is atomically rewritten on
	// every mutation. Empty = in-memory only.
	PersistPath string

	// OnPersistError is invoked whenever a load or write fails. Typically
	// wired to a slog.Warn so dev failures surface in `hamr dev` output.
	// Nil = silent.
	OnPersistError func(error)
}

// NewStripeMock returns a mock backend, loading any persisted state if
// PersistPath is set. Load failures are reported via OnPersistError but
// never fatal — the in-memory state simply starts empty.
//
// All log lines from the mock are prefixed with [hamr:stripe] (via the
// "component" slog attr that the dev handler interprets as a tag override)
// so they're distinguishable from the rest of `hamr dev`'s output.
func NewStripeMock(opts StripeMockOptions) *StripeMock {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("component", "stripe")
	m := &StripeMock{
		baseURL:        strings.TrimRight(opts.BaseURL, "/"),
		logger:         logger,
		persistPath:    opts.PersistPath,
		persistErr:     opts.OnPersistError,
		sessions:       map[string]*stripeSession{},
		accounts:       map[string]*stripeAccount{},
		paymentIntents: map[string]*stripePaymentIntent{},
		charges:        map[string]*stripeCharge{},
		transfers:      map[string]*stripeTransfer{},
		refunds:        map[string]*stripeRefund{},
		payouts:        map[string]*stripePayout{},
	}
	m.loadFromDisk()
	return m
}

// RegisterAPIRoutes mounts the Stripe API endpoints on mux. The mux MUST be
// served at the root of its listener (e.g. on a dedicated stripe-only port)
// because stripe-go validates that req.URL.Path starts with /v1 and rejects
// anything served under a sub-path.
//
//	POST /v1/checkout/sessions       — create session
//	GET  /v1/checkout/sessions/{id}  — retrieve session
func (m *StripeMock) RegisterAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/v1/checkout/sessions", m.handleCheckoutSessions)
	mux.HandleFunc("/v1/checkout/sessions/", m.handleCheckoutSessionByID)
	m.registerAccountRoutes(mux)
	m.registerPaymentIntentRoutes(mux)
	m.registerRefundRoutes(mux)
	m.registerPayoutRoutes(mux)
}

// stripeSession is the in-memory representation. Mirrors the subset of
// CheckoutSession fields apps actually inspect; everything else is omitted
// from responses (stripe-go ignores unknown JSON fields, missing fields
// default to zero values).
type stripeSession struct {
	ID              string            `json:"id"`
	PaymentIntentID string            `json:"payment_intent_id"`
	Created         time.Time         `json:"created"`
	Mode            string            `json:"mode"`
	Currency        string            `json:"currency"`
	AmountTotal     int64             `json:"amount_total"`
	LineItems       []stripeLineItem  `json:"line_items"`
	SuccessURL      string            `json:"success_url"`
	CancelURL       string            `json:"cancel_url"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	Status          string            `json:"status"`         // "open" | "complete" | "expired"
	PaymentStatus   string            `json:"payment_status"` // "paid" | "unpaid" | "no_payment_required"
}

type stripeLineItem struct {
	Name       string `json:"name"`
	UnitAmount int64  `json:"unit_amount"`
	Quantity   int64  `json:"quantity"`
	Currency   string `json:"currency"`
}

// handleCheckoutSessions handles the collection endpoint. POST creates;
// GET (list) is not implemented yet.
func (m *StripeMock) handleCheckoutSessions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		m.createCheckoutSession(w, r)
	default:
		writeStripeError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
	}
}

// handleCheckoutSessionByID handles GET /v1/checkout/sessions/{id}.
func (m *StripeMock) handleCheckoutSessionByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/checkout/sessions/")
	if id == "" || strings.Contains(id, "/") {
		writeStripeError(w, http.StatusNotFound, "invalid_request_error", "session id required")
		return
	}
	if r.Method != http.MethodGet {
		writeStripeError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}

	m.mu.RLock()
	sess, ok := m.sessions[id]
	m.mu.RUnlock()
	if !ok {
		writeStripeError(w, http.StatusNotFound, "invalid_request_error",
			fmt.Sprintf("No such checkout.session: '%s'", id))
		return
	}
	writeStripeJSON(w, http.StatusOK, m.serializeSession(sess))
}

// createCheckoutSession parses Stripe's bracket-form-encoded POST body, builds
// an in-memory session, and returns a checkout.session-shaped JSON response.
func (m *StripeMock) createCheckoutSession(w http.ResponseWriter, r *http.Request) {
	parsed, ok := readStripeForm(w, r)
	if !ok {
		return
	}

	sess, err := buildSessionFromParams(parsed)
	if err != nil {
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	sess.ID = "cs_test_" + randomHex(24)
	sess.PaymentIntentID = "pi_test_" + randomHex(24)
	sess.Created = time.Now()
	sess.Status = "open"
	sess.PaymentStatus = "unpaid"

	m.mu.Lock()
	m.sessions[sess.ID] = sess
	m.persist()
	m.mu.Unlock()

	writeStripeJSON(w, http.StatusOK, m.serializeSession(sess))
}

// buildSessionFromParams projects the decoded Stripe params into the mock's
// internal session shape. Mirrors enough of Stripe's validation to fail
// loudly on the most common mistakes (missing line items, mixed currencies).
func buildSessionFromParams(p map[string]any) (*stripeSession, error) {
	s := &stripeSession{
		Mode:       getString(p, "mode"),
		SuccessURL: getString(p, "success_url"),
		CancelURL:  getString(p, "cancel_url"),
		Metadata:   stringMap(p, "metadata"),
	}
	if s.Mode == "" {
		s.Mode = "payment"
	}

	rawItems, ok := p["line_items"].([]any)
	if !ok || len(rawItems) == 0 {
		return nil, errors.New("line_items is required and must be a non-empty array")
	}
	for i, raw := range rawItems {
		itemMap, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("line_items[%d] is not an object", i)
		}
		li, err := buildLineItem(itemMap, i)
		if err != nil {
			return nil, err
		}
		if s.Currency == "" {
			s.Currency = li.Currency
		} else if s.Currency != li.Currency {
			return nil, fmt.Errorf(
				"line_items[%d].price_data.currency=%q does not match earlier currency %q (a session may only have one currency)",
				i, li.Currency, s.Currency)
		}
		s.LineItems = append(s.LineItems, li)
		s.AmountTotal += li.UnitAmount * li.Quantity
	}
	return s, nil
}

// buildLineItem extracts a single line item from a decoded params map.
// Supports inline price_data only — referencing existing prices by ID is a
// later concern.
func buildLineItem(item map[string]any, idx int) (stripeLineItem, error) {
	qty := getInt64(item, "quantity")
	if qty <= 0 {
		qty = 1
	}
	priceData, ok := item["price_data"].(map[string]any)
	if !ok {
		return stripeLineItem{}, fmt.Errorf("line_items[%d].price_data is required (referencing existing price IDs is not yet mocked)", idx)
	}
	currency := strings.ToLower(getString(priceData, "currency"))
	if currency == "" {
		return stripeLineItem{}, fmt.Errorf("line_items[%d].price_data.currency is required", idx)
	}
	unitAmount := getInt64(priceData, "unit_amount")
	if unitAmount < 0 {
		return stripeLineItem{}, fmt.Errorf("line_items[%d].price_data.unit_amount must be non-negative", idx)
	}
	productData, _ := priceData["product_data"].(map[string]any)
	name := getString(productData, "name")

	return stripeLineItem{
		Name:       name,
		UnitAmount: unitAmount,
		Quantity:   qty,
		Currency:   currency,
	}, nil
}

// serializeSession builds the JSON wire representation matching Stripe's
// checkout.session object. Only fields the mock tracks are populated;
// everything else is null/zero, which stripe-go's struct unmarshaler treats
// as unset.
func (m *StripeMock) serializeSession(s *stripeSession) map[string]any {
	out := map[string]any{
		"id":              s.ID,
		"object":          "checkout.session",
		"created":         s.Created.Unix(),
		"livemode":        false,
		"mode":            s.Mode,
		"currency":        s.Currency,
		"amount_total":    s.AmountTotal,
		"amount_subtotal": s.AmountTotal,
		"payment_intent":  s.PaymentIntentID,
		"success_url":     s.SuccessURL,
		"cancel_url":      s.CancelURL,
		"status":          s.Status,
		"payment_status":  s.PaymentStatus,
		"url":             m.baseURL + "/__hamr/stripe/checkout?session=" + s.ID,
	}
	if s.Metadata != nil {
		out["metadata"] = s.Metadata
	} else {
		out["metadata"] = map[string]string{}
	}
	return out
}

// --- helpers ---

// readLimitedBody reads up to 1 MiB from a request body. Stripe form bodies
// are tiny in practice; the cap exists to keep a misbehaving caller from
// holding the mock open. Closing the body is the caller's responsibility.
func readLimitedBody(r *http.Request) ([]byte, error) {
	defer r.Body.Close() //nolint:errcheck
	return io.ReadAll(io.LimitReader(r.Body, 1<<20))
}

// getString reads a string at key, tolerating nil maps (returns "").
func getString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// getInt64 reads a base-10 integer at key. Form-decoded values are always
// strings, so we parse here. Missing/empty/unparseable returns 0.
func getInt64(m map[string]any, key string) int64 {
	s := getString(m, key)
	if s == "" {
		return 0
	}
	var n int64
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int64(r-'0')
	}
	return n
}

// stringMap converts a decoded map[string]any whose values are all strings
// into map[string]string. Returns nil if missing or empty. Used for metadata.
func stringMap(m map[string]any, key string) map[string]string {
	raw, ok := m[key].(map[string]any)
	if !ok || len(raw) == 0 {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

// writeStripeJSON writes a successful Stripe-shaped response with the API
// version header set so stripe-go's logging treats it as a normal API hit.
func writeStripeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Stripe-Version", stripeAPIVersion)
	w.Header().Set("Request-Id", "req_mock_"+randomHex(8))
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

// writeStripeError matches Stripe's top-level error shape so stripe-go
// surfaces a typed *stripe.Error to callers instead of a generic decode
// failure.
func writeStripeError(w http.ResponseWriter, code int, errType, msg string) {
	writeStripeJSON(w, code, map[string]any{
		"error": map[string]any{
			"type":    errType,
			"message": msg,
		},
	})
}

func randomHex(n int) string {
	b := make([]byte, n/2+1)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:n]
}

// proxyClientBaseURLFromPort returns the proxy's client-reachable origin
// ("http://localhost:<port>") for an actual bound port. Always http:// — the
// mock is localhost-only by design. Used after the proxy listener has been
// established so the URL reflects any +1-on-busy port walking that occurred,
// rather than the value originally written in hamr.toml.
//
// The host portion always renders as "localhost" — listen addresses like
// ":3001" or "0.0.0.0:3001" are bind-side concerns, not client-reachable
// destinations.
func proxyClientBaseURLFromPort(port int) string {
	return fmt.Sprintf("http://localhost:%d", port)
}
