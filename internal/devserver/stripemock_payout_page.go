package devserver

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"
)

// registerPayoutUIRoutes mounts the dev-facing payout page + outcome
// handler on the proxy mux. Called from RegisterUIRoutes.
//
//	GET  /__hamr/stripe/payout?id=<po_id>      — state + Mark paid / Mark failed
//	POST /__hamr/stripe/payout/complete        — apply outcome, fire webhook
func (m *StripeMock) registerPayoutUIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/__hamr/stripe/payout", m.handlePayoutPage)
	mux.HandleFunc("/__hamr/stripe/payout/complete", m.handlePayoutComplete)
}

// payoutOutcomeRule maps a button to the resulting status + event.
type payoutOutcomeRule struct {
	finalStatus    string
	eventType      string
	failureCode    string // populated only when status=failed
	failureMessage string
}

var payoutOutcomes = map[string]payoutOutcomeRule{
	"paid": {finalStatus: "paid", eventType: "payout.paid"},
	"fail": {
		finalStatus:    "failed",
		eventType:      "payout.failed",
		failureCode:    "account_closed",
		failureMessage: "The bank account has been closed.",
	},
}

// handlePayoutPage renders the payout state for ?id=<po_id>. Returns 404
// for unknown payouts, 410 for already-terminal payouts.
func (m *StripeMock) handlePayoutPage(w http.ResponseWriter, r *http.Request) {
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
	po, ok := m.payouts[id]
	if ok {
		po = clonePayout(po)
	}
	m.mu.RUnlock()
	if !ok {
		http.Error(w, "payout not found", http.StatusNotFound)
		return
	}
	if po.Status == "paid" || po.Status == "failed" || po.Status == "canceled" {
		http.Error(w, fmt.Sprintf("payout already %s", po.Status), http.StatusGone)
		return
	}

	var buf bytes.Buffer
	if err := stripePayoutTmpl.Execute(&buf, struct {
		Payout    *stripePayout
		AmountFmt string
		ArrivesIn string
	}{
		Payout:    po,
		AmountFmt: formatStripeAmount(po.Amount, po.Currency),
		ArrivesIn: arrivalLabel(po.ArrivalDate),
	}); err != nil {
		http.Error(w, "render: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(buf.Bytes()) //nolint:errcheck
}

// handlePayoutComplete records the outcome and fires payout.paid or
// payout.failed async. Same race-safety as the other complete handlers.
func (m *StripeMock) handlePayoutComplete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !checkSameOrigin(w, r) {
		return
	}
	id := strings.TrimSpace(r.FormValue("id"))
	outcome := strings.TrimSpace(r.FormValue("outcome"))
	if id == "" {
		http.Error(w, "missing id form field", http.StatusBadRequest)
		return
	}
	rule, ok := payoutOutcomes[outcome]
	if !ok {
		http.Error(w, fmt.Sprintf("unknown outcome %q (allowed: paid, fail)", outcome), http.StatusBadRequest)
		return
	}

	m.mu.Lock()
	po, exists := m.payouts[id]
	if !exists {
		m.mu.Unlock()
		http.Error(w, "payout not found", http.StatusNotFound)
		return
	}
	if po.Status == "paid" || po.Status == "failed" || po.Status == "canceled" {
		m.mu.Unlock()
		http.Error(w, fmt.Sprintf("payout already %s", po.Status), http.StatusConflict)
		return
	}
	po.Status = rule.finalStatus
	po.FailureCode = rule.failureCode
	po.FailureMessage = rule.failureMessage
	dataObject := m.serializePayout(po)
	m.persist()
	m.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := m.FireEvent(ctx, rule.eventType, dataObject); err != nil {
			m.logger.Warn("webhook delivery failed",
				"payout", id,
				"event_type", rule.eventType,
				"err", err,
			)
		}
	}()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = fmt.Fprintf(w, `<!DOCTYPE html>
<html><head><title>hamr — Payout %s</title>
<style>body{background:#0a2540;color:#fff;font-family:-apple-system,sans-serif;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0}
.card{background:#1a3a5c;border-radius:12px;padding:36px;max-width:480px;text-align:center}
h1{margin:0 0 12px}.sub{color:#a3c4e0;font-size:14px;margin:0 0 18px}
code{background:#0a2540;padding:2px 6px;border-radius:4px;font-size:12px}</style>
</head><body><div class="card">
<h1>%s</h1>
<p class="sub">Payout <code>%s</code> is now <code>%s</code>.</p>
<p class="sub">A signed <code>%s</code> webhook has been sent to your app.</p>
<p class="sub">You can close this tab.</p>
</div></body></html>`,
		outcome, payoutOutcomeHeading(outcome), id, rule.finalStatus, rule.eventType)
}

