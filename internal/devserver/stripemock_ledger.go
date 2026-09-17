package devserver

import (
	"time"
)

// ledgerEntry is one money movement on a balance, the mock's stand-in for a
// Stripe balance transaction. Ledgers are derived from the stored objects on
// every read, so they can never disagree with them.
type ledgerEntry struct {
	Created     time.Time
	Type        string // charge | refund | transfer | transfer_reversal | payment | payment_refund | application_fee | application_fee_refund | adjustment | payout
	Description string
	Source      string // id of the object that moved the money
	Currency    string
	Amount      int64
	Fee         int64
	Status      string // for payouts: pending | paid | failed ...; empty otherwise
}

// Net is what the entry does to the balance. Failed and canceled payouts
// never left the balance.
func (e ledgerEntry) Net() int64 {
	if e.Type == "payout" && (e.Status == "failed" || e.Status == "canceled") {
		return 0
	}
	return e.Amount - e.Fee
}

// balanceOf sums a ledger per currency.
func balanceOf(entries []ledgerEntry) map[string]int64 {
	out := map[string]int64{}
	for _, e := range entries {
		out[e.Currency] += e.Net()
	}
	return out
}

// chargeOwner returns the connected account a charge was made as (direct
// charge via the Stripe-Account header), or "" for the platform. Caller holds
// m.mu.
func (m *StripeMock) chargeOwner(ch *stripeCharge) (acct string, appFee int64) {
	if pi, ok := m.paymentIntents[ch.PaymentIntentID]; ok && pi.StripeAccount != "" {
		return pi.StripeAccount, pi.ApplicationFeeAmount
	}
	return "", 0
}

// platformLedger lists every movement on the platform's own balance, newest
// first. Caller holds m.mu.
// ponytail: no pending/available split and no currency conversion; every
// entry is available at once in its own currency.
func (m *StripeMock) platformLedger() []ledgerEntry {
	var out []ledgerEntry
	for _, ch := range m.charges {
		owner, appFee := m.chargeOwner(ch)
		if owner != "" {
			if appFee > 0 {
				out = append(out, ledgerEntry{Created: ch.Created, Type: "application_fee", Description: "Application fee from " + owner,
					Source: ch.ID, Currency: ch.Currency, Amount: appFee})
			}
			continue
		}
		out = append(out, ledgerEntry{Created: ch.Created, Type: "charge", Description: ch.Description,
			Source: ch.ID, Currency: ch.Currency, Amount: ch.AmountCaptured, Fee: stripeFee(ch.AmountCaptured, ch.Currency)})
	}
	for _, rf := range m.refunds {
		ch, ok := m.charges[rf.ChargeID]
		if !ok {
			continue
		}
		if owner, appFee := m.chargeOwner(ch); owner != "" {
			if feeBack := refundedAppFee(rf, ch, appFee); feeBack > 0 {
				out = append(out, ledgerEntry{Created: rf.Created, Type: "application_fee_refund", Description: "Application fee refund to " + owner,
					Source: rf.ID, Currency: rf.Currency, Amount: -feeBack})
			}
			continue
		}
		out = append(out, ledgerEntry{Created: rf.Created, Type: "refund", Description: "Refund for " + rf.ChargeID,
			Source: rf.ID, Currency: rf.Currency, Amount: -rf.Amount})
	}
	for _, tr := range m.transfers {
		out = append(out, ledgerEntry{Created: tr.Created, Type: "transfer", Description: "Transfer to " + tr.Destination,
			Source: tr.ID, Currency: tr.Currency, Amount: -tr.Amount})
		for _, rv := range tr.Reversals {
			out = append(out, ledgerEntry{Created: rv.Created, Type: "transfer_reversal", Description: "Reversal of " + tr.ID,
				Source: rv.ID, Currency: tr.Currency, Amount: rv.Amount})
		}
	}
	out = append(out, m.disputeEntries("")...)
	for _, po := range m.payouts {
		if po.AccountID == "" {
			out = append(out, payoutEntry(po))
		}
	}
	sortNewestFirst(out, func(e ledgerEntry) (time.Time, string) { return e.Created, e.Source })
	return out
}

