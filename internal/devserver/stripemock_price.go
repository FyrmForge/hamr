package devserver

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
)

// Products and Prices exist so a Checkout Session can reference
// line_items[].price=<id> instead of inlining price_data, and so a
// subscription created that way reports the real ids.
//
// ponytail: prices are per_unit only; no tiers, no metered usage, no
// currency_options. Products carry name/description/metadata only.

// stripeProduct is a Stripe Product.
type stripeProduct struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Active      bool              `json:"active"`
	Created     time.Time         `json:"created"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// stripePrice is a Stripe Price. Interval is empty for a one_time price.
type stripePrice struct {
	ID            string            `json:"id"`
	ProductID     string            `json:"product_id"`
	Currency      string            `json:"currency"`
	UnitAmount    int64             `json:"unit_amount"`
	Interval      string            `json:"interval,omitempty"` // day | week | month | year; "" = one_time
	IntervalCount int64             `json:"interval_count,omitempty"`
	Nickname      string            `json:"nickname,omitempty"`
	LookupKey     string            `json:"lookup_key,omitempty"`
	Active        bool              `json:"active"`
	Created       time.Time         `json:"created"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

func cloneProduct(p *stripeProduct) *stripeProduct {
	if p == nil {
		return nil
	}
	c := *p
	c.Metadata = cloneStringMap(p.Metadata)
	return &c
}

func clonePrice(p *stripePrice) *stripePrice {
	if p == nil {
		return nil
	}
	c := *p
	c.Metadata = cloneStringMap(p.Metadata)
	return &c
}

// priceType is Stripe's price.type.
func (p *stripePrice) priceType() string {
	if p.Interval == "" {
		return "one_time"
	}
	return "recurring"
}

// serializeProduct renders the product object stripe-go decodes.
func (m *StripeMock) serializeProduct(p *stripeProduct) map[string]any {
	return map[string]any{
		"id":          p.ID,
		"object":      "product",
		"active":      p.Active,
		"created":     p.Created.Unix(),
		"updated":     p.Created.Unix(),
		"name":        p.Name,
		"description": nullableString(p.Description),
		"livemode":    false,
		"type":        "service",
		"metadata":    orEmptyMap(p.Metadata),
	}
}

// serializePrice renders the price object stripe-go decodes. product is the
// id, as Stripe returns it unexpanded.
func (m *StripeMock) serializePrice(p *stripePrice) map[string]any {
	var recurring any
	if p.Interval != "" {
		recurring = map[string]any{
			"interval": p.Interval, "interval_count": p.IntervalCount,
			"usage_type": "licensed", "meter": nil, "trial_period_days": nil,
		}
	}
	return map[string]any{
		"id":                  p.ID,
		"object":              "price",
		"active":              p.Active,
		"billing_scheme":      "per_unit",
		"created":             p.Created.Unix(),
		"currency":            p.Currency,
		"livemode":            false,
		"lookup_key":          nullableString(p.LookupKey),
		"metadata":            orEmptyMap(p.Metadata),
		"nickname":            nullableString(p.Nickname),
		"product":             p.ProductID,
		"recurring":           recurring,
		"tax_behavior":        "unspecified",
		"type":                p.priceType(),
		"unit_amount":         p.UnitAmount,
		"unit_amount_decimal": fmt.Sprint(p.UnitAmount),
	}
}

// registerPriceRoutes mounts /v1/products and /v1/prices.
func (m *StripeMock) registerPriceRoutes(mux stripeRouter) {
	mux.HandleFunc("/v1/products", m.handleProducts)
	mux.HandleFunc("/v1/products/", m.handleProductByID)
	mux.HandleFunc("/v1/prices", m.handlePrices)
	mux.HandleFunc("/v1/prices/", m.handlePriceByID)
}

// resolvePriceLocked returns a price and its product. Caller holds m.mu.
func (m *StripeMock) resolvePriceLocked(id string) (*stripePrice, *stripeProduct, bool) {
	p, ok := m.prices[id]
	if !ok {
		return nil, nil, false
	}
	return p, m.products[p.ProductID], true
}

// handleProducts handles POST (create) and GET (list) on the collection.
func (m *StripeMock) handleProducts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		m.createProduct(w, r)
	case http.MethodGet:
		m.listProducts(w, r)
	default:
		writeStripeError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
	}
}

