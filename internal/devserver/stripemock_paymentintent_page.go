package devserver

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"
)

// registerPaymentIntentUIRoutes mounts the dev-facing PI page + outcome
// handler on the proxy mux. Called from RegisterUIRoutes.
//
//	GET  /__hamr/stripe/payment_intent?id=<pi_id>     — state + Succeed/Fail buttons
//	POST /__hamr/stripe/payment_intent/complete       — apply outcome, fire webhooks
func (m *StripeMock) registerPaymentIntentUIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/__hamr/stripe/payment_intent", guardUnsafe(m.handlePaymentIntentPage))
	mux.HandleFunc("/__hamr/stripe/payment_intent/complete", guardUnsafe(m.handlePaymentIntentComplete))
}

// piOutcomeRule maps a button press to the resulting PI state and the
// webhook events to fire. Cascades for succeed are computed at fire time
// because they depend on whether transfer_data is set.
type piOutcomeRule struct {
	finalStatus string // PI status after the outcome
	primaryEvt  string // payment_intent.* event fired first
}

var paymentIntentOutcomes = map[string]piOutcomeRule{
	"succeed": {finalStatus: "succeeded", primaryEvt: "payment_intent.succeeded"},
	"fail":    {finalStatus: "requires_payment_method", primaryEvt: "payment_intent.payment_failed"},
}

