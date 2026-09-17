package devserver

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// registerBalanceRoutes mounts the balance endpoints.
//
//	GET  /v1/balance_transactions/{id}  — fee + net for a charge or dispute movement
//	GET  /v1/balance_settings           — payout schedule (Stripe-Account scoped)
//	POST /v1/balance_settings           — update it
func (m *StripeMock) registerBalanceRoutes(mux stripeRouter) {
	mux.HandleFunc("/v1/balance_transactions/", m.handleBalanceTransactionByID)
	mux.HandleFunc("/v1/balance_settings", m.handleBalanceSettings)
}

// stripeFee is the processing fee on a card charge. ponytail: one flat
// standard rate per currency (1.5% + 20 for gbp/eur, 2.9% + 30 otherwise);
// real pricing varies by card origin and plan.
func stripeFee(amount int64, currency string) int64 {
	if amount <= 0 {
		return 0
	}
	switch currency {
	case "gbp", "eur":
		return (amount*15+500)/1000 + 20
	default:
		return (amount*29+500)/1000 + 30
	}
}

// disputeFee is what Stripe charges for receiving a chargeback. It is never
// returned, even when the dispute is won. ponytail: one flat amount in the
// currency's minor unit; Stripe's varies by country, and the separate
// "countered" fee (charged on submitting evidence, returned on a win) is not
// modelled because the mock has no evidence step.
const disputeFee = 2000

// Balance transaction ids are derived from the object that moved the money,
// so they need no storage of their own and exist for charges persisted
// before this feature did.
func chargeBalanceTxnID(chargeID string) string      { return "txn_" + chargeID }
func disputeWithdrawalTxnID(disputeID string) string { return "txn_" + disputeID + "_withdrawal" }
func disputeReinstateTxnID(disputeID string) string  { return "txn_" + disputeID + "_reinstatement" }

func (m *StripeMock) handleBalanceTransactionByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/balance_transactions/")
	if r.Method != http.MethodGet {
		writeStripeError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	m.mu.RLock()
	obj := m.balanceTransactionLocked(id)
	m.mu.RUnlock()
	if obj == nil {
		writeStripeError(w, http.StatusNotFound, "invalid_request_error", fmt.Sprintf("No such balance transaction: '%s'", id))
		return
	}
	writeStripeJSON(w, http.StatusOK, obj)
}

// balanceTransactionLocked resolves a derived balance transaction id back to
// its charge or dispute. Caller holds m.mu.
func (m *StripeMock) balanceTransactionLocked(id string) map[string]any {
	rest := strings.TrimPrefix(id, "txn_")
	if ch, ok := m.charges[rest]; ok {
		fee := stripeFee(ch.AmountCaptured, ch.Currency)
		return serializeBalanceTxn(id, ch.ID, "charge", "charge", ch.AmountCaptured, fee, ch.Currency, ch.Created, "Stripe processing fees")
	}
	if dp, ok := m.disputes[strings.TrimSuffix(rest, "_withdrawal")]; ok && strings.HasSuffix(rest, "_withdrawal") {
		return serializeBalanceTxn(id, dp.ID, "adjustment", "dispute", -dp.Amount, disputeFee, dp.Currency, dp.Created, "Dispute fee")
	}
	if dp, ok := m.disputes[strings.TrimSuffix(rest, "_reinstatement")]; ok && strings.HasSuffix(rest, "_reinstatement") && dp.Status == "won" {
		return serializeBalanceTxn(id, dp.ID, "adjustment", "dispute_reversal", dp.Amount, 0, dp.Currency, dp.ClosedAt, "")
	}
	return nil
}

func serializeBalanceTxn(id, source, typ, category string, amount, fee int64, currency string, created time.Time, feeDesc string) map[string]any {
	feeDetails := []map[string]any{}
	if fee != 0 {
		feeDetails = append(feeDetails, map[string]any{"amount": fee, "currency": currency, "type": "stripe_fee", "description": feeDesc})
	}
	return map[string]any{
		"id":                 id,
		"object":             "balance_transaction",
		"amount":             amount,
		"fee":                fee,
		"net":                amount - fee,
		"currency":           currency,
		"created":            created.Unix(),
		"available_on":       created.Unix(),
		"status":             "available",
		"type":               typ,
		"reporting_category": category,
		"source":             source,
		"fee_details": feeDetails,
	}
}

// --- balance settings ---