// handleProductByID handles GET on /v1/products/{id}.
func (m *StripeMock) handleProductByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/products/")
	if id == "" || strings.Contains(id, "/") {
		writeStripeError(w, http.StatusNotFound, "invalid_request_error", "product id required")
		return
	}
	if r.Method != http.MethodGet {
		writeStripeError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	m.mu.RLock()
	p, ok := m.products[id]
	if ok {
		p = cloneProduct(p)
	}
	m.mu.RUnlock()
	if !ok {
		writeStripeErrorCode(w, http.StatusNotFound, "invalid_request_error", "resource_missing",
			fmt.Sprintf("No such product: '%s'", id))
		return
	}
	writeStripeJSON(w, http.StatusOK, m.serializeProduct(p))
}

// buildProductFromParams validates a product form (top-level or the
// product_data of a price create).
func (m *StripeMock) buildProductFromParams(p map[string]any) (*stripeProduct, error) {
	name := strings.TrimSpace(getString(p, "name"))
	if name == "" {
		return nil, errors.New("name is required")
	}
	return &stripeProduct{
		ID:          "prod_test_" + randomHex(14),
		Name:        name,
		Description: getString(p, "description"),
		Active:      getString(p, "active") != "false",
		Created:     m.now(),
		Metadata:    stringMap(p, "metadata"),
	}, nil
}

// createProduct serves POST /v1/products.
func (m *StripeMock) createProduct(w http.ResponseWriter, r *http.Request) {
	p, ok := readStripeForm(w, r)
	if !ok {
		return
	}
	prod, err := m.buildProductFromParams(p)
	if err != nil {
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	m.mu.Lock()
	m.products[prod.ID] = prod
	prodCopy := cloneProduct(prod)
	m.persist()
	m.mu.Unlock()

	writeStripeJSON(w, http.StatusOK, m.serializeProduct(prodCopy))
}

// listProducts serves GET /v1/products with the active filter. Newest
// first, paged.
func (m *StripeMock) listProducts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	active := q.Get("active")

	m.mu.RLock()
	all := make([]*stripeProduct, 0, len(m.products))
	for _, p := range m.products {
		if active != "" && p.Active != (active == "true") {
			continue
		}
		all = append(all, cloneProduct(p))
	}
	m.mu.RUnlock()

	sortNewestFirst(all, func(p *stripeProduct) (time.Time, string) { return p.Created, p.ID })
	page, more := pageAfter(all, q, func(p *stripeProduct) string { return p.ID })
	out := make([]map[string]any, len(page))
	for i, p := range page {
		out[i] = m.serializeProduct(p)
	}
	writeStripeJSON(w, http.StatusOK, map[string]any{
		"object": "list", "url": "/v1/products", "has_more": more, "data": out,
	})
}

// handlePrices handles POST (create) and GET (list) on the collection.
func (m *StripeMock) handlePrices(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		m.createPrice(w, r)
	case http.MethodGet:
		m.listPrices(w, r)
	default:
		writeStripeError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
	}
}

// handlePriceByID handles GET (retrieve) and POST (update: active, metadata,
// nickname, lookup_key) on /v1/prices/{id}.
func (m *StripeMock) handlePriceByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/prices/")
	if id == "" || strings.Contains(id, "/") {
		writeStripeError(w, http.StatusNotFound, "invalid_request_error", "price id required")
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
	pr, ok := m.prices[id]
	if !ok {
		m.mu.Unlock()
		writeStripeErrorCode(w, http.StatusNotFound, "invalid_request_error", "resource_missing",
			fmt.Sprintf("No such price: '%s'", id))
		return
	}
	if p != nil {
		if s := getString(p, "active"); s != "" {
			pr.Active = s == "true"
		}
		if md := stringMap(p, "metadata"); md != nil {
			pr.Metadata = md
		}
		if _, set := p["nickname"]; set {
			pr.Nickname = getString(p, "nickname")
		}
		if _, set := p["lookup_key"]; set {
			key := getString(p, "lookup_key")
			if err := m.claimLookupKeyLocked(pr, key, getString(p, "transfer_lookup_key") == "true"); err != nil {
				m.mu.Unlock()
				writeStripeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
				return
			}
		}
		m.persist()
	}
	prCopy := clonePrice(pr)
	m.mu.Unlock()

	writeStripeJSON(w, http.StatusOK, m.serializePrice(prCopy))
}

// claimLookupKeyLocked assigns key to pr. A key held by another price is
// moved over when transfer is set, otherwise refused like Stripe does.
// Caller holds m.mu (write).
func (m *StripeMock) claimLookupKeyLocked(pr *stripePrice, key string, transfer bool) error {
	if key != "" {
		for _, other := range m.prices {
			if other != pr && other.LookupKey == key {
				if !transfer {
					return stripeText("Lookup key '%s' is already in use", key)
				}
				other.LookupKey = ""
			}
		}
	}
	pr.LookupKey = key
	return nil
}

