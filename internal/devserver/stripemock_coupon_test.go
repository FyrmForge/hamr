package devserver

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	stripe "github.com/stripe/stripe-go/v86"
	"github.com/stripe/stripe-go/v86/checkout/session"
	"github.com/stripe/stripe-go/v86/coupon"
	"github.com/stripe/stripe-go/v86/promotioncode"
)

// TestStripeMock_Coupon_RoundTrip creates, retrieves, lists and deletes a
// percent coupon through the real stripe-go client.
func TestStripeMock_Coupon_RoundTrip(t *testing.T) {
	_, srv := newTestStripeServer(t)
	withStripeBackend(t, srv.URL, func() {
		params := &stripe.CouponParams{
			ID:               stripe.String("SPRING25"),
			Name:             stripe.String("Spring sale"),
			PercentOff:       stripe.Float64(25),
			Duration:         stripe.String("repeating"),
			DurationInMonths: stripe.Int64(3),
			MaxRedemptions:   stripe.Int64(10),
		}
		params.AddMetadata("campaign", "spring")
		c, err := coupon.New(params)
		require.NoError(t, err)
		assert.Equal(t, "SPRING25", c.ID)
		assert.Equal(t, "Spring sale", c.Name)
		assert.Equal(t, float64(25), c.PercentOff)
		assert.Equal(t, int64(0), c.AmountOff)
		assert.Equal(t, stripe.CouponDurationRepeating, c.Duration)
		assert.Equal(t, int64(3), c.DurationInMonths)
		assert.Equal(t, int64(10), c.MaxRedemptions)
		assert.True(t, c.Valid)
		assert.Equal(t, "spring", c.Metadata["campaign"])

		got, err := coupon.Get("SPRING25", nil)
		require.NoError(t, err)
		assert.Equal(t, c.ID, got.ID)

		// An amount coupon with a generated id.
		amt, err := coupon.New(&stripe.CouponParams{
			AmountOff: stripe.Int64(500), Currency: stripe.String("gbp"), Duration: stripe.String("once"),
		})
		require.NoError(t, err)
		assert.NotEmpty(t, amt.ID)
		assert.Equal(t, int64(500), amt.AmountOff)
		assert.Equal(t, stripe.Currency("gbp"), amt.Currency)

		var ids []string
		iter := coupon.List(&stripe.CouponListParams{})
		for iter.Next() {
			ids = append(ids, iter.Coupon().ID)
		}
		require.NoError(t, iter.Err())
		assert.ElementsMatch(t, []string{"SPRING25", amt.ID}, ids)

		// Duplicate id and invalid combinations surface as *stripe.Error.
		_, err = coupon.New(&stripe.CouponParams{ID: stripe.String("SPRING25"), PercentOff: stripe.Float64(5)})
		var se *stripe.Error
		require.ErrorAs(t, err, &se)
		assert.Equal(t, stripe.ErrorCode("resource_already_exists"), se.Code)

		_, err = coupon.New(&stripe.CouponParams{PercentOff: stripe.Float64(5), AmountOff: stripe.Int64(5), Currency: stripe.String("gbp")})
		require.ErrorAs(t, err, &se)
		_, err = coupon.New(&stripe.CouponParams{AmountOff: stripe.Int64(5)})
		require.ErrorAs(t, err, &se, "amount_off without currency")
		_, err = coupon.New(&stripe.CouponParams{PercentOff: stripe.Float64(5), Duration: stripe.String("repeating")})
		require.ErrorAs(t, err, &se, "repeating without duration_in_months")

		del, err := coupon.Del("SPRING25", nil)
		require.NoError(t, err)
		assert.True(t, del.Deleted)
		_, err = coupon.Get("SPRING25", nil)
		require.ErrorAs(t, err, &se)
		assert.Equal(t, http.StatusNotFound, se.HTTPStatusCode)
	})
}