// connectedLedger lists every movement on one connected account's balance,
// newest first. Caller holds m.mu.
func (m *StripeMock) connectedLedger(acct string) []ledgerEntry {
	var out []ledgerEntry
	for _, tr := range m.transfers {
		if tr.Destination != acct {
			continue
		}
		out = append(out, ledgerEntry{Created: tr.Created, Type: "payment", Description: tr.Description,
			Source: tr.ID, Currency: tr.Currency, Amount: tr.Amount})
		for _, rv := range tr.Reversals {
			out = append(out, ledgerEntry{Created: rv.Created, Type: "payment_refund", Description: "Reversal of " + tr.ID,
				Source: rv.ID, Currency: tr.Currency, Amount: -rv.Amount})
		}
	}
	for _, ch := range m.charges {
		owner, appFee := m.chargeOwner(ch)
		if owner != acct {
			continue
		}
		out = append(out, ledgerEntry{Created: ch.Created, Type: "charge", Description: ch.Description,
			Source: ch.ID, Currency: ch.Currency, Amount: ch.AmountCaptured, Fee: stripeFee(ch.AmountCaptured, ch.Currency) + appFee})
	}
	for _, rf := range m.refunds {
		if ch, ok := m.charges[rf.ChargeID]; ok {
			if owner, appFee := m.chargeOwner(ch); owner == acct {
				out = append(out, ledgerEntry{Created: rf.Created, Type: "refund", Description: "Refund for " + rf.ChargeID,
					Source: rf.ID, Currency: rf.Currency, Amount: -rf.Amount, Fee: -refundedAppFee(rf, ch, appFee)})
			}
		}
	}
	out = append(out, m.disputeEntries(acct)...)
	for _, po := range m.payouts {
		if po.AccountID == acct {
			out = append(out, payoutEntry(po))
		}
	}
	sortNewestFirst(out, func(e ledgerEntry) (time.Time, string) { return e.Created, e.Source })
	return out
}

// disputeEntries lists the chargeback movements that land on acct ("" for
// the platform): a dispute hits whoever holds the charge. Caller holds m.mu.
func (m *StripeMock) disputeEntries(acct string) []ledgerEntry {
	var out []ledgerEntry
	for _, dp := range m.disputes {
		ch, ok := m.charges[dp.ChargeID]
		if !ok {
			continue
		}
		if owner, _ := m.chargeOwner(ch); owner != acct {
			continue
		}
		out = append(out, ledgerEntry{Created: dp.Created, Type: "adjustment", Description: "Chargeback withdrawal for " + dp.ChargeID,
			Source: dp.ID, Currency: dp.Currency, Amount: -dp.Amount, Fee: disputeFee})
		if dp.Status == "won" {
			out = append(out, ledgerEntry{Created: dp.ClosedAt, Type: "adjustment", Description: "Chargeback reversal for " + dp.ChargeID,
				Source: dp.ID, Currency: dp.Currency, Amount: dp.Amount})
		}
	}
	return out
}

// refundedAppFee is the slice of a direct charge's application fee that a
// refund with refund_application_fee=true hands back, pro rata to the
// refunded amount like Stripe does. Zero when the flag is off.
func refundedAppFee(rf *stripeRefund, ch *stripeCharge, appFee int64) int64 {
	if !rf.RefundApplicationFee || appFee <= 0 || ch.AmountCaptured <= 0 {
		return 0
	}
	return appFee * rf.Amount / ch.AmountCaptured
}

func payoutEntry(po *stripePayout) ledgerEntry {
	return ledgerEntry{Created: po.Created, Type: "payout", Description: "Payout to bank account",
		Source: po.ID, Currency: po.Currency, Amount: -po.Amount, Status: po.Status}
}

// connectedBalance is a connected account's balance per currency. Caller
// holds m.mu.
func (m *StripeMock) connectedBalance(acct string) map[string]int64 {
	return balanceOf(m.connectedLedger(acct))
}
