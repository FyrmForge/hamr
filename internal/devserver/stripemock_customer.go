package devserver

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Customers are created by POST /v1/customers or implicitly when a
// mode=subscription Checkout Session is paid without customer=. Only the
// fields a subscription flow reads back are kept.
//
// ponytail: no delete, no payment methods, no tax ids, no test_clock.

// stripeCustomer is a Stripe Customer.
type stripeCustomer struct {
	ID          string            `json:"id"`
	Email       string            `json:"email,omitempty"`
	Name        string            `json:"name,omitempty"`
	Description string            `json:"description,omitempty"`
	Created     time.Time         `json:"created"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

func cloneCustomer(c *stripeCustomer) *stripeCustomer {
	if c == nil {
		return nil
	}
	cc := *c
	cc.Metadata = cloneStringMap(c.Metadata)
	return &cc
}

// serializeCustomer renders the customer object stripe-go decodes.
func (m *StripeMock) serializeCustomer(c *stripeCustomer) map[string]any {
	return map[string]any{
		"id":             c.ID,
		"object":         "customer",
		"created":        c.Created.Unix(),
		"email":          nullableString(c.Email),
		"name":           nullableString(c.Name),
		"description":    nullableString(c.Description),
		"currency":       nil,
		"delinquent":     false,
		"livemode":       false,
		"balance":        0,
		"invoice_prefix": "MOCK",
		"metadata":       orEmptyMap(c.Metadata),
	}
}

// customerEmailLocked is the stored customer's email, "" when unknown.
// Caller holds m.mu.
func (m *StripeMock) customerEmailLocked(id string) (email, name string) {
	if c, ok := m.customers[id]; ok {
		return c.Email, c.Name
	}
	return "", ""
}

// orEmptyMap renders nil metadata as {} the way Stripe does.
func orEmptyMap(md map[string]string) map[string]string {
	if md == nil {
		return map[string]string{}
	}
	return md
}

// registerCustomerRoutes mounts /v1/customers.
func (m *StripeMock) registerCustomerRoutes(mux stripeRouter) {
	mux.HandleFunc("/v1/customers", m.handleCustomers)
	mux.HandleFunc("/v1/customers/", m.handleCustomerByID)
}

// handleCustomers handles POST (create) and GET (list) on the collection.
func (m *StripeMock) handleCustomers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		m.createCustomer(w, r)
	case http.MethodGet:
		m.listCustomers(w, r)
	default:
		writeStripeError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
	}
}

// handleCustomerByID handles GET (retrieve) and POST (update: email, name,
// description, metadata) on /v1/customers/{id}.
func (m *StripeMock) handleCustomerByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/customers/")
	if id == "" || strings.Contains(id, "/") {
		writeStripeError(w, http.StatusNotFound, "invalid_request_error", "customer id required")
		return
	}
	var p map[string]any
	if r.Method == http.MethodPost {
		var ok bool
		if p, ok = readStripeForm(w, r); !ok {
			return
		}
	} else if r.Method != http.MethodGet {
		writeStripeError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}

	m.mu.Lock()
	c, ok := m.customers[id]
	if !ok {
		m.mu.Unlock()
		writeStripeErrorCode(w, http.StatusNotFound, "invalid_request_error", "resource_missing",
			fmt.Sprintf("No such customer: '%s'", id))
		return
	}
	if p != nil {
		if _, set := p["email"]; set {
			c.Email = getString(p, "email")
		}
		if _, set := p["name"]; set {
			c.Name = getString(p, "name")
		}
		if _, set := p["description"]; set {
			c.Description = getString(p, "description")
		}
		if md := stringMap(p, "metadata"); md != nil {
			c.Metadata = md
		}
		m.persist()
	}
	cCopy := cloneCustomer(c)
	m.mu.Unlock()

	writeStripeJSON(w, http.StatusOK, m.serializeCustomer(cCopy))
}

// createCustomer serves POST /v1/customers.
func (m *StripeMock) createCustomer(w http.ResponseWriter, r *http.Request) {
	p, ok := readStripeForm(w, r)
	if !ok {
		return
	}
	c := &stripeCustomer{
		ID:          "cus_test_" + randomHex(14),
		Email:       getString(p, "email"),
		Name:        getString(p, "name"),
		Description: getString(p, "description"),
		Created:     m.now(),
		Metadata:    stringMap(p, "metadata"),
	}

	m.mu.Lock()
	m.customers[c.ID] = c
	cCopy := cloneCustomer(c)
	m.persist()
	m.mu.Unlock()

	m.logger.Info("customer created", "id", c.ID, "email", c.Email)
	writeStripeJSON(w, http.StatusOK, m.serializeCustomer(cCopy))
}

// listCustomers serves GET /v1/customers with the email filter (exact,
// case-insensitive). Newest first, paged.
func (m *StripeMock) listCustomers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	email := q.Get("email")

	m.mu.RLock()
	all := make([]*stripeCustomer, 0, len(m.customers))
	for _, c := range m.customers {
		if email != "" && !strings.EqualFold(c.Email, email) {
			continue
		}
		all = append(all, cloneCustomer(c))
	}
	m.mu.RUnlock()

	sortNewestFirst(all, func(c *stripeCustomer) (time.Time, string) { return c.Created, c.ID })
	page, more := pageAfter(all, q, func(c *stripeCustomer) string { return c.ID })
	out := make([]map[string]any, len(page))
	for i, c := range page {
		out[i] = m.serializeCustomer(c)
	}
	writeStripeJSON(w, http.StatusOK, map[string]any{
		"object": "list", "url": "/v1/customers", "has_more": more, "data": out,
	})
}