// TestStripeMock_PromotionCode_RoundTrip covers create with promotion[coupon],
// the case-insensitive code lookup apps use, and deactivation.
func TestStripeMock_PromotionCode_RoundTrip(t *testing.T) {
	_, srv := newTestStripeServer(t)
	withStripeBackend(t, srv.URL, func() {
		_, err := coupon.New(&stripe.CouponParams{ID: stripe.String("TEN"), PercentOff: stripe.Float64(10)})
		require.NoError(t, err)

		pc, err := promotioncode.New(&stripe.PromotionCodeParams{
			Code:           stripe.String("summer20"),
			Promotion:      &stripe.PromotionCodePromotionParams{Type: stripe.String("coupon"), Coupon: stripe.String("TEN")},
			MaxRedemptions: stripe.Int64(2),
		})
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(pc.ID, "promo_test_"))
		assert.Equal(t, "summer20", pc.Code)
		assert.True(t, pc.Active)
		require.NotNil(t, pc.Promotion)
		require.NotNil(t, pc.Promotion.Coupon)
		assert.Equal(t, "TEN", pc.Promotion.Coupon.ID, "coupon is inlined under promotion")
		assert.Equal(t, float64(10), pc.Promotion.Coupon.PercentOff)

		// Unknown coupon and duplicate active code are 400s.
		var se *stripe.Error
		_, err = promotioncode.New(&stripe.PromotionCodeParams{
			Promotion: &stripe.PromotionCodePromotionParams{Type: stripe.String("coupon"), Coupon: stripe.String("nope")},
		})
		require.ErrorAs(t, err, &se)
		_, err = promotioncode.New(&stripe.PromotionCodeParams{
			Code:      stripe.String("SUMMER20"),
			Promotion: &stripe.PromotionCodePromotionParams{Type: stripe.String("coupon"), Coupon: stripe.String("TEN")},
		})
		require.ErrorAs(t, err, &se, "same code, different case, still active")

		// Lookup by code is case-insensitive.
		iter := promotioncode.List(&stripe.PromotionCodeListParams{Code: stripe.String("SUMMER20")})
		require.True(t, iter.Next(), "list by code should find it: %v", iter.Err())
		assert.Equal(t, pc.ID, iter.PromotionCode().ID)
		assert.False(t, iter.Next())

		// Deactivate, then the active filter excludes it.
		upd, err := promotioncode.Update(pc.ID, &stripe.PromotionCodeParams{Active: new(false)})
		require.NoError(t, err)
		assert.False(t, upd.Active)
		iter = promotioncode.List(&stripe.PromotionCodeListParams{Code: stripe.String("summer20"), Active: new(true)})
		assert.False(t, iter.Next())
		require.NoError(t, iter.Err())
	})
}

// TestStripeMock_Session_DiscountAtCreate passes discounts[0].coupon on
// create and checks the totals a real session would report.
func TestStripeMock_Session_DiscountAtCreate(t *testing.T) {
	mock, srv := newTestStripeServer(t)
	withStripeBackend(t, srv.URL, func() {
		_, err := coupon.New(&stripe.CouponParams{ID: stripe.String("THIRD"), PercentOff: new(33.333)})
		require.NoError(t, err)

		params := sessionParams(3000)
		params.Discounts = []*stripe.CheckoutSessionDiscountParams{{Coupon: stripe.String("THIRD")}}
		sess, err := session.New(params)
		require.NoError(t, err)
		assert.Equal(t, int64(3000), sess.AmountSubtotal)
		assert.Equal(t, int64(2000), sess.AmountTotal, "33.333% of 3000 rounds to 1000 off")
		require.NotNil(t, sess.TotalDetails)
		assert.Equal(t, int64(1000), sess.TotalDetails.AmountDiscount)
		require.Len(t, sess.Discounts, 1)
		assert.Equal(t, "THIRD", sess.Discounts[0].Coupon.ID)
		assert.Nil(t, sess.Discounts[0].PromotionCode)

		// Redemption is counted on completion, not on create.
		mock.mu.RLock()
		assert.Equal(t, int64(0), mock.coupons["THIRD"].TimesRedeemed)
		mock.mu.RUnlock()
		_, _, err = mock.completeCheckout(sess.ID, "paid", "")
		require.NoError(t, err)
		paid, err := session.Get(sess.ID, nil)
		require.NoError(t, err)
		require.NotNil(t, paid.PaymentIntent, "payment_intent appears once paid")
		mock.mu.RLock()
		assert.Equal(t, int64(1), mock.coupons["THIRD"].TimesRedeemed)
		pi := mock.paymentIntents[paid.PaymentIntent.ID]
		mock.mu.RUnlock()
		require.NotNil(t, pi)
		assert.Equal(t, int64(2000), pi.Amount, "the PaymentIntent charges the discounted total")

		// Rejections: unknown coupon, wrong-currency amount coupon, discounts
		// together with allow_promotion_codes, more than one discount.
		var se *stripe.Error
		p := sessionParams(3000)
		p.Discounts = []*stripe.CheckoutSessionDiscountParams{{Coupon: stripe.String("nope")}}
		_, err = session.New(p)
		require.ErrorAs(t, err, &se)

		_, err = coupon.New(&stripe.CouponParams{ID: stripe.String("USD5"), AmountOff: stripe.Int64(500), Currency: stripe.String("usd")})
		require.NoError(t, err)
		p = sessionParams(3000)
		p.Discounts = []*stripe.CheckoutSessionDiscountParams{{Coupon: stripe.String("USD5")}}
		_, err = session.New(p)
		require.ErrorAs(t, err, &se)
		assert.Contains(t, se.Msg, "currency")

		p = sessionParams(3000)
		p.AllowPromotionCodes = new(true)
		p.Discounts = []*stripe.CheckoutSessionDiscountParams{{Coupon: stripe.String("THIRD")}}
		_, err = session.New(p)
		require.ErrorAs(t, err, &se)

		p = sessionParams(3000)
		p.Discounts = []*stripe.CheckoutSessionDiscountParams{{Coupon: stripe.String("THIRD")}, {Coupon: stripe.String("THIRD")}}
		_, err = session.New(p)
		require.ErrorAs(t, err, &se)
	})
}

