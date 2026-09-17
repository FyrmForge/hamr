package devserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"
)

// Read-only fake dashboards. They show the mock's records laid out like the
// Stripe dashboards QA already knows (platform dashboard, Express dashboard),
// in hamr's dev styling with a "hamr mock" badge and no Stripe branding. No
// page here changes state; the controls stay on /__hamr/stripe.
//
//	GET /__hamr/stripe/dashboard[/<section>]  — platform: home, payments, balances, connect, transfers, payouts, disputes, events
//	GET /__hamr/stripe/express/<account>      — one connected account's Express view

// viewListLimit caps rows per table on the fake dashboards.
const viewListLimit = 100

var platformSections = []struct{ Key, Label string }{
	{"", "Home"},
	{"payments", "Payments"},
	{"balances", "Balances"},
	{"connect", "Connected accounts"},
	{"transfers", "Transfers"},
	{"payouts", "Payouts"},
	{"disputes", "Disputes"},
	{"events", "Events"},
}

func (m *StripeMock) registerViewRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/__hamr/stripe/dashboard", guardUnsafe(m.handlePlatformView))
	mux.HandleFunc("/__hamr/stripe/dashboard/", guardUnsafe(m.handlePlatformView))
	mux.HandleFunc("/__hamr/stripe/express/", guardUnsafe(m.handleExpressView))
}

// paymentRow is one row of the Payments list: a PaymentIntent joined with its
// charge, dispute and refunds.
type paymentRow struct {
	ID          string
	Created     time.Time
	Amount      int64
	Currency    string
	Status      string // succeeded | refunded | partially_refunded | disputed | failed | incomplete | uncaptured
	Method      string
	Description string
	Customer    string
	Fee         int64
	Refunded    int64
	Connect     string
	Metadata    map[string]string
}

// accountRow is one connected account, v1 or v2, in the shape both
// dashboards show.
type accountRow struct {
	ID        string
	API       string // v1 | v2
	Email     string
	Country   string
	Dashboard string
	Status    string // complete | restricted
	Created   time.Time
	Balance   string
	// Capabilities lists "name: status" lines; Requirements what is due.
	Capabilities []string
	Requirements []string
}

type eventRow struct {
	*stripeEvent
	Pretty string
}

type platformView struct {
	Section   string
	Title     string
	Balance   []string
	Ledger    []ledgerEntry
	Payments  []paymentRow
	Accounts  []accountRow
	Transfers []*stripeTransfer
	Payouts   []*stripePayout
	Disputes  []*stripeDispute
	Events    []eventRow
}

func (m *StripeMock) handlePlatformView(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	section := strings.Trim(strings.TrimPrefix(r.URL.Path, "/__hamr/stripe/dashboard"), "/")
	title := ""
	for _, s := range platformSections {
		if s.Key == section {
			title = s.Label
		}
	}
	if title == "" {
		http.NotFound(w, r)
		return
	}

	v := platformView{Section: section, Title: title}
	m.mu.RLock()
	ledger := m.platformLedger()
	v.Balance = moneyList(balanceOf(ledger))
	switch section {
	case "":
		v.Payments = capRows(m.paymentRowsLocked(), 10)
		v.Payouts = capRows(m.payoutsForLocked(""), 5)
	case "payments":
		v.Payments = capRows(m.paymentRowsLocked(), viewListLimit)
	case "balances":
		v.Ledger = capRows(ledger, viewListLimit)
	case "connect":
		v.Accounts = capRows(m.accountRowsLocked(), viewListLimit)
	case "transfers":
		v.Transfers = capRows(m.transfersToLocked(""), viewListLimit)
	case "payouts":
		v.Payouts = capRows(m.payoutsForLocked(""), viewListLimit)
	case "disputes":
		v.Disputes = snapshotDisputes(m.disputes)
	case "events":
		for _, ev := range snapshotEvents(m.events) {
			v.Events = append(v.Events, eventRow{stripeEvent: ev, Pretty: prettyJSON(ev.Payload)})
		}
	}
	m.mu.RUnlock()
	renderView(w, "platform", v)
}

type expressView struct {
	Account  accountRow
	Balance  []string
	Schedule string
	Activity []ledgerEntry
	Payouts  []*stripePayout
}