// handlePaymentIntentPage renders the PI state for ?id=<pi_id>. Returns
// 404 for unknown PIs, 410 for already-terminal PIs (so a stale tab can't
// double-fire).
func (m *StripeMock) handlePaymentIntentPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		http.Error(w, "missing id query param", http.StatusBadRequest)
		return
	}

	m.mu.RLock()
	pi, ok := m.paymentIntents[id]
	if ok {
		pi = clonePaymentIntent(pi)
	}
	m.mu.RUnlock()
	if !ok {
		http.Error(w, "payment_intent not found", http.StatusNotFound)
		return
	}
	if pi.Status == "succeeded" || pi.Status == "canceled" {
		http.Error(w, fmt.Sprintf("payment_intent already %s", pi.Status), http.StatusGone)
		return
	}

	netToConnected := pi.Amount - pi.ApplicationFeeAmount
	if pi.TransferDataAmount > 0 {
		netToConnected = pi.TransferDataAmount
	}

	var buf bytes.Buffer
	if err := stripePaymentIntentTmpl.Execute(&buf, struct {
		PI              *stripePaymentIntent
		AmountFmt       string
		FeeFmt          string
		TransferAmtFmt  string
		IsDestCharge    bool
		IsDirectCharge  bool
	}{
		PI:             pi,
		AmountFmt:      formatStripeAmount(pi.Amount, pi.Currency),
		FeeFmt:         formatStripeAmount(pi.ApplicationFeeAmount, pi.Currency),
		TransferAmtFmt: formatStripeAmount(netToConnected, pi.Currency),
		IsDestCharge:   pi.TransferDataDestination != "",
		IsDirectCharge: pi.StripeAccount != "",
	}); err != nil {
		http.Error(w, "render: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(buf.Bytes()) //nolint:errcheck
}

// handlePaymentIntentComplete applies the outcome and fires the cascade of
// webhooks (PI event first, then charge.succeeded, then transfer.created
// if applicable). Webhooks are sent synchronously in a single goroutine so
// they arrive in the same order real Stripe sends them.
func (m *StripeMock) handlePaymentIntentComplete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := strings.TrimSpace(r.FormValue("id"))
	outcome := strings.TrimSpace(r.FormValue("outcome"))
	if id == "" {
		http.Error(w, "missing id form field", http.StatusBadRequest)
		return
	}
	rule, ok := paymentIntentOutcomes[outcome]
	if !ok {
		http.Error(w, fmt.Sprintf("unknown outcome %q (allowed: succeed, fail)", outcome), http.StatusBadRequest)
		return
	}

	m.mu.Lock()
	pi, exists := m.paymentIntents[id]
	if !exists {
		m.mu.Unlock()
		http.Error(w, "payment_intent not found", http.StatusNotFound)
		return
	}
	if pi.Status == "succeeded" || pi.Status == "canceled" {
		m.mu.Unlock()
		http.Error(w, fmt.Sprintf("payment_intent already %s", pi.Status), http.StatusConflict)
		return
	}
	// Implicit-confirm fallback: a dev creating a PI without a payment
	// method and clicking Succeed straight from the UI shouldn't get a
	// Charge with payment_method="". Default to pm_card_visa (same value
	// confirmPaymentIntent uses) so the synthesised Charge looks plausible.
	// Real Stripe rejects this path; the mock prefers ergonomics for dev.
	if outcome == "succeed" && pi.PaymentMethod == "" {
		pi.PaymentMethod = "pm_card_visa"
	}
	pi.Status = rule.finalStatus

	// Capture the cascade objects we'll need to fire webhooks for. Building
	// them while holding the lock guarantees we serialize cleanly against
	// any concurrent retrieve.
	var fires []webhookFire

	if outcome == "succeed" {
		pi.Failed = false // a prior decline is cleared once the PI succeeds
		ch := &stripeCharge{
			ID:                   "ch_test_" + randomHex(24),
			Amount:               pi.Amount,
			AmountCaptured:       pi.Amount,
			Currency:             pi.Currency,
			Status:               "succeeded",
			Paid:                 true,
			Captured:             true,
			PaymentIntentID:      pi.ID,
			PaymentMethod:        pi.PaymentMethod,
			ApplicationFeeAmount: pi.ApplicationFeeAmount,
			Destination:          pi.TransferDataDestination,
			Description:          pi.Description,
			ReceiptEmail:         pi.ReceiptEmail,
			Customer:             pi.Customer,
			Created:              time.Now(),
			Metadata:             pi.Metadata,
		}
		m.charges[ch.ID] = ch
		pi.LatestChargeID = ch.ID

		// Destination-charge cascade: auto-create a Transfer to the connected
		// account. By default the entire amount minus the application fee is
		// transferred, unless transfer_data.amount overrides.
		if pi.TransferDataDestination != "" {
			amt := pi.TransferDataAmount
			if amt == 0 {
				amt = pi.Amount - pi.ApplicationFeeAmount
			}
			tr := &stripeTransfer{
				ID:                  "tr_test_" + randomHex(24),
				Amount:              amt,
				Currency:            pi.Currency,
				Destination:         pi.TransferDataDestination,
				SourceTransactionID: ch.ID,
				Created:             time.Now(),
			}
			m.transfers[tr.ID] = tr
			pi.TransferID = tr.ID
			ch.TransferID = tr.ID

			fires = append(fires,
				webhookFire{rule.primaryEvt, m.serializePaymentIntent(pi, ch)},
				webhookFire{"charge.succeeded", m.serializeCharge(ch)},
				webhookFire{"transfer.created", m.serializeTransfer(tr)},
			)
		} else {
			fires = append(fires,
				webhookFire{rule.primaryEvt, m.serializePaymentIntent(pi, ch)},
				webhookFire{"charge.succeeded", m.serializeCharge(ch)},
			)
		}
	} else {
		// Fail path: just fire the PI failed event. No Charge is created in
		// real Stripe for a sync card decline, so don't synthesise one here.
		// Mark the PI failed so the dashboard can tell this declined PI apart
		// from a never-attempted one (both sit at requires_payment_method).
		pi.Failed = true
		fires = append(fires, webhookFire{rule.primaryEvt, m.serializePaymentIntent(pi, nil)})
	}
	m.persist()
	m.mu.Unlock()

	// Cascade ordering contract: events must be delivered in the order they
	// appear in `fires`. Real Stripe delivers cascade events sequentially in
	// a deterministic order (pi.succeeded → charge.succeeded → transfer.created)
	// and apps lift state-machine assumptions from that order. Do NOT
	// parallelise this loop — `TestStripeMock_PaymentIntentComplete_DestinationChargeCascade`
	// asserts the order via a single ordered sink. A failure mid-loop is
	// logged but does NOT abort the cascade; this matches real Stripe's
	// independent-delivery semantics (each event is its own retry surface).
	m.fireEventsAsync(fires, "payment_intent", id)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = fmt.Fprintf(w, `<!DOCTYPE html>
<html><head><title>hamr — PaymentIntent %s</title>
<style>body{background:#0a2540;color:#fff;font-family:-apple-system,sans-serif;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0}
.card{background:#1a3a5c;border-radius:12px;padding:36px;max-width:480px;text-align:center}
h1{margin:0 0 12px}.sub{color:#a3c4e0;font-size:14px;margin:0 0 18px}
code{background:#0a2540;padding:2px 6px;border-radius:4px;font-size:12px}</style>
</head><body><div class="card">
<h1>%s</h1>
<p class="sub">PaymentIntent <code>%s</code> is now <code>%s</code>.</p>
<p class="sub">Signed webhook events have been sent to your app.</p>
<p class="sub">You can close this tab.</p>
</div></body></html>`,
		outcome, outcomeHeading(outcome), id, rule.finalStatus)
}

func outcomeHeading(outcome string) string {
	switch outcome {
	case "succeed":
		return "✓ Payment succeeded"
	case "fail":
		return "✗ Payment failed"
	default:
		return outcome
	}
}