// TestStripeMock_Checkout_PromotionCodeOnHostedPage drives the buyer path: a
// session with allow_promotion_codes, a code typed on the mock page, then a
// bad code that re-renders inline and leaves the session open.
func TestStripeMock_Checkout_PromotionCodeOnHostedPage(t *testing.T) {
	mock, srv, _ := newFullStripeStack(t, "")
	withStripeBackend(t, srv.URL, func() {
		_, err := coupon.New(&stripe.CouponParams{ID: stripe.String("HALF"), PercentOff: stripe.Float64(50), MaxRedemptions: stripe.Int64(1)})
		require.NoError(t, err)
		_, err = promotioncode.New(&stripe.PromotionCodeParams{
			Code:      stripe.String("HALFOFF"),
			Promotion: &stripe.PromotionCodePromotionParams{Type: stripe.String("coupon"), Coupon: stripe.String("HALF")},
		})
		require.NoError(t, err)

		p := sessionParams(2000)
		p.AllowPromotionCodes = new(true)
		sess, err := session.New(p)
		require.NoError(t, err)
		assert.True(t, sess.AllowPromotionCodes)

		body := getCheckoutPage(t, mock, sess.ID, http.StatusOK)
		assert.Contains(t, body, `name="promotion_code"`, "page offers a code input")

		// Wrong code: 200 with the inline error, session untouched.
		resp := postCompleteWithCode(t, mock, sess.ID, "paid", "NOPE")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		page := readBodyString(t, resp)
		assert.Contains(t, page, "No such promotion code")
		assert.Contains(t, page, `value="NOPE"`, "typed code is kept in the input")
		got, err := session.Get(sess.ID, nil)
		require.NoError(t, err)
		assert.Equal(t, stripe.CheckoutSessionStatus("open"), got.Status)
		assert.Equal(t, int64(2000), got.AmountTotal)

		// Right code, any case: discounted, completed, counted.
		resp = postCompleteWithCode(t, mock, sess.ID, "paid", "halfoff")
		require.Equal(t, http.StatusSeeOther, resp.StatusCode)
		got, err = session.Get(sess.ID, nil)
		require.NoError(t, err)
		assert.Equal(t, stripe.CheckoutSessionStatus("complete"), got.Status)
		assert.Equal(t, int64(1000), got.AmountTotal)
		assert.Equal(t, int64(1000), got.TotalDetails.AmountDiscount)
		require.Len(t, got.Discounts, 1)
		assert.Equal(t, "HALF", got.Discounts[0].Coupon.ID)
		require.NotNil(t, got.Discounts[0].PromotionCode)
		pc, err := promotioncode.Get(got.Discounts[0].PromotionCode.ID, nil)
		require.NoError(t, err)
		assert.Equal(t, int64(1), pc.TimesRedeemed)
		c, err := coupon.Get("HALF", nil)
		require.NoError(t, err)
		assert.Equal(t, int64(1), c.TimesRedeemed)
		assert.False(t, c.Valid, "max_redemptions reached")

		// Exhausted coupon: the next buyer is refused with Stripe's wording.
		sess2, err := session.New(p)
		require.NoError(t, err)
		resp = postCompleteWithCode(t, mock, sess2.ID, "paid", "HALFOFF")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Contains(t, readBodyString(t, resp), "maximum redemptions")
	})
}

