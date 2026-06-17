package devserver

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
)

// RegisterUIRoutes mounts the dev-facing checkout page + outcome handler on
// mux. These routes live on the proxy mux (/__hamr/* namespace), separate
// from the Stripe-API routes which require a path-free root and run on the
// dedicated stripe listener.
//
//	GET  /__hamr/stripe/checkout?session=<id>  — pick-an-outcome page
//	POST /__hamr/stripe/complete               — record outcome, fire webhook, redirect
func (m *StripeMock) RegisterUIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/__hamr/stripe/checkout", guardUnsafe(m.handleCheckoutPage))
	mux.HandleFunc("/__hamr/stripe/complete", guardUnsafe(m.handleComplete))
	m.registerAccountUIRoutes(mux)
	m.registerPaymentIntentUIRoutes(mux)
	m.registerPayoutUIRoutes(mux)
	m.registerDashboardRoutes(mux)
}

// outcomeRule maps a button press to the resulting session state, the event
// type to fire, and which redirect URL to follow. Centralized here so adding
// or renaming an outcome is a one-line change.
type outcomeRule struct {
	status        string // session.status after the outcome
	paymentStatus string // session.payment_status after the outcome
	eventType     string // Stripe webhook event type
	useSuccessURL bool   // true = redirect to success_url, false = cancel_url
	createPayment bool   // synthesize a succeeded PaymentIntent + Charge and fire their events
	leaveOpen     bool   // sync card decline: no state change, no event, return to checkout to retry
}

var stripeOutcomes = map[string]outcomeRule{
	"paid": {
		status:        "complete",
		paymentStatus: "paid",
		eventType:     "checkout.session.completed",
		useSuccessURL: true,
		createPayment: true,
	},
	"failed": {
		// A synchronous card decline. Real Stripe leaves the Checkout Session
		// open (the buyer stays on the hosted page to retry with another card)
		// and fires no webhook — the decline is surfaced inline, not via an
		// event. async_payment_failed is only for delayed/async methods (e.g.
		// bank debits), which this button does not model.
		leaveOpen: true,
	},
	"cancelled": {
		status:        "expired",
		paymentStatus: "unpaid",
		eventType:     "checkout.session.expired",
		useSuccessURL: false,
	},
}