// stripeBalanceSettings is an account's payout schedule and negative-balance
// handling, set via /v1/balance_settings with the Stripe-Account header.
type stripeBalanceSettings struct {
	DebitNegativeBalances bool     `json:"debit_negative_balances"`
	Interval              string   `json:"interval"` // manual | daily | weekly | monthly
	MonthlyPayoutDays     []int64  `json:"monthly_payout_days,omitempty"`
	WeeklyPayoutDays      []string `json:"weekly_payout_days,omitempty"`
	DelayDays             int64    `json:"delay_days"`
}

func defaultBalanceSettings() *stripeBalanceSettings {
	return &stripeBalanceSettings{Interval: "daily", DelayDays: 2}
}

func (m *StripeMock) handleBalanceSettings(w http.ResponseWriter, r *http.Request) {
	acct := strings.TrimSpace(r.Header.Get("Stripe-Account"))
	switch r.Method {
	case http.MethodGet:
	case http.MethodPost:
		p, ok := readStripeForm(w, r)
		if !ok {
			return
		}
		m.mu.Lock()
		if acct != "" && !m.accountExists(acct) {
			m.mu.Unlock()
			writeStripeError(w, http.StatusNotFound, "invalid_request_error", fmt.Sprintf("No such account: '%s'", acct))
			return
		}
		bs := m.balanceSettings[acct]
		if bs == nil {
			bs = defaultBalanceSettings()
			m.balanceSettings[acct] = bs
		}
		if err := applyBalanceSettings(bs, p); err != nil {
			m.mu.Unlock()
			writeStripeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		m.persist()
		m.mu.Unlock()
	default:
		writeStripeError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	m.mu.RLock()
	obj := serializeBalanceSettings(m.balanceSettingsFor(acct))
	m.mu.RUnlock()
	writeStripeJSON(w, http.StatusOK, obj)
}

// balanceSettingsFor returns acct's settings or Stripe's defaults. Caller
// holds m.mu.
func (m *StripeMock) balanceSettingsFor(acct string) *stripeBalanceSettings {
	if bs := m.balanceSettings[acct]; bs != nil {
		return bs
	}
	return defaultBalanceSettings()
}

func applyBalanceSettings(bs *stripeBalanceSettings, p map[string]any) error {
	pay, _ := p["payments"].(map[string]any)
	if pay == nil {
		return nil
	}
	if v := getString(pay, "debit_negative_balances"); v != "" {
		bs.DebitNegativeBalances = v == "true"
	}
	if st, ok := pay["settlement_timing"].(map[string]any); ok {
		if n, ok := getInt64(st, "delay_days"); ok && getString(st, "delay_days") != "" {
			bs.DelayDays = n
		}
	}
	po, _ := pay["payouts"].(map[string]any)
	sched, _ := po["schedule"].(map[string]any)
	if sched == nil {
		return nil
	}
	if iv := getString(sched, "interval"); iv != "" {
		switch iv {
		case "manual", "daily", "weekly", "monthly":
			bs.Interval = iv
		default:
			return fmt.Errorf("invalid payments.payouts.schedule.interval %q", iv)
		}
	}
	if days, ok := sched["monthly_payout_days"].([]any); ok {
		bs.MonthlyPayoutDays = nil
		for _, d := range days {
			n, err := strconv.ParseInt(fmt.Sprint(d), 10, 64)
			if err != nil || n < 1 || n > 31 {
				return fmt.Errorf("monthly_payout_days must be between 1 and 31, got %v", d)
			}
			bs.MonthlyPayoutDays = append(bs.MonthlyPayoutDays, n)
		}
	}
	if days, ok := sched["weekly_payout_days"].([]any); ok {
		bs.WeeklyPayoutDays = nil
		for _, d := range days {
			bs.WeeklyPayoutDays = append(bs.WeeklyPayoutDays, fmt.Sprint(d))
		}
	}
	return nil
}

func serializeBalanceSettings(bs *stripeBalanceSettings) map[string]any {
	schedule := map[string]any{"interval": bs.Interval}
	if len(bs.MonthlyPayoutDays) > 0 {
		schedule["monthly_payout_days"] = bs.MonthlyPayoutDays
	}
	if len(bs.WeeklyPayoutDays) > 0 {
		schedule["weekly_payout_days"] = bs.WeeklyPayoutDays
	}
	return map[string]any{
		"object": "balance_settings",
		"payments": map[string]any{
			"debit_negative_balances": bs.DebitNegativeBalances,
			"payouts": map[string]any{
				"schedule":             schedule,
				"statement_descriptor": nil,
				"status":               "enabled",
			},
			"settlement_timing": map[string]any{"delay_days": bs.DelayDays},
		},
	}
}
