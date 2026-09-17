package devserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	stripe "github.com/stripe/stripe-go/v86"
)

// These tests drive the mock through stripe.Client — the namespaced client
// Connect apps on Accounts v2 use — rather than the v1 package helpers.

func newMockClient(srvURL string) *stripe.Client {
	return stripe.NewClient("sk_test_mock", stripe.WithBackends(
		stripe.NewBackendsWithConfig(&stripe.BackendConfig{URL: stripe.String(srvURL)}),
	))
}

// recipientAccountParams mirrors what a marketplace creates for a seller.
func recipientAccountParams(email string) *stripe.V2CoreAccountCreateParams {
	return &stripe.V2CoreAccountCreateParams{
		ContactEmail: stripe.String(email),
		Dashboard:    stripe.String("express"),
		Identity:     &stripe.V2CoreAccountCreateIdentityParams{Country: stripe.String("GB")},
		Defaults: &stripe.V2CoreAccountCreateDefaultsParams{
			Currency: stripe.String("gbp"),
			Responsibilities: &stripe.V2CoreAccountCreateDefaultsResponsibilitiesParams{
				FeesCollector:   stripe.String("application"),
				LossesCollector: stripe.String("application"),
			},
		},
		Configuration: &stripe.V2CoreAccountCreateConfigurationParams{
			Recipient: &stripe.V2CoreAccountCreateConfigurationRecipientParams{
				Capabilities: &stripe.V2CoreAccountCreateConfigurationRecipientCapabilitiesParams{
					StripeBalance: &stripe.V2CoreAccountCreateConfigurationRecipientCapabilitiesStripeBalanceParams{
						StripeTransfers: &stripe.V2CoreAccountCreateConfigurationRecipientCapabilitiesStripeBalanceStripeTransfersParams{
							Requested: stripe.Bool(true),
						},
					},
				},
			},
		},
	}
}

// succeededPI runs createAndSucceedPI against this test's server; it uses the
// package-level stripe-go backend.
func succeededPI(t *testing.T, mock *StripeMock, srv *httptest.Server, amount int64) *stripe.PaymentIntent {
	t.Helper()
	var pi *stripe.PaymentIntent
	withStripeBackend(t, srv.URL, func() { pi = createAndSucceedPI(t, mock, amount, "") })
	return pi
}

func transfersStatus(acct *stripe.V2CoreAccount) string {
	if acct.Configuration == nil || acct.Configuration.Recipient == nil ||
		acct.Configuration.Recipient.Capabilities == nil ||
		acct.Configuration.Recipient.Capabilities.StripeBalance == nil ||
		acct.Configuration.Recipient.Capabilities.StripeBalance.StripeTransfers == nil {
		return ""
	}
	return string(acct.Configuration.Recipient.Capabilities.StripeBalance.StripeTransfers.Status)
}

// thinSink is an app's v2 webhook route: it only accepts what
// ParseEventNotification accepts.
type thinSink struct {
	URL    string
	mu     sync.Mutex
	got    []stripe.EventNotificationContainer
	errs   []error
	notify chan struct{}
}

func newThinSink(t *testing.T, secret string) *thinSink {
	t.Helper()
	s := &thinSink{notify: make(chan struct{}, 16)}
	client := stripe.NewClient("sk_test_mock")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n, err := client.ParseEventNotification(body, r.Header.Get("Stripe-Signature"), secret)
		s.mu.Lock()
		if err != nil {
			s.errs = append(s.errs, err)
		} else {
			s.got = append(s.got, n)
		}
		s.mu.Unlock()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.notify <- struct{}{}
	}))
	t.Cleanup(srv.Close)
	s.URL = srv.URL
	return s
}

func (s *thinSink) WaitFor(t *testing.T, n int) []stripe.EventNotificationContainer {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		s.mu.Lock()
		if len(s.got) >= n {
			out := append([]stripe.EventNotificationContainer(nil), s.got...)
			s.mu.Unlock()
			return out
		}
		errs := s.errs
		s.mu.Unlock()
		select {
		case <-s.notify:
		case <-deadline:
			t.Fatalf("timed out waiting for %d thin events (errs=%v)", n, errs)
		}
	}
}

