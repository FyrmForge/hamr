package devserver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	stripe "github.com/stripe/stripe-go/v86"
	"github.com/stripe/stripe-go/v86/checkout/session"
	"github.com/stripe/stripe-go/v86/invoice"
	"github.com/stripe/stripe-go/v86/subscription"
)

// TestStripeMock_Checkout_CustomerAndPriceRefs: a session that references an
// existing customer= and line_items[].price= carries both through to the
// subscription, its invoice and the customer-filtered list.
func TestStripeMock_Checkout_CustomerAndPriceRefs(t *testing.T) {
	mock, srv, _ := newFullStripeStack(t, "")
	custID, priceID, prodID := seedCustomerAndPrice(t, mock, "month")
	withStripeBackend(t, srv.URL, func() {
		p := &stripe.CheckoutSessionParams{
			Mode:       stripe.String(string(stripe.CheckoutSessionModeSubscription)),
			Customer:   stripe.String(custID),
			LineItems:  []*stripe.CheckoutSessionLineItemParams{{Price: stripe.String(priceID), Quantity: stripe.Int64(2)}},
			SuccessURL: stripe.String("https://app.example/success"),
			CancelURL:  stripe.String("https://app.example/cancel"),
		}
		sess, err := session.New(p)
		require.NoError(t, err)
		assert.Equal(t, int64(2400), sess.AmountTotal, "resolved from the stored price × quantity")
		assert.Equal(t, stripe.Currency("gbp"), sess.Currency)
		require.NotNil(t, sess.Customer)
		assert.Equal(t, custID, sess.Customer.ID)
		require.NotNil(t, sess.CustomerDetails, "attached customer fills customer_details while open")
		assert.Equal(t, "ada@example.com", sess.CustomerDetails.Email)
		assert.Equal(t, "Ada", sess.CustomerDetails.Name)

		_, _, err = mock.completeCheckout(sess.ID, "paid", "")
		require.NoError(t, err)
		mock.mu.RLock()
		subID := mock.sessions[sess.ID].SubscriptionID
		sub := cloneSubscription(mock.subscriptions[subID])
		mock.mu.RUnlock()
		assert.Equal(t, custID, sub.CustomerID, "customer= is kept, no new customer minted")
		require.Len(t, sub.Items, 1)
		assert.Equal(t, priceID, sub.Items[0].PriceID)
		assert.Equal(t, prodID, sub.Items[0].ProductID)
		assert.Equal(t, "Pro", sub.Items[0].ProductName)

		iter := subscription.List(&stripe.SubscriptionListParams{Customer: stripe.String(custID)})
		require.True(t, iter.Next(), "GET /v1/subscriptions?customer= finds it")
		got := iter.Subscription()
		assert.Equal(t, subID, got.ID)
		assert.Equal(t, priceID, got.Items.Data[0].Price.ID)
		assert.Equal(t, "pro_monthly", got.Items.Data[0].Price.LookupKey, "stored price is rendered, not a synthesised one")
		assert.False(t, iter.Next())

		inv, err := invoice.Get(got.LatestInvoice.ID, nil)
		require.NoError(t, err)
		assert.Equal(t, "ada@example.com", inv.CustomerEmail)
		assert.Equal(t, "Ada", inv.CustomerName)

		// A paid subscription session without customer= mints a Customer
		// that carries the session's email.
		anon := subscriptionSessionParams(500, "month")
		anon.CustomerEmail = stripe.String("bob@example.com")
		anonSess, err := session.New(anon)
		require.NoError(t, err)
		assert.Nil(t, anonSess.CustomerDetails, "null while open with no customer attached")
		_, _, err = mock.completeCheckout(anonSess.ID, "paid", "")
		require.NoError(t, err)
		mock.mu.RLock()
		anonCust := mock.customers[mock.sessions[anonSess.ID].CustomerID]
		mock.mu.RUnlock()
		require.NotNil(t, anonCust)
		assert.Equal(t, "bob@example.com", anonCust.Email)
		done, err := session.Get(anonSess.ID, nil)
		require.NoError(t, err)
		require.NotNil(t, done.CustomerDetails)
		assert.Equal(t, "bob@example.com", done.CustomerDetails.Email)
	})
}

// TestStripeMock_Checkout_CustomerAndPriceRefs_Rejected covers the 400s:
// unknown ids, an inactive price, a recurring price in payment mode, and
// the mutually exclusive parameter pairs.
func TestStripeMock_Checkout_CustomerAndPriceRefs_Rejected(t *testing.T) {
	mock, srv, _ := newFullStripeStack(t, "")
	custID, priceID, _ := seedCustomerAndPrice(t, mock, "month")
	mock.mu.Lock()
	mock.prices["price_off"] = &stripePrice{ID: "price_off", ProductID: mock.prices[priceID].ProductID, Currency: "gbp", UnitAmount: 100, Created: mock.now()}
	mock.mu.Unlock()
	withStripeBackend(t, srv.URL, func() {
		mk := func(mode, customer, price string) *stripe.CheckoutSessionParams {
			return &stripe.CheckoutSessionParams{
				Mode:       stripe.String(mode),
				Customer:   stripe.String(customer),
				LineItems:  []*stripe.CheckoutSessionLineItemParams{{Price: stripe.String(price)}},
				SuccessURL: stripe.String("https://app.example/success"),
				CancelURL:  stripe.String("https://app.example/cancel"),
			}
		}
		expect := func(p *stripe.CheckoutSessionParams, code stripe.ErrorCode, msg string) {
			t.Helper()
			var se *stripe.Error
			_, err := session.New(p)
			require.ErrorAs(t, err, &se, msg)
			assert.Equal(t, 400, se.HTTPStatusCode, msg)
			assert.Equal(t, code, se.Code, msg)
			assert.Contains(t, se.Msg, msg)
		}
		expect(mk("subscription", "cus_nope", priceID), stripe.ErrorCodeResourceMissing, "No such customer: 'cus_nope'")
		expect(mk("subscription", custID, "price_nope"), stripe.ErrorCodeResourceMissing, "No such price: 'price_nope'")
		expect(mk("subscription", custID, "price_off"), "", "inactive")
		expect(mk("payment", custID, priceID), "", "use mode=subscription")

		both := mk("subscription", custID, priceID)
		both.CustomerEmail = stripe.String("x@example.com")
		expect(both, "", "customer, customer_email")

		both = subscriptionSessionParams(100, "month")
		both.LineItems[0].Price = stripe.String(priceID)
		expect(both, "", "price, price_data")
	})
}

// seedCustomerAndPrice inserts a customer plus a product and a recurring
// price directly into the mock and returns their ids. It does not go through
// the /v1/customers or /v1/prices routes so the checkout tests stay
// independent of them.
func seedCustomerAndPrice(t *testing.T, mock *StripeMock, interval string) (custID, priceID, prodID string) {
	t.Helper()
	custID, priceID, prodID = "cus_test_"+randomHex(14), "price_test_"+randomHex(16), "prod_test_"+randomHex(16)
	mock.mu.Lock()
	mock.customers[custID] = &stripeCustomer{ID: custID, Email: "ada@example.com", Name: "Ada", Created: mock.now()}
	mock.products[prodID] = &stripeProduct{ID: prodID, Name: "Pro", Active: true, Created: mock.now()}
	mock.prices[priceID] = &stripePrice{
		ID: priceID, ProductID: prodID, Currency: "gbp", UnitAmount: 1200,
		Interval: interval, IntervalCount: 1, LookupKey: "pro_monthly", Active: true, Created: mock.now(),
	}
	mock.persist()
	mock.mu.Unlock()
	return custID, priceID, prodID
}
