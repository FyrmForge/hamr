package devserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Accounts v2 (/v2/core/accounts). Stripe no longer lets new platforms create
// v1 Express accounts, so Connect apps create v2 accounts with configurations
// (customer / merchant / recipient) and learn about capability changes from
// thin event notifications rather than account.updated.
//
// v2 differs from v1 on the wire: POST bodies are JSON, timestamps are RFC
// 3339 strings, and errors use the v2 error shape.

// capRecipientTransfers is the capability a recipient account needs before
// /v1/transfers to it is accepted.
const capRecipientTransfers = "recipient.stripe_balance.stripe_transfers"

// stripeV2Account is a v2 connected account. Capabilities maps
// "<configuration>.<capability path>" (e.g. capRecipientTransfers) to
// pending | active | restricted | unsupported.
type stripeV2Account struct {
	ID              string            `json:"id"`
	ContactEmail    string            `json:"contact_email,omitempty"`
	DisplayName     string            `json:"display_name,omitempty"`
	Dashboard       string            `json:"dashboard,omitempty"` // express | full | none
	Country         string            `json:"country,omitempty"`
	Currency        string            `json:"currency,omitempty"`
	FeesCollector   string            `json:"fees_collector,omitempty"`
	LossesCollector string            `json:"losses_collector,omitempty"`
	Configurations  []string          `json:"configurations"`
	Capabilities    map[string]string `json:"capabilities,omitempty"`
	// ReturnURL is the latest account link's return_url; the onboarding page
	// sends the user back there, like Stripe's hosted flow.
	ReturnURL string            `json:"return_url,omitempty"`
	Created   time.Time         `json:"created"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// onboarded reports whether every requested capability is active.
func (a *stripeV2Account) onboarded() bool {
	for _, st := range a.Capabilities {
		if st != "active" {
			return false
		}
	}
	return true
}

func cloneV2Account(a *stripeV2Account) *stripeV2Account {
	if a == nil {
		return nil
	}
	c := *a
	c.Configurations = append([]string(nil), a.Configurations...)
	c.Capabilities = cloneStringMap(a.Capabilities)
	c.Metadata = cloneStringMap(a.Metadata)
	return &c
}

// registerV2AccountRoutes mounts the v2 account endpoints.
//
//	POST /v2/core/accounts        — create
//	GET  /v2/core/accounts/{id}   — retrieve (include[] accepted; everything is always returned)
//	POST /v2/core/account_links   — hosted onboarding link, points at the mock onboarding page
func (m *StripeMock) registerV2AccountRoutes(mux stripeRouter) {
	mux.HandleFunc("/v2/core/accounts", m.handleV2Accounts)
	mux.HandleFunc("/v2/core/accounts/", m.handleV2AccountByID)
	mux.HandleFunc("/v2/core/account_links", m.handleV2AccountLinks)
}

func (m *StripeMock) handleV2Accounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeStripeErrorV2(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	body, ok := readV2JSON(w, r)
	if !ok {
		return
	}
	acct, err := buildV2Account(body)
	if err != nil {
		writeStripeErrorV2(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	m.mu.Lock()
	m.v2Accounts[acct.ID] = acct
	out := serializeV2Account(acct)
	m.persist()
	m.mu.Unlock()
	writeStripeJSON(w, http.StatusOK, out)
}

func (m *StripeMock) handleV2AccountByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v2/core/accounts/")
	if id == "" || strings.Contains(id, "/") || r.Method != http.MethodGet {
		writeStripeErrorV2(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	m.mu.RLock()
	acct := cloneV2Account(m.v2Accounts[id])
	m.mu.RUnlock()
	if acct == nil {
		writeStripeErrorV2(w, http.StatusNotFound, "invalid_request_error", fmt.Sprintf("No such account: '%s'", id))
		return
	}
	writeStripeJSON(w, http.StatusOK, serializeV2Account(acct))
}

func (m *StripeMock) handleV2AccountLinks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeStripeErrorV2(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	body, ok := readV2JSON(w, r)
	if !ok {
		return
	}
	acctID := getString(body, "account")
	useCase, _ := body["use_case"].(map[string]any)
	ucType := getString(useCase, "type")
	if acctID == "" || (ucType != "account_onboarding" && ucType != "account_update") {
		writeStripeErrorV2(w, http.StatusBadRequest, "invalid_request_error",
			"account and use_case.type (account_onboarding or account_update) are required")
		return
	}
	details, _ := useCase[ucType].(map[string]any)

	m.mu.Lock()
	acct, exists := m.v2Accounts[acctID]
	if !exists {
		m.mu.Unlock()
		writeStripeErrorV2(w, http.StatusNotFound, "invalid_request_error", fmt.Sprintf("No such account: '%s'", acctID))
		return
	}
	acct.ReturnURL = getString(details, "return_url")
	m.persist()
	m.mu.Unlock()

	now := time.Now()
	writeStripeJSON(w, http.StatusOK, map[string]any{
		"object":     "v2.core.account_link",
		"account":    acctID,
		"created":    formatV2Time(now),
		"expires_at": formatV2Time(now.Add(5 * time.Minute)),
		"livemode":   false,
		"url":        m.baseURL + "/__hamr/stripe/onboarding?account=" + url.QueryEscape(acctID),
		"use_case":   useCase,
	})
}

// buildV2Account turns a create body into an account awaiting onboarding:
// every requested capability starts pending.
func buildV2Account(body map[string]any) (*stripeV2Account, error) {
	identity, _ := body["identity"].(map[string]any)
	defaults, _ := body["defaults"].(map[string]any)
	resp, _ := defaults["responsibilities"].(map[string]any)
	acct := &stripeV2Account{
		ID:              "acct_test_" + randomHex(16),
		ContactEmail:    getString(body, "contact_email"),
		DisplayName:     getString(body, "display_name"),
		Dashboard:       orDefault(getString(body, "dashboard"), "none"),
		Country:         strings.ToUpper(getString(identity, "country")),
		Currency:        strings.ToLower(getString(defaults, "currency")),
		FeesCollector:   getString(resp, "fees_collector"),
		LossesCollector: getString(resp, "losses_collector"),
		Capabilities:    map[string]string{},
		Created:         time.Now(),
		Metadata:        stringMap(body, "metadata"),
	}
	configs, _ := body["configuration"].(map[string]any)
	for _, name := range []string{"customer", "merchant", "recipient"} {
		cfg, ok := configs[name].(map[string]any)
		if !ok {
			continue
		}
		acct.Configurations = append(acct.Configurations, name)
		if caps, ok := cfg["capabilities"].(map[string]any); ok {
			collectRequested(caps, name, acct.Capabilities)
		}
	}
	// Stripe grants payouts alongside transfers once bank details check out;
	// it is not requestable, so add it as pending when transfers are.
	if _, ok := acct.Capabilities[capRecipientTransfers]; ok {
		acct.Capabilities["recipient.stripe_balance.payouts"] = "pending"
	}
	return acct, nil
}

// collectRequested walks a capabilities subtree and marks every
// {"requested": true} leaf pending under its dotted path.
func collectRequested(node map[string]any, prefix string, out map[string]string) {
	if req, ok := node["requested"].(bool); ok {
		if req {
			out[prefix] = "pending"
		}
		return
	}
	for k, v := range node {
		if child, ok := v.(map[string]any); ok {
			collectRequested(child, prefix+"."+k, out)
		}
	}
}

// serializeV2Account renders the v2.core.account wire shape. Capabilities are
// rebuilt into Stripe's nesting from their dotted paths.
func serializeV2Account(a *stripeV2Account) map[string]any {
	configuration := map[string]any{}
	for _, name := range a.Configurations {
		configuration[name] = map[string]any{"applied": true, "capabilities": map[string]any{}}
	}
	for path, status := range a.Capabilities {
		parts := strings.Split(path, ".")
		cfg, ok := configuration[parts[0]].(map[string]any)
		if !ok {
			continue
		}
		node := cfg["capabilities"].(map[string]any)
		for _, p := range parts[1 : len(parts)-1] {
			child, ok := node[p].(map[string]any)
			if !ok {
				child = map[string]any{}
				node[p] = child
			}
			node = child
		}
		node[parts[len(parts)-1]] = map[string]any{"status": status, "status_details": []any{}}
	}

	requirements := map[string]any{"entries": []any{}}
	if pending := pendingCapabilities(a); len(pending) > 0 {
		restricts := make([]map[string]any, 0, len(pending))
		for _, path := range pending {
			cfgName, capName, _ := strings.Cut(path, ".")
			restricts = append(restricts, map[string]any{
				"capability":    capName,
				"configuration": cfgName,
				"deadline":      map[string]any{"status": "currently_due"},
			})
		}
		entry := func(desc string) map[string]any {
			return map[string]any{
				"awaiting_action_from": "user",
				"description":          desc,
				"errors":               []any{},
				"impact":               map[string]any{"restricts_capabilities": restricts},
				"minimum_deadline":     map[string]any{"status": "currently_due"},
				"requested_reasons":    []any{map[string]any{"code": "routine_onboarding"}},
			}
		}
		requirements["entries"] = []any{entry("identity.individual"), entry("payout_method")}
		requirements["summary"] = map[string]any{"minimum_deadline": map[string]any{"status": "currently_due"}}
	}

	meta := a.Metadata
	if meta == nil {
		meta = map[string]string{}
	}
	out := map[string]any{
		"id":                     a.ID,
		"object":                 "v2.core.account",
		"applied_configurations": append([]string{}, a.Configurations...),
		"closed":                 false,
		"configuration":          configuration,
		"contact_email":          nullableString(a.ContactEmail),
		"created":                formatV2Time(a.Created),
		"dashboard":              a.Dashboard,
		"display_name":           nullableString(a.DisplayName),
		"identity":               map[string]any{"country": nullableString(a.Country)},
		"livemode":               false,
		"metadata":               meta,
		"requirements":           requirements,
		"defaults": map[string]any{
			"currency": nullableString(a.Currency),
			"responsibilities": map[string]any{
				"fees_collector":         nullableString(a.FeesCollector),
				"losses_collector":       nullableString(a.LossesCollector),
				"requirements_collector": "stripe",
			},
		},
	}
	return out
}

// pendingCapabilities lists capability paths that are not active, sorted.
func pendingCapabilities(a *stripeV2Account) []string {
	var out []string
	for path, st := range a.Capabilities {
		if st != "active" {
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out
}

// readV2JSON decodes a v2 POST body. stripe-go sends v2 POSTs as JSON, not
// the bracket form v1 uses.
func readV2JSON(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	raw, err := readLimitedBody(r)
	if err != nil {
		writeStripeErrorV2(w, http.StatusBadRequest, "invalid_request_error", "read body: "+err.Error())
		return nil, false
	}
	body := map[string]any{}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			writeStripeErrorV2(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON body: "+err.Error())
			return nil, false
		}
	}
	return body, true
}

// writeStripeErrorV2 writes the v2 error shape stripe-go decodes into
// *stripe.V2RawError.
func writeStripeErrorV2(w http.ResponseWriter, status int, errType, msg string) {
	writeStripeJSON(w, status, map[string]any{
		"error": map[string]any{"type": errType, "code": errType, "message": msg},
	})
}

// --- onboarding ---

// completeV2Onboarding activates every requested capability and fires the thin
// events Stripe sends when onboarding finishes. Returns the account's return
// URL ("" when no link set one).
func (m *StripeMock) completeV2Onboarding(id string) (string, error) {
	m.mu.Lock()
	acct, ok := m.v2Accounts[id]
	if !ok {
		m.mu.Unlock()
		return "", stripeErr(http.StatusNotFound, "account not found")
	}
	if acct.onboarded() {
		m.mu.Unlock()
		return "", stripeErr(http.StatusConflict, "account already onboarded")
	}
	for path := range acct.Capabilities {
		acct.Capabilities[path] = "active"
	}
	events := []string{"v2.core.account[requirements].updated"}
	for _, cfg := range acct.Configurations {
		for path := range acct.Capabilities {
			if strings.HasPrefix(path, cfg+".") {
				events = append(events, "v2.core.account[configuration."+cfg+"].capability_status_updated")
				break
			}
		}
	}
	returnURL := acct.ReturnURL
	m.persist()
	m.mu.Unlock()

	m.fireThinAsync(id, events...)
	return returnURL, nil
}

// handleV2OnboardingPage renders the one-button onboarding page for a v2
// account. Called from handleOnboardingPage when the id is a v2 account.
func (m *StripeMock) handleV2OnboardingPage(w http.ResponseWriter, acct *stripeV2Account) {
	caps := make([]string, 0, len(acct.Capabilities))
	for path := range acct.Capabilities {
		caps = append(caps, path)
	}
	sort.Strings(caps)
	var buf bytes.Buffer
	if err := stripeV2OnboardingTmpl.Execute(&buf, struct {
		Account      *stripeV2Account
		Capabilities []string
		IsOnboarded  bool
	}{acct, caps, acct.onboarded()}); err != nil {
		http.Error(w, "render: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(buf.Bytes()) //nolint:errcheck
}

var stripeV2OnboardingTmpl = template.Must(template.New("stripe-v2-onboarding").Parse(`<!DOCTYPE html>
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
.kv{display:grid;grid-template-columns:minmax(140px,auto) 1fr;gap:8px 16px;font-size:13px;border-top:1px solid rgba(255,255,255,0.1);padding-top:18px;margin-bottom:24px}
.k{color:#a3c4e0;word-break:break-word}
.v{color:#fff;word-break:break-word}
.flag-on{color:#22c55e;font-weight:600}
.flag-off{color:#fbbf24;font-weight:600}
button{width:100%;padding:14px;border:none;border-radius:8px;font-size:15px;font-weight:600;cursor:pointer;background:#635bff;color:#fff;font-family:inherit}
.banner{background:#14532d;color:#86efac;padding:12px;border-radius:6px;font-size:13px;margin-bottom:18px;text-align:center}
.session{font-size:11px;color:#5a7a9a;margin-top:18px;word-break:break-all;font-family:'SF Mono',Monaco,Consolas,monospace}
</style>
</head>
<body>
<div class="card">
<span class="badge">Dev Mock</span>
<h1>Stripe Connect Onboarding</h1>
<p class="sub">Accounts v2. Real Stripe collects identity and bank details here. The mock collapses it to one button, then fires the thin events and sends you back to the app.</p>
{{if .IsOnboarded}}<div class="banner">✓ Every requested capability is active.</div>{{end}}
<div class="kv">
<div class="k">Configurations</div><div class="v">{{range $i, $c := .Account.Configurations}}{{if $i}}, {{end}}{{$c}}{{end}}</div>
{{if .Account.Country}}<div class="k">Country</div><div class="v">{{.Account.Country}}</div>{{end}}
{{if .Account.ContactEmail}}<div class="k">Email</div><div class="v">{{.Account.ContactEmail}}</div>{{end}}
{{range .Capabilities}}{{$st := index $.Account.Capabilities .}}<div class="k">{{.}}</div><div class="v {{if eq $st "active"}}flag-on{{else}}flag-off{{end}}">{{$st}}</div>{{end}}
</div>
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
