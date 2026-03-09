package devserver

import (
	"bytes"
	"html/template"
	"regexp"
	"sort"
)

var ansiRegexp = regexp.MustCompile(`\x1B\[[0-9;]*[A-Za-z]`)

func stripANSI(s string) string {
	return ansiRegexp.ReplaceAllString(s, "")
}

type errorEntry struct {
	Rule   string
	Output string
}

func renderErrorPage(errors map[string]string) []byte {
	entries := make([]errorEntry, 0, len(errors))
	for rule, output := range errors {
		entries = append(entries, errorEntry{Rule: rule, Output: stripANSI(output)})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Rule < entries[j].Rule })

	var buf bytes.Buffer
	_ = errorPageTmpl.Execute(&buf, struct {
		Entries  []errorEntry
		ReloadJS template.JS
	}{
		Entries:  entries,
		ReloadJS: template.JS(reloadJS), //nolint:gosec
	})
	return buf.Bytes()
}

var errorPageTmpl = template.Must(template.New("errorpage").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>hamr — Build Error</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{background:#111113;color:#D4D4D4;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;
min-height:100vh;display:flex;flex-direction:column;align-items:center;padding:40px 20px}
.header{display:flex;align-items:center;gap:16px;margin-bottom:32px}
.header img{width:48px;height:48px}
.header h1{font-size:24px;font-weight:700;color:#EF4444}
.card{background:#1A1A1E;border:1px solid #2E2E34;border-radius:10px;width:100%;max-width:800px;
margin-bottom:20px;overflow:hidden}
.card-header{padding:14px 18px;background:#1F1F24;border-bottom:1px solid #2E2E34;
font-weight:600;font-size:15px;color:#E8E8E8}
.card-body{padding:16px 18px}
pre{background:#0D0D0F;border:1px solid #2E2E34;border-radius:6px;padding:14px;
font-family:'SF Mono',Monaco,Consolas,monospace;font-size:12px;line-height:1.5;
color:#F87171;overflow-x:auto;white-space:pre-wrap;word-break:break-all;max-height:400px;overflow-y:auto}
.note{margin-top:24px;font-size:13px;color:#6B7280;text-align:center}
</style>
</head>
<body>
<div class="header">
<img src="/__hamr/logo.png" alt="hamr">
<h1>Build Error</h1>
</div>
<div id="__hamr-errors">
{{range .Entries}}
<div class="card">
<div class="card-header">{{.Rule}}</div>
<div class="card-body"><pre>{{.Output}}</pre></div>
</div>
{{end}}
</div>
<p class="note">This page will refresh automatically when the error is fixed.</p>
<script>window.__hamr_error_page = true;</script>
<script>
{{.ReloadJS}}
</script>
</body>
</html>
`))
