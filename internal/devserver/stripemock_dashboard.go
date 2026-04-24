package devserver

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strings"
	"time"
)

// registerDashboardRoutes mounts the dashboard + per-resource action
// endpoints. Called from RegisterUIRoutes.
//
//	GET  /__hamr/stripe                       — index (5 tables)
//	POST /__hamr/stripe/resend                — re-fire natural events for ?resource=&id=
//	POST /__hamr/stripe/refund                — issue refund on a PI from the dashboard
//	POST /__hamr/stripe/expire                — mark an open session expired
func (m *StripeMock) registerDashboardRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/__hamr/stripe", m.handleDashboard)
	mux.HandleFunc("/__hamr/stripe/resend", m.handleResend)
	mux.HandleFunc("/__hamr/stripe/refund", m.handleDashboardRefund)
	mux.HandleFunc("/__hamr/stripe/expire", m.handleDashboardExpire)
}

// dashboardLimit caps the number of rows shown per table. Plenty for dev;
// keeps the page from getting unwieldy after a long testing session.
const dashboardLimit = 25

// handleDashboard renders the index. Snapshots are taken under m.mu.RLock
// so the page is internally consistent even if concurrent mutations happen
// during rendering.
func (m *StripeMock) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Path matches /__hamr/stripe exactly; deeper paths fall through to
	// other handlers (e.g., /__hamr/stripe/checkout).
	if r.URL.Path != "/__hamr/stripe" {
		http.NotFound(w, r)
		return
	}

	m.mu.RLock()
	data := dashboardData{
		Sessions: snapshotSessions(m.sessions),
		Accounts: snapshotAccounts(m.accounts),
		PIs:      snapshotPaymentIntents(m.paymentIntents),
		Refunds:  snapshotRefunds(m.refunds),
		Payouts:  snapshotPayouts(m.payouts),
	}
	m.mu.RUnlock()

	var buf bytes.Buffer
	if err := stripeDashboardTmpl.Execute(&buf, data); err != nil {
		http.Error(w, "render: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(buf.Bytes()) //nolint:errcheck
}

// handleResend re-fires the natural event(s) for a resource in its current
// state. Routes by resource type; each branch builds the same data.object
// snapshots the original outcome path would have built. No state mutation
// (re-firing webhooks doesn't change the resource).
func (m *StripeMock) handleResend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !checkSameOrigin(w, r) {
		return
	}
	resource := r.FormValue("resource")
	id := strings.TrimSpace(r.FormValue("id"))
	if id == "" || resource == "" {
		http.Error(w, "missing resource or id", http.StatusBadRequest)
		return
	}

	type fire struct {
		eventType string
		object    map[string]any
	}
	var fires []fire
	var notFound bool

	m.mu.RLock()
	switch resource {
	case "session":
		sess, ok := m.sessions[id]
		if !ok {
			notFound = true
			break
		}
		evt := sessionResendEvent(sess)
		if evt != "" {
			fires = append(fires, fire{evt, m.serializeSession(sess)})
		}
	case "account":
		acct, ok := m.accounts[id]
		if !ok {
			notFound = true
			break
		}
		// Only meaningful to resend account.updated once the account has
		// reached the post-onboarding state. Otherwise no-op (button
		// shouldn't be rendered at all in that case).
		if acct.DetailsSubmitted {
			fires = append(fires, fire{"account.updated", m.serializeAccount(acct)})
		}
	case "payment_intent":
		pi, ok := m.paymentIntents[id]
		if !ok {
			notFound = true
			break
		}
		switch pi.Status {
		case "succeeded":
			fires = append(fires, fire{"payment_intent.succeeded", m.serializePaymentIntent(pi)})
			if ch, ok := m.charges[pi.LatestChargeID]; ok {
				fires = append(fires, fire{"charge.succeeded", m.serializeCharge(ch)})
			}
			if pi.TransferID != "" {
				if tr, ok := m.transfers[pi.TransferID]; ok {
					fires = append(fires, fire{"transfer.created", m.serializeTransfer(tr)})
				}
			}
		case "requires_payment_method":
			// PI was failed (status reset by handlePaymentIntentComplete on fail outcome).
			fires = append(fires, fire{"payment_intent.payment_failed", m.serializePaymentIntent(pi)})
		}
	case "refund":
		rf, ok := m.refunds[id]
		if !ok {
			notFound = true
			break
		}
		// Refund event payload is the post-refund Charge, not the Refund
		// object — matches what real Stripe sends on charge.refunded.
		if ch, ok := m.charges[rf.ChargeID]; ok {
			fires = append(fires, fire{"charge.refunded", m.serializeCharge(ch)})
		}
	case "payout":
		po, ok := m.payouts[id]
		if !ok {
			notFound = true
			break
		}
		switch po.Status {
		case "paid":
			fires = append(fires, fire{"payout.paid", m.serializePayout(po)})
		case "failed":
			fires = append(fires, fire{"payout.failed", m.serializePayout(po)})
		}
	default:
		m.mu.RUnlock()
		http.Error(w, fmt.Sprintf("unknown resource %q", resource), http.StatusBadRequest)
		return
	}
	m.mu.RUnlock()

	if notFound {
		http.Error(w, "resource not found", http.StatusNotFound)
		return
	}
	if len(fires) == 0 {
		http.Error(w, "nothing to resend in current state", http.StatusBadRequest)
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for _, f := range fires {
			if err := m.FireEvent(ctx, f.eventType, f.object); err != nil {
				m.logger.Warn("dashboard resend failed",
					"resource", resource,
					"id", id,
					"event_type", f.eventType,
					"err", err,
				)
			}
		}
	}()

	http.Redirect(w, r, "/__hamr/stripe", http.StatusSeeOther)
}

