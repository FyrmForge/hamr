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

// registerAccountUIRoutes mounts the dev-facing onboarding page + outcome
// handler on the proxy mux. Called from RegisterUIRoutes; kept private.
//
//	GET  /__hamr/stripe/onboarding?account=<id>  — current state + Complete button
//	POST /__hamr/stripe/account/complete         — flip enabled flags, fire account.updated
func (m *StripeMock) registerAccountUIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/__hamr/stripe/onboarding", m.handleOnboardingPage)
	mux.HandleFunc("/__hamr/stripe/account/complete", m.handleAccountComplete)
}

// handleOnboardingPage renders the onboarding state for ?account=<id>.
// 400 without an account param, 404 for unknown accounts. Already-onboarded
// accounts render the page with a banner — there's no Complete button so a
// stale tab can't double-fire.
func (m *StripeMock) handleOnboardingPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("account"))
	if id == "" {
		http.Error(w, "missing account query param", http.StatusBadRequest)
		return
	}

	m.mu.RLock()
	acct, ok := m.accounts[id]
	if ok {
		acct = cloneAccount(acct)
	}
	m.mu.RUnlock()
	if !ok {
		http.Error(w, "account not found", http.StatusNotFound)
		return
	}

	var buf bytes.Buffer
	if err := stripeOnboardingTmpl.Execute(&buf, struct {
		Account     *stripeAccount
		IsOnboarded bool
	}{
		Account:     acct,
		IsOnboarded: acct.DetailsSubmitted && acct.ChargesEnabled && acct.PayoutsEnabled,
	}); err != nil {
		http.Error(w, "render: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(buf.Bytes()) //nolint:errcheck
}

// handleAccountComplete flips the account into the fully-onboarded state and
// fires account.updated. Idempotent: re-submitting against an already-
// onboarded account returns 409 so a double-click can't spam webhooks.
func (m *StripeMock) handleAccountComplete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !checkSameOrigin(w, r) {
		return
	}
	id := strings.TrimSpace(r.FormValue("account"))
	if id == "" {
		http.Error(w, "missing account form field", http.StatusBadRequest)
		return
	}

	m.mu.Lock()
	acct, exists := m.accounts[id]
	if !exists {
		m.mu.Unlock()
		http.Error(w, "account not found", http.StatusNotFound)
		return
	}
	if acct.DetailsSubmitted {
		m.mu.Unlock()
		http.Error(w, "account already onboarded", http.StatusConflict)
		return
	}
	acct.ChargesEnabled = true
	acct.PayoutsEnabled = true
	acct.DetailsSubmitted = true
	acct.CurrentlyDue = nil
	acct.DisabledReason = ""
	dataObject := m.serializeAccount(acct)
	m.persist()
	m.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := m.FireEvent(ctx, "account.updated", dataObject); err != nil {
			m.logger.Warn("webhook delivery failed",
				"account", id,
				"event_type", "account.updated",
				"err", err,
			)
		}
	}()

	// Render a small success page so the dev sees confirmation without
	// needing a separate tab — real Stripe would redirect to the
	// account_link's `return_url` here, but the mock doesn't track that
	// and it's friendlier to land on a "you're done" message.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = fmt.Fprintf(w, `<!DOCTYPE html>