// handleCheckoutPage renders the outcome-picker for ?session=<id>. Returns
// 400 without a session param, 404 for unknown sessions, 410 for sessions
// already completed/expired (so a stale browser tab doesn't double-fire).
func (m *StripeMock) handleCheckoutPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("session"))
	if id == "" {
		http.Error(w, "missing session query param", http.StatusBadRequest)
		return
	}

	m.mu.RLock()
	sess, ok := m.sessions[id]
	if ok {
		sess = cloneSession(sess)
	}
	m.mu.RUnlock()
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	if sess.Status != "open" {
		http.Error(w, fmt.Sprintf("session already %s; reload the app to start a new checkout", sess.Status), http.StatusGone)
		return
	}

	var buf bytes.Buffer
	if err := stripeCheckoutTmpl.Execute(&buf, struct {
		Session *stripeSession
		Total   string
	}{
		Session: sess,
		Total:   formatStripeAmount(sess.AmountTotal, sess.Currency),
	}); err != nil {
		http.Error(w, "render: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(buf.Bytes()) //nolint:errcheck
}

// handleComplete records the outcome and fires the matching webhook (async,
// fire-and-forget — we redirect immediately to mirror Stripe's UX). Errors
// during webhook delivery are logged via the mock's logger.
func (m *StripeMock) handleComplete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := strings.TrimSpace(r.FormValue("session"))
	outcome := strings.TrimSpace(r.FormValue("outcome"))
	if id == "" {
		http.Error(w, "missing session form field", http.StatusBadRequest)
		return
	}

	redirect, leaveOpen, err := m.completeCheckout(id, outcome)
	if err != nil {
		writeStripeOpError(w, err)
		return
	}
	// Synchronous decline: the session stays open with no state change and no
	// webhook. Send the buyer back to the hosted checkout page to retry.
	if leaveOpen {
		http.Redirect(w, r, "/__hamr/stripe/checkout?session="+url.QueryEscape(id), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

// writeStripeOpError maps a *stripeOpError to its HTTP status, falling back to
// 500 for any other error type.
func writeStripeOpError(w http.ResponseWriter, err error) {
	var oe *stripeOpError
	if errors.As(err, &oe) {
		http.Error(w, oe.msg, oe.status)
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

// formatStripeAmount renders an amount in the smallest currency unit using
// a currency-appropriate symbol. Currency is the lowercase ISO code as
// stored on the session. Unknown codes fall through as "<code> x.xx".
func formatStripeAmount(amount int64, currency string) string {
	switch strings.ToLower(currency) {
	case "gbp":
		return fmt.Sprintf("£%.2f", float64(amount)/100)
	case "usd":
		return fmt.Sprintf("$%.2f", float64(amount)/100)
	case "eur":
		return fmt.Sprintf("€%.2f", float64(amount)/100)
	case "jpy":
		return fmt.Sprintf("¥%d", amount)
	default:
		return fmt.Sprintf("%s %.2f", strings.ToUpper(currency), float64(amount)/100)
	}
}

// stripeFmtAmountForItem renders a per-item subtotal (unit_amount * quantity)
// using the same currency rules. Used by the checkout template's {{...}} call.
func stripeFmtAmountForItem(item stripeLineItem) string {
	return formatStripeAmount(item.UnitAmount*item.Quantity, item.Currency)
}

var stripeCheckoutTmpl = template.Must(template.New("stripe-checkout").
	Funcs(template.FuncMap{"itemTotal": stripeFmtAmountForItem}).
	Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>hamr — Stripe Checkout (Mock)</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{background:#0a2540;color:#fff;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;min-height:100vh;display:flex;align-items:center;justify-content:center;padding:24px}
.checkout{background:#1a3a5c;border-radius:12px;padding:36px;max-width:480px;width:100%;box-shadow:0 20px 60px rgba(0,0,0,0.3)}
.badge{display:inline-block;background:#ff6b35;color:#fff;font-size:11px;font-weight:700;padding:3px 8px;border-radius:4px;margin-bottom:16px;text-transform:uppercase;letter-spacing:0.05em}
h1{font-size:22px;margin-bottom:6px}
.sub{font-size:13px;color:#a3c4e0;margin-bottom:24px}
.items{border-top:1px solid rgba(255,255,255,0.1);margin-bottom:8px}
.item{display:flex;justify-content:space-between;padding:12px 0;border-bottom:1px solid rgba(255,255,255,0.1);font-size:14px}
.item-name{color:#a3c4e0}
.item-price{font-weight:600}
.total{display:flex;justify-content:space-between;padding:16px 0;font-size:18px;font-weight:700;border-top:2px solid rgba(255,255,255,0.2);margin-bottom:24px}
.actions{display:flex;flex-direction:column;gap:10px}
form{margin:0}
button{width:100%;padding:14px;border:none;border-radius:8px;font-size:15px;font-weight:600;cursor:pointer;font-family:inherit;transition:filter 0.15s}
button:hover{filter:brightness(1.1)}
.btn-pay{background:#635bff;color:#fff}
.btn-fail{background:#df1b41;color:#fff}
.btn-cancel{background:transparent;color:#a3c4e0;border:1px solid rgba(255,255,255,0.15)}
.session{font-size:11px;color:#5a7a9a;margin-top:20px;word-break:break-all;font-family:'SF Mono',Monaco,Consolas,monospace}
.meta{font-size:12px;color:#5a7a9a;margin-top:8px}
.hint{font-size:12px;color:#a3c4e0;margin:18px 0 14px}
</style>
</head>
<body>
<div class="checkout">
<span class="badge">Dev Mock</span>
<h1>Stripe Checkout</h1>
<p class="sub">Pick an outcome — your app's webhook handler will receive a real signed event.</p>

<div class="items">
{{range .Session.LineItems}}
<div class="item">
<span class="item-name">{{.Name}} ×{{.Quantity}}</span>
<span class="item-price">{{itemTotal .}}</span>
</div>
{{end}}
</div>
<div class="total">
<span>Total</span>
<span>{{.Total}}</span>
</div>

<p class="hint">Choose the outcome for this payment:</p>
<div class="actions">
<form method="POST" action="/__hamr/stripe/complete">
<input type="hidden" name="session" value="{{.Session.ID}}">
<input type="hidden" name="outcome" value="paid">
<button type="submit" class="btn-pay">Pay Successfully</button>
</form>
<form method="POST" action="/__hamr/stripe/complete">
<input type="hidden" name="session" value="{{.Session.ID}}">
<input type="hidden" name="outcome" value="failed">
<button type="submit" class="btn-fail">Card Declined</button>
</form>
<form method="POST" action="/__hamr/stripe/complete">
<input type="hidden" name="session" value="{{.Session.ID}}">
<input type="hidden" name="outcome" value="cancelled">
<button type="submit" class="btn-cancel">Cancel Payment</button>
</form>
</div>

<p class="session">Session: {{.Session.ID}}</p>
{{if .Session.Metadata}}
<div class="meta">
{{range $k, $v := .Session.Metadata}}<p>{{$k}}: {{$v}}</p>{{end}}
</div>
{{end}}
</div>
</body>
</html>
`))