// TestStripeMock_Checkout_FullDiscountNeedsNoPayment: a 100% coupon completes
// the session as no_payment_required with no PaymentIntent, like Stripe.
func TestStripeMock_Checkout_FullDiscountNeedsNoPayment(t *testing.T) {
	mock, srv, _ := newFullStripeStack(t, "")
	sink := newWebhookSink(t, "whsec_test")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: sink.URL, Secret: "whsec_test"})
	withStripeBackend(t, srv.URL, func() {
		_, err := coupon.New(&stripe.CouponParams{ID: stripe.String("FREE"), PercentOff: stripe.Float64(100)})
		require.NoError(t, err)
		p := sessionParams(1500)
		p.Discounts = []*stripe.CheckoutSessionDiscountParams{{Coupon: stripe.String("FREE")}}
		sess, err := session.New(p)
		require.NoError(t, err)
		assert.Equal(t, int64(0), sess.AmountTotal)

		resp := postComplete(t, mock, sess.ID, "paid")
		require.Equal(t, http.StatusSeeOther, resp.StatusCode)
		got, err := session.Get(sess.ID, nil)
		require.NoError(t, err)
		assert.Equal(t, stripe.CheckoutSessionPaymentStatus("no_payment_required"), got.PaymentStatus)
		assert.Nil(t, got.PaymentIntent)

		evt := sink.Wait(t, 3*time.Second)
		assert.Equal(t, stripe.EventType("checkout.session.completed"), evt.Type)
		mock.mu.RLock()
		nPIs, nCharges := len(mock.paymentIntents), len(mock.charges)
		mock.mu.RUnlock()
		assert.Zero(t, nPIs)
		assert.Zero(t, nCharges)
	})
}

// TestStripeMock_Checkout_FailedRedemptionKeepsSessionPayable: a typed code
// whose coupon is exhausted by the time of payment is refused, and the
// session is left without a discount so the buyer can still pay full price.
func TestStripeMock_Checkout_FailedRedemptionKeepsSessionPayable(t *testing.T) {
	mock, srv, _ := newFullStripeStack(t, "")
	withStripeBackend(t, srv.URL, func() {
		_, err := coupon.New(&stripe.CouponParams{ID: stripe.String("ONE"), PercentOff: stripe.Float64(50), MaxRedemptions: stripe.Int64(1)})
		require.NoError(t, err)
		_, err = promotioncode.New(&stripe.PromotionCodeParams{
			Code: stripe.String("ONEOFF"), Promotion: &stripe.PromotionCodePromotionParams{Type: stripe.String("coupon"), Coupon: stripe.String("ONE")},
		})
		require.NoError(t, err)

		p := sessionParams(2000)
		p.AllowPromotionCodes = new(true)
		first, err := session.New(p)
		require.NoError(t, err)
		second, err := session.New(p)
		require.NoError(t, err)

		// Exhaust the coupon on the first session, then the second session
		// types the same code: apply succeeds, redeem must fail and roll back.
		_, _, err = mock.completeCheckout(first.ID, "paid", "ONEOFF")
		require.NoError(t, err)
		_, _, err = mock.completeCheckout(second.ID, "paid", "ONEOFF")
		var pe *errPromotionCode
		require.ErrorAs(t, err, &pe)
		assert.Contains(t, err.Error(), "maximum redemptions")

		got, err := session.Get(second.ID, nil)
		require.NoError(t, err)
		assert.Equal(t, stripe.CheckoutSessionStatus("open"), got.Status)
		assert.Empty(t, got.Discounts, "rejected code leaves no discount behind")
		assert.Equal(t, int64(2000), got.AmountTotal)
		assert.Contains(t, getCheckoutPage(t, mock, second.ID, http.StatusOK), `name="promotion_code"`, "code input is still offered")

		_, _, err = mock.completeCheckout(second.ID, "paid", "")
		require.NoError(t, err, "pays at full price without the code")
		got, err = session.Get(second.ID, nil)
		require.NoError(t, err)
		assert.Equal(t, stripe.CheckoutSessionStatus("complete"), got.Status)
		assert.Equal(t, int64(2000), got.AmountTotal)
	})
}

