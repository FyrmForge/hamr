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

func renderWaitingPage(target string) []byte {
	var buf bytes.Buffer
	_ = waitingPageTmpl.Execute(&buf, struct {
		Target   string
		ReloadJS template.JS
	}{
		Target:   target,
		ReloadJS: template.JS(reloadJS), //nolint:gosec
	})
	return buf.Bytes()
}

// All class names and keyframes in the templates below are prefixed with
// __hamr-wait- / __hamr-err- and the styles are scoped to a unique root id.
// This prevents collisions when the page is grafted into a running site via
// reload.js's swapBody (which only swaps body content and doesn't transfer
// <head> styles, so any user-site CSS would otherwise hijack generic class
// names like .header / .card / .note). The <style> block lives inside <body>
// so it travels with the swapped content.

var waitingPageTmpl = template.Must(template.New("waitingpage").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>hamr — Waiting for Server</title>
</head>
<body>
<style>
#__hamr-wait-root,#__hamr-wait-root *{box-sizing:border-box;margin:0;padding:0}
#__hamr-wait-root{background:#111113;color:#D4D4D4;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;
width:100%;min-height:100vh;display:flex;flex-direction:column;align-items:center;justify-content:center;padding:40px 20px}
@keyframes __hamr-wait-pulse{0%,100%{opacity:1}50%{opacity:0.3}}
@keyframes __hamr-wait-spin{0%{transform:rotate(0deg)}100%{transform:rotate(360deg)}}
#__hamr-wait-root .__hamr-wait-logo{width:80px;height:80px;margin-bottom:32px;animation:__hamr-wait-pulse 2s ease-in-out infinite}
#__hamr-wait-root h1{font-size:22px;font-weight:700;color:#E8E8E8;margin-bottom:12px}
#__hamr-wait-root .__hamr-wait-sub{font-size:14px;color:#6B7280;margin-bottom:32px;text-align:center}
#__hamr-wait-root .__hamr-wait-spinner{width:24px;height:24px;border:3px solid #2E2E34;border-top-color:#FFB347;
border-radius:50%;animation:__hamr-wait-spin 0.8s linear infinite;margin-bottom:16px}
#__hamr-wait-root .__hamr-wait-status{font-size:13px;color:#4B5563;transition:color 0.3s}
#__hamr-wait-root .__hamr-wait-status.__hamr-wait-checking{color:#FFB347}
</style>
<div id="__hamr-wait-root">
<img src="/__hamr/logo.png" class="__hamr-wait-logo" alt="hamr">
<h1>Waiting for Server</h1>
<p class="__hamr-wait-sub">The backend at <code>{{.Target}}</code> is not responding yet.</p>
<div class="__hamr-wait-spinner"></div>
<p class="__hamr-wait-status" id="__hamr-wait-status">Retrying...</p>
</div>
<script>window.__hamr_waiting_page = true;</script>
<script>
{{.ReloadJS}}
</script>
<script>
(function(){
  var status = document.getElementById("__hamr-wait-status");
  var delay = 1000;
  function check() {
    status.className = "__hamr-wait-status __hamr-wait-checking";
    status.textContent = "Checking...";
    fetch(location.href, {cache:"no-store"}).then(function(r) {
      if (r.headers.get("X-Hamr-Waiting")) {
        retry();
      } else {
        location.reload();
      }
    }).catch(function(){ retry(); });
  }
  function retry() {
    status.className = "__hamr-wait-status";
    status.textContent = "Retrying...";
    setTimeout(check, delay);
    delay = Math.min(delay * 1.5, 5000);
  }
  setTimeout(check, delay);
})();
</script>
</body>
</html>
`))

var errorPageTmpl = template.Must(template.New("errorpage").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>hamr — Build Error</title>
</head>
<body>
<style>
#__hamr-err-root,#__hamr-err-root *{box-sizing:border-box;margin:0;padding:0}
#__hamr-err-root{background:#111113;color:#D4D4D4;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;
width:100%;min-height:100vh;display:flex;flex-direction:column;align-items:center;padding:40px 20px}
#__hamr-err-root .__hamr-err-header{display:flex;align-items:center;gap:16px;margin-bottom:32px}
#__hamr-err-root .__hamr-err-header img{width:48px;height:48px}
#__hamr-err-root .__hamr-err-header h1{font-size:24px;font-weight:700;color:#EF4444}
#__hamr-err-root .__hamr-err-card{background:#1A1A1E;border:1px solid #2E2E34;border-radius:10px;width:100%;max-width:800px;
margin-bottom:20px;overflow:hidden}
#__hamr-err-root .__hamr-err-card-header{padding:14px 18px;background:#1F1F24;border-bottom:1px solid #2E2E34;
font-weight:600;font-size:15px;color:#E8E8E8}
#__hamr-err-root .__hamr-err-card-body{padding:16px 18px}
#__hamr-err-root .__hamr-err-card-body pre{background:#0D0D0F;border:1px solid #2E2E34;border-radius:6px;padding:14px;
font-family:'SF Mono',Monaco,Consolas,monospace;font-size:12px;line-height:1.5;
color:#F87171;overflow-x:auto;white-space:pre-wrap;word-break:break-all;max-height:400px;overflow-y:auto}
#__hamr-err-root .__hamr-err-note{margin-top:24px;font-size:13px;color:#6B7280;text-align:center}
</style>
<div id="__hamr-err-root">
<div class="__hamr-err-header">
<img src="/__hamr/logo.png" alt="hamr">
<h1>Build Error</h1>
</div>
<div id="__hamr-errors">
{{range .Entries}}
<div class="__hamr-err-card">
<div class="__hamr-err-card-header">{{.Rule}}</div>
<div class="__hamr-err-card-body"><pre>{{.Output}}</pre></div>
</div>
{{end}}
</div>
<p class="__hamr-err-note">This page will refresh automatically when the error is fixed.</p>
</div>
<script>window.__hamr_error_page = true;</script>
<script>
{{.ReloadJS}}
</script>
</body>
</html>
`))
