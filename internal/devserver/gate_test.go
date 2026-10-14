package devserver

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// gateEcho records what next saw.
func gateEcho(seen *http.Request) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = *r
		_, _ = w.Write([]byte("app"))
	})
}

func gateDo(h http.Handler, method, target string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestNewGate_NoPasswordPassthrough(t *testing.T) {
	next := http.NotFoundHandler()
	h := NewGate("", nil, false, next)
	if rec := gateDo(h, "GET", "/x", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d", rec.Code)
	}
}

func TestNewGate_Auth(t *testing.T) {
	const pw = "s3cret"
	good := gateToken(pw, time.Now())
	tests := []struct {
		name string
		hdr  map[string]string
		want int
	}{
		{"none", nil, 401},
		{"good cookie", map[string]string{"Cookie": gateCookie + "=" + good}, 200},
		{"bad cookie", map[string]string{"Cookie": gateCookie + "=nope"}, 401},
		{"good header", map[string]string{gateHeader: pw}, 200},
		{"bad header", map[string]string{gateHeader: "wrong"}, 401},
		{"cookie among others", map[string]string{"Cookie": "a=1; " + gateCookie + "=" + good + "; b=2"}, 200},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var seen http.Request
			h := NewGate(pw, nil, false, gateEcho(&seen))
			if rec := gateDo(h, "GET", "/x", tt.hdr); rec.Code != tt.want {
				t.Fatalf("code = %d, want %d", rec.Code, tt.want)
			}
		})
	}
}

func TestNewGate_Bypass(t *testing.T) {
	h := NewGate("pw", []string{" /payments ", "", "/webhooks/stripe/", "payments/hook"}, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	tests := []struct {
		path string
		want int
	}{
		{"/payments", 200},
		{"/payments/x", 200},
		{"/payments-admin", 401},
		{"/paymentsx", 401},
		{"/webhooks/stripe", 200},
		{"/webhooks/stripe/evt", 200},
		{"/webhooks", 401},
		{"/payments/hook/evt", 200},
		{"/payments/../admin", 401},
		{"/", 401},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if rec := gateDo(h, "POST", tt.path, nil); rec.Code != tt.want {
				t.Fatalf("code = %d, want %d", rec.Code, tt.want)
			}
		})
	}

	// /__hamr/* is never bypassed, even by an entry that would cover it.
	wide := NewGate("pw", []string{"/", "/__hamr"}, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	for _, tt := range []struct {
		path string
		want int
	}{
		{"/anything", 200},
		{"/__hamr/mail", 401},
		{"/__hamr/app/actions/reseed", 401},
		{"/x/../__hamr/mail", 401},
	} {
		t.Run(tt.path, func(t *testing.T) {
			if rec := gateDo(wide, "POST", tt.path, nil); rec.Code != tt.want {
				t.Fatalf("code = %d, want %d", rec.Code, tt.want)
			}
		})
	}
}