// handleDashboardRefund issues a refund on a PI directly from the
// dashboard. Reuses the existing applyRefund path so all the validation,
// charge mutation, and webhook delivery semantics are identical to a
// stripe-go refund.New call.
func (m *StripeMock) handleDashboardRefund(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !checkSameOrigin(w, r) {
		return
	}
	piID := strings.TrimSpace(r.FormValue("payment_intent"))
	if piID == "" {
		http.Error(w, "missing payment_intent", http.StatusBadRequest)
		return
	}
	rf, _, eventObject, err := m.applyRefund(refundInput{
		piID:            piID,
		amount:          getInt64(map[string]any{"amount": r.FormValue("amount")}, "amount"),
		reverseTransfer: r.FormValue("reverse_transfer") == "true",
		refundAppFee:    r.FormValue("refund_application_fee") == "true",
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := m.FireEvent(ctx, "charge.refunded", eventObject); err != nil {
			m.logger.Warn("dashboard refund webhook delivery failed",
				"refund", rf.ID,
				"err", err,
			)
		}
	}()
	http.Redirect(w, r, "/__hamr/stripe", http.StatusSeeOther)
}

// handleDashboardExpire marks an open session as expired and fires
// checkout.session.expired. Equivalent to clicking "Cancel Payment" on the
// existing checkout page but reachable from the dashboard for sessions
// that were created via the API but never completed.
func (m *StripeMock) handleDashboardExpire(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !checkSameOrigin(w, r) {
		return
	}
	id := strings.TrimSpace(r.FormValue("session"))
	if id == "" {
		http.Error(w, "missing session", http.StatusBadRequest)
		return
	}

	m.mu.Lock()
	sess, ok := m.sessions[id]
	if !ok {
		m.mu.Unlock()
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	if sess.Status != "open" {
		m.mu.Unlock()
		http.Error(w, fmt.Sprintf("session already %s", sess.Status), http.StatusConflict)
		return
	}
	sess.Status = "expired"
	sess.PaymentStatus = "unpaid"
	dataObject := m.serializeSession(sess)
	m.persist()
	m.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := m.FireEvent(ctx, "checkout.session.expired", dataObject); err != nil {
			m.logger.Warn("dashboard expire webhook delivery failed",
				"session", id,
				"err", err,
			)
		}
	}()
	http.Redirect(w, r, "/__hamr/stripe", http.StatusSeeOther)
}

// sessionResendEvent picks the right event type to re-fire based on the
// session's current outcome state. Returns "" if the session is open
// (nothing to resend until an outcome runs).
func sessionResendEvent(s *stripeSession) string {
	switch {
	case s.Status == "expired":
		return "checkout.session.expired"
	case s.Status == "complete" && s.PaymentStatus == "paid":
		return "checkout.session.completed"
	case s.Status == "complete":
		return "checkout.session.async_payment_failed"
	}
	return ""
}

// --- snapshot helpers ---
//
// Each snapshotter copies map values into a slice sorted newest-first and
// caps at dashboardLimit. Pointer values share storage with the live state
// — safe because the dashboard only reads, but callers must not mutate.

func snapshotSessions(m map[string]*stripeSession) []*stripeSession {
	out := make([]*stripeSession, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	if len(out) > dashboardLimit {
		out = out[:dashboardLimit]
	}
	return out
}

func snapshotAccounts(m map[string]*stripeAccount) []*stripeAccount {
	out := make([]*stripeAccount, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	if len(out) > dashboardLimit {
		out = out[:dashboardLimit]
	}
	return out
}

func snapshotPaymentIntents(m map[string]*stripePaymentIntent) []*stripePaymentIntent {
	out := make([]*stripePaymentIntent, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	if len(out) > dashboardLimit {
		out = out[:dashboardLimit]
	}
	return out
}

func snapshotRefunds(m map[string]*stripeRefund) []*stripeRefund {
	out := make([]*stripeRefund, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	if len(out) > dashboardLimit {
		out = out[:dashboardLimit]
	}
	return out
}

func snapshotPayouts(m map[string]*stripePayout) []*stripePayout {
	out := make([]*stripePayout, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	if len(out) > dashboardLimit {
		out = out[:dashboardLimit]
	}
	return out
}

// dashboardData is the template input.
type dashboardData struct {
	Sessions []*stripeSession
	Accounts []*stripeAccount
	PIs      []*stripePaymentIntent
	Refunds  []*stripeRefund
	Payouts  []*stripePayout
}

// dashboardFuncs exposes formatting helpers to the template.
var dashboardFuncs = template.FuncMap{
	"shortID": func(id string) string {
		// Stripe IDs (cs_test_X, pi_test_X, etc.) get tail-truncated to
		// keep table columns narrow but still recognisable.
		if len(id) <= 16 {
			return id
		}
		return id[:14] + "…"
	},
	"amount":     formatStripeAmount,
	"sessionEvt": sessionResendEvent,
	"hasResendForSession": func(s *stripeSession) bool {
		return sessionResendEvent(s) != ""
	},
	"piResendable": func(p *stripePaymentIntent) bool {
		return p.Status == "succeeded" || p.Status == "requires_payment_method"
	},
	"payoutResendable": func(p *stripePayout) bool {
		return p.Status == "paid" || p.Status == "failed"
	},
	"since": func(t time.Time) string {
		return time.Since(t).Round(time.Second).String() + " ago"
	},
}

var stripeDashboardTmpl = template.Must(template.New("stripe-dashboard").Funcs(dashboardFuncs).Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>hamr — Stripe Mock Dashboard</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{background:#0f1419;color:#d4d4d4;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;min-height:100vh}
a{color:#7dd3fc;text-decoration:none}
a:hover{text-decoration:underline}
code{font-family:'SF Mono',Monaco,Consolas,monospace;background:#0a2540;padding:1px 5px;border-radius:3px;font-size:11px;color:#a3c4e0}
.wrap{max-width:1200px;margin:0 auto;padding:32px 24px}
h1{font-size:22px;font-weight:700;color:#e8e8e8;margin-bottom:6px;display:flex;align-items:center;gap:12px}
.badge{display:inline-block;background:#635bff;color:#fff;font-size:10px;font-weight:700;padding:3px 8px;border-radius:4px;text-transform:uppercase;letter-spacing:0.05em}
.sub{font-size:13px;color:#64748b;margin-bottom:24px}
section{background:#161b22;border:1px solid #2e3642;border-radius:8px;margin-bottom:20px;overflow:hidden}
section h2{font-size:14px;font-weight:600;color:#e8e8e8;padding:14px 18px;background:#1a1f26;border-bottom:1px solid #2e3642;display:flex;justify-content:space-between;align-items:center}
section h2 .count{color:#64748b;font-size:12px;font-weight:500}
.empty{padding:18px;text-align:center;color:#64748b;font-size:13px}
table{width:100%;border-collapse:collapse;font-size:12px}
th,td{padding:9px 14px;text-align:left;border-bottom:1px solid #2e3642;vertical-align:middle}
th{color:#64748b;font-weight:600;text-transform:uppercase;font-size:10px;letter-spacing:0.05em;background:#0d1117}
tr:last-child td{border-bottom:none}
.status-tag{display:inline-block;padding:2px 6px;border-radius:3px;font-size:10px;font-weight:700;text-transform:uppercase}
.status-paid,.status-succeeded,.status-complete{background:#14532d;color:#86efac}
.status-failed,.status-canceled{background:#481414;color:#fca5a5}
.status-pending,.status-requires_payment_method,.status-requires_confirmation,.status-requires_action,.status-requires_capture{background:#422006;color:#fbbf24}
.status-expired,.status-unpaid{background:#3f1d52;color:#c4b5fd}
.status-open{background:#1e3a5f;color:#93c5fd}
.flag-on{color:#86efac;font-weight:600}
.flag-off{color:#fca5a5}
form.row-form{display:inline-flex;gap:6px;margin:0;align-items:center}
form.row-form input[type=number]{background:#0a2540;color:#d4d4d4;border:1px solid #2e3642;border-radius:4px;padding:4px 6px;font-size:11px;font-family:inherit;width:90px}
button.action{background:#1a1f26;color:#d4d4d4;border:1px solid #2e3642;border-radius:4px;padding:4px 10px;font-size:11px;font-weight:500;cursor:pointer;font-family:inherit}
button.action:hover{filter:brightness(1.4)}
button.action.danger{color:#f87171;border-color:#481414}
button.action.primary{background:#635bff;color:#fff;border-color:#635bff}
.actions-cell{white-space:nowrap;display:flex;gap:6px;flex-wrap:wrap;align-items:center}
.tag{display:inline-block;background:#5a7a9a;color:#0a2540;font-size:9px;font-weight:700;padding:1px 5px;border-radius:3px;text-transform:uppercase;letter-spacing:0.05em;margin-left:4px}
</style>
</head>
<body>
<div class="wrap">
<h1>Stripe Mock <span class="badge">Dev Dashboard</span></h1>
<p class="sub">Snapshot of every resource the local Stripe mock has captured. State is persisted to <code>.hamr/stripe/state.json</code> by default — survives <code>hamr dev</code> restart.</p>

<section>
<h2>Checkout Sessions <span class="count">{{len .Sessions}}</span></h2>
{{if .Sessions -}}
<table>
<thead><tr><th>ID</th><th>Status</th><th>Amount</th><th>Actions</th></tr></thead>
<tbody>
{{range .Sessions}}
<tr>
<td><code>{{shortID .ID}}</code></td>
<td><span class="status-tag status-{{.Status}}">{{.Status}}</span>{{if eq .Status "complete"}} <span class="status-tag status-{{.PaymentStatus}}">{{.PaymentStatus}}</span>{{end}}</td>
<td>{{amount .AmountTotal .Currency}}</td>
<td class="actions-cell">
{{if eq .Status "open"}}
<form class="row-form" method="POST" action="/__hamr/stripe/expire"><input type="hidden" name="session" value="{{.ID}}"><button class="action danger">Expire</button></form>
{{end}}
{{if hasResendForSession .}}
<form class="row-form" method="POST" action="/__hamr/stripe/resend"><input type="hidden" name="resource" value="session"><input type="hidden" name="id" value="{{.ID}}"><button class="action">Resend {{sessionEvt .}}</button></form>
{{end}}
</td>
</tr>
{{end}}
</tbody>
</table>
{{- else}}<div class="empty">No checkout sessions captured yet.</div>{{end}}
</section>

<section>
<h2>Connect Accounts <span class="count">{{len .Accounts}}</span></h2>
{{if .Accounts -}}
<table>
<thead><tr><th>ID</th><th>Type</th><th>Country</th><th>Charges</th><th>Payouts</th><th>Actions</th></tr></thead>
<tbody>
{{range .Accounts}}
<tr>
<td><code>{{shortID .ID}}</code></td>
<td>{{.Type}}</td>
<td>{{.Country}}</td>
<td>{{if .ChargesEnabled}}<span class="flag-on">yes</span>{{else}}<span class="flag-off">no</span>{{end}}</td>
<td>{{if .PayoutsEnabled}}<span class="flag-on">yes</span>{{else}}<span class="flag-off">no</span>{{end}}</td>
<td class="actions-cell">
{{if not .DetailsSubmitted}}
<a class="action" href="/__hamr/stripe/onboarding?account={{.ID}}">Onboard</a>
{{end}}
{{if .DetailsSubmitted}}
<form class="row-form" method="POST" action="/__hamr/stripe/resend"><input type="hidden" name="resource" value="account"><input type="hidden" name="id" value="{{.ID}}"><button class="action">Resend account.updated</button></form>
{{end}}
</td>
</tr>
{{end}}
</tbody>
</table>
{{- else}}<div class="empty">No connected accounts captured yet.</div>{{end}}
</section>

<section>
<h2>PaymentIntents <span class="count">{{len .PIs}}</span></h2>
{{if .PIs -}}
<table>
<thead><tr><th>ID</th><th>Status</th><th>Amount</th><th>Connect</th><th>Actions</th></tr></thead>
<tbody>
{{range .PIs}}
<tr>
<td><code>{{shortID .ID}}</code></td>
<td><span class="status-tag status-{{.Status}}">{{.Status}}</span></td>
<td>{{amount .Amount .Currency}}{{if gt .ApplicationFeeAmount 0}} <span class="tag">fee {{amount .ApplicationFeeAmount .Currency}}</span>{{end}}</td>
<td>{{if .TransferDataDestination}}→ <code>{{shortID .TransferDataDestination}}</code>{{else if .StripeAccount}}as <code>{{shortID .StripeAccount}}</code>{{else}}—{{end}}</td>
<td class="actions-cell">
{{if eq .Status "requires_confirmation" "requires_action" "requires_payment_method" "requires_capture" "processing"}}
<a class="action" href="/__hamr/stripe/payment_intent?id={{.ID}}">Resolve</a>
{{end}}
{{if eq .Status "succeeded"}}
<form class="row-form" method="POST" action="/__hamr/stripe/refund">
<input type="hidden" name="payment_intent" value="{{.ID}}">
<input type="number" name="amount" placeholder="full" min="1" max="{{.Amount}}">
{{if .TransferDataDestination}}<label style="font-size:10px;color:#a3c4e0"><input type="checkbox" name="reverse_transfer" value="true" checked>reverse</label>{{end}}
<button class="action danger">Refund</button>
</form>
{{end}}
{{if piResendable .}}
<form class="row-form" method="POST" action="/__hamr/stripe/resend"><input type="hidden" name="resource" value="payment_intent"><input type="hidden" name="id" value="{{.ID}}"><button class="action">Resend</button></form>
{{end}}
</td>
</tr>
{{end}}
</tbody>
</table>
{{- else}}<div class="empty">No PaymentIntents captured yet.</div>{{end}}
</section>

<section>
<h2>Refunds <span class="count">{{len .Refunds}}</span></h2>
{{if .Refunds -}}
<table>
<thead><tr><th>ID</th><th>Charge</th><th>Amount</th><th>Reverse Transfer</th><th>Actions</th></tr></thead>
<tbody>
{{range .Refunds}}
<tr>
<td><code>{{shortID .ID}}</code></td>
<td><code>{{shortID .ChargeID}}</code></td>
<td>{{amount .Amount .Currency}}</td>
<td>{{if .ReverseTransfer}}<span class="flag-on">yes</span>{{else}}—{{end}}</td>
<td class="actions-cell">
<form class="row-form" method="POST" action="/__hamr/stripe/resend"><input type="hidden" name="resource" value="refund"><input type="hidden" name="id" value="{{.ID}}"><button class="action">Resend charge.refunded</button></form>
</td>
</tr>
{{end}}
</tbody>
</table>
{{- else}}<div class="empty">No refunds captured yet.</div>{{end}}
</section>

<section>
<h2>Payouts <span class="count">{{len .Payouts}}</span></h2>
{{if .Payouts -}}
<table>
<thead><tr><th>ID</th><th>Status</th><th>Amount</th><th>Account</th><th>Actions</th></tr></thead>
<tbody>
{{range .Payouts}}
<tr>
<td><code>{{shortID .ID}}</code></td>
<td><span class="status-tag status-{{.Status}}">{{.Status}}</span></td>
<td>{{amount .Amount .Currency}}</td>
<td>{{if .AccountID}}<code>{{shortID .AccountID}}</code>{{else}}platform{{end}}</td>
<td class="actions-cell">
{{if eq .Status "pending"}}
<a class="action" href="/__hamr/stripe/payout?id={{.ID}}">Resolve</a>
{{end}}
{{if payoutResendable .}}
<form class="row-form" method="POST" action="/__hamr/stripe/resend"><input type="hidden" name="resource" value="payout"><input type="hidden" name="id" value="{{.ID}}"><button class="action">Resend payout.{{.Status}}</button></form>
{{end}}
</td>
</tr>
{{end}}
</tbody>
</table>
{{- else}}<div class="empty">No payouts captured yet.</div>{{end}}
</section>

</div>
</body>
</html>
`))