var stripePaymentIntentTmpl = template.Must(template.New("stripe-pi").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>hamr — Stripe PaymentIntent (Mock)</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{background:#0a2540;color:#fff;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;min-height:100vh;display:flex;align-items:center;justify-content:center;padding:24px}
.card{background:#1a3a5c;border-radius:12px;padding:36px;max-width:560px;width:100%;box-shadow:0 20px 60px rgba(0,0,0,0.3)}
.badge{display:inline-block;background:#ff6b35;color:#fff;font-size:11px;font-weight:700;padding:3px 8px;border-radius:4px;margin-bottom:16px;text-transform:uppercase;letter-spacing:0.05em}
h1{font-size:22px;margin-bottom:6px}
.sub{font-size:13px;color:#a3c4e0;margin-bottom:24px}
.kv{display:grid;grid-template-columns:160px 1fr;gap:8px 16px;font-size:13px;border-top:1px solid rgba(255,255,255,0.1);padding-top:18px;margin-bottom:24px}
.k{color:#a3c4e0}
.v{color:#fff;word-break:break-word}
.amount{font-size:28px;font-weight:700;color:#fff;text-align:center;margin:8px 0 4px}
.status{font-family:'SF Mono',Monaco,Consolas,monospace;font-size:12px;color:#fbbf24}
.actions{display:flex;flex-direction:column;gap:10px;margin-top:18px}
form{margin:0}
button{width:100%;padding:14px;border:none;border-radius:8px;font-size:15px;font-weight:600;cursor:pointer;font-family:inherit;transition:filter 0.15s}
button:hover{filter:brightness(1.1)}
.btn-pay{background:#22c55e;color:#fff}
.btn-fail{background:#df1b41;color:#fff}
.note{background:#0a2540;border-radius:6px;padding:12px;font-size:12px;color:#a3c4e0;margin-bottom:14px}
.session{font-size:11px;color:#5a7a9a;margin-top:18px;word-break:break-all;font-family:'SF Mono',Monaco,Consolas,monospace}
.tag{display:inline-block;background:#5a7a9a;color:#0a2540;font-size:10px;font-weight:700;padding:2px 6px;border-radius:3px;margin-right:6px;text-transform:uppercase;letter-spacing:0.05em}
</style>
</head>
<body>
<div class="card">
<span class="badge">Dev Mock</span>
<h1>PaymentIntent</h1>
<p class="sub">Pick an outcome — your app's webhook handler will receive real signed events.</p>

<p class="amount">{{.AmountFmt}}</p>
<p class="status" style="text-align:center;margin-bottom:18px">status: {{.PI.Status}}</p>

<div class="kv">
{{if .IsDestCharge}}<div class="k">
<span class="tag">Connect</span>destination charge
</div><div class="v"></div>{{end}}
{{if .IsDirectCharge}}<div class="k">
<span class="tag">Connect</span>direct charge
</div><div class="v">as {{.PI.StripeAccount}}</div>{{end}}
{{if gt .PI.ApplicationFeeAmount 0}}<div class="k">application fee</div><div class="v">{{.FeeFmt}}</div>{{end}}
{{if .IsDestCharge}}
<div class="k">→ destination</div><div class="v"><code>{{.PI.TransferDataDestination}}</code></div>
<div class="k">→ transfer amount</div><div class="v">{{.TransferAmtFmt}}</div>
{{end}}
{{if .PI.OnBehalfOf}}<div class="k">on_behalf_of</div><div class="v"><code>{{.PI.OnBehalfOf}}</code></div>{{end}}
{{if .PI.Description}}<div class="k">description</div><div class="v">{{.PI.Description}}</div>{{end}}
{{if .PI.ReceiptEmail}}<div class="k">receipt</div><div class="v">{{.PI.ReceiptEmail}}</div>{{end}}
<div class="k">capture method</div><div class="v">{{.PI.CaptureMethod}}</div>
</div>

{{if .IsDestCharge}}
<div class="note">On succeed: fires <code>payment_intent.succeeded</code>, <code>charge.succeeded</code>, then <code>transfer.created</code> (in that order).</div>
{{else}}
<div class="note">On succeed: fires <code>payment_intent.succeeded</code> then <code>charge.succeeded</code>.</div>
{{end}}

<div class="actions">
<form method="POST" action="/__hamr/stripe/payment_intent/complete">
<input type="hidden" name="id" value="{{.PI.ID}}">
<input type="hidden" name="outcome" value="succeed">
<button type="submit" class="btn-pay">Succeed</button>
</form>
<form method="POST" action="/__hamr/stripe/payment_intent/complete">
<input type="hidden" name="id" value="{{.PI.ID}}">
<input type="hidden" name="outcome" value="fail">
<button type="submit" class="btn-fail">Fail (card declined)</button>
</form>
</div>

<p class="session">PaymentIntent: {{.PI.ID}}</p>
</div>
</body>
</html>
`))