func (m *StripeMock) handleExpressView(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/__hamr/stripe/express/"), "/")

	m.mu.RLock()
	var row *accountRow
	for _, a := range m.accountRowsLocked() {
		if a.ID == id {
			row = &a
			break
		}
	}
	if row == nil {
		m.mu.RUnlock()
		http.Error(w, "account not found", http.StatusNotFound)
		return
	}
	v := expressView{Account: *row, Schedule: payoutScheduleText(m.balanceSettingsFor(id))}
	ledger := m.connectedLedger(id)
	v.Balance = moneyList(balanceOf(ledger))
	for _, e := range ledger {
		if e.Type != "payout" {
			v.Activity = append(v.Activity, e)
		}
	}
	v.Activity = capRows(v.Activity, viewListLimit)
	v.Payouts = capRows(m.payoutsForLocked(id), viewListLimit)
	m.mu.RUnlock()
	renderView(w, "express", v)
}

// --- row builders (caller holds m.mu) ---

func (m *StripeMock) paymentRowsLocked() []paymentRow {
	rows := make([]paymentRow, 0, len(m.paymentIntents))
	for _, pi := range m.paymentIntents {
		row := paymentRow{
			ID: pi.ID, Created: pi.Created, Amount: pi.Amount, Currency: pi.Currency,
			Description: pi.Description, Customer: pi.ReceiptEmail, Metadata: cloneStringMap(pi.Metadata),
		}
		switch {
		case pi.TransferDataDestination != "":
			row.Connect = "→ " + pi.TransferDataDestination
		case pi.StripeAccount != "":
			row.Connect = "as " + pi.StripeAccount
		}
		ch, hasCharge := m.charges[pi.LatestChargeID]
		switch {
		case hasCharge:
			row.Method = "visa •••• 4242"
			row.Fee = stripeFee(ch.AmountCaptured, ch.Currency)
			row.Refunded = ch.AmountRefunded
			switch {
			case ch.DisputeID != "":
				row.Status = "disputed"
			case ch.Refunded:
				row.Status = "refunded"
			case ch.AmountRefunded > 0:
				row.Status = "partially_refunded"
			default:
				row.Status = "succeeded"
			}
		case pi.Failed:
			row.Status = "failed"
		case pi.Status == "requires_capture":
			row.Status = "uncaptured"
		case pi.Status == "canceled":
			row.Status = "canceled"
		default:
			row.Status = "incomplete"
		}
		rows = append(rows, row)
	}
	sortNewestFirst(rows, func(p paymentRow) (time.Time, string) { return p.Created, p.ID })
	return rows
}

func (m *StripeMock) accountRowsLocked() []accountRow {
	rows := make([]accountRow, 0, len(m.accounts)+len(m.v2Accounts))
	for _, a := range m.accounts {
		row := accountRow{
			ID: a.ID, API: "v1", Email: a.Email, Country: a.Country, Dashboard: a.Type,
			Created: a.Created, Balance: formatBalances(m.connectedBalance(a.ID)),
			Capabilities: []string{"charges: " + enabledText(a.ChargesEnabled), "payouts: " + enabledText(a.PayoutsEnabled)},
			Requirements: append([]string(nil), a.CurrentlyDue...),
			Status:       "restricted",
		}
		if a.DetailsSubmitted && a.ChargesEnabled && a.PayoutsEnabled {
			row.Status = "complete"
		}
		rows = append(rows, row)
	}
	for _, a := range m.v2Accounts {
		row := accountRow{
			ID: a.ID, API: "v2", Email: a.ContactEmail, Country: a.Country, Dashboard: a.Dashboard,
			Created: a.Created, Balance: formatBalances(m.connectedBalance(a.ID)), Status: "restricted",
		}
		for _, path := range slices.Sorted(maps.Keys(a.Capabilities)) {
			row.Capabilities = append(row.Capabilities, path+": "+a.Capabilities[path])
		}
		if a.onboarded() {
			row.Status = "complete"
		} else {
			row.Requirements = []string{"identity details", "payout bank account"}
		}
		rows = append(rows, row)
	}
	sortNewestFirst(rows, func(a accountRow) (time.Time, string) { return a.Created, a.ID })
	return rows
}

// transfersToLocked lists transfers to dest, or every transfer for "".
func (m *StripeMock) transfersToLocked(dest string) []*stripeTransfer {
	out := []*stripeTransfer{}
	for _, tr := range m.transfers {
		if dest == "" || tr.Destination == dest {
			out = append(out, cloneTransfer(tr))
		}
	}
	sortNewestFirst(out, func(t *stripeTransfer) (time.Time, string) { return t.Created, t.ID })
	return out
}