<html><head><title>hamr — Onboarding Complete</title>
<style>body{background:#0a2540;color:#fff;font-family:-apple-system,sans-serif;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0}
.card{background:#1a3a5c;border-radius:12px;padding:36px;max-width:480px;text-align:center}
h1{margin:0 0 12px}.sub{color:#a3c4e0;font-size:14px;margin:0 0 18px}
code{background:#0a2540;padding:2px 6px;border-radius:4px;font-size:12px}</style>
</head><body><div class="card">
<h1>✓ Onboarding complete</h1>
<p class="sub">Account <code>%s</code> is now charges-enabled and payouts-enabled.</p>
<p class="sub">A signed <code>account.updated</code> webhook has been sent to your app.</p>
<p class="sub">You can close this tab.</p>
</div></body></html>`, id)
}

var stripeOnboardingTmpl = template.Must(template.New("stripe-onboarding").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>hamr — Stripe Connect Onboarding (Mock)</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{background:#0a2540;color:#fff;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;min-height:100vh;display:flex;align-items:center;justify-content:center;padding:24px}
.card{background:#1a3a5c;border-radius:12px;padding:36px;max-width:560px;width:100%;box-shadow:0 20px 60px rgba(0,0,0,0.3)}
.badge{display:inline-block;background:#ff6b35;color:#fff;font-size:11px;font-weight:700;padding:3px 8px;border-radius:4px;margin-bottom:16px;text-transform:uppercase;letter-spacing:0.05em}
h1{font-size:22px;margin-bottom:6px}
.sub{font-size:13px;color:#a3c4e0;margin-bottom:24px}
.kv{display:grid;grid-template-columns:140px 1fr;gap:8px 16px;font-size:13px;border-top:1px solid rgba(255,255,255,0.1);padding-top:18px;margin-bottom:24px}
.k{color:#a3c4e0}
.v{color:#fff;word-break:break-word}
.flag-on{color:#22c55e;font-weight:600}
.flag-off{color:#ef4444;font-weight:600}
.req{margin:14px 0 24px}
.req-list{font-size:13px;color:#fbbf24;font-family:'SF Mono',Monaco,Consolas,monospace;background:#0a2540;border-radius:6px;padding:12px}
.req-list ul{margin:0;padding-left:18px}
button{width:100%;padding:14px;border:none;border-radius:8px;font-size:15px;font-weight:600;cursor:pointer;background:#635bff;color:#fff;font-family:inherit;transition:filter 0.15s}
button:hover{filter:brightness(1.1)}
.banner{background:#14532d;color:#86efac;padding:12px;border-radius:6px;font-size:13px;margin-bottom:18px;text-align:center}
.session{font-size:11px;color:#5a7a9a;margin-top:18px;word-break:break-all;font-family:'SF Mono',Monaco,Consolas,monospace}
</style>
</head>
<body>
<div class="card">
<span class="badge">Dev Mock</span>
<h1>Stripe Connect Onboarding</h1>
<p class="sub">Real Stripe would walk the user through a multi-step KYC form here. The mock collapses it to one button.</p>

{{if .IsOnboarded}}
<div class="banner">✓ This account is fully onboarded. Re-submitting is a no-op.</div>
{{end}}

<div class="kv">
<div class="k">Type</div><div class="v">{{.Account.Type}}</div>
<div class="k">Country</div><div class="v">{{.Account.Country}}</div>
{{if .Account.Email}}<div class="k">Email</div><div class="v">{{.Account.Email}}</div>{{end}}
<div class="k">Currency</div><div class="v">{{.Account.DefaultCurrency}}</div>
<div class="k">Charges enabled</div><div class="v {{if .Account.ChargesEnabled}}flag-on{{else}}flag-off{{end}}">{{if .Account.ChargesEnabled}}yes{{else}}no{{end}}</div>
<div class="k">Payouts enabled</div><div class="v {{if .Account.PayoutsEnabled}}flag-on{{else}}flag-off{{end}}">{{if .Account.PayoutsEnabled}}yes{{else}}no{{end}}</div>
<div class="k">Details submitted</div><div class="v {{if .Account.DetailsSubmitted}}flag-on{{else}}flag-off{{end}}">{{if .Account.DetailsSubmitted}}yes{{else}}no{{end}}</div>
</div>

{{if .Account.CurrentlyDue}}
<div class="req">
<p class="sub" style="margin-bottom:8px">Requirements currently due:</p>
<div class="req-list"><ul>
{{range .Account.CurrentlyDue}}<li>{{.}}</li>{{end}}
</ul></div>
</div>
{{end}}

{{if not .IsOnboarded}}
<form method="POST" action="/__hamr/stripe/account/complete">
<input type="hidden" name="account" value="{{.Account.ID}}">
<button type="submit">Complete Onboarding</button>
</form>
{{end}}

<p class="session">Account: {{.Account.ID}}</p>
</div>
</body>
</html>
`))
