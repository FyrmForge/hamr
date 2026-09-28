package devserver

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The billing portal is a dev page at /__hamr/stripe/portal?session=<id>
// standing in for Stripe's hosted customer portal.
//
// ponytail: one global configuration; only subscription_cancel.enabled is
// honoured. No plan switching, no payment-method collection.

// stripePortalSession is a billing_portal.session.
type stripePortalSession struct {
	ID         string    `json:"id"`
	CustomerID string    `json:"customer_id"`
	ReturnURL  string    `json:"return_url,omitempty"`
	Created    time.Time `json:"created"`
}

// stripePortalConfig is the subset of billing_portal.configuration the mock
// keeps. CancelEnabled hides the portal's cancel button when false.
type stripePortalConfig struct {
	ID            string `json:"id,omitempty"`
	CancelEnabled bool   `json:"cancel_enabled"`
}

// registerPortalRoutes mounts /v1/billing_portal/*.
func (m *StripeMock) registerPortalRoutes(mux stripeRouter) {
	mux.HandleFunc("/v1/billing_portal/sessions", m.handlePortalSessions)
	mux.HandleFunc("/v1/billing_portal/configurations", m.handlePortalConfigurations)
}

// registerPortalUIRoutes mounts the hosted portal page and its action handler
// on the proxy mux.
//
//	GET  /__hamr/stripe/portal?session=<id>  — the customer's subscriptions and invoices
//	POST /__hamr/stripe/portal/action         — cancel/keep a subscription, "update" the card
func (m *StripeMock) registerPortalUIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/__hamr/stripe/portal", guardUnsafe(m.handlePortalPage))
	mux.HandleFunc("/__hamr/stripe/portal/action", guardUnsafe(m.handlePortalAction))
}

// handlePortalSessions serves POST /v1/billing_portal/sessions.
func (m *StripeMock) handlePortalSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeStripeError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	p, ok := readStripeForm(w, r)
	if !ok {
		return
	}
	cust := getString(p, "customer")
	if cust == "" {
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error", "Missing required param: customer.")
		return
	}
	ps := &stripePortalSession{
		ID:         "bps_test_" + randomHex(24),
		CustomerID: cust,
		ReturnURL:  getString(p, "return_url"),
		Created:    m.now(),
	}

	m.mu.Lock()
	if _, exists := m.customers[cust]; !exists {
		m.mu.Unlock()
		writeStripeErrorCode(w, http.StatusBadRequest, "invalid_request_error", "resource_missing",
			fmt.Sprintf("No such customer: '%s'", cust))
		return
	}
	m.portalSessions[ps.ID] = ps
	m.persist()
	m.mu.Unlock()

	m.logger.Info("portal session created", "id", ps.ID, "customer", cust)
	writeStripeJSON(w, http.StatusOK, m.serializePortalSession(ps))
}

func (m *StripeMock) serializePortalSession(ps *stripePortalSession) map[string]any {
	return map[string]any{
		"id":            ps.ID,
		"object":        "billing_portal.session",
		"created":       ps.Created.Unix(),
		"customer":      ps.CustomerID,
		"livemode":      false,
		"return_url":    nullableString(ps.ReturnURL),
		"url":           m.baseURL + "/__hamr/stripe/portal?session=" + url.QueryEscape(ps.ID),
		"configuration": "bpc_test_default",
		"locale":        "auto",
		"flow":          nil,
		"on_behalf_of":  nil,
	}
}

// handlePortalConfigurations serves POST (create/replace the single global
// configuration) and GET (list) on /v1/billing_portal/configurations.
func (m *StripeMock) handlePortalConfigurations(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		p, ok := readStripeForm(w, r)
		if !ok {
			return
		}
		features, _ := p["features"].(map[string]any)
		cancel, _ := features["subscription_cancel"].(map[string]any)
		enabled := getString(cancel, "enabled")

		m.mu.Lock()
		if enabled != "" {
			m.portalConfig.CancelEnabled = enabled == "true"
		}
		if m.portalConfig.ID == "" {
			m.portalConfig.ID = "bpc_test_" + randomHex(14)
		}
		cfg := m.portalConfig
		m.persist()
		m.mu.Unlock()
		writeStripeJSON(w, http.StatusOK, m.serializePortalConfig(cfg))
	case http.MethodGet:
		m.mu.RLock()
		cfg := m.portalConfig
		m.mu.RUnlock()
		data := []map[string]any{}
		if cfg.ID != "" {
			data = append(data, m.serializePortalConfig(cfg))
		}
		writeStripeJSON(w, http.StatusOK, map[string]any{
			"object": "list", "url": "/v1/billing_portal/configurations", "has_more": false, "data": data,
		})
	default:
		writeStripeError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
	}
}

func (m *StripeMock) serializePortalConfig(cfg stripePortalConfig) map[string]any {
	return map[string]any{
		"id":         cfg.ID,
		"object":     "billing_portal.configuration",
		"active":     true,
		"created":    m.now().Unix(),
		"is_default": true,
		"livemode":   false,
		"features": map[string]any{
			"subscription_cancel": map[string]any{
				"enabled":            cfg.CancelEnabled,
				"mode":               "at_period_end",
				"proration_behavior": "none",
			},
		},
		"business_profile":   map[string]any{},
		"default_return_url": nil,
		"metadata":           map[string]string{},
	}
}