func TestStripeMock_V2Account_CreateRetrieveAndLink(t *testing.T) {
	_, srv, _ := newFullStripeStack(t, "")
	c := newMockClient(srv.URL)
	ctx := context.Background()

	acct, err := c.V2CoreAccounts.Create(ctx, recipientAccountParams("seller@example.com"))
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(acct.ID, "acct_test_"))
	assert.Equal(t, "seller@example.com", acct.ContactEmail)
	assert.Equal(t, "pending", transfersStatus(acct))
	assert.False(t, acct.Created.IsZero(), "v2 created must decode as an RFC 3339 time")
	require.NotNil(t, acct.Requirements)
	assert.NotEmpty(t, acct.Requirements.Entries, "a pending capability has requirements due")

	got, err := c.V2CoreAccounts.Retrieve(ctx, acct.ID, &stripe.V2CoreAccountRetrieveParams{
		Include: stripe.StringSlice([]string{"configuration.recipient", "requirements"}),
	})
	require.NoError(t, err)
	assert.Equal(t, acct.ID, got.ID)
	assert.Equal(t, "pending", transfersStatus(got))

	link, err := c.V2CoreAccountLinks.Create(ctx, &stripe.V2CoreAccountLinkCreateParams{
		Account: stripe.String(acct.ID),
		UseCase: &stripe.V2CoreAccountLinkCreateUseCaseParams{
			Type: stripe.String("account_onboarding"),
			AccountOnboarding: &stripe.V2CoreAccountLinkCreateUseCaseAccountOnboardingParams{
				Configurations: stripe.StringSlice([]string{"recipient"}),
				ReturnURL:      stripe.String("http://app.test/seller/stripe/return"),
				RefreshURL:     stripe.String("http://app.test/seller/stripe/refresh"),
			},
		},
	})
	require.NoError(t, err)
	assert.Contains(t, link.URL, "/__hamr/stripe/onboarding?account="+acct.ID)

	_, err = c.V2CoreAccounts.Retrieve(ctx, "acct_test_missing", nil)
	require.Error(t, err, "unknown v2 account must surface as an error")
}

// TestStripeMock_V2Onboarding_ThinEventsAndTransferGate is the Connect v2
// loop: a transfer to a fresh account is refused, onboarding fires the two
// thin events and redirects to return_url, then the transfer goes through.
func TestStripeMock_V2Onboarding_ThinEventsAndTransferGate(t *testing.T) {
	const secret = "whsec_test_devmock"
	thin := newThinSink(t, secret)
	snap := newOrderedWebhookSink(t, secret)
	mock, srv, _ := newFullStripeStack(t, "")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: snap.URL, ThinURL: thin.URL, Secret: secret})
	c := newMockClient(srv.URL)
	ctx := context.Background()

	acct, err := c.V2CoreAccounts.Create(ctx, recipientAccountParams("seller@example.com"))
	require.NoError(t, err)
	_, err = c.V2CoreAccountLinks.Create(ctx, &stripe.V2CoreAccountLinkCreateParams{
		Account: stripe.String(acct.ID),
		UseCase: &stripe.V2CoreAccountLinkCreateUseCaseParams{
			Type: stripe.String("account_onboarding"),
			AccountOnboarding: &stripe.V2CoreAccountLinkCreateUseCaseAccountOnboardingParams{
				Configurations: stripe.StringSlice([]string{"recipient"}),
				ReturnURL:      stripe.String("http://app.test/seller/stripe/return"),
				RefreshURL:     stripe.String("http://app.test/seller/stripe/refresh"),
			},
		},
	})
	require.NoError(t, err)

	seedPlatformCharge(t, mock, 2000) // a charge nets amount minus Stripe's fee

	// Transfers are refused until the capability is active.
	_, err = c.V1Transfers.Create(ctx, &stripe.TransferCreateParams{
		Amount: stripe.Int64(1000), Currency: stripe.String("gbp"), Destination: stripe.String(acct.ID),
	})
	var serr *stripe.Error
	require.ErrorAs(t, err, &serr)
	assert.Equal(t, http.StatusBadRequest, serr.HTTPStatusCode)
	assert.Equal(t, stripe.ErrorCode("insufficient_capabilities_for_transfer"), serr.Code)

	// The onboarding page renders, and completing it redirects to return_url.
	resp, err := http.Get(srv.URL + "/__hamr/stripe/onboarding?account=" + acct.ID)
	require.NoError(t, err)
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close() //nolint:errcheck
	assert.Contains(t, string(page), "recipient.stripe_balance.stripe_transfers")

	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err = noFollow.PostForm(srv.URL+"/__hamr/stripe/account/complete", url.Values{"account": {acct.ID}})
	require.NoError(t, err)
	resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusSeeOther, resp.StatusCode)
	assert.Equal(t, "http://app.test/seller/stripe/return", resp.Header.Get("Location"))

	events := thin.WaitFor(t, 2)
	var sawRequirements, sawCapability bool
	for _, n := range events {
		switch e := n.(type) {
		case *stripe.V2CoreAccountIncludingRequirementsUpdatedEventNotification:
			sawRequirements = e.RelatedObject.ID == acct.ID
		case *stripe.V2CoreAccountIncludingConfigurationRecipientCapabilityStatusUpdatedEventNotification:
			sawCapability = e.RelatedObject.ID == acct.ID
		}
	}
	assert.True(t, sawRequirements, "requirements.updated thin event for the account")
	assert.True(t, sawCapability, "recipient capability_status_updated thin event for the account")

	got, err := c.V2CoreAccounts.Retrieve(ctx, acct.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, "active", transfersStatus(got))
	assert.Equal(t, stripe.V2CoreAccountConfigurationRecipientCapabilitiesStripeBalancePayoutsStatus("active"),
		got.Configuration.Recipient.Capabilities.StripeBalance.Payouts.Status)

	tr, err := c.V1Transfers.Create(ctx, &stripe.TransferCreateParams{
		Amount: stripe.Int64(1000), Currency: stripe.String("gbp"), Destination: stripe.String(acct.ID),
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1000), tr.Amount)
	assert.Equal(t, "transfer.created", string(snap.WaitFor(t, 1, 3*time.Second)[0].Type))

	// A second completion is a conflict, not a second round of events.
	resp, err = noFollow.PostForm(srv.URL+"/__hamr/stripe/account/complete", url.Values{"account": {acct.ID}})
	require.NoError(t, err)
	resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
}