// TestStripeMock_Checkout_DeletedCouponRefusedAtPay: a coupon deleted after
// the session was created stops the payment, as Stripe does.
func TestStripeMock_Checkout_DeletedCouponRefusedAtPay(t *testing.T) {
	mock, srv := newTestStripeServer(t)
	withStripeBackend(t, srv.URL, func() {
		_, err := coupon.New(&stripe.CouponParams{ID: stripe.String("GONE"), PercentOff: stripe.Float64(10)})
		require.NoError(t, err)
		p := sessionParams(1000)
		p.Discounts = []*stripe.CheckoutSessionDiscountParams{{Coupon: stripe.String("GONE")}}
		sess, err := session.New(p)
		require.NoError(t, err)
		_, err = coupon.Del("GONE", nil)
		require.NoError(t, err)

		_, _, err = mock.completeCheckout(sess.ID, "paid", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "No such coupon: 'GONE'")
		got, err := session.Get(sess.ID, nil)
		require.NoError(t, err)
		assert.Equal(t, stripe.CheckoutSessionStatus("open"), got.Status)
	})
}

// TestStripeMock_Session_DiscountRules covers discounts[0].promotion_code at
// create, expiry rejections (redeem_by on the coupon, expires_at on the
// code, both checked at create) and the amount_off cap at the subtotal.
func TestStripeMock_Session_DiscountRules(t *testing.T) {
	_, srv := newTestStripeServer(t)
	withStripeBackend(t, srv.URL, func() {
		past := time.Now().Add(-time.Hour).Unix()
		_, err := coupon.New(&stripe.CouponParams{ID: stripe.String("TEN"), PercentOff: stripe.Float64(10)})
		require.NoError(t, err)
		_, err = coupon.New(&stripe.CouponParams{ID: stripe.String("OLD"), PercentOff: stripe.Float64(10), RedeemBy: new(past)})
		require.NoError(t, err)
		_, err = coupon.New(&stripe.CouponParams{ID: stripe.String("GBP50"), AmountOff: stripe.Int64(5000), Currency: stripe.String("gbp")})
		require.NoError(t, err)
		promo := func(code, couponID string, expiresAt int64) string {
			t.Helper()
			params := &stripe.PromotionCodeParams{
				Code: stripe.String(code), Promotion: &stripe.PromotionCodePromotionParams{Type: stripe.String("coupon"), Coupon: stripe.String(couponID)},
			}
			if expiresAt > 0 {
				params.ExpiresAt = new(expiresAt)
			}
			pc, err := promotioncode.New(params)
			require.NoError(t, err)
			return pc.ID
		}
		tenID := promo("TENOFF", "TEN", 0)
		oldID := promo("OLDCODE", "TEN", past)

		tests := []struct {
			name       string
			discount   *stripe.CheckoutSessionDiscountParams
			wantTotal  int64
			wantCoupon string
			wantPromo  string
			wantErr    string
		}{
			{name: "promotion_code at create", discount: &stripe.CheckoutSessionDiscountParams{PromotionCode: stripe.String(tenID)}, wantTotal: 2700, wantCoupon: "TEN", wantPromo: tenID},
			{name: "coupon past redeem_by", discount: &stripe.CheckoutSessionDiscountParams{Coupon: stripe.String("OLD")}, wantErr: "This coupon has expired"},
			{name: "code past expires_at", discount: &stripe.CheckoutSessionDiscountParams{PromotionCode: stripe.String(oldID)}, wantErr: "This promotion code has expired"},
			{name: "amount_off capped at subtotal", discount: &stripe.CheckoutSessionDiscountParams{Coupon: stripe.String("GBP50")}, wantTotal: 0, wantCoupon: "GBP50"},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				p := sessionParams(3000)
				p.Discounts = []*stripe.CheckoutSessionDiscountParams{tc.discount}
				sess, err := session.New(p)
				if tc.wantErr != "" {
					var se *stripe.Error
					require.ErrorAs(t, err, &se)
					assert.Contains(t, se.Msg, tc.wantErr)
					return
				}
				require.NoError(t, err)
				assert.Equal(t, tc.wantTotal, sess.AmountTotal)
				assert.Equal(t, 3000-tc.wantTotal, sess.TotalDetails.AmountDiscount)
				require.Len(t, sess.Discounts, 1)
				assert.Equal(t, tc.wantCoupon, sess.Discounts[0].Coupon.ID)
				if tc.wantPromo != "" {
					require.NotNil(t, sess.Discounts[0].PromotionCode)
					assert.Equal(t, tc.wantPromo, sess.Discounts[0].PromotionCode.ID)
				}
			})
		}
	})
}

