package devserver

import (
	"bytes"
	"html/template"
	"net/http"
	"strings"
	"time"
)

// handleInbox renders the newest-first inbox list. Supports ?q= substring
// match against from, to, and body.
func (m *SMSMock) handleInbox(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	msgs := m.List()

	var filtered []*smsMessage
	if query == "" {
		filtered = msgs
	} else {
		needle := strings.ToLower(query)
		for _, msg := range msgs {
			if matchesSMSQuery(msg, needle) {
				filtered = append(filtered, msg)
			}
		}
	}

	var buf bytes.Buffer
	err := smsInboxTmpl.Execute(&buf, struct {
		Query    string
		Messages []*smsMessage
	}{
		Query:    query,
		Messages: filtered,
	})
	if err != nil {
		http.Error(w, "render inbox: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(buf.Bytes()) //nolint:errcheck
}

// handleDetail renders the single-message page.
func (m *SMSMock) handleDetail(w http.ResponseWriter, _ *http.Request, msg *smsMessage) {
	var buf bytes.Buffer
	err := smsDetailTmpl.Execute(&buf, struct {
		Msg *smsMessage
	}{
		Msg: msg,
	})
	if err != nil {
		http.Error(w, "render detail: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(buf.Bytes()) //nolint:errcheck
}

func matchesSMSQuery(msg *smsMessage, needle string) bool {
	return strings.Contains(strings.ToLower(msg.From), needle) ||
		strings.Contains(strings.ToLower(msg.To), needle) ||
		strings.Contains(strings.ToLower(msg.Body), needle)
}

var smsTmplFuncs = template.FuncMap{
	"fmtTime": func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.Format("2006-01-02 15:04:05")
	},
	"since": func(t time.Time) string {
		return formatSince(time.Since(t))
	},
	"preview": func(s string) string {
		s = strings.Join(strings.Fields(s), " ")
		if len(s) > 120 {
			return s[:120] + "…"
		}
		return s
	},
}

// smsInboxTmpl and smsDetailTmpl reuse mailCSS so the mock dashboards share
// one look.
var smsInboxTmpl = template.Must(template.New("sms-inbox").Funcs(smsTmplFuncs).Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>hamr — SMS Inbox</title>
<style>` + mailCSS + `</style>
</head>
<body>
<div class="wrap">
<h1>SMS Inbox <span class="badge">Dev Mock</span></h1>
<p class="sub">Captured messages sent through <code>sms.Sender</code> during this <code>hamr dev</code> session. Inbox lives in memory; it survives app restarts but is wiped when <code>hamr dev</code> itself restarts (e.g. on <code>hamr.toml</code> change).</p>
<div class="toolbar">
<form method="GET" action="/__hamr/sms" style="display:flex;gap:8px">
<input type="text" name="q" value="{{.Query}}" placeholder="search from, to, body…" autofocus>
<button type="submit" class="btn">Search</button>
{{if .Query}}<a href="/__hamr/sms" class="btn">Clear</a>{{end}}
</form>
<div style="flex:1"></div>
{{if .Messages}}
<form method="POST" action="/__hamr/sms/clear" onsubmit="return confirm('Clear entire inbox?')">
<button type="submit" class="btn btn-danger">Clear inbox</button>
</form>
{{end}}
</div>
<div class="list">
{{if not .Messages}}
<div class="list-empty">
{{if .Query}}No messages match <code>{{.Query}}</code>.{{else}}No messages captured yet. Configure your app with <code>SMS_MOCK=true</code> and send something.{{end}}
</div>
{{else}}
{{range .Messages}}
<a class="row" href="/__hamr/sms/{{.ID}}">
<div class="from">{{.To}}</div>
<div class="subject">
<span class="status-tag status-{{.Status}}">{{.Status}}</span>
{{preview .Body}}
<span class="preview">from {{.From}}</span>
</div>
<div class="when">{{since .ReceivedAt}} ago</div>
</a>
{{end}}
{{end}}
</div>
</div>
</body>
</html>
`))

var smsDetailTmpl = template.Must(template.New("sms-detail").Funcs(smsTmplFuncs).Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>hamr — SMS to {{.Msg.To}}</title>
<style>` + mailCSS + `</style>
</head>
<body>
<div class="wrap">
<h1>SMS to {{.Msg.To}} <span class="badge">Dev Mock</span></h1>
<p class="sub"><a href="/__hamr/sms">← Inbox</a> · received {{fmtTime .Msg.ReceivedAt}} · {{.Msg.ID}}</p>

<div class="panel">
<div class="kv">
<div class="k">Status</div><div class="v"><span class="status-tag status-{{.Msg.Status}}">{{.Msg.Status}}</span>{{if .Msg.StatusNote}} {{.Msg.StatusNote}}{{end}}</div>
<div class="k">From</div><div class="v">{{.Msg.From}}</div>
<div class="k">To</div><div class="v">{{.Msg.To}}</div>
<div class="k">Length</div><div class="v">{{len .Msg.Body}} chars</div>
{{if .Msg.Ref}}<div class="k">Ref</div><div class="v">{{.Msg.Ref}}</div>{{end}}
</div>
</div>

<div class="panel">
<div class="panel-header">Body</div>
<pre>{{.Msg.Body}}</pre>
</div>

<div class="panel">
<div class="panel-header">Outcome simulation</div>
<div class="controls">
<form class="form-inline" method="POST" action="/__hamr/sms/{{.Msg.ID}}/fail">
<input type="text" name="note" placeholder="failure reason (optional)">
<button class="btn btn-danger" type="submit">Mark failed</button>
</form>
<form class="form-inline" method="POST" action="/__hamr/sms/{{.Msg.ID}}/delay">
<input type="number" name="seconds" value="30" min="1">
<button class="btn" type="submit">Mark delayed</button>
</form>
<div style="flex:1"></div>
<form method="POST" action="/__hamr/sms/{{.Msg.ID}}/delete" onsubmit="return confirm('Delete this message?')">
<button class="btn btn-danger" type="submit">Delete</button>
</form>
</div>
<p class="hint">Post-hoc outcome tagging for UI-visible state. For send-time failure (apps exercising error paths), use magic recipient numbers ending in <code>555-0001</code> (invalid) or <code>555-0002</code> (undeliverable).</p>
</div>
</div>
</body>
</html>
`))