func payoutOutcomeHeading(outcome string) string {
	switch outcome {
	case "paid":
		return "✓ Payout paid"
	case "fail":
		return "✗ Payout failed"
	default:
		return outcome
	}
}

// arrivalLabel renders a friendly relative time for the payout's expected
// arrival date — e.g. "in 2 days" or "today" for instant payouts.
func arrivalLabel(t time.Time) string {
	d := time.Until(t)
	switch {
	case d <= time.Hour:
		return "today"
	case d < 24*time.Hour:
		return "today"
	case d < 48*time.Hour:
		return "tomorrow"
	default:
		return fmt.Sprintf("in %d days", int(d/(24*time.Hour))+1)
	}
}

var stripePayoutTmpl = template.Must(template.New("stripe-payout").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>hamr — Stripe Payout (Mock)</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{background:#0a2540;color:#fff;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;min-height:100vh;display:flex;align-items:center;justify-content:center;padding:24px}
.card{background:#1a3a5c;border-radius:12px;padding:36px;max-width:520px;width:100%;box-shadow:0 20px 60px rgba(0,0,0,0.3)}
.badge{display:inline-block;background:#ff6b35;color:#fff;font-size:11px;font-weight:700;padding:3px 8px;border-radius:4px;margin-bottom:16px;text-transform:uppercase;letter-spacing:0.05em}
h1{font-size:22px;margin-bottom:6px}
.sub{font-size:13px;color:#a3c4e0;margin-bottom:24px}
.amount{font-size:28px;font-weight:700;text-align:center;margin:8px 0 4px}
.status{font-family:'SF Mono',Monaco,Consolas,monospace;font-size:12px;color:#fbbf24;text-align:center;margin-bottom:18px}
.kv{display:grid;grid-template-columns:140px 1fr;gap:8px 16px;font-size:13px;border-top:1px solid rgba(255,255,255,0.1);padding-top:18px;margin-bottom:24px}
.k{color:#a3c4e0}
.v{color:#fff;word-break:break-word}
.actions{display:flex;flex-direction:column;gap:10px}
form{margin:0}
button{width:100%;padding:14px;border:none;border-radius:8px;font-size:15px;font-weight:600;cursor:pointer;font-family:inherit;transition:filter 0.15s}
button:hover{filter:brightness(1.1)}
.btn-paid{background:#22c55e;color:#fff}
.btn-failed{background:#df1b41;color:#fff}
.tag{display:inline-block;background:#5a7a9a;color:#0a2540;font-size:10px;font-weight:700;padding:2px 6px;border-radius:3px;margin-right:6px;text-transform:uppercase;letter-spacing:0.05em}
.session{font-size:11px;color:#5a7a9a;margin-top:18px;word-break:break-all;font-family:'SF Mono',Monaco,Consolas,monospace}
</style>
</head>
<body>
<div class="card">
<span class="badge">Dev Mock</span>
<h1>Payout</h1>
<p class="sub">A payout represents funds leaving Stripe to a bank account. Pick the outcome — your app's webhook handler will receive a real signed event.</p>

<p class="amount">{{.AmountFmt}}</p>
<p class="status">status: {{.Payout.Status}} · arrives {{.ArrivesIn}}</p>

<div class="kv">
{{if .Payout.AccountID}}<div class="k">
<span class="tag">Connect</span>connected acct
</div><div class="v"><code>{{.Payout.AccountID}}</code></div>{{end}}
<div class="k">method</div><div class="v">{{.Payout.Method}}</div>
<div class="k">source type</div><div class="v">{{.Payout.SourceType}}</div>
{{if .Payout.Description}}<div class="k">description</div><div class="v">{{.Payout.Description}}</div>{{end}}
{{if .Payout.StatementDescriptor}}<div class="k">statement</div><div class="v">{{.Payout.StatementDescriptor}}</div>{{end}}
</div>

<div class="actions">
<form method="POST" action="/__hamr/stripe/payout/complete">
<input type="hidden" name="id" value="{{.Payout.ID}}">
<input type="hidden" name="outcome" value="paid">
<button type="submit" class="btn-paid">Mark paid</button>
</form>
<form method="POST" action="/__hamr/stripe/payout/complete">
<input type="hidden" name="id" value="{{.Payout.ID}}">
<input type="hidden" name="outcome" value="fail">
<button type="submit" class="btn-failed">Mark failed</button>
</form>
</div>

<p class="session">Payout: {{.Payout.ID}}</p>
</div>
</body>
</html>
`))