func TestStripeMock_TransferReversal_FiresTransferReversed(t *testing.T) {
	const secret = "whsec_test_devmock"
	app := newOrderedWebhookSink(t, secret)
	mock, srv, _ := newFullStripeStack(t, "")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: app.URL, Secret: secret})
	c := newMockClient(srv.URL)
	ctx := context.Background()
	acctID := seedConnectedAccount(t, mock)
	seedPlatformCharge(t, mock, 6000)

	tr, err := c.V1Transfers.Create(ctx, &stripe.TransferCreateParams{
		Amount: stripe.Int64(3000), Currency: stripe.String("gbp"), Destination: stripe.String(acctID),
	})
	require.NoError(t, err)

	rv, err := c.V1TransferReversals.Create(ctx, &stripe.TransferReversalCreateParams{
		ID: stripe.String(tr.ID), Amount: stripe.Int64(1200),
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1200), rv.Amount)

	got, err := c.V1Transfers.Retrieve(ctx, tr.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, int64(1200), got.AmountReversed)
	require.NotNil(t, got.Reversals)
	require.Len(t, got.Reversals.Data, 1)
	assert.Equal(t, rv.ID, got.Reversals.Data[0].ID)

	_, err = c.V1TransferReversals.Create(ctx, &stripe.TransferReversalCreateParams{
		ID: stripe.String(tr.ID), Amount: stripe.Int64(5000),
	})
	require.Error(t, err, "reversing more than is left must fail")

	events := app.WaitFor(t, 2, 3*time.Second)
	assert.Equal(t, "transfer.created", string(events[0].Type))
	assert.Equal(t, "transfer.reversed", string(events[1].Type))
	var evTr stripe.Transfer
	require.NoError(t, json.Unmarshal(events[1].Data.Raw, &evTr))
	assert.Equal(t, int64(1200), evTr.AmountReversed)
}

func TestStripeMock_TransferFromSourceTransaction_Validates(t *testing.T) {
	mock, srv, _ := newFullStripeStack(t, "")
	c := newMockClient(srv.URL)
	ctx := context.Background()
	acctID := seedConnectedAccount(t, mock)
	pi := succeededPI(t, mock, srv, 2000)

	mock.mu.RLock()
	chargeID := mock.paymentIntents[pi.ID].LatestChargeID
	mock.mu.RUnlock()

	_, err := c.V1Transfers.Create(ctx, &stripe.TransferCreateParams{
		Amount: stripe.Int64(1500), Currency: stripe.String("gbp"), Destination: stripe.String(acctID),
		SourceTransaction: stripe.String(chargeID),
	})
	require.NoError(t, err)

	_, err = c.V1Transfers.Create(ctx, &stripe.TransferCreateParams{
		Amount: stripe.Int64(2500), Currency: stripe.String("gbp"), Destination: stripe.String(acctID),
		SourceTransaction: stripe.String(chargeID),
	})
	require.Error(t, err, "a transfer larger than its source charge must fail")
}