// TestStripeMock_MCP_CompleteWithPromotionCode drives stripe.complete with a
// promotion_code the way an agent would.
func TestStripeMock_MCP_CompleteWithPromotionCode(t *testing.T) {
	mock, srv := newTestStripeServer(t)
	withStripeBackend(t, srv.URL, func() {
		_, err := coupon.New(&stripe.CouponParams{ID: stripe.String("HALF"), PercentOff: stripe.Float64(50)})
		require.NoError(t, err)
		_, err = promotioncode.New(&stripe.PromotionCodeParams{
			Code: stripe.String("HALFOFF"), Promotion: &stripe.PromotionCodePromotionParams{Type: stripe.String("coupon"), Coupon: stripe.String("HALF")},
		})
		require.NoError(t, err)
		p := sessionParams(2000)
		p.AllowPromotionCodes = new(true)
		sess, err := session.New(p)
		require.NoError(t, err)

		g := &mcpGateway{stripeMock: mock}
		_, err = g.stripeComplete([]byte(`{"session":"` + sess.ID + `","outcome":"paid","promotion_code":"nope"}`))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "No such promotion code")

		out, err := g.stripeComplete([]byte(`{"session":"` + sess.ID + `","outcome":"paid","promotion_code":"halfoff"}`))
		require.NoError(t, err)
		assert.Equal(t, okResult{OK: true}, out)
		got, err := session.Get(sess.ID, nil)
		require.NoError(t, err)
		assert.Equal(t, stripe.CheckoutSessionStatus("complete"), got.Status)
		assert.Equal(t, int64(1000), got.AmountTotal)
		require.Len(t, got.Discounts, 1)
		assert.Equal(t, "HALF", got.Discounts[0].Coupon.ID)
	})
}

// sessionParams is a one-item gbp payment session for the amount.
func sessionParams(unitAmount int64) *stripe.CheckoutSessionParams {
	return &stripe.CheckoutSessionParams{
		Mode: stripe.String(string(stripe.CheckoutSessionModePayment)),
		LineItems: []*stripe.CheckoutSessionLineItemParams{{
			PriceData: &stripe.CheckoutSessionLineItemPriceDataParams{
				Currency:    stripe.String("gbp"),
				ProductData: &stripe.CheckoutSessionLineItemPriceDataProductDataParams{Name: stripe.String("Thing")},
				UnitAmount:  new(unitAmount),
			},
			Quantity: stripe.Int64(1),
		}},
		SuccessURL: stripe.String("https://app.example/success"),
		CancelURL:  stripe.String("https://app.example/cancel"),
	}
}

// readBodyString drains and closes a response body.
func readBodyString(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close() //nolint:errcheck
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(b)
}

// postCompleteWithCode is postComplete with a typed promotion code.
func postCompleteWithCode(t *testing.T, mock *StripeMock, sessID, outcome, code string) *http.Response {
	t.Helper()
	form := url.Values{"session": {sessID}, "outcome": {outcome}, "promotion_code": {code}}
	req, err := http.NewRequest(http.MethodPost, mock.baseURL+"/__hamr/stripe/complete", strings.NewReader(form.Encode()))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	require.NoError(t, err)
	return resp
}