func TestGateTokenValid(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	tok := gateToken("pw", now)
	ts, mac, _ := strings.Cut(tok, ".")
	for _, tc := range []struct {
		name  string
		token string
		pw    string
		at    time.Time
		want  bool
	}{
		{"fresh", tok, "pw", now, true},
		{"just inside max age", tok, "pw", now.Add(gateMaxAge), true},
		{"expired", tok, "pw", now.Add(gateMaxAge + time.Second), false},
		{"issued in future", tok, "pw", now.Add(-time.Minute), false},
		{"other password", tok, "other", now, false},
		{"bare mac", mac, "pw", now, false},
		{"forged time", "1900000000." + mac, "pw", now, false},
		{"garbage", "x.y", "pw", now, false},
		{"round trip", ts + "." + mac, "pw", now, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := gateTokenValid(tc.token, tc.pw, tc.at); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNewGate_Strips(t *testing.T) {
	var seen http.Request
	h := NewGate("pw", []string{"/open"}, false, gateEcho(&seen))
	good := gateToken("pw", time.Now())

	gateDo(h, "GET", "/x", map[string]string{
		gateHeader:      "pw",
		"Cookie":        "a=1; " + gateCookie + "=" + good + "; b=2",
		"Authorization": "Bearer t",
	})
	if seen.Header.Get(gateHeader) != "" {
		t.Error("gate header forwarded")
	}
	if got := seen.Header.Get("Cookie"); got != "a=1; b=2" {
		t.Errorf("cookie = %q", got)
	}
	if seen.Header.Get("Authorization") != "Bearer t" {
		t.Error("Authorization touched")
	}

	// Bypassed requests are stripped too; a lone gate cookie drops the header.
	seen = http.Request{}
	gateDo(h, "GET", "/open", map[string]string{gateHeader: "x", "Cookie": gateCookie + "=y"})
	if seen.Header == nil || seen.Header.Get(gateHeader) != "" || seen.Header.Get("Cookie") != "" {
		t.Errorf("bypass not stripped: %v", seen.Header)
	}
}

func TestNewGate_Unauthorised(t *testing.T) {
	h := NewGate("pw", nil, false, http.NotFoundHandler())

	rec := gateDo(h, "GET", "/a/b?x=1&y=2", map[string]string{"Accept": "text/html,*/*"})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("html code = %d", rec.Code)
	}
	if want := gatePath + "?next=" + url.QueryEscape("/a/b?x=1&y=2"); rec.Header().Get("Location") != want {
		t.Errorf("location = %q, want %q", rec.Header().Get("Location"), want)
	}

	for _, tc := range []struct{ method, accept string }{
		{"GET", "application/json"}, {"GET", ""}, {"POST", "text/html"},
	} {
		rec = gateDo(h, tc.method, "/api", map[string]string{"Accept": tc.accept})
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "hamr gate: password required") {
			t.Errorf("%s %q: code=%d body=%q", tc.method, tc.accept, rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type = %q", ct)
		}
	}
}

func TestGateNext(t *testing.T) {
	tests := []struct{ in, want string }{
		{"/dash?x=1", "/dash?x=1"},
		{"/", "/"},
		{"", "/"},
		{"//evil.com", "/"},
		{"https://evil.com", "/"},
		{`/\evil`, "/"},
		{"/\t/evil.com", "/"},
		{"/a\\b", "/"},
		{"/\n/evil.com", "/"},
		{"evil.com", "/"},
	}
	for _, tt := range tests {
		if got := gateNext(tt.in); got != tt.want {
			t.Errorf("gateNext(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestGate_Page(t *testing.T) {
	h := NewGate("pw", nil, false, http.NotFoundHandler())
	rec := gateDo(h, "GET", gatePath+"?next="+url.QueryEscape(`/a"><script>`), nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `name="password"`) {
		t.Fatalf("code=%d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "<script>") {
		t.Error("next not escaped")
	}
}

func TestGate_Post(t *testing.T) {
	post := func(secure bool, pw, next string) *httptest.ResponseRecorder {
		h := NewGate("pw", nil, secure, http.NotFoundHandler())
		req := httptest.NewRequest("POST", gatePath, strings.NewReader(url.Values{"password": {pw}, "next": {next}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	for _, secure := range []bool{false, true} {
		rec := post(secure, "pw", "/dash")
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/dash" {
			t.Fatalf("code=%d loc=%q", rec.Code, rec.Header().Get("Location"))
		}
		cs := rec.Result().Cookies()
		if len(cs) != 1 {
			t.Fatalf("cookies = %v", cs)
		}
		c := cs[0]
		if c.Name != gateCookie || !gateTokenValid(c.Value, "pw", time.Now()) || c.Path != "/" || !c.HttpOnly ||
			c.SameSite != http.SameSiteLaxMode || c.Secure != secure || c.MaxAge != 7*24*3600 {
			t.Errorf("cookie = %+v", c)
		}
	}

	if rec := post(false, "pw", "//evil.com"); rec.Header().Get("Location") != "/" {
		t.Errorf("open redirect: %q", rec.Header().Get("Location"))
	}

	rec := post(false, "wrong", "/dash")
	if rec.Code != http.StatusUnauthorized || len(rec.Result().Cookies()) != 0 || !strings.Contains(rec.Body.String(), "Wrong password") {
		t.Errorf("bad pw: code=%d", rec.Code)
	}
}

func TestGate_MethodNotAllowed(t *testing.T) {
	h := NewGate("pw", nil, false, http.NotFoundHandler())
	if rec := gateDo(h, "PUT", gatePath, nil); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("code = %d", rec.Code)
	}
}

func TestNewGate_WrongHeaderDelayed(t *testing.T) {
	old := gateFailDelay
	gateFailDelay = 50 * time.Millisecond
	t.Cleanup(func() { gateFailDelay = old })
	h := NewGate("pw", nil, false, http.NotFoundHandler())

	start := time.Now()
	if rec := gateDo(h, "GET", "/x", map[string]string{gateHeader: "wrong"}); rec.Code != 401 {
		t.Fatalf("code = %d", rec.Code)
	}
	if time.Since(start) < gateFailDelay {
		t.Fatal("wrong header not delayed")
	}

	start = time.Now()
	gateDo(h, "GET", "/x", nil)
	gateDo(h, "GET", "/x", map[string]string{gateHeader: "pw"})
	if time.Since(start) >= gateFailDelay {
		t.Fatal("headerless/correct requests must not be delayed")
	}
}