func TestStripeMock_BalanceTransactionOnCharge(t *testing.T) {
	mock, srv, _ := newFullStripeStack(t, "")
	c := newMockClient(srv.URL)
	sessID := createTestSession(t, mock, []stripe.LineItem{{Description: "Lesson", Quantity: 1, AmountTotal: 10000, Currency: "gbp"}})
	resp := postComplete(t, mock, sessID, "paid")
	resp.Body.Close() //nolint:errcheck

	mock.mu.RLock()
	piID := mock.sessions[sessID].PaymentIntentID
	chargeID := mock.paymentIntents[piID].LatestChargeID
	mock.mu.RUnlock()

	pi, err := c.V1PaymentIntents.Retrieve(context.Background(), piID, nil)
	require.NoError(t, err)
	require.NotNil(t, pi.LatestCharge)
	require.NotNil(t, pi.LatestCharge.BalanceTransaction)
	require.NotNil(t, pi.LatestCharge.PaymentMethodDetails)
	assert.Equal(t, "4242", pi.LatestCharge.PaymentMethodDetails.Card.Last4)

	bt, err := c.V1BalanceTransactions.Retrieve(context.Background(), pi.LatestCharge.BalanceTransaction.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, chargeBalanceTxnID(chargeID), bt.ID)
	assert.Equal(t, stripeFee(pi.LatestCharge.AmountCaptured, "gbp"), bt.Fee)
	assert.Positive(t, bt.Fee)
	assert.Equal(t, bt.Amount-bt.Fee, bt.Net)
}

func TestStripeMock_BalanceSettings_ScopedToAccount(t *testing.T) {
	mock, srv, _ := newFullStripeStack(t, "")
	c := newMockClient(srv.URL)
	acctID := seedConnectedAccount(t, mock)

	p := &stripe.BalanceSettingsUpdateParams{
		Payments: &stripe.BalanceSettingsUpdatePaymentsParams{
			DebitNegativeBalances: stripe.Bool(true),
			Payouts: &stripe.BalanceSettingsUpdatePaymentsPayoutsParams{
				Schedule: &stripe.BalanceSettingsUpdatePaymentsPayoutsScheduleParams{
					Interval:          stripe.String("monthly"),
					MonthlyPayoutDays: stripe.Int64Slice([]int64{31}),
				},
			},
		},
	}
	p.StripeAccount = stripe.String(acctID)
	bs, err := c.V1BalanceSettings.Update(context.Background(), p)
	require.NoError(t, err)
	assert.True(t, bs.Payments.DebitNegativeBalances)
	assert.Equal(t, stripe.BalanceSettingsPaymentsPayoutsScheduleInterval("monthly"), bs.Payments.Payouts.Schedule.Interval)
	assert.Equal(t, []int64{31}, bs.Payments.Payouts.Schedule.MonthlyPayoutDays)

	mock.mu.RLock()
	defer mock.mu.RUnlock()
	require.Contains(t, mock.balanceSettings, acctID)
	assert.NotContains(t, mock.balanceSettings, "", "settings belong to the account in the header, not the platform")
}

func TestStripeMock_ListRefunds_PagesThroughAll(t *testing.T) {
	mock, srv, _ := newFullStripeStack(t, "")
	c := newMockClient(srv.URL)
	ctx := context.Background()
	pi := succeededPI(t, mock, srv, 5000)
	for range 12 {
		_, err := c.V1Refunds.Create(ctx, &stripe.RefundCreateParams{PaymentIntent: stripe.String(pi.ID), Amount: stripe.Int64(100)})
		require.NoError(t, err)
	}
	mock.mu.RLock()
	chargeID := mock.paymentIntents[pi.ID].LatestChargeID
	mock.mu.RUnlock()

	var n int
	for rf, err := range c.V1Refunds.List(ctx, &stripe.RefundListParams{Charge: stripe.String(chargeID)}).All(ctx) {
		require.NoError(t, err)
		assert.Equal(t, chargeID, rf.Charge.ID)
		n++
	}
	assert.Equal(t, 12, n, "List(...).All must walk past the default page size of 10")
}