// portalSubRow is one subscription line on the portal page.
type portalSubRow struct {
	ID                string
	Plan              string
	Status            string
	PeriodEnd         string
	CancelAtPeriodEnd bool
}

// portalInvoiceRow is one invoice line on the portal page.
type portalInvoiceRow struct {
	Number  string
	Status  string
	Total   string
	Created string
}

type portalPageData struct {
	SessionID     string
	Email         string
	Name          string
	ReturnURL     string
	Notice        string
	CancelEnabled bool
	Subs          []portalSubRow
	Invoices      []portalInvoiceRow
}

// handlePortalPage renders the portal for ?session=<id>: 400 without the
// param, 404 for an unknown session.
func (m *StripeMock) handlePortalPage(w http.ResponseWriter, r *http.Request) {
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
	ps, ok := m.portalSessions[id]
	if !ok {
		m.mu.RUnlock()
		http.Error(w, "portal session not found", http.StatusNotFound)
		return
	}
	data := portalPageData{
		SessionID:     ps.ID,
		ReturnURL:     orDefault(ps.ReturnURL, "/"),
		CancelEnabled: m.portalConfig.CancelEnabled,
	}
	data.Email, data.Name = m.customerEmailLocked(ps.CustomerID)
	var subs []*stripeSubscription
	for _, s := range m.subscriptions {
		if s.CustomerID == ps.CustomerID {
			subs = append(subs, s)
		}
	}
	var invs []*stripeInvoice
	for _, in := range m.invoices {
		if in.CustomerID == ps.CustomerID {
			invs = append(invs, in)
		}
	}
	sortNewestFirst(subs, func(s *stripeSubscription) (time.Time, string) { return s.Created, s.ID })
	sortNewestFirst(invs, func(in *stripeInvoice) (time.Time, string) { return in.Created, in.ID })
	for _, s := range subs {
		var plan []string
		for _, it := range s.Items {
			plan = append(plan, fmt.Sprintf("%s × %d at %s / %s", it.ProductName, it.Quantity,
				formatStripeAmount(it.UnitAmount, s.Currency), it.Interval))
		}
		data.Subs = append(data.Subs, portalSubRow{
			ID:                s.ID,
			Plan:              strings.Join(plan, ", "),
			Status:            s.Status,
			PeriodEnd:         s.CurrentPeriodEnd.UTC().Format("2006-01-02"),
			CancelAtPeriodEnd: s.CancelAtPeriodEnd,
		})
	}
	for _, in := range invs {
		data.Invoices = append(data.Invoices, portalInvoiceRow{
			Number:  in.Number,
			Status:  in.Status,
			Total:   formatStripeAmount(in.Total(), in.Currency),
			Created: in.Created.UTC().Format("2006-01-02"),
		})
	}
	m.mu.RUnlock()

	if r.URL.Query().Get("notice") == "card" {
		data.Notice = "Card updated (mock: nothing happened)"
	}
	var buf bytes.Buffer
	if err := stripePortalTmpl.Execute(&buf, data); err != nil {
		http.Error(w, "render: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(buf.Bytes()) //nolint:errcheck
}

// handlePortalAction serves the portal's forms: cancel/keep flip
// cancel_at_period_end and fire one customer.subscription.updated; card is a
// no-op that only shows a notice. Redirects back to the portal page.
func (m *StripeMock) handlePortalAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sessID := strings.TrimSpace(r.FormValue("session"))
	subID := strings.TrimSpace(r.FormValue("subscription"))
	action := strings.TrimSpace(r.FormValue("action"))
	back := "/__hamr/stripe/portal?session=" + url.QueryEscape(sessID)

	m.mu.Lock()
	ps, ok := m.portalSessions[sessID]
	if !ok {
		m.mu.Unlock()
		http.Error(w, "portal session not found", http.StatusNotFound)
		return
	}
	switch action {
	case "card":
		m.mu.Unlock()
		http.Redirect(w, r, back+"&notice=card", http.StatusSeeOther)
		return
	case "cancel", "keep":
	default:
		m.mu.Unlock()
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	if !m.portalConfig.CancelEnabled {
		m.mu.Unlock()
		http.Error(w, "subscription cancellation is disabled in the portal configuration", http.StatusForbidden)
		return
	}
	sub, ok := m.subscriptions[subID]
	if !ok {
		m.mu.Unlock()
		http.Error(w, "subscription not found", http.StatusNotFound)
		return
	}
	if sub.CustomerID != ps.CustomerID {
		m.mu.Unlock()
		http.Error(w, "subscription belongs to another customer", http.StatusForbidden)
		return
	}
	if sub.Status == "canceled" {
		m.mu.Unlock()
		http.Error(w, "subscription is already canceled", http.StatusConflict)
		return
	}
	sub.CancelAtPeriodEnd = action == "cancel"
	m.persist()
	out := m.serializeSubscription(sub)
	m.mu.Unlock()

	m.fireEventsAsync([]webhookFire{{eventType: "customer.subscription.updated", object: out}}, "subscription", subID)
	http.Redirect(w, r, back, http.StatusSeeOther)
}

var stripePortalTmpl = template.Must(template.New("stripe-portal").
	Funcs(template.FuncMap{"shortID": dashboardFuncs["shortID"]}).
	Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>hamr — Stripe Customer Portal (Mock)</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{background:#0a2540;color:#fff;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;min-height:100vh;display:flex;align-items:flex-start;justify-content:center;padding:24px}
.portal{background:#1a3a5c;border-radius:12px;padding:36px;max-width:760px;width:100%;box-shadow:0 20px 60px rgba(0,0,0,0.3)}
.badge{display:inline-block;background:#ff6b35;color:#fff;font-size:11px;font-weight:700;padding:3px 8px;border-radius:4px;margin-bottom:16px;text-transform:uppercase;letter-spacing:0.05em}
h1{font-size:22px;margin-bottom:6px}
h2{font-size:15px;color:#a3c4e0;margin:24px 0 8px}
.sub{font-size:13px;color:#a3c4e0;margin-bottom:8px}
.notice{background:rgba(134,239,172,0.15);color:#86efac;border-radius:8px;padding:10px 12px;font-size:13px;margin:12px 0}
table{width:100%;border-collapse:collapse;font-size:13px}
th{text-align:left;color:#a3c4e0;font-weight:600;padding:8px 6px;border-bottom:1px solid rgba(255,255,255,0.15)}
td{padding:10px 6px;border-bottom:1px solid rgba(255,255,255,0.08);vertical-align:middle}
.mono{font-family:'SF Mono',Monaco,Consolas,monospace;font-size:12px;color:#a3c4e0}
.empty{font-size:13px;color:#5a7a9a;padding:8px 0}
form{margin:0;display:inline}
button{padding:8px 12px;border:none;border-radius:8px;font-size:13px;font-weight:600;cursor:pointer;font-family:inherit;transition:filter 0.15s}
button:hover{filter:brightness(1.1)}
.btn-primary{background:#635bff;color:#fff;padding:12px 16px;font-size:14px}
.btn-danger{background:#df1b41;color:#fff}
.btn-ghost{background:transparent;color:#a3c4e0;border:1px solid rgba(255,255,255,0.15)}
.footer{display:flex;justify-content:space-between;align-items:center;margin-top:28px}
a.back{color:#a3c4e0;font-size:13px}
.session{font-size:11px;color:#5a7a9a;margin-top:20px;word-break:break-all;font-family:'SF Mono',Monaco,Consolas,monospace}
</style>
</head>
<body>
<div class="portal">
<span class="badge">Dev Mock</span>
<h1>Customer portal</h1>
<p class="sub">{{if .Name}}{{.Name}} · {{end}}{{if .Email}}{{.Email}}{{else}}(no email){{end}}</p>
{{if .Notice}}<p class="notice">{{.Notice}}</p>{{end}}

<h2>Subscriptions</h2>
{{if .Subs}}
<table>
<tr><th>ID</th><th>Plan</th><th>Status</th><th>Period end</th><th>Cancel at period end</th><th></th></tr>
{{range .Subs}}
<tr>
<td class="mono">{{shortID .ID}}</td>
<td>{{.Plan}}</td>
<td>{{.Status}}</td>
<td>{{.PeriodEnd}}</td>
<td>{{if .CancelAtPeriodEnd}}yes{{else}}no{{end}}</td>
<td>
{{if and $.CancelEnabled (ne .Status "canceled")}}
<form method="POST" action="/__hamr/stripe/portal/action">
<input type="hidden" name="session" value="{{$.SessionID}}">
<input type="hidden" name="subscription" value="{{.ID}}">
{{if .CancelAtPeriodEnd}}
<input type="hidden" name="action" value="keep">
<button type="submit" class="btn-ghost">Keep subscription</button>
{{else}}
<input type="hidden" name="action" value="cancel">
<button type="submit" class="btn-danger">Cancel at period end</button>
{{end}}
</form>
{{end}}
</td>
</tr>
{{end}}
</table>
{{else}}<p class="empty">No subscriptions.</p>{{end}}

<h2>Invoices</h2>
{{if .Invoices}}
<table>
<tr><th>Number</th><th>Status</th><th>Total</th><th>Created</th></tr>
{{range .Invoices}}
<tr><td class="mono">{{.Number}}</td><td>{{.Status}}</td><td>{{.Total}}</td><td>{{.Created}}</td></tr>
{{end}}
</table>
{{else}}<p class="empty">No invoices.</p>{{end}}

<div class="footer">
<form method="POST" action="/__hamr/stripe/portal/action">
<input type="hidden" name="session" value="{{.SessionID}}">
<input type="hidden" name="action" value="card">
<button type="submit" class="btn-primary">Update card</button>
</form>
<a class="back" href="{{.ReturnURL}}">← Back</a>
</div>

<p class="session">Session: {{.SessionID}}</p>
</div>
</body>
</html>
`))