// payoutsForLocked lists payouts on one balance ("" = platform).
func (m *StripeMock) payoutsForLocked(acct string) []*stripePayout {
	out := []*stripePayout{}
	for _, po := range m.payouts {
		if po.AccountID == acct {
			out = append(out, clonePayout(po))
		}
	}
	sortNewestFirst(out, func(p *stripePayout) (time.Time, string) { return p.Created, p.ID })
	return out
}

// --- helpers ---

func capRows[T any](rows []T, n int) []T {
	if len(rows) > n {
		return rows[:n]
	}
	return rows
}

func enabledText(on bool) string {
	if on {
		return "active"
	}
	return "inactive"
}

func moneyList(b map[string]int64) []string {
	out := []string{}
	for _, cur := range slices.Sorted(maps.Keys(b)) {
		out = append(out, formatMoney(b[cur], cur))
	}
	return out
}

// formatMoney is formatStripeAmount with the sign in front of the symbol.
func formatMoney(amount int64, currency string) string {
	if amount < 0 {
		return "−" + formatStripeAmount(-amount, currency)
	}
	return formatStripeAmount(amount, currency)
}

func payoutScheduleText(bs *stripeBalanceSettings) string {
	switch bs.Interval {
	case "monthly":
		days := make([]string, len(bs.MonthlyPayoutDays))
		for i, d := range bs.MonthlyPayoutDays {
			days[i] = fmt.Sprint(d)
		}
		return "Monthly, on day " + strings.Join(days, ", ")
	case "weekly":
		return "Weekly, on " + strings.Join(bs.WeeklyPayoutDays, ", ")
	case "manual":
		return "Manual"
	default:
		return fmt.Sprintf("Daily, %d-day rolling basis", bs.DelayDays)
	}
}

func prettyJSON(raw []byte) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return string(raw)
	}
	return buf.String()
}