func TestStripeMock_RefundFullyRefunded_HasErrorCode(t *testing.T) {
	mock, srv, _ := newFullStripeStack(t, "")
	c := newMockClient(srv.URL)
	pi := succeededPI(t, mock, srv, 1000)
	_, err := c.V1Refunds.Create(context.Background(), &stripe.RefundCreateParams{PaymentIntent: stripe.String(pi.ID)})
	require.NoError(t, err)
	_, err = c.V1Refunds.Create(context.Background(), &stripe.RefundCreateParams{PaymentIntent: stripe.String(pi.ID)})
	var serr *stripe.Error
	require.ErrorAs(t, err, &serr)
	assert.Equal(t, stripe.ErrorCodeChargeAlreadyRefunded, serr.Code)
}

func TestStripeMock_CheckoutExpireAPI(t *testing.T) {
	const secret = "whsec_test_devmock"
	app := newWebhookSink(t, secret)
	mock, srv, _ := newFullStripeStack(t, "")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: app.URL, Secret: secret})
	c := newMockClient(srv.URL)
	sessID := createTestSession(t, mock, []stripe.LineItem{{Description: "Lesson", Quantity: 1, AmountTotal: 1000, Currency: "gbp"}})

	sess, err := c.V1CheckoutSessions.Expire(context.Background(), sessID, &stripe.CheckoutSessionExpireParams{})
	require.NoError(t, err)
	assert.Equal(t, stripe.CheckoutSessionStatusExpired, sess.Status)
	assert.Equal(t, "checkout.session.expired", string(app.Wait(t, 3*time.Second).Type))

	_, err = c.V1CheckoutSessions.Expire(context.Background(), sessID, &stripe.CheckoutSessionExpireParams{})
	var serr *stripe.Error
	require.ErrorAs(t, err, &serr)
	assert.Equal(t, http.StatusBadRequest, serr.HTTPStatusCode)
}

func TestStripeMock_Checkout_PaymentIntentDataMetadataReachesCharge(t *testing.T) {
	const secret = "whsec_test_devmock"
	app := newOrderedWebhookSink(t, secret)
	mock, srv, _ := newFullStripeStack(t, "")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: app.URL, Secret: secret})
	c := newMockClient(srv.URL)

	sess, err := c.V1CheckoutSessions.Create(context.Background(), &stripe.CheckoutSessionCreateParams{
		Mode:       stripe.String("payment"),
		SuccessURL: stripe.String("http://app.test/ok"),
		CancelURL:  stripe.String("http://app.test/cancel"),
		LineItems: []*stripe.CheckoutSessionCreateLineItemParams{{
			Quantity: stripe.Int64(1),
			PriceData: &stripe.CheckoutSessionCreateLineItemPriceDataParams{
				Currency: stripe.String("gbp"), UnitAmount: stripe.Int64(2500),
				ProductData: &stripe.CheckoutSessionCreateLineItemPriceDataProductDataParams{Name: stripe.String("Lesson")},
			},
		}},
		Metadata: map[string]string{"order_id": "o-session"},
		PaymentIntentData: &stripe.CheckoutSessionCreatePaymentIntentDataParams{
			Metadata: map[string]string{"order_id": "o-pi"},
		},
	})
	require.NoError(t, err)
	resp := postComplete(t, mock, sess.ID, "paid")
	resp.Body.Close() //nolint:errcheck

	events := app.WaitFor(t, 3, 3*time.Second)
	var ch stripe.Charge
	require.NoError(t, json.Unmarshal(events[2].Data.Raw, &ch))
	assert.Equal(t, "charge.succeeded", string(events[2].Type))
	assert.Equal(t, "o-pi", ch.Metadata["order_id"], "payment_intent_data.metadata lands on the charge")
}

