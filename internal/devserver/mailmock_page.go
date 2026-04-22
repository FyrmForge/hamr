package devserver

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

// handleInbox renders the newest-first inbox list. Supports ?q= substring
// match against subject and recipient email fields.
func (m *MailMock) handleInbox(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	msgs := m.List()

	var filtered []*mailMessage
	if query == "" {
		filtered = msgs
	} else {
		needle := strings.ToLower(query)
		for _, msg := range msgs {
			if matchesQuery(msg, needle) {
				filtered = append(filtered, msg)
			}
		}
	}

	var buf bytes.Buffer
	err := inboxTmpl.Execute(&buf, struct {
		Query    string
		Messages []*mailMessage
		Now      time.Time
	}{
		Query:    query,
		Messages: filtered,
		Now:      time.Now(),
	})
	if err != nil {
		http.Error(w, "render inbox: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(buf.Bytes()) //nolint:errcheck
}

// handleDetail renders the tabbed detail page. The HTML tab is rendered via
// an <iframe sandbox=""> pointing at handleHTMLFrame.
func (m *MailMock) handleDetail(w http.ResponseWriter, r *http.Request, msg *mailMessage) {
	tab := r.URL.Query().Get("tab")
	if tab == "" {
		if msg.HTML != "" {
			tab = "html"
		} else {
			tab = "text"
		}
	}
	viewport := r.URL.Query().Get("viewport")
	images := r.URL.Query().Get("images")

	// Raw view is a best-effort RFC822-ish reconstruction, not real MIME. We
	// do not persist the wire bytes because the mock never receives MIME.
	raw := renderRawMIMEish(msg)

	// Sorted header pairs for stable display.
	type headerRow struct{ K, V string }
	var headerRows []headerRow
	for k, v := range msg.Headers {
		headerRows = append(headerRows, headerRow{k, v})
	}
	sort.Slice(headerRows, func(i, j int) bool { return headerRows[i].K < headerRows[j].K })

	var tagRows []headerRow
	for k, v := range msg.Tags {
		tagRows = append(tagRows, headerRow{k, v})
	}
	sort.Slice(tagRows, func(i, j int) bool { return tagRows[i].K < tagRows[j].K })

	var buf bytes.Buffer
	err := detailTmpl.Execute(&buf, struct {
		Msg        *mailMessage
		Tab        string
		Viewport   string
		Images     string
		Raw        string
		Headers    []headerRow
		Tags       []headerRow
		HasHTML    bool
		HasText    bool
		IFrameSrc  string
		ViewportPx int
	}{
		Msg:        msg,
		Tab:        tab,
		Viewport:   viewport,
		Images:     images,
		Raw:        raw,
		Headers:    headerRows,
		Tags:       tagRows,
		HasHTML:    msg.HTML != "",
		HasText:    msg.Text != "",
		IFrameSrc:  buildHTMLFrameSrc(msg.ID, viewport, images),
		ViewportPx: viewportPx(viewport),
	})
	if err != nil {
		http.Error(w, "render detail: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(buf.Bytes()) //nolint:errcheck
}

// handleHTMLFrame serves the iframe body: the email HTML rewritten so cid:
// references resolve and with security headers that lock down what the iframe
// can do. The iframe itself carries sandbox="" on the parent page.
//
// The optional ?theme=dark|light param sets a color-scheme hint on the root
// document. This affects UA defaults inside the iframe (scrollbars, form
// controls, unstyled text colors) — it does NOT change the browser's real
// prefers-color-scheme media query, which is controlled by the user's OS.
func (m *MailMock) handleHTMLFrame(w http.ResponseWriter, r *http.Request, msg *mailMessage) {
	images := r.URL.Query().Get("images")
	theme := r.URL.Query().Get("theme")
	if theme != "dark" {
		theme = "light"
	}

	html := rewriteCID(msg.HTML, msg.ID)
	if images == "blocked" {
		html = stripImgSrc(html)
	}

	// Restrictive CSP:
	//   - no scripts
	//   - images only same-origin (cid resolves here) or data: URIs
	//   - inline styles allowed (emails rely on them)
	//   - no default: everything else denied
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	// Wrap in a minimal document so inline styles in the email body don't
	// bleed into the parent. `color-scheme` (meta + style) tells the UA which
	// default palette to use for scrollbars, form widgets, and unstyled text.
	_, _ = fmt.Fprintf(w,
		`<!DOCTYPE html><html style="color-scheme:%s"><head><meta charset="utf-8"><meta name="color-scheme" content="%s"><meta name="referrer" content="no-referrer"></head><body>%s</body></html>`,
		theme, theme, html)
}

// --- helpers ---

func matchesQuery(msg *mailMessage, needle string) bool {
	if strings.Contains(strings.ToLower(msg.Subject), needle) {
		return true
	}
	for _, a := range msg.To {
		if strings.Contains(strings.ToLower(a.Email), needle) || strings.Contains(strings.ToLower(a.Name), needle) {
			return true
		}
	}
	for _, a := range msg.Cc {
		if strings.Contains(strings.ToLower(a.Email), needle) {
			return true
		}
	}
	for _, a := range msg.Bcc {
		if strings.Contains(strings.ToLower(a.Email), needle) {
			return true
		}
	}
	if strings.Contains(strings.ToLower(msg.From.Email), needle) || strings.Contains(strings.ToLower(msg.From.Name), needle) {
		return true
	}
	return false
}

func buildHTMLFrameSrc(id, viewport, images string) string {
	q := make([]string, 0, 2)
	if viewport != "" {
		q = append(q, "viewport="+viewport)
	}
	if images != "" {
		q = append(q, "images="+images)
	}
	src := "/__hamr/mail/" + id + "/html"
	if len(q) > 0 {
		src += "?" + strings.Join(q, "&")
	}
	return src
}

func viewportPx(v string) int {
	if v == "mobile" {
		return 375
	}
	return 0
}

// cidRef matches any cid:<token> reference in HTML, regardless of the
// surrounding attribute (src=, srcset=, background=, style url(), ...).
// Token charset is permissive (anything that isn't whitespace or a quote/
// angle-bracket) to accept RFC 2392 content-ids without parsing them.
var cidRef = regexp.MustCompile(`[cC][iI][dD]:([^\s"'<>)]+)`)

// rewriteCID rewrites every cid:<id> reference in HTML to point at the
// mock's inline endpoint. Replacement is global (regex-based) so it catches
// src=, srcset= (multi-URL lists), background=, and style url() usages —
// not just src= like the first cut handled.
func rewriteCID(html, msgID string) string {
	prefix := "/__hamr/mail/" + msgID + "/inline/"
	return cidRef.ReplaceAllString(html, prefix+"$1")
}

// imgSrcAttr matches src= or srcset= values on <img> tags. The captured
// group 1 is the attribute name so we can preserve case in the replacement.
var imgSrcAttr = regexp.MustCompile(`(?i)(<img\b[^>]*?\b(?:src|srcset))\s*=\s*(?:"[^"]*"|'[^']*'|[^\s>]+)`)

// cssUrlAttr matches url(...) values inside inline style attributes. We scope
// it to style=" " or style=' ' blocks so we don't touch arbitrary text.
var cssUrlInStyle = regexp.MustCompile(`(?i)(style\s*=\s*["'][^"']*?)url\(\s*(?:"[^"]*"|'[^']*'|[^)]+)\s*\)`)

// transparentPixel is a 1x1 transparent SVG data URI used as the placeholder
// when images are blocked.
const transparentPixel = `data:image/svg+xml;utf8,%3Csvg xmlns=%22http://www.w3.org/2000/svg%22 width=%221%22 height=%221%22/%3E`

// stripImgSrc rewrites image-loading attributes on <img> tags and url() refs
// inside inline styles to a transparent placeholder so the images-blocked
// view renders layout without loading any remote (or same-origin) bytes.
func stripImgSrc(html string) string {
	html = imgSrcAttr.ReplaceAllString(html, `$1="`+transparentPixel+`"`)
	html = cssUrlInStyle.ReplaceAllString(html, `${1}url("`+transparentPixel+`")`)
	return html
}

// renderRawMIMEish produces a human-readable, RFC822-shaped dump of a stored
// message. This is NOT real MIME; it's a debugging aid. We never received
// real wire bytes, so we can't show them.
func renderRawMIMEish(msg *mailMessage) string {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\n", msg.From.Display())
	writeAddrList(&b, "To", msg.To)
	writeAddrList(&b, "Cc", msg.Cc)
	writeAddrList(&b, "Bcc", msg.Bcc)
	if msg.ReplyTo != nil {
		fmt.Fprintf(&b, "Reply-To: %s\n", msg.ReplyTo.Display())
	}
	fmt.Fprintf(&b, "Subject: %s\n", msg.Subject)
	fmt.Fprintf(&b, "Date: %s\n", msg.ReceivedAt.Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: <%s@hamr.local>\n", msg.ID)
	for k, v := range msg.Headers {
		fmt.Fprintf(&b, "%s: %s\n", k, v)
	}
	hasText := msg.Text != ""
	hasHTML := msg.HTML != ""
	switch {
	case hasText && hasHTML:
		fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=\"hamr-boundary\"\n\n--hamr-boundary\nContent-Type: text/plain; charset=utf-8\n\n%s\n\n--hamr-boundary\nContent-Type: text/html; charset=utf-8\n\n%s\n\n--hamr-boundary--\n", msg.Text, msg.HTML)
	case hasHTML:
		fmt.Fprintf(&b, "Content-Type: text/html; charset=utf-8\n\n%s\n", msg.HTML)
	case hasText:
		fmt.Fprintf(&b, "Content-Type: text/plain; charset=utf-8\n\n%s\n", msg.Text)
	default:
		fmt.Fprint(&b, "\n(no body)\n")
	}
	if len(msg.Attach) > 0 {
		b.WriteString("\n--- Attachments ---\n")
		for i, a := range msg.Attach {
			fmt.Fprintf(&b, "[%d] %s (%s, %d bytes)\n", i, a.Filename, a.ContentType, len(a.Data))
		}
	}
	if len(msg.Inline) > 0 {
		b.WriteString("\n--- Inline ---\n")
		for _, a := range msg.Inline {
			fmt.Fprintf(&b, "cid:%s  %s (%s, %d bytes)\n", a.ContentID, a.Filename, a.ContentType, len(a.Data))
		}
	}
	return b.String()
}

func writeAddrList(b *strings.Builder, name string, addrs []mailAddress) {
	if len(addrs) == 0 {
		return
	}
	parts := make([]string, len(addrs))
	for i, a := range addrs {
		parts[i] = a.Display()
	}
	fmt.Fprintf(b, "%s: %s\n", name, strings.Join(parts, ", "))
}

// --- templates ---

var mailTmplFuncs = template.FuncMap{
	"joinAddrs": func(addrs []mailAddress) string {
		parts := make([]string, len(addrs))
		for i, a := range addrs {
			parts[i] = a.Display()
		}
		return strings.Join(parts, ", ")
	},
	"fmtTime": func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.Format("2006-01-02 15:04:05")
	},
	"since": func(t time.Time) string {
		d := time.Since(t).Round(time.Second)
		return d.String()
	},
}

// mailCSS styles the dev inbox chrome. Always dark — the preview theme toggle
// on the detail page only affects the HTML-preview iframe (and its surrounding
// .iframe-wrap background), not the inbox/detail chrome itself.
const mailCSS = `
*{box-sizing:border-box;margin:0;padding:0}
body{background:#0f1419;color:#d4d4d4;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;min-height:100vh}
a{color:#7dd3fc;text-decoration:none}
a:hover{text-decoration:underline}
code{font-family:'SF Mono',Monaco,Consolas,monospace}
.wrap{max-width:1100px;margin:0 auto;padding:32px 24px}
h1{font-size:22px;font-weight:700;color:#e8e8e8;margin-bottom:6px;display:flex;align-items:center;gap:12px}
.badge{display:inline-block;background:#f97316;color:#fff;font-size:10px;font-weight:700;padding:3px 8px;border-radius:4px;text-transform:uppercase;letter-spacing:0.05em}
.sub{font-size:13px;color:#64748b;margin-bottom:24px}
.toolbar{display:flex;gap:12px;align-items:center;margin-bottom:20px;flex-wrap:wrap}
.toolbar input[type=text]{background:#1a1f26;color:#d4d4d4;border:1px solid #2e3642;border-radius:6px;padding:8px 12px;font-size:14px;font-family:inherit;min-width:280px}
.toolbar input[type=text]:focus{outline:none;border-color:#f97316}
.btn{background:#1a1f26;color:#d4d4d4;border:1px solid #2e3642;border-radius:6px;padding:8px 14px;font-size:13px;font-weight:500;cursor:pointer;font-family:inherit;transition:filter 0.15s}
.btn:hover{filter:brightness(1.2)}
.btn-danger{color:#f87171;border-color:#481414}
.btn-primary{background:#f97316;color:#fff;border-color:#f97316}
.list{background:#161b22;border:1px solid #2e3642;border-radius:8px;overflow:hidden}
.list-empty{padding:48px 24px;text-align:center;color:#64748b;font-size:14px}
.row{display:grid;grid-template-columns:200px 1fr 110px;gap:16px;padding:12px 18px;border-bottom:1px solid #2e3642;font-size:13px;align-items:center}
.row:last-child{border-bottom:none}
.row:hover{background:#1a1f26}
.row .from{color:#a3c4e0;font-weight:500;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.row .subject{color:#e8e8e8;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.row .subject .preview{color:#64748b;margin-left:8px}
.row .when{color:#64748b;font-size:12px;text-align:right}
.status-tag{display:inline-block;padding:1px 6px;border-radius:3px;font-size:10px;font-weight:700;text-transform:uppercase;margin-right:6px}
.status-delivered{background:#14532d;color:#86efac}
.status-failed{background:#481414;color:#fca5a5}
.status-delayed{background:#422006;color:#fbbf24}
.tabs{display:flex;gap:2px;border-bottom:1px solid #2e3642;margin-bottom:18px;flex-wrap:wrap}
.tab{padding:9px 14px;font-size:13px;color:#64748b;border:none;background:none;cursor:pointer;border-bottom:2px solid transparent;font-family:inherit}
.tab.active{color:#e8e8e8;border-bottom-color:#f97316}
.tab:hover{color:#d4d4d4}
.panel{background:#161b22;border:1px solid #2e3642;border-radius:8px;padding:18px;margin-bottom:16px}
.panel-header{font-size:12px;color:#64748b;text-transform:uppercase;letter-spacing:0.05em;margin-bottom:10px}
.kv{display:grid;grid-template-columns:140px 1fr;gap:6px 16px;font-size:13px}
.kv .k{color:#64748b}
.kv .v{color:#e8e8e8;word-break:break-word}
.iframe-wrap{border-radius:6px;overflow:hidden;border:1px solid #2e3642;transition:background 0.15s}
.iframe-wrap.preview-light{background:#fff}
.iframe-wrap.preview-dark{background:#111}
.iframe-wrap iframe{display:block;width:100%;height:600px;border:none}
.iframe-wrap.mobile iframe{width:375px;margin:0 auto}
pre{background:#0d1117;border:1px solid #2e3642;border-radius:6px;padding:14px;font-family:'SF Mono',Monaco,Consolas,monospace;font-size:12px;line-height:1.6;color:#d4d4d4;overflow-x:auto;white-space:pre-wrap;word-break:break-word;max-height:600px;overflow-y:auto}
.controls{display:flex;gap:8px;margin-bottom:14px;flex-wrap:wrap}
.attachments a{display:inline-block;background:#1a1f26;border:1px solid #2e3642;border-radius:6px;padding:8px 12px;color:#7dd3fc;font-size:13px;margin-right:8px;margin-bottom:8px}
.form-inline{display:inline-flex;gap:8px;align-items:center}
.form-inline input[type=text],.form-inline input[type=number]{background:#1a1f26;color:#d4d4d4;border:1px solid #2e3642;border-radius:6px;padding:6px 10px;font-size:13px;font-family:inherit;max-width:180px}
.hint{font-size:12px;color:#64748b;margin-top:6px}
`

var inboxTmpl = template.Must(template.New("inbox").Funcs(mailTmplFuncs).Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>hamr — Mail Inbox</title>
<style>` + mailCSS + `</style>
</head>
<body>
<div class="wrap">
<h1>Mail Inbox <span class="badge">Dev Mock</span></h1>
<p class="sub">Captured messages sent through <code>email.Sender</code> during this <code>hamr dev</code> session. Inbox lives in memory; it survives app restarts but is wiped when <code>hamr dev</code> itself restarts (e.g. on <code>hamr.toml</code> change).</p>
<div class="toolbar">
<form method="GET" action="/__hamr/mail" style="display:flex;gap:8px">
<input type="text" name="q" value="{{.Query}}" placeholder="search subject, recipient, sender…" autofocus>
<button type="submit" class="btn">Search</button>
{{if .Query}}<a href="/__hamr/mail" class="btn">Clear</a>{{end}}
</form>
<div style="flex:1"></div>
{{if .Messages}}
<form method="POST" action="/__hamr/mail/clear" onsubmit="return confirm('Clear entire inbox?')">
<button type="submit" class="btn btn-danger">Clear inbox</button>
</form>
{{end}}
</div>
<div class="list">
{{if not .Messages}}
<div class="list-empty">
{{if .Query}}No messages match <code>{{.Query}}</code>.{{else}}No messages captured yet. Configure your app with <code>EMAIL_MOCK=true</code> and send something.{{end}}
</div>
{{else}}
{{range .Messages}}
<a class="row" href="/__hamr/mail/{{.ID}}">
<div class="from">{{.From.Display}}</div>
<div class="subject">
<span class="status-tag status-{{.Status}}">{{.Status}}</span>
<strong>{{.Subject}}</strong>
<span class="preview">→ {{joinAddrs .To}}</span>
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

var detailTmpl = template.Must(template.New("detail").Funcs(mailTmplFuncs).Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>hamr — {{.Msg.Subject}}</title>
<style>` + mailCSS + `</style>
</head>
<body>
<div class="wrap">
<h1>{{.Msg.Subject}} <span class="badge">Dev Mock</span></h1>
<p class="sub"><a href="/__hamr/mail">← Inbox</a> · received {{fmtTime .Msg.ReceivedAt}} · {{.Msg.ID}}</p>

<div class="panel">
<div class="kv">
<div class="k">Status</div><div class="v"><span class="status-tag status-{{.Msg.Status}}">{{.Msg.Status}}</span>{{if .Msg.StatusNote}} {{.Msg.StatusNote}}{{end}}</div>
<div class="k">From</div><div class="v">{{.Msg.From.Display}}</div>
<div class="k">To</div><div class="v">{{joinAddrs .Msg.To}}</div>
{{if .Msg.Cc}}<div class="k">Cc</div><div class="v">{{joinAddrs .Msg.Cc}}</div>{{end}}
{{if .Msg.Bcc}}<div class="k">Bcc</div><div class="v">{{joinAddrs .Msg.Bcc}}</div>{{end}}
{{if .Msg.ReplyTo}}<div class="k">Reply-To</div><div class="v">{{.Msg.ReplyTo.Display}}</div>{{end}}
</div>
</div>

<div class="tabs">
{{if .HasHTML}}<a class="tab {{if eq .Tab "html"}}active{{end}}" href="?tab=html{{if ne .Viewport ""}}&viewport={{.Viewport}}{{end}}{{if ne .Images ""}}&images={{.Images}}{{end}}">HTML</a>{{end}}
{{if .HasText}}<a class="tab {{if eq .Tab "text"}}active{{end}}" href="?tab=text">Plaintext</a>{{end}}
<a class="tab {{if eq .Tab "raw"}}active{{end}}" href="?tab=raw">Raw</a>
<a class="tab {{if eq .Tab "headers"}}active{{end}}" href="?tab=headers">Headers</a>
{{if or .Msg.Attach .Msg.Inline}}<a class="tab {{if eq .Tab "attachments"}}active{{end}}" href="?tab=attachments">Attachments</a>{{end}}
</div>

{{if eq .Tab "html"}}
<div class="controls">
<a class="btn" href="?tab=html">Desktop</a>
<a class="btn {{if eq .Viewport "mobile"}}btn-primary{{end}}" href="?tab=html&viewport=mobile{{if ne .Images ""}}&images={{.Images}}{{end}}">Mobile</a>
<div style="flex:1"></div>
<a class="btn" href="?tab=html{{if ne .Viewport ""}}&viewport={{.Viewport}}{{end}}">Images on</a>
<a class="btn {{if eq .Images "blocked"}}btn-primary{{end}}" href="?tab=html&images=blocked{{if ne .Viewport ""}}&viewport={{.Viewport}}{{end}}">Images blocked</a>
<button type="button" id="hamr-preview-theme" class="btn" data-base-src="{{.IFrameSrc}}" title="Toggle the preview background + color-scheme hint — affects only the email view, not the inbox chrome"></button>
</div>
<p class="hint">Rendered in a sandboxed iframe — no JS, no network access except same-origin inline images. The preview-theme toggle changes the iframe background and passes a <code>color-scheme</code> meta to the rendered email (affects UA-default text/scrollbar colors; doesn't change the real <code>prefers-color-scheme</code> media query).</p>
<div id="hamr-iframe-wrap" class="iframe-wrap {{if eq .Viewport "mobile"}}mobile{{end}} preview-light">
<iframe id="hamr-preview-iframe" src="{{.IFrameSrc}}" sandbox="" referrerpolicy="no-referrer"></iframe>
</div>
<script>
(function(){
  var KEY="__hamr_mail_preview_theme";
  var btn=document.getElementById("hamr-preview-theme");
  var wrap=document.getElementById("hamr-iframe-wrap");
  var frame=document.getElementById("hamr-preview-iframe");
  if(!btn||!wrap||!frame)return;
  var baseSrc=btn.getAttribute("data-base-src")||"";
  var theme="light";
  try{if(localStorage.getItem(KEY)==="dark")theme="dark";}catch(e){}
  function apply(){
    wrap.classList.toggle("preview-dark",theme==="dark");
    wrap.classList.toggle("preview-light",theme!=="dark");
    var sep=baseSrc.indexOf("?")>=0?"&":"?";
    frame.src=baseSrc+sep+"theme="+encodeURIComponent(theme);
    btn.textContent=theme==="dark"?"☾ Dark preview":"☀ Light preview";
    btn.setAttribute("aria-label",theme==="dark"?"Switch preview to light":"Switch preview to dark");
  }
  apply();
  btn.addEventListener("click",function(){
    theme=theme==="dark"?"light":"dark";
    try{localStorage.setItem(KEY,theme);}catch(e){}
    apply();
  });
})();
</script>
{{else if eq .Tab "text"}}
<pre>{{.Msg.Text}}</pre>
{{else if eq .Tab "raw"}}
<p class="hint">Reconstructed from stored fields — not the actual wire bytes (JSON ingest doesn't receive MIME). Use your provider's test env to validate real MIME.</p>
<pre>{{.Raw}}</pre>
{{else if eq .Tab "headers"}}
<div class="panel">
<div class="panel-header">Custom headers</div>
{{if .Headers}}
<div class="kv">
{{range .Headers}}<div class="k">{{.K}}</div><div class="v">{{.V}}</div>{{end}}
</div>
{{else}}<p class="hint">No custom headers set.</p>{{end}}
</div>
<div class="panel">
<div class="panel-header">Tags</div>
{{if .Tags}}
<div class="kv">
{{range .Tags}}<div class="k">{{.K}}</div><div class="v">{{.V}}</div>{{end}}
</div>
{{else}}<p class="hint">No tags set.</p>{{end}}
</div>
{{else if eq .Tab "attachments"}}
<div class="panel">
<div class="panel-header">Attachments ({{len .Msg.Attach}})</div>
<div class="attachments">
{{range $i, $a := .Msg.Attach}}
<a href="/__hamr/mail/{{$.Msg.ID}}/attachment/{{$i}}">{{$a.Filename}} <span style="color:#64748b">({{$a.ContentType}}, {{len $a.Data}} b)</span></a>
{{else}}<p class="hint">None.</p>{{end}}
</div>
</div>
<div class="panel">
<div class="panel-header">Inline ({{len .Msg.Inline}})</div>
<div class="attachments">
{{range .Msg.Inline}}
<a href="/__hamr/mail/{{$.Msg.ID}}/inline/{{.ContentID}}">cid:{{.ContentID}} — {{.Filename}} <span style="color:#64748b">({{.ContentType}}, {{len .Data}} b)</span></a>
{{else}}<p class="hint">None.</p>{{end}}
</div>
</div>
{{end}}

<div class="panel">
<div class="panel-header">Outcome simulation</div>
<div class="controls">
<form class="form-inline" method="POST" action="/__hamr/mail/{{.Msg.ID}}/fail">
<input type="text" name="note" placeholder="failure reason (optional)">
<button class="btn btn-danger" type="submit">Mark failed</button>
</form>
<form class="form-inline" method="POST" action="/__hamr/mail/{{.Msg.ID}}/delay">
<input type="number" name="seconds" value="30" min="1">
<button class="btn" type="submit">Mark delayed</button>
</form>
<div style="flex:1"></div>
<form method="POST" action="/__hamr/mail/{{.Msg.ID}}/delete" onsubmit="return confirm('Delete this message?')">
<button class="btn btn-danger" type="submit">Delete</button>
</form>
</div>
<p class="hint">Post-hoc outcome tagging for UI-visible state. For send-time failure (apps exercising error paths), use magic recipients: <code>bounce@...</code> or <code>reject@...</code>.</p>
</div>
</div>
</body>
</html>
`))