func renderView(w http.ResponseWriter, name string, data any) {
	var buf bytes.Buffer
	if err := stripeViewTmpl.ExecuteTemplate(&buf, name, data); err != nil {
		http.Error(w, "render: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(buf.Bytes()) //nolint:errcheck
}

var stripeViewFuncs = template.FuncMap{
	"money":    formatMoney,
	"sections": func() any { return platformSections },
	"date":     func(t time.Time) string { return t.Local().Format("2 Jan 2006, 15:04") },
	"day":      func(t time.Time) string { return t.Local().Format("2 Jan 2006") },
	"label": func(s string) string {
		s = strings.ReplaceAll(s, "_", " ")
		if s == "" {
			return s
		}
		return strings.ToUpper(s[:1]) + s[1:]
	},
	"net": func(e ledgerEntry) int64 { return e.Net() },
}

var stripeViewTmpl = template.Must(template.New("views").Funcs(stripeViewFuncs).Parse(`
{{define "head"}}<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.}} — hamr Stripe mock</title>
<style>
:root{--bg:#0f1419;--panel:#161b22;--line:#2e3642;--text:#d4d4d4;--muted:#64748b;--accent:#8b85ff;--ok:#86efac;--okbg:#14532d;--bad:#fca5a5;--badbg:#481414;--warn:#fbbf24;--warnbg:#422006}
*{box-sizing:border-box;margin:0;padding:0}
body{background:var(--bg);color:var(--text);font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;font-size:13px;min-height:100vh}
a{color:var(--accent);text-decoration:none}a:hover{text-decoration:underline}
code,pre{font-family:'SF Mono',Monaco,Consolas,monospace;font-size:11px}
.shell{display:flex;min-height:100vh}
nav{width:220px;flex-shrink:0;background:var(--panel);border-right:1px solid var(--line);padding:18px 10px}
nav .brand{font-weight:700;padding:0 10px 14px;display:flex;align-items:center;gap:8px;color:#e8e8e8}
nav a{display:block;padding:7px 10px;border-radius:6px;color:var(--text)}
nav a.on{background:#1f2630;color:#fff;font-weight:600}
nav a:hover{text-decoration:none;background:#1a2029}
nav .foot{margin-top:18px;padding:12px 10px 0;border-top:1px solid var(--line);font-size:12px}
nav .foot a{display:inline;padding:0;color:var(--accent)}
.badge{display:inline-block;background:#ff6b35;color:#fff;font-size:10px;font-weight:700;padding:2px 7px;border-radius:4px;text-transform:uppercase;letter-spacing:.05em}
main{flex:1;min-width:0;padding:28px 32px;max-width:1200px}
h1{font-size:22px;color:#e8e8e8;margin-bottom:18px}
h2{font-size:14px;color:#e8e8e8;margin:26px 0 10px}
.cards{display:flex;gap:14px;flex-wrap:wrap}
.card{background:var(--panel);border:1px solid var(--line);border-radius:8px;padding:14px 18px;min-width:200px}
.card .k{color:var(--muted);font-size:11px;text-transform:uppercase;letter-spacing:.05em;margin-bottom:6px}
.card .v{font-size:22px;font-weight:600;color:#e8e8e8}
.tablewrap{background:var(--panel);border:1px solid var(--line);border-radius:8px;overflow-x:auto}
table{width:100%;border-collapse:collapse}
th,td{padding:9px 12px;text-align:left;border-bottom:1px solid var(--line);vertical-align:top;white-space:nowrap}
td.wrap{white-space:normal}
th{color:var(--muted);font-weight:600;font-size:10px;text-transform:uppercase;letter-spacing:.05em}
tr:last-child td{border-bottom:none}
.num{text-align:right;font-variant-numeric:tabular-nums}
.empty{padding:18px;text-align:center;color:var(--muted)}
.muted{color:var(--muted)}
.pill{display:inline-block;padding:2px 7px;border-radius:10px;font-size:11px;font-weight:600;background:#1f2630;color:var(--text)}
.pill.succeeded,.pill.paid,.pill.complete,.pill.active,.pill.won,.pill.delivered{background:var(--okbg);color:var(--ok)}
.pill.failed,.pill.disputed,.pill.lost,.pill.restricted,.pill.canceled{background:var(--badbg);color:var(--bad)}
.pill.pending,.pill.incomplete,.pill.uncaptured,.pill.needs_response,.pill.partially_refunded,.pill.in_transit{background:var(--warnbg);color:var(--warn)}
details summary{cursor:pointer;color:var(--accent)}
pre{white-space:pre-wrap;word-break:break-all;margin-top:8px;color:var(--text);max-width:720px}
.kv{display:grid;grid-template-columns:180px 1fr;gap:6px 16px}
.kv .k{color:var(--muted)}
@media (max-width:760px){.shell{flex-direction:column}nav{width:auto;border-right:none;border-bottom:1px solid var(--line)}nav a{display:inline-block}main{padding:18px 16px}.kv{grid-template-columns:1fr}}
</style>
</head>
<body>{{end}}

{{define "platform"}}{{template "head" .Title}}
<div class="shell">
<nav>
<div class="brand">Platform <span class="badge">hamr mock</span></div>
{{range sections}}<a href="/__hamr/stripe/dashboard{{if .Key}}/{{.Key}}{{end}}" class="{{if eq .Key $.Section}}on{{end}}">{{.Label}}</a>{{end}}
<div class="foot"><a href="/__hamr/stripe">Mock controls →</a><p class="muted" style="margin-top:6px">Read-only. Change state from the controls page.</p></div>
</nav>
<main>
<h1>{{.Title}}</h1>

{{if or (eq .Section "") (eq .Section "balances")}}
<div class="cards">{{range .Balance}}<div class="card"><div class="k">Balance</div><div class="v">{{.}}</div></div>{{else}}<div class="card"><div class="k">Balance</div><div class="v">—</div></div>{{end}}</div>
{{end}}

{{if eq .Section ""}}<h2>Recent payments</h2>{{end}}
{{if or (eq .Section "") (eq .Section "payments")}}{{template "payments" .Payments}}{{end}}
{{if eq .Section ""}}<h2>Recent payouts</h2>{{template "payouts" .Payouts}}{{end}}

{{if eq .Section "balances"}}
<h2>Balance transactions</h2>
<div class="tablewrap">{{if .Ledger}}<table>
<thead><tr><th>Type</th><th class="num">Amount</th><th class="num">Fee</th><th class="num">Net</th><th>Description</th><th>Source</th><th>Date</th></tr></thead>
<tbody>{{range .Ledger}}<tr>
<td>{{label .Type}}{{if .Status}} <span class="pill {{.Status}}">{{label .Status}}</span>{{end}}</td>
<td class="num">{{money .Amount .Currency}}</td><td class="num">{{money .Fee .Currency}}</td><td class="num">{{money (net .) .Currency}}</td>
<td class="wrap">{{.Description}}</td><td><code>{{.Source}}</code></td><td>{{date .Created}}</td>
</tr>{{end}}</tbody></table>{{else}}<div class="empty">No balance transactions.</div>{{end}}</div>
{{end}}

{{if eq .Section "connect"}}
<div class="tablewrap">{{if .Accounts}}<table>
<thead><tr><th>Account</th><th>Status</th><th>API</th><th>Dashboard</th><th>Country</th><th class="num">Balance</th><th>Created</th><th></th></tr></thead>
<tbody>{{range .Accounts}}<tr>
<td>{{if .Email}}{{.Email}}<br>{{end}}<code class="muted">{{.ID}}</code></td>
<td><span class="pill {{.Status}}">{{label .Status}}</span></td>
<td>{{.API}}</td><td>{{.Dashboard}}</td><td>{{.Country}}</td><td class="num">{{.Balance}}</td><td>{{day .Created}}</td>
<td><a href="/__hamr/stripe/express/{{.ID}}">Express view →</a></td>
</tr>{{end}}</tbody></table>{{else}}<div class="empty">No connected accounts.</div>{{end}}</div>
{{end}}

{{if eq .Section "transfers"}}{{template "transfers" .Transfers}}{{end}}
{{if eq .Section "payouts"}}{{template "payouts" .Payouts}}{{end}}

{{if eq .Section "disputes"}}
<div class="tablewrap">{{if .Disputes}}<table>
<thead><tr><th class="num">Amount</th><th>Status</th><th>Reason</th><th>Payment</th><th>Evidence due</th><th>Opened</th></tr></thead>
<tbody>{{range .Disputes}}<tr>
<td class="num">{{money .Amount .Currency}}</td><td><span class="pill {{.Status}}">{{label .Status}}</span></td><td>{{label .Reason}}</td>
<td><code>{{.PaymentIntentID}}</code></td><td>{{day .EvidenceDueBy}}</td><td>{{date .Created}}</td>
</tr>{{end}}</tbody></table>{{else}}<div class="empty">No disputes.</div>{{end}}</div>
{{end}}

{{if eq .Section "events"}}
<div class="tablewrap">{{if .Events}}<table>
<thead><tr><th>Event</th><th>Delivery</th><th>Account</th><th>Date</th></tr></thead>
<tbody>{{range .Events}}<tr>
<td class="wrap"><strong>{{.Type}}</strong>{{if .Thin}} <span class="pill">thin</span>{{end}}<br><code class="muted">{{.ID}}</code>
<details><summary>Payload</summary><pre>{{.Pretty}}</pre></details></td>
<td class="wrap">{{if .Delivered}}<span class="pill delivered">Delivered</span>{{else if .LastError}}<span class="pill failed">Failed</span><br><span class="muted">{{.LastError}}</span>{{else}}<span class="pill">Not sent</span>{{end}}</td>
<td>{{if .Account}}<code>{{.Account}}</code>{{else}}<span class="muted">platform</span>{{end}}</td>
<td>{{date .Created}}</td>
</tr>{{end}}</tbody></table>{{else}}<div class="empty">No events yet. The log is kept in memory and clears when hamr dev restarts.</div>{{end}}</div>
{{end}}
</main>
</div>
</body></html>{{end}}

{{define "payments"}}<div class="tablewrap">{{if .}}<table>
<thead><tr><th class="num">Amount</th><th>Status</th><th>Payment method</th><th>Description</th><th>Customer</th><th class="num">Fee</th><th>Connect</th><th>Date</th></tr></thead>
<tbody>{{range .}}<tr>
<td class="num">{{money .Amount .Currency}}{{if gt .Refunded 0}}<br><span class="muted">{{money .Refunded .Currency}} refunded</span>{{end}}</td>
<td><span class="pill {{.Status}}">{{label .Status}}</span></td>
<td>{{if .Method}}{{.Method}}{{else}}<span class="muted">—</span>{{end}}</td>
<td class="wrap">{{if .Description}}{{.Description}}{{else}}<code class="muted">{{.ID}}</code>{{end}}{{range $k, $v := .Metadata}}<br><span class="muted">{{$k}}: {{$v}}</span>{{end}}</td>
<td>{{.Customer}}</td>
<td class="num">{{if .Fee}}{{money .Fee .Currency}}{{end}}</td>
<td>{{if .Connect}}<code>{{.Connect}}</code>{{end}}</td>
<td>{{date .Created}}</td>
</tr>{{end}}</tbody></table>{{else}}<div class="empty">No payments.</div>{{end}}</div>{{end}}

{{define "payouts"}}<div class="tablewrap">{{if .}}<table>
<thead><tr><th class="num">Amount</th><th>Status</th><th>Type</th><th>Arrival</th><th>Created</th><th>ID</th></tr></thead>
<tbody>{{range .}}<tr>
<td class="num">{{money .Amount .Currency}}</td><td><span class="pill {{.Status}}">{{label .Status}}</span>{{if .FailureMessage}}<br><span class="muted">{{.FailureMessage}}</span>{{end}}</td>
<td>{{if .Automatic}}Automatic{{else}}Manual{{end}}, {{.Method}}</td><td>{{day .ArrivalDate}}</td><td>{{date .Created}}</td><td><code class="muted">{{.ID}}</code></td>
</tr>{{end}}</tbody></table>{{else}}<div class="empty">No payouts.</div>{{end}}</div>{{end}}

{{define "transfers"}}<div class="tablewrap">{{if .}}<table>
<thead><tr><th class="num">Amount</th><th class="num">Reversed</th><th>Destination</th><th>Source charge</th><th>Description</th><th>Date</th></tr></thead>
<tbody>{{range .}}<tr>
<td class="num">{{money .Amount .Currency}}</td>
<td class="num">{{if .AmountReversed}}{{money .AmountReversed .Currency}}{{else}}<span class="muted">—</span>{{end}}</td>
<td><a href="/__hamr/stripe/express/{{.Destination}}"><code>{{.Destination}}</code></a></td>
<td>{{if .SourceTransactionID}}<code>{{.SourceTransactionID}}</code>{{else}}<span class="muted">platform balance</span>{{end}}</td>
<td class="wrap">{{.Description}}{{range $k, $v := .Metadata}}<br><span class="muted">{{$k}}: {{$v}}</span>{{end}}</td>
<td>{{date .Created}}</td>
</tr>{{end}}</tbody></table>{{else}}<div class="empty">No transfers.</div>{{end}}</div>{{end}}

{{define "express"}}{{template "head" "Express dashboard"}}
<div class="shell">
<nav>
<div class="brand">Express <span class="badge">hamr mock</span></div>
<a class="on" href="/__hamr/stripe/express/{{.Account.ID}}">Home</a>
<div class="foot"><a href="/__hamr/stripe/dashboard/connect">← Platform dashboard</a><p class="muted" style="margin-top:6px">What this connected account sees in its Express dashboard. Read-only.</p></div>
</nav>
<main>
<h1>{{if .Account.Email}}{{.Account.Email}}{{else}}{{.Account.ID}}{{end}} <span class="pill {{.Account.Status}}">{{label .Account.Status}}</span></h1>

<div class="cards">
{{range .Balance}}<div class="card"><div class="k">Balance</div><div class="v">{{.}}</div></div>{{else}}<div class="card"><div class="k">Balance</div><div class="v">—</div></div>{{end}}
<div class="card"><div class="k">Payout schedule</div><div class="v" style="font-size:15px">{{.Schedule}}</div></div>
</div>

<h2>Payouts</h2>
{{template "payouts" .Payouts}}

<h2>Activity</h2>
<div class="tablewrap">{{if .Activity}}<table>
<thead><tr><th>Type</th><th class="num">Amount</th><th class="num">Fees</th><th class="num">Net</th><th>Description</th><th>Date</th></tr></thead>
<tbody>{{range .Activity}}<tr>
<td>{{label .Type}}</td><td class="num">{{money .Amount .Currency}}</td><td class="num">{{if .Fee}}{{money .Fee .Currency}}{{end}}</td><td class="num">{{money (net .) .Currency}}</td>
<td class="wrap">{{.Description}}</td><td>{{date .Created}}</td>
</tr>{{end}}</tbody></table>{{else}}<div class="empty">No activity yet.</div>{{end}}</div>

<h2>Account</h2>
<div class="tablewrap" style="padding:14px 18px"><div class="kv">
<div class="k">Account ID</div><div><code>{{.Account.ID}}</code> ({{.Account.API}})</div>
{{if .Account.Country}}<div class="k">Country</div><div>{{.Account.Country}}</div>{{end}}
<div class="k">Capabilities</div><div>{{range .Account.Capabilities}}{{.}}<br>{{end}}</div>
<div class="k">Still needed</div><div>{{range .Account.Requirements}}{{.}}<br>{{else}}<span class="muted">Nothing</span>{{end}}</div>
</div></div>
</main>
</div>
</body></html>{{end}}
`))
