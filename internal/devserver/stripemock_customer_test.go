package devserver

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	stripe "github.com/stripe/stripe-go/v86"
	"github.com/stripe/stripe-go/v86/customer"
	"github.com/stripe/stripe-go/v86/price"
	"github.com/stripe/stripe-go/v86/product"
)

// TestStripeMock_Customer_RoundTrip creates, retrieves, lists by email and
// updates a customer through the real stripe-go client.
func TestStripeMock_Customer_RoundTrip(t *testing.T) {
	_, srv := newTestStripeServer(t)
	withStripeBackend(t, srv.URL, func() {
		params := &stripe.CustomerParams{
			Email: stripe.String("ada@example.com"),
			Name:  stripe.String("Ada"),
		}
		params.AddMetadata("user_id", "42")
		c, err := customer.New(params)
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(c.ID, "cus_test_"))

		got, err := customer.Get(c.ID, nil)
		require.NoError(t, err)
		assert.Equal(t, "ada@example.com", got.Email)
		assert.Equal(t, "Ada", got.Name)
		assert.Equal(t, "42", got.Metadata["user_id"])

		_, err = customer.New(&stripe.CustomerParams{Email: stripe.String("bob@example.com")})
		require.NoError(t, err)

		// Email filter is exact but case-insensitive.
		iter := customer.List(&stripe.CustomerListParams{Email: stripe.String("ADA@example.com")})
		require.True(t, iter.Next(), "list by email should find it: %v", iter.Err())
		assert.Equal(t, c.ID, iter.Customer().ID)
		assert.False(t, iter.Next())
		require.NoError(t, iter.Err())

		upd, err := customer.Update(c.ID, &stripe.CustomerParams{Name: stripe.String("Ada L.")})
		require.NoError(t, err)
		assert.Equal(t, "Ada L.", upd.Name)
		assert.Equal(t, "ada@example.com", upd.Email, "untouched fields keep their value")

		var se *stripe.Error
		_, err = customer.Get("cus_nope", nil)
		require.ErrorAs(t, err, &se)
		assert.Equal(t, http.StatusNotFound, se.HTTPStatusCode)
		assert.Equal(t, stripe.ErrorCodeResourceMissing, se.Code)
		_, err = customer.Update("cus_nope", &stripe.CustomerParams{Name: stripe.String("x")})
		require.ErrorAs(t, err, &se)
		assert.Equal(t, http.StatusNotFound, se.HTTPStatusCode)
	})
}

// TestStripeMock_Price_RoundTrip covers inline product_data, recurring vs
// one_time, lookup_keys lookup, duplicate lookup_key and unknown product.
func TestStripeMock_Price_RoundTrip(t *testing.T) {
	_, srv := newTestStripeServer(t)
	withStripeBackend(t, srv.URL, func() {
		pr, err := price.New(&stripe.PriceParams{
			Currency:    stripe.String("gbp"),
			UnitAmount:  stripe.Int64(999),
			Recurring:   &stripe.PriceRecurringParams{Interval: stripe.String("month")},
			ProductData: &stripe.PriceProductDataParams{Name: stripe.String("Pro plan")},
			LookupKey:   stripe.String("pro_monthly"),
		})
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(pr.ID, "price_test_"))
		require.NotNil(t, pr.Product)
		assert.True(t, strings.HasPrefix(pr.Product.ID, "prod_test_"))

		got, err := price.Get(pr.ID, nil)
		require.NoError(t, err)
		assert.Equal(t, stripe.PriceTypeRecurring, got.Type)
		require.NotNil(t, got.Recurring)
		assert.Equal(t, stripe.PriceRecurringIntervalMonth, got.Recurring.Interval)
		assert.Equal(t, int64(1), got.Recurring.IntervalCount)
		assert.Equal(t, int64(999), got.UnitAmount)
		assert.Equal(t, "pro_monthly", got.LookupKey)

		prod, err := product.Get(pr.Product.ID, nil)
		require.NoError(t, err)
		assert.Equal(t, "Pro plan", prod.Name)
		assert.True(t, prod.Active)

		// One-time price against the existing product.
		once, err := price.New(&stripe.PriceParams{
			Currency: stripe.String("gbp"), UnitAmount: stripe.Int64(500), Product: stripe.String(prod.ID),
		})
		require.NoError(t, err)
		assert.Equal(t, stripe.PriceTypeOneTime, once.Type)
		assert.Nil(t, once.Recurring)

		iter := price.List(&stripe.PriceListParams{LookupKeys: stripe.StringSlice([]string{"pro_monthly"})})
		require.True(t, iter.Next(), "list by lookup_keys should find it: %v", iter.Err())
		assert.Equal(t, pr.ID, iter.Price().ID)
		assert.False(t, iter.Next())
		require.NoError(t, iter.Err())

		iter = price.List(&stripe.PriceListParams{Product: stripe.String(prod.ID), Type: stripe.String("one_time")})
		require.True(t, iter.Next())
		assert.Equal(t, once.ID, iter.Price().ID)
		assert.False(t, iter.Next())

		var se *stripe.Error
		_, err = price.New(&stripe.PriceParams{
			Currency: stripe.String("gbp"), UnitAmount: stripe.Int64(1), Product: stripe.String(prod.ID),
			LookupKey: stripe.String("pro_monthly"),
		})
		require.ErrorAs(t, err, &se, "duplicate lookup_key")
		assert.Equal(t, http.StatusBadRequest, se.HTTPStatusCode)

		_, err = price.New(&stripe.PriceParams{
			Currency: stripe.String("gbp"), UnitAmount: stripe.Int64(1), Product: stripe.String("prod_nope"),
		})
		require.ErrorAs(t, err, &se, "unknown product")
		assert.Equal(t, http.StatusBadRequest, se.HTTPStatusCode)
		assert.Equal(t, stripe.ErrorCodeResourceMissing, se.Code)

		_, err = product.New(&stripe.ProductParams{})
		require.ErrorAs(t, err, &se, "product without name")
		assert.Equal(t, http.StatusBadRequest, se.HTTPStatusCode)
	})
}