func TestStripeMock_Idempotency_ReplaysFirstResponse(t *testing.T) {
	_, srv, _ := newFullStripeStack(t, "")
	c := newMockClient(srv.URL)
	ctx := context.Background()

	p1 := recipientAccountParams("a@example.com")
	p1.SetIdempotencyKey("seller-connect-1")
	a1, err := c.V2CoreAccounts.Create(ctx, p1)
	require.NoError(t, err)

	p2 := recipientAccountParams("a@example.com")
	p2.SetIdempotencyKey("seller-connect-1")
	a2, err := c.V2CoreAccounts.Create(ctx, p2)
	require.NoError(t, err)
	assert.Equal(t, a1.ID, a2.ID, "a replayed idempotency key must return the first account, not make a second")

	p3 := recipientAccountParams("a@example.com")
	p3.SetIdempotencyKey("seller-connect-2")
	a3, err := c.V2CoreAccounts.Create(ctx, p3)
	require.NoError(t, err)
	assert.NotEqual(t, a1.ID, a3.ID)
}

func TestStripeMock_ConnectedPayout_EventCarriesAccount(t *testing.T) {
	const secret = "whsec_test_devmock"
	app := newOrderedWebhookSink(t, secret)
	mock, srv, _ := newFullStripeStack(t, "")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: app.URL, Secret: secret})
	c := newMockClient(srv.URL)
	acctID := seedConnectedAccount(t, mock)
	seedPlatformCharge(t, mock, 8000)

	_, err := c.V1Transfers.Create(context.Background(), &stripe.TransferCreateParams{
		Amount: stripe.Int64(4000), Currency: stripe.String("gbp"), Destination: stripe.String(acctID),
	})
	require.NoError(t, err)
	app.WaitFor(t, 1, 3*time.Second)

	po, err := mock.payoutAccountBalance(acctID)
	require.NoError(t, err)
	assert.Equal(t, int64(4000), po.Amount)
	_, err = mock.payoutAccountBalance(acctID)
	require.Error(t, err, "the balance is already on its way out")

	resp := postPayoutComplete(t, mock, po.ID, "paid")
	resp.Body.Close() //nolint:errcheck
	events := app.WaitFor(t, 2, 3*time.Second)
	assert.Equal(t, "payout.paid", string(events[1].Type))
	assert.Equal(t, acctID, events[1].Account, "connected-account payouts name the account on the event")
}

func TestStripeMock_Dispute_OpenAndCloseWon(t *testing.T) {
	const secret = "whsec_test_devmock"
	app := newOrderedWebhookSink(t, secret)
	mock, srv, _ := newFullStripeStack(t, "")
	pi := succeededPI(t, mock, srv, 3000)
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: app.URL, Secret: secret})

	dp, err := mock.openDispute(pi.ID)
	require.NoError(t, err)
	_, err = mock.openDispute(pi.ID)
	require.Error(t, err, "a charge can only be disputed once")
	app.WaitFor(t, 2, 3*time.Second) // opening and closing are separate deliveries; keep them in order
	require.NoError(t, mock.closeDispute(dp.ID, "won"))
	require.Error(t, mock.closeDispute(dp.ID, "lost"), "a closed dispute stays closed")

	events := app.WaitFor(t, 4, 3*time.Second)
	types := make([]string, len(events))
	for i, e := range events {
		types[i] = string(e.Type)
	}
	assert.Equal(t, []string{"charge.dispute.created", "charge.dispute.funds_withdrawn", "charge.dispute.closed", "charge.dispute.funds_reinstated"}, types)

	var created, closed stripe.Dispute
	require.NoError(t, json.Unmarshal(events[0].Data.Raw, &created))
	require.NoError(t, json.Unmarshal(events[2].Data.Raw, &closed))
	assert.Equal(t, stripe.DisputeStatusNeedsResponse, created.Status)
	require.Len(t, created.BalanceTransactions, 1)
	assert.Equal(t, int64(disputeFee), created.BalanceTransactions[0].Fee)
	assert.Equal(t, stripe.DisputeStatusWon, closed.Status)
	require.Len(t, closed.BalanceTransactions, 2)
	assert.Equal(t, int64(0), closed.BalanceTransactions[0].Fee+closed.BalanceTransactions[1].Fee, "a won dispute returns the fee")
}

func TestStripeMock_EventLog_ResendReplaysSameEventID(t *testing.T) {
	const secret = "whsec_test_devmock"
	app := newOrderedWebhookSink(t, secret)
	mock, _, _ := newFullStripeStack(t, "")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: app.URL, Secret: secret})

	require.NoError(t, mock.FireEvent(t.Context(), "checkout.session.completed", map[string]any{"id": "cs_test_x", "object": "checkout.session"}))
	mock.mu.RLock()
	require.Len(t, mock.events, 1)
	ev := *mock.events[0]
	mock.mu.RUnlock()
	assert.True(t, ev.Delivered)
	assert.Equal(t, "cs_test_x", ev.ObjectID)

	require.NoError(t, mock.resendEvent(t.Context(), ev.ID))
	events := app.WaitFor(t, 2, 3*time.Second)
	assert.Equal(t, events[0].ID, events[1].ID, "a resend delivers the same event id")

	mock.mu.RLock()
	assert.Equal(t, 2, mock.events[0].Attempts)
	mock.mu.RUnlock()
}

