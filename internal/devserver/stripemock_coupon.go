package devserver

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// stripeCoupon is a Stripe Coupon: the discount rule. Exactly one of
// PercentOff / AmountOff is set. Duration says how many billing periods a
// subscription keeps the discount: once (first invoice), repeating
// (DurationInMonths), forever. One-time payments only ever see "once".
type stripeCoupon struct {
	ID               string            `json:"id"`
	Name             string            `json:"name,omitempty"`
	PercentOff       float64           `json:"percent_off,omitempty"`
	AmountOff        int64             `json:"amount_off,omitempty"`
	Currency         string            `json:"currency,omitempty"` // required with AmountOff
	Duration         string            `json:"duration"`           // once | repeating | forever
	DurationInMonths int64             `json:"duration_in_months,omitempty"`
	MaxRedemptions   int64             `json:"max_redemptions,omitempty"` // 0 = unlimited
	TimesRedeemed    int64             `json:"times_redeemed"`
	RedeemBy         time.Time         `json:"redeem_by"` // zero = never expires
	Created          time.Time         `json:"created"`
	Metadata         map[string]string `json:"metadata,omitempty"`
}

// stripePromotionCode is the customer-facing code (SUMMER20) that redeems a
// coupon. Code matching is case-insensitive, as in Stripe.
type stripePromotionCode struct {
	ID             string            `json:"id"`
	Code           string            `json:"code"`
	CouponID       string            `json:"coupon_id"`
	Active         bool              `json:"active"`
	MaxRedemptions int64             `json:"max_redemptions,omitempty"` // 0 = unlimited
	TimesRedeemed  int64             `json:"times_redeemed"`
	ExpiresAt      time.Time         `json:"expires_at"` // zero = never
	Created        time.Time         `json:"created"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

// stripeDiscount is one discount applied to a checkout session (Stripe allows
// a single one per session). PromotionCodeID is empty when the app passed the
// coupon directly.
type stripeDiscount struct {
	CouponID        string `json:"coupon"`
	PromotionCodeID string `json:"promotion_code,omitempty"`
}

// validationError says why a coupon can no longer be redeemed, nil if it can.
func (c *stripeCoupon) validationError(now time.Time) error {
	if !c.RedeemBy.IsZero() && now.After(c.RedeemBy) {
		return stripeText("This coupon has expired")
	}
	if c.MaxRedemptions > 0 && c.TimesRedeemed >= c.MaxRedemptions {
		return stripeText("This coupon has reached its maximum redemptions")
	}
	return nil
}

// discountFor is the amount a coupon takes off subtotal in currency. Percent
// discounts round to the nearest minor unit; amount discounts are capped at
// the subtotal and must be in the session's currency.
func (c *stripeCoupon) discountFor(subtotal int64, currency string) (int64, error) {
	if c.PercentOff > 0 {
		return int64(math.Round(float64(subtotal) * c.PercentOff / 100)), nil
	}
	if c.Currency != currency {
		return 0, stripeText("This coupon is in currency %s and cannot be applied to a session in currency %s",
			strings.ToUpper(c.Currency), strings.ToUpper(currency))
	}
	return min(c.AmountOff, subtotal), nil
}

// validationError says why a promotion code cannot be redeemed, nil if it can.
func (pc *stripePromotionCode) validationError(now time.Time) error {
	if !pc.Active {
		return stripeText("This promotion code is inactive")
	}
	if !pc.ExpiresAt.IsZero() && now.After(pc.ExpiresAt) {
		return stripeText("This promotion code has expired")
	}
	if pc.MaxRedemptions > 0 && pc.TimesRedeemed >= pc.MaxRedemptions {
		return stripeText("This promotion code has reached its maximum redemptions")
	}
	return nil
}

// registerCouponRoutes mounts /v1/coupons and /v1/promotion_codes.
func (m *StripeMock) registerCouponRoutes(mux stripeRouter) {
	mux.HandleFunc("/v1/coupons", m.handleCoupons)
	mux.HandleFunc("/v1/coupons/", m.handleCouponByID)
	mux.HandleFunc("/v1/promotion_codes", m.handlePromotionCodes)
	mux.HandleFunc("/v1/promotion_codes/", m.handlePromotionCodeByID)
}

// handleCoupons handles POST (create) and GET (list) on the collection.
func (m *StripeMock) handleCoupons(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		m.createCoupon(w, r)
	case http.MethodGet:
		m.listCoupons(w, r)
	default:
		writeStripeError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
	}
}

// handleCouponByID handles GET (retrieve) and DELETE on /v1/coupons/{id}.
func (m *StripeMock) handleCouponByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/coupons/")
	if id == "" || strings.Contains(id, "/") {
		writeStripeError(w, http.StatusNotFound, "invalid_request_error", "coupon id required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		m.mu.RLock()
		c, ok := m.coupons[id]
		if ok {
			c = cloneCoupon(c)
		}
		m.mu.RUnlock()
		if !ok {
			writeStripeError(w, http.StatusNotFound, "invalid_request_error", fmt.Sprintf("No such coupon: '%s'", id))
			return
		}
		writeStripeJSON(w, http.StatusOK, m.serializeCoupon(c))
	case http.MethodDelete:
		// Sessions and subscriptions keep the snapshotted coupon id and
		// amount, so nothing here is rewritten; an open session that still
		// references the coupon is refused at payment by redeemDiscount.
		m.mu.Lock()
		_, ok := m.coupons[id]
		delete(m.coupons, id)
		m.persist()
		m.mu.Unlock()
		if !ok {
			writeStripeError(w, http.StatusNotFound, "invalid_request_error", fmt.Sprintf("No such coupon: '%s'", id))
			return
		}
		writeStripeJSON(w, http.StatusOK, map[string]any{"id": id, "object": "coupon", "deleted": true})
	default:
		writeStripeError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
	}
}

// createCoupon serves POST /v1/coupons.
func (m *StripeMock) createCoupon(w http.ResponseWriter, r *http.Request) {
	p, ok := readStripeForm(w, r)
	if !ok {
		return
	}
	c, err := buildCouponFromParams(p)
	if err != nil {
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	c.Created = m.now()
	if c.ID == "" {
		c.ID = strings.ToUpper(randomHex(8))
	}

	m.mu.Lock()
	if _, dup := m.coupons[c.ID]; dup {
		m.mu.Unlock()
		writeStripeErrorCode(w, http.StatusBadRequest, "invalid_request_error", "resource_already_exists",
			fmt.Sprintf("Coupon already exists: '%s'", c.ID))
		return
	}
	m.coupons[c.ID] = c
	cCopy := cloneCoupon(c)
	m.persist()
	m.mu.Unlock()

	writeStripeJSON(w, http.StatusOK, m.serializeCoupon(cCopy))
}

// listCoupons serves GET /v1/coupons, newest first, paged like Stripe.
func (m *StripeMock) listCoupons(w http.ResponseWriter, r *http.Request) {
	m.mu.RLock()
	all := make([]*stripeCoupon, 0, len(m.coupons))
	for _, c := range m.coupons {
		all = append(all, cloneCoupon(c))
	}
	m.mu.RUnlock()

	sortNewestFirst(all, func(c *stripeCoupon) (time.Time, string) { return c.Created, c.ID })
	page, more := pageAfter(all, r.URL.Query(), func(c *stripeCoupon) string { return c.ID })
	out := make([]map[string]any, len(page))
	for i, c := range page {
		out[i] = m.serializeCoupon(c)
	}
	writeStripeJSON(w, http.StatusOK, map[string]any{
		"object": "list", "url": "/v1/coupons", "has_more": more, "data": out,
	})
}

// buildCouponFromParams validates the create form the way Stripe does:
// exactly one of percent_off / amount_off, amount_off needs a currency,
// repeating needs duration_in_months.
func buildCouponFromParams(p map[string]any) (*stripeCoupon, error) {
	c := &stripeCoupon{
		ID:       getString(p, "id"),
		Name:     getString(p, "name"),
		Currency: strings.ToLower(getString(p, "currency")),
		Duration: orDefault(getString(p, "duration"), "once"),
		Metadata: stringMap(p, "metadata"),
	}
	if s := getString(p, "percent_off"); s != "" {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil || v <= 0 || v > 100 {
			return nil, errors.New("percent_off must be a number greater than 0 and at most 100")
		}
		c.PercentOff = v
	}
	amountOff, ok := getInt64(p, "amount_off")
	if !ok || amountOff < 0 {
		return nil, errors.New("amount_off must be a non-negative integer")
	}
	c.AmountOff = amountOff
	switch {
	case c.PercentOff > 0 && c.AmountOff > 0:
		return nil, stripeText("You may only specify one of these parameters: amount_off, percent_off")
	case c.PercentOff == 0 && c.AmountOff == 0:
		return nil, stripeText("You must specify one of these parameters: amount_off, percent_off")
	case c.AmountOff > 0 && c.Currency == "":
		return nil, errors.New("currency is required when amount_off is set")
	}
	switch c.Duration {
	case "once", "forever":
	case "repeating":
		n, ok := getInt64(p, "duration_in_months")
		if !ok || n <= 0 {
			return nil, errors.New("duration_in_months is required when duration is repeating")
		}
		c.DurationInMonths = n
	default:
		return nil, stripeText("Invalid duration: must be one of once, repeating, or forever")
	}
	if n, ok := getInt64(p, "max_redemptions"); !ok || n < 0 {
		return nil, errors.New("max_redemptions must be a non-negative integer")
	} else {
		c.MaxRedemptions = n
	}
	if ts, ok := getInt64(p, "redeem_by"); !ok || ts < 0 {
		return nil, errors.New("redeem_by must be a unix timestamp")
	} else if ts > 0 {
		c.RedeemBy = time.Unix(ts, 0)
	}
	return c, nil
}

// serializeCoupon renders the coupon object stripe-go decodes.
func (m *StripeMock) serializeCoupon(c *stripeCoupon) map[string]any {
	out := map[string]any{
		"id":                 c.ID,
		"object":             "coupon",
		"amount_off":         nullableInt64(c.AmountOff),
		"created":            c.Created.Unix(),
		"currency":           nullableString(c.Currency),
		"duration":           c.Duration,
		"duration_in_months": nullableInt64(c.DurationInMonths),
		"livemode":           false,
		"max_redemptions":    nullableInt64(c.MaxRedemptions),
		"name":               nullableString(c.Name),
		"percent_off":        nil,
		"redeem_by":          nil,
		"times_redeemed":     c.TimesRedeemed,
		"valid":              c.validationError(m.now()) == nil,
	}
	if c.PercentOff > 0 {
		out["percent_off"] = c.PercentOff
	}
	if !c.RedeemBy.IsZero() {
		out["redeem_by"] = c.RedeemBy.Unix()
	}
	if c.Metadata != nil {
		out["metadata"] = c.Metadata
	} else {
		out["metadata"] = map[string]string{}
	}
	return out
}

// handlePromotionCodes handles POST (create) and GET (list) on the collection.
func (m *StripeMock) handlePromotionCodes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		m.createPromotionCode(w, r)
	case http.MethodGet:
		m.listPromotionCodes(w, r)
	default:
		writeStripeError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
	}
}

// handlePromotionCodeByID handles GET (retrieve) and POST (update: active,
// metadata) on /v1/promotion_codes/{id}.
func (m *StripeMock) handlePromotionCodeByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/promotion_codes/")
	if id == "" || strings.Contains(id, "/") {
		writeStripeError(w, http.StatusNotFound, "invalid_request_error", "promotion_code id required")
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
	pc, ok := m.promotionCodes[id]
	if !ok {
		m.mu.Unlock()
		writeStripeError(w, http.StatusNotFound, "invalid_request_error", fmt.Sprintf("No such promotion_code: '%s'", id))
		return
	}
	if p != nil {
		if s := getString(p, "active"); s != "" {
			pc.Active = s == "true"
		}
		if md := stringMap(p, "metadata"); md != nil {
			pc.Metadata = md
		}
		m.persist()
	}
	pcCopy, coupon := clonePromotionCode(pc), cloneCoupon(m.coupons[pc.CouponID])
	m.mu.Unlock()

	writeStripeJSON(w, http.StatusOK, m.serializePromotionCode(pcCopy, coupon))
}

// createPromotionCode serves POST /v1/promotion_codes. The coupon comes in
// as promotion[type]=coupon&promotion[coupon]=<id> on this API version.
func (m *StripeMock) createPromotionCode(w http.ResponseWriter, r *http.Request) {
	p, ok := readStripeForm(w, r)
	if !ok {
		return
	}
	promo, _ := p["promotion"].(map[string]any)
	pc := &stripePromotionCode{
		ID:       "promo_test_" + randomHex(24),
		Code:     strings.TrimSpace(getString(p, "code")),
		CouponID: getString(promo, "coupon"),
		Active:   getString(p, "active") != "false",
		Created:  m.now(),
		Metadata: stringMap(p, "metadata"),
	}
	if pc.CouponID == "" {
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error", "promotion[coupon] is required")
		return
	}
	if pc.Code == "" {
		pc.Code = strings.ToUpper(randomHex(8))
	}
	if n, ok := getInt64(p, "max_redemptions"); !ok || n < 0 {
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error", "max_redemptions must be a non-negative integer")
		return
	} else {
		pc.MaxRedemptions = n
	}
	if ts, ok := getInt64(p, "expires_at"); !ok || ts < 0 {
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error", "expires_at must be a unix timestamp")
		return
	} else if ts > 0 {
		pc.ExpiresAt = time.Unix(ts, 0)
	}

	m.mu.Lock()
	coupon, ok := m.coupons[pc.CouponID]
	if !ok {
		m.mu.Unlock()
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("No such coupon: '%s'", pc.CouponID))
		return
	}
	if dup := m.findPromotionCode(pc.Code); dup != nil && dup.Active {
		m.mu.Unlock()
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error",
			fmt.Sprintf("An active promotion code with `code: %s` already exists.", pc.Code))
		return
	}
	m.promotionCodes[pc.ID] = pc
	pcCopy, couponCopy := clonePromotionCode(pc), cloneCoupon(coupon)
	m.persist()
	m.mu.Unlock()

	writeStripeJSON(w, http.StatusOK, m.serializePromotionCode(pcCopy, couponCopy))
}

// listPromotionCodes serves GET /v1/promotion_codes with the filters apps
// use: code (case-insensitive), coupon, active. Newest first, paged.
func (m *StripeMock) listPromotionCodes(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	code, couponID, active := q.Get("code"), q.Get("coupon"), q.Get("active")

	type row struct {
		pc     *stripePromotionCode
		coupon *stripeCoupon
	}
	m.mu.RLock()
	var all []row
	for _, pc := range m.promotionCodes {
		if code != "" && !strings.EqualFold(pc.Code, code) {
			continue
		}
		if couponID != "" && pc.CouponID != couponID {
			continue
		}
		if active != "" && pc.Active != (active == "true") {
			continue
		}
		all = append(all, row{clonePromotionCode(pc), cloneCoupon(m.coupons[pc.CouponID])})
	}
	m.mu.RUnlock()

	sortNewestFirst(all, func(r row) (time.Time, string) { return r.pc.Created, r.pc.ID })
	page, more := pageAfter(all, q, func(r row) string { return r.pc.ID })
	out := make([]map[string]any, len(page))
	for i, r := range page {
		out[i] = m.serializePromotionCode(r.pc, r.coupon)
	}
	writeStripeJSON(w, http.StatusOK, map[string]any{
		"object": "list", "url": "/v1/promotion_codes", "has_more": more, "data": out,
	})
}

// serializePromotionCode renders the promotion_code object. The coupon is
// inlined under promotion.coupon like Stripe does; a deleted coupon falls
// back to its id.
func (m *StripeMock) serializePromotionCode(pc *stripePromotionCode, coupon *stripeCoupon) map[string]any {
	var couponOut any = pc.CouponID
	if coupon != nil {
		couponOut = m.serializeCoupon(coupon)
	}
	out := map[string]any{
		"id":              pc.ID,
		"object":          "promotion_code",
		"active":          pc.Active,
		"code":            pc.Code,
		"created":         pc.Created.Unix(),
		"customer":        nil,
		"expires_at":      nil,
		"livemode":        false,
		"max_redemptions": nullableInt64(pc.MaxRedemptions),
		"promotion":       map[string]any{"type": "coupon", "coupon": couponOut},
		"restrictions": map[string]any{
			"first_time_transaction": false, "minimum_amount": nil, "minimum_amount_currency": nil,
		},
		"times_redeemed": pc.TimesRedeemed,
	}
	if !pc.ExpiresAt.IsZero() {
		out["expires_at"] = pc.ExpiresAt.Unix()
	}
	if pc.Metadata != nil {
		out["metadata"] = pc.Metadata
	} else {
		out["metadata"] = map[string]string{}
	}
	return out
}

// findPromotionCode returns the promotion code with this code, matched
// case-insensitively. An active code wins over an inactive one with the same
// text, since Stripe lets the text be reused once the old code is
// deactivated. Caller holds m.mu.
func (m *StripeMock) findPromotionCode(code string) *stripePromotionCode {
	var found *stripePromotionCode
	for _, pc := range m.promotionCodes {
		if strings.EqualFold(pc.Code, code) && (found == nil || pc.Active) {
			found = pc
		}
	}
	return found
}

// applyDiscount resolves a discount onto an open session and snapshots the
// discounted amount. Exactly one of couponID, promoID (promo_…) or code
// (what the buyer typed) is set. Errors carry Stripe's wording so they can
// be shown inline. Caller holds m.mu (write).
func (m *StripeMock) applyDiscount(sess *stripeSession, couponID, promoID, code string) error {
	now := m.now()
	var pc *stripePromotionCode
	switch {
	case code != "":
		if pc = m.findPromotionCode(code); pc == nil {
			return stripeText("No such promotion code: '%s'", code)
		}
	case promoID != "":
		var ok bool
		if pc, ok = m.promotionCodes[promoID]; !ok {
			return stripeText("No such promotion_code: '%s'", promoID)
		}
	}
	if pc != nil {
		if err := pc.validationError(now); err != nil {
			return err
		}
		couponID, promoID = pc.CouponID, pc.ID
	}
	c, ok := m.coupons[couponID]
	if !ok {
		return stripeText("No such coupon: '%s'", couponID)
	}
	if err := c.validationError(now); err != nil {
		return err
	}
	discount, err := c.discountFor(sess.AmountTotal, sess.Currency)
	if err != nil {
		return err
	}
	sess.Discounts = []stripeDiscount{{CouponID: couponID, PromotionCodeID: promoID}}
	sess.AmountDiscount = discount
	return nil
}

// redeemDiscount counts a completed session's discount as one redemption on
// its coupon and promotion code. Stripe increments at completion, not at
// session create, and validates again then: a limit reached or a code
// deactivated while the session sat open stops the payment. Caller holds
// m.mu (write).
func (m *StripeMock) redeemDiscount(sess *stripeSession) error {
	now := m.now()
	for _, d := range sess.Discounts {
		c, pc := m.coupons[d.CouponID], m.promotionCodes[d.PromotionCodeID]
		if c == nil {
			// Deleted after the session was created: Stripe refuses the payment.
			return stripeText("No such coupon: '%s'", d.CouponID)
		}
		if pc != nil {
			if err := pc.validationError(now); err != nil {
				return err
			}
		}
		if err := c.validationError(now); err != nil {
			return err
		}
		c.TimesRedeemed++
		if pc != nil {
			pc.TimesRedeemed++
		}
	}
	return nil
}

// stripeText is an error carrying Stripe's own message text, which is
// capitalised because it is shown to the buyer or returned as the API error
// message. Kept separate from errors.New so the lint rule for Go-style error
// strings does not apply to Stripe's wording.
func stripeText(format string, a ...any) error {
	return fmt.Errorf(format, a...)
}

// nullableInt64 renders 0 as JSON null, for fields Stripe leaves unset
// rather than zero (amount_off on a percent coupon, max_redemptions, …).
func nullableInt64(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}