// createPrice serves POST /v1/prices. Exactly one of product=<id> or
// product_data[name] (creates the product inline) is required.
func (m *StripeMock) createPrice(w http.ResponseWriter, r *http.Request) {
	p, ok := readStripeForm(w, r)
	if !ok {
		return
	}
	pr := &stripePrice{
		ID:        "price_test_" + randomHex(14),
		ProductID: getString(p, "product"),
		Currency:  strings.ToLower(getString(p, "currency")),
		Nickname:  getString(p, "nickname"),
		Active:    getString(p, "active") != "false",
		Created:   m.now(),
		Metadata:  stringMap(p, "metadata"),
	}
	if pr.Currency == "" {
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error", "currency is required")
		return
	}
	if _, set := p["unit_amount"]; !set {
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error", "unit_amount is required")
		return
	}
	if n, ok := getInt64(p, "unit_amount"); !ok || n < 0 {
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error", "unit_amount must be a non-negative integer")
		return
	} else {
		pr.UnitAmount = n
	}
	if rec, ok := p["recurring"].(map[string]any); ok {
		switch pr.Interval = getString(rec, "interval"); pr.Interval {
		case "day", "week", "month", "year":
		default:
			writeStripeError(w, http.StatusBadRequest, "invalid_request_error",
				"recurring[interval] must be one of day, week, month, or year")
			return
		}
		n, ok := getInt64(rec, "interval_count")
		if !ok || n < 0 {
			writeStripeError(w, http.StatusBadRequest, "invalid_request_error", "recurring[interval_count] must be a positive integer")
			return
		}
		pr.IntervalCount = max(n, 1)
	}
	productData, hasProductData := p["product_data"].(map[string]any)
	if (pr.ProductID == "") == !hasProductData {
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error",
			"You must specify exactly one of these parameters: product, product_data")
		return
	}
	var inline *stripeProduct
	if hasProductData {
		var err error
		if inline, err = m.buildProductFromParams(productData); err != nil {
			writeStripeError(w, http.StatusBadRequest, "invalid_request_error", "product_data["+err.Error()+"]")
			return
		}
		pr.ProductID = inline.ID
	}

	m.mu.Lock()
	if inline == nil {
		if _, ok := m.products[pr.ProductID]; !ok {
			m.mu.Unlock()
			writeStripeErrorCode(w, http.StatusBadRequest, "invalid_request_error", "resource_missing",
				fmt.Sprintf("No such product: '%s'", pr.ProductID))
			return
		}
	}
	if err := m.claimLookupKeyLocked(pr, getString(p, "lookup_key"), getString(p, "transfer_lookup_key") == "true"); err != nil {
		m.mu.Unlock()
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if inline != nil {
		m.products[inline.ID] = inline
	}
	m.prices[pr.ID] = pr
	prCopy := clonePrice(pr)
	m.persist()
	m.mu.Unlock()

	writeStripeJSON(w, http.StatusOK, m.serializePrice(prCopy))
}

// listPrices serves GET /v1/prices with the filters apps use: active,
// product, type, lookup_keys[] (stripe-go sends lookup_keys[0]=…). Newest
// first, paged.
func (m *StripeMock) listPrices(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	active, product, typ := q.Get("active"), q.Get("product"), q.Get("type")
	var lookupKeys []string
	for k, vals := range q {
		if strings.HasPrefix(k, "lookup_keys[") {
			lookupKeys = append(lookupKeys, vals...)
		}
	}

	m.mu.RLock()
	all := make([]*stripePrice, 0, len(m.prices))
	for _, p := range m.prices {
		if active != "" && p.Active != (active == "true") {
			continue
		}
		if product != "" && p.ProductID != product {
			continue
		}
		if typ != "" && p.priceType() != typ {
			continue
		}
		if len(lookupKeys) > 0 && (p.LookupKey == "" || !slices.Contains(lookupKeys, p.LookupKey)) {
			continue
		}
		all = append(all, clonePrice(p))
	}
	m.mu.RUnlock()

	sortNewestFirst(all, func(p *stripePrice) (time.Time, string) { return p.Created, p.ID })
	page, more := pageAfter(all, q, func(p *stripePrice) string { return p.ID })
	out := make([]map[string]any, len(page))
	for i, p := range page {
		out[i] = m.serializePrice(p)
	}
	writeStripeJSON(w, http.StatusOK, map[string]any{
		"object": "list", "url": "/v1/prices", "has_more": more, "data": out,
	})
}