func TestStripeMock_ThinEvent_NoThinURLLogsWithoutDelivering(t *testing.T) {
	const secret = "whsec_test_devmock"
	app := newOrderedWebhookSink(t, secret)
	mock, _, _ := newFullStripeStack(t, "")
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: app.URL, Secret: secret})

	require.NoError(t, mock.fireEvent(t.Context(), webhookFire{eventType: "v2.core.account[requirements].updated", thinRelatedID: "acct_test_x"}))
	mock.mu.RLock()
	defer mock.mu.RUnlock()
	require.Len(t, mock.events, 1)
	assert.True(t, mock.events[0].Thin)
	assert.Equal(t, 0, mock.events[0].Attempts, "no thin URL configured, so nothing is sent to the v1 route")
}

func TestStripeMock_Dashboard_RendersV2DisputesEventsAndControls(t *testing.T) {
	const secret = "whsec_test_devmock"
	app := newOrderedWebhookSink(t, secret)
	mock, srv, _ := newFullStripeStack(t, "")
	c := newMockClient(srv.URL)
	acct, err := c.V2CoreAccounts.Create(context.Background(), recipientAccountParams("seller@example.com"))
	require.NoError(t, err)
	pi := succeededPI(t, mock, srv, 2000)
	mock.SetWebhookEndpoint(WebhookEndpoint{URL: app.URL, Secret: secret})
	dp, err := mock.openDispute(pi.ID)
	require.NoError(t, err)
	app.WaitFor(t, 2, 3*time.Second)

	body := getDashboard(t, mock, http.StatusOK)
	assert.Contains(t, body, "Connect Accounts (v2)")
	assert.Contains(t, body, acct.ID[:14])
	assert.Contains(t, body, "seller@example.com")
	assert.Contains(t, body, "Disputes")
	assert.Contains(t, body, dp.ID[:14])
	assert.Contains(t, body, "Close: won")
	assert.Contains(t, body, "charge.dispute.created")
	assert.NotContains(t, body, `name="payment_intent" value="`+pi.ID+`"><button class="action danger">Dispute`,
		"an already-disputed payment offers no second Dispute button")

	mock.mu.RLock()
	var evID string
	for _, ev := range mock.events {
		if ev.Type == "charge.dispute.created" {
			evID = ev.ID
		}
	}
	mock.mu.RUnlock()
	form := url.Values{"event": {evID}}
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noFollow.PostForm(srv.URL+"/__hamr/stripe/resend", form)
	require.NoError(t, err)
	resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusSeeOther, resp.StatusCode)
	events := app.WaitFor(t, 3, 3*time.Second)
	assert.Equal(t, events[0].ID, events[2].ID)

	resp, err = noFollow.PostForm(srv.URL+"/__hamr/stripe/resend", url.Values{"event": {"evt_test_missing"}})
	require.NoError(t, err)
	resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// A handler that panics must not wedge its Idempotency-Key: the retry runs
// the handler again instead of blocking forever on the first attempt.
func TestStripeMock_Idempotency_PanicDoesNotWedgeKey(t *testing.T) {
	m := NewStripeMock(StripeMockOptions{BaseURL: "http://localhost:3000"})
	calls := 0
	h := m.idempotent(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			panic("boom")
		}
		w.WriteHeader(http.StatusCreated)
	})
	req := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/v1/payment_intents", nil)
		r.Header.Set("Idempotency-Key", "k1")
		return r
	}

	assert.Panics(t, func() { h(httptest.NewRecorder(), req()) })

	done := make(chan int)
	go func() {
		rec := httptest.NewRecorder()
		h(rec, req())
		done <- rec.Code
	}()
	select {
	case code := <-done:
		assert.Equal(t, http.StatusCreated, code)
		assert.Equal(t, 2, calls)
	case <-time.After(2 * time.Second):
		t.Fatal("retry blocked on the panicked request's key")
	}
}
