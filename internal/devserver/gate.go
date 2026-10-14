package devserver

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"html/template"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

const (
	gatePath       = "/__hamr/gate"
	gateCookie     = "__hamr_gate"
	gateHeader     = "X-Hamr-Gate"
	gateMaxAge     = 7 * 24 * time.Hour
	gateMaxBodyLen = 4 << 10
)

// gateFailDelay slows password guessing; a var so tests can shrink it.
var gateFailDelay = 500 * time.Millisecond

var gatePage = template.Must(template.New("gate").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>hamr</title>
<style>
*{box-sizing:border-box}
body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;background:#111;color:#eee;font:16px/1.4 system-ui,sans-serif}
form{width:100%;max-width:340px;margin:16px;padding:24px;background:#1b1b1b;border:1px solid #333;border-radius:10px}
h1{margin:0 0 16px;font-size:20px;color:#f97316}
input{width:100%;padding:10px 12px;margin-bottom:12px;background:#111;color:#eee;border:1px solid #444;border-radius:6px;font:inherit}
input:focus{outline:2px solid #f97316;border-color:#f97316}
button{width:100%;padding:10px;background:#f97316;color:#111;border:0;border-radius:6px;font:inherit;font-weight:600;cursor:pointer}
.err{margin:0 0 12px;color:#f87171;font-size:14px}
</style></head><body>
<form method="post" action="/__hamr/gate">
<h1>hamr</h1>
{{if .Error}}<p class="err">Wrong password.</p>{{end}}
<input type="password" name="password" placeholder="Password" autofocus required autocomplete="current-password">
<input type="hidden" name="next" value="{{.Next}}">
<button type="submit">Continue</button>
</form></body></html>`))

// NewGate password-gates next. See
// docs/proposed-features/staging-proxy-and-dev-actions.md (decision 4).
// An empty password disables the gate. Requests whose path matches a bypass
// entry (segment boundary) skip the check. The gate header and cookie are
// never forwarded to next.
func NewGate(password string, bypass []string, secure bool, next http.Handler) http.Handler {
	if password == "" {
		return next
	}
	var bypassPaths []string
	for _, p := range bypass {
		if p = strings.TrimSpace(p); p != "" {
			// Same normalisation buildFrontDoor validates: "a/b" means "/a/b".
			bypassPaths = append(bypassPaths, strings.TrimSuffix(path.Clean("/"+p), "/"))
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == gatePath {
			serveGate(w, r, password, secure)
			return
		}

		ok := gateBypassed(bypassPaths, r.URL.Path) || gateAuthorised(r, password, time.Now())
		stripGate(r)
		if ok {
			next.ServeHTTP(w, r)
			return
		}

		if (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
			strings.Contains(r.Header.Get("Accept"), "text/html") {
			http.Redirect(w, r, gatePath+"?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"hamr gate: password required"}` + "\n"))
	})
}

// gateToken is "<unix issue time>.<hex HMAC-SHA256(password, "hamr-gate|"+time)>".
// Carrying the issue time lets the server enforce gateMaxAge itself; a copied
// cookie stops working after it, not just when the browser drops it.
func gateToken(password string, issued time.Time) string {
	ts := strconv.FormatInt(issued.Unix(), 10)
	return ts + "." + gateMAC(password, ts)
}

func gateMAC(password, ts string) string {
	m := hmac.New(sha256.New, []byte(password))
	m.Write([]byte("hamr-gate|" + ts))
	return hex.EncodeToString(m.Sum(nil))
}

// gateTokenValid checks the MAC and that the token is younger than gateMaxAge.
func gateTokenValid(token, password string, now time.Time) bool {
	ts, mac, ok := strings.Cut(token, ".")
	if !ok {
		return false
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false
	}
	if age := now.Sub(time.Unix(sec, 0)); age < 0 || age > gateMaxAge {
		return false
	}
	return secureEqual(mac, gateMAC(password, ts))
}

func serveGate(w http.ResponseWriter, r *http.Request, password string, secure bool) {
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		renderGate(w, http.StatusOK, gateNext(r.URL.Query().Get("next")), false)
	case http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, gateMaxBodyLen)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		next := gateNext(r.PostForm.Get("next"))
		if !secureEqual(r.PostForm.Get("password"), password) {
			time.Sleep(gateFailDelay)
			renderGate(w, http.StatusUnauthorized, next, true)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     gateCookie,
			Value:    gateToken(password, time.Now()),
			Path:     "/",
			MaxAge:   int(gateMaxAge.Seconds()),
			HttpOnly: true,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
		})
		http.Redirect(w, r, next, http.StatusSeeOther)
	default:
		w.Header().Set("Allow", "GET, HEAD, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func renderGate(w http.ResponseWriter, status int, next string, failed bool) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = gatePage.Execute(w, struct {
		Next  string
		Error bool
	}{next, failed})
}

// gateNext accepts only same-origin relative paths; anything else is "/".
// Backslashes and control characters are refused outright: browsers treat
// "\\" as "/" and strip tabs/newlines, so "/\t/evil.com" would become
// "//evil.com".
func gateNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") ||
		strings.ContainsFunc(next, func(r rune) bool { return r == '\\' || r < 0x20 || r == 0x7f }) {
		return "/"
	}
	return next
}

func secureEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func gateAuthorised(r *http.Request, password string, now time.Time) bool {
	h := r.Header.Get(gateHeader)
	if h != "" && secureEqual(h, password) {
		return true
	}
	for _, c := range r.Cookies() {
		if c.Name == gateCookie && gateTokenValid(c.Value, password, now) {
			return true
		}
	}
	if h != "" {
		time.Sleep(gateFailDelay) // wrong header: same brute-force brake as the form
	}
	return false
}

// gateBypassed matches on segment boundaries against the cleaned path, so
// "/payments/../admin" cannot ride a "/payments" bypass. The hamr panel
// (/__hamr/*) is never bypassed: it holds captured mail and the actions.
func gateBypassed(bypass []string, p string) bool {
	p = path.Clean("/" + p)
	if p == "/__hamr" || strings.HasPrefix(p, "/__hamr/") {
		return false
	}
	for _, b := range bypass {
		if p == b || strings.HasPrefix(p, b+"/") {
			return true
		}
	}
	return false
}

// stripGate removes the gate header and only the gate cookie; other cookies
// are kept as sent.
func stripGate(r *http.Request) {
	r.Header.Del(gateHeader)
	lines := r.Header.Values("Cookie")
	if len(lines) == 0 {
		return
	}
	var kept []string
	for _, line := range lines {
		for part := range strings.SplitSeq(line, ";") {
			name, _, _ := strings.Cut(strings.TrimSpace(part), "=")
			if name != gateCookie && strings.TrimSpace(part) != "" {
				kept = append(kept, strings.TrimSpace(part))
			}
		}
	}
	if len(kept) == 0 {
		r.Header.Del("Cookie")
		return
	}
	r.Header.Set("Cookie", strings.Join(kept, "; "))
}
