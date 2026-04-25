package devserver

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestMailMock(t *testing.T) (*MailMock, *http.ServeMux) {
	t.Helper()
	mm := NewMailMock(MailMockOptions{MaxMessages: 3, MaxMessageBytes: 1024})
	mux := http.NewServeMux()
	mm.RegisterRoutes(mux)
	return mm, mux
}

func postJSON(t *testing.T, mux http.Handler, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestIngest_HappyPath(t *testing.T) {
	t.Parallel()
	mm, mux := newTestMailMock(t)

	msg := map[string]any{
		"From":    map[string]string{"Email": "app@example.com"},
		"To":      []map[string]string{{"Email": "ada@example.com"}},
		"Subject": "hello",
		"Text":    "hi",
	}
	rec := postJSON(t, mux, "/__hamr/mail/ingest", msg)
	require.Equal(t, http.StatusOK, rec.Code)

	var out struct{ ID string }
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.True(t, strings.HasPrefix(out.ID, "msg_"))

	stored := mm.Get(out.ID)
	require.NotNil(t, stored)
	assert.Equal(t, "hello", stored.Subject)
	assert.Equal(t, "delivered", stored.Status)
	assert.False(t, stored.ReceivedAt.IsZero())
}

func TestIngest_MagicBounce(t *testing.T) {
	t.Parallel()
	mm, mux := newTestMailMock(t)

	rec := postJSON(t, mux, "/__hamr/mail/ingest", map[string]any{
		"To": []map[string]string{{"Email": "bounce@example.com"}},
	})
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)

	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	assert.Equal(t, "bounced", body["error"])
	assert.Empty(t, mm.List(), "nothing should be stored on bounce")
}

func TestIngest_MagicReject(t *testing.T) {
	t.Parallel()
	mm, mux := newTestMailMock(t)

	rec := postJSON(t, mux, "/__hamr/mail/ingest", map[string]any{
		"To": []map[string]string{{"Email": "ok@example.com"}},
		"Cc": []map[string]string{{"Email": "reject@example.com"}},
	})
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	assert.Equal(t, "rejected", body["error"])
	assert.Empty(t, mm.List())
}

func TestIngest_MagicInBcc(t *testing.T) {
	t.Parallel()
	mm, mux := newTestMailMock(t)

	rec := postJSON(t, mux, "/__hamr/mail/ingest", map[string]any{
		"To":  []map[string]string{{"Email": "ok@example.com"}},
		"Bcc": []map[string]string{{"Email": "Bounce@example.com"}},
	})
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	assert.Equal(t, "bounced", body["error"])
	assert.Empty(t, mm.List())
}

func TestIngest_SizeLimit(t *testing.T) {
	t.Parallel()
	_, mux := newTestMailMock(t)

	// 2 KiB body into a 1 KiB cap.
	big := strings.Repeat("A", 2048)
	rec := postJSON(t, mux, "/__hamr/mail/ingest", map[string]any{"Subject": big})
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
}

func TestIngest_MethodNotAllowed(t *testing.T) {
	t.Parallel()
	_, mux := newTestMailMock(t)

	req := httptest.NewRequest(http.MethodGet, "/__hamr/mail/ingest", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestIngest_InvalidJSON(t *testing.T) {
	t.Parallel()
	_, mux := newTestMailMock(t)

	req := httptest.NewRequest(http.MethodPost, "/__hamr/mail/ingest", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRingBuffer_EvictsOldest(t *testing.T) {
	t.Parallel()
	mm, mux := newTestMailMock(t) // cap=3

	for i, subj := range []string{"a", "b", "c", "d"} {
		rec := postJSON(t, mux, "/__hamr/mail/ingest", map[string]any{"Subject": subj})
		require.Equal(t, http.StatusOK, rec.Code, "msg %d", i)
	}
	list := mm.List() // newest first
	require.Len(t, list, 3)
	assert.Equal(t, "d", list[0].Subject)
	assert.Equal(t, "c", list[1].Subject)
	assert.Equal(t, "b", list[2].Subject)
	// "a" was evicted.
}

func TestInbox_RendersWithSearch(t *testing.T) {
	t.Parallel()
	_, mux := newTestMailMock(t)
	postJSON(t, mux, "/__hamr/mail/ingest", map[string]any{"Subject": "welcome to hamr"})
	postJSON(t, mux, "/__hamr/mail/ingest", map[string]any{"Subject": "password reset", "To": []map[string]string{{"Email": "u@example.com"}}})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/__hamr/mail", nil))
	body := rec.Body.String()
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, body, "welcome to hamr")
	assert.Contains(t, body, "password reset")

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/__hamr/mail?q=password", nil))
	body = rec.Body.String()
	assert.Contains(t, body, "password reset")
	assert.NotContains(t, body, "welcome to hamr")
}

func TestDetail_DeleteClearFailDelay(t *testing.T) {
	t.Parallel()
	mm, mux := newTestMailMock(t)

	// Ingest one.
	rec := postJSON(t, mux, "/__hamr/mail/ingest", map[string]any{"Subject": "x"})
	var out struct{ ID string }
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	id := out.ID
	require.NotEmpty(t, id)

	// Detail page 200s.
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/__hamr/mail/"+id, nil))
	assert.Equal(t, http.StatusOK, rec.Code)

	// Fail endpoint sets status.
	req := httptest.NewRequest(http.MethodPost, "/__hamr/mail/"+id+"/fail", strings.NewReader("note=simulated"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	m := mm.Get(id)
	require.NotNil(t, m)
	assert.Equal(t, "failed", m.Status)
	assert.Contains(t, m.StatusNote, "simulated")

	// Delay endpoint updates status.
	req = httptest.NewRequest(http.MethodPost, "/__hamr/mail/"+id+"/delay", strings.NewReader("seconds=60"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	m = mm.Get(id)
	assert.Equal(t, "delayed", m.Status)

	// Delete works.
	req = httptest.NewRequest(http.MethodPost, "/__hamr/mail/"+id+"/delete", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Nil(t, mm.Get(id))

	// Clear endpoint empties inbox.
	postJSON(t, mux, "/__hamr/mail/ingest", map[string]any{"Subject": "y"})
	require.Len(t, mm.List(), 1)
	req = httptest.NewRequest(http.MethodPost, "/__hamr/mail/clear", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Empty(t, mm.List())
}

func TestHTMLFrame_SetsSandboxHeaders(t *testing.T) {
	t.Parallel()
	_, mux := newTestMailMock(t)

	rec := postJSON(t, mux, "/__hamr/mail/ingest", map[string]any{
		"Subject": "security",
		"HTML":    `<p>hi <img src="cid:logo"></p><a href="https://example.com" target="_self">link</a><script>alert(1)</script>`,
	})
	var out struct{ ID string }
	_ = json.Unmarshal(rec.Body.Bytes(), &out)

	req := httptest.NewRequest(http.MethodGet, "/__hamr/mail/"+out.ID+"/html", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	csp := rec.Header().Get("Content-Security-Policy")
	assert.Contains(t, csp, "default-src 'none'")
	assert.Contains(t, csp, "img-src 'self' data:")
	assert.NotContains(t, csp, "script-src")
	assert.Equal(t, "no-referrer", rec.Header().Get("Referrer-Policy"))
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))

	body := rec.Body.String()
	// cid: rewritten.
	assert.Contains(t, body, "/__hamr/mail/"+out.ID+"/inline/logo")
	assert.NotContains(t, body, `cid:logo`)
	// <base target="_blank"> makes link clicks open new tabs instead of
	// navigating inside the iframe — works in concert with the parent's
	// sandbox="allow-popups allow-popups-to-escape-sandbox".
	assert.Contains(t, body, `<base target="_blank">`)
	// Belt-and-suspenders: every <a> in the email body gets its target
	// rewritten to _blank, so an email with target="_self" cannot navigate
	// the iframe even if some browser misinterprets <base>.
	assert.Contains(t, body, `<a target="_blank" href="https://example.com">link</a>`)
	assert.NotContains(t, body, `target="_self"`)
	// Script tag is present in body but blocked by CSP — we don't strip it ourselves.
	// The parent iframe sandbox (no allow-scripts) + CSP default-src 'none' are the defense.
}

func TestHTMLFrame_ImagesBlocked(t *testing.T) {
	t.Parallel()
	_, mux := newTestMailMock(t)

	rec := postJSON(t, mux, "/__hamr/mail/ingest", map[string]any{
		"HTML": `<img src="https://tracker.example.com/pixel.gif">`,
	})
	var out struct{ ID string }
	_ = json.Unmarshal(rec.Body.Bytes(), &out)

	req := httptest.NewRequest(http.MethodGet, "/__hamr/mail/"+out.ID+"/html?images=blocked", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	body := rec.Body.String()
	assert.NotContains(t, body, "tracker.example.com")
	assert.Contains(t, body, "data:image/svg+xml")
}

func TestAttachmentAndInlineEndpoints(t *testing.T) {
	t.Parallel()
	_, mux := newTestMailMock(t)

	// Small PNG-ish payload.
	data := []byte("binary-bytes-here")
	rec := postJSON(t, mux, "/__hamr/mail/ingest", map[string]any{
		"Attachments": []map[string]any{{"Filename": "report.pdf", "ContentType": "application/pdf", "Data": data}},
		"Inline":      []map[string]any{{"ContentID": "logo", "Filename": "logo.png", "ContentType": "image/png", "Data": data}},
	})
	var out struct{ ID string }
	_ = json.Unmarshal(rec.Body.Bytes(), &out)

	// Attachment download.
	req := httptest.NewRequest(http.MethodGet, "/__hamr/mail/"+out.ID+"/attachment/0", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/pdf", rec.Header().Get("Content-Type"))
	assert.Contains(t, rec.Header().Get("Content-Disposition"), `filename="report.pdf"`)
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	got, _ := io.ReadAll(rec.Body)
	assert.Equal(t, data, got)

	// Inline lookup by cid.
	req = httptest.NewRequest(http.MethodGet, "/__hamr/mail/"+out.ID+"/inline/logo", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "image/png", rec.Header().Get("Content-Type"))

	// Missing attachment index 404s.
	req = httptest.NewRequest(http.MethodGet, "/__hamr/mail/"+out.ID+"/attachment/99", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDetail_UnknownIDReturns404(t *testing.T) {
	t.Parallel()
	_, mux := newTestMailMock(t)

	req := httptest.NewRequest(http.MethodGet, "/__hamr/mail/msg_notreal", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestLocalPart(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "bounce", localPart("Bounce@example.com"))
	assert.Equal(t, "reject", localPart("reject@example.com"))
	assert.Equal(t, "foo", localPart("foo@bar.baz"))
	assert.Equal(t, "plain", localPart("plain"))
}

// TestConcurrentSetStatusAndRead exercises the deep-copy contract on Get/List:
// concurrent SetStatus + Get/List must not produce a data race. Run under
// `go test -race` to catch any regression.
func TestConcurrentSetStatusAndRead(t *testing.T) {
	t.Parallel()
	mm := NewMailMock(MailMockOptions{MaxMessages: 10, MaxMessageBytes: 1024})
	mux := http.NewServeMux()
	mm.RegisterRoutes(mux)

	// Seed one message.
	rec := postJSON(t, mux, "/__hamr/mail/ingest", map[string]any{"Subject": "race"})
	require.Equal(t, http.StatusOK, rec.Code)
	var out struct{ ID string }
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	id := out.ID

	const iterations = 500
	var wg sync.WaitGroup
	wg.Add(3)

	// Writer: flips status between failed/delayed.
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			status := "failed"
			if i%2 == 0 {
				status = "delayed"
			}
			mm.SetStatus(id, status, "iter")
		}
	}()
	// Reader via Get.
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			if msg := mm.Get(id); msg != nil {
				_ = msg.Status
				_ = msg.StatusNote
			}
		}
	}()
	// Reader via List.
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			for _, msg := range mm.List() {
				_ = msg.Status
			}
		}
	}()

	wg.Wait()
}

func TestGet_ReturnsDeepCopy(t *testing.T) {
	t.Parallel()
	mm, mux := newTestMailMock(t)

	rec := postJSON(t, mux, "/__hamr/mail/ingest", map[string]any{
		"Subject": "original",
		"Headers": map[string]string{"X-Original": "yes"},
	})
	var out struct{ ID string }
	_ = json.Unmarshal(rec.Body.Bytes(), &out)

	snapshot := mm.Get(out.ID)
	require.NotNil(t, snapshot)

	// Mutate the returned snapshot; the stored message must be unaffected.
	snapshot.Subject = "MUTATED"
	snapshot.Headers["X-Original"] = "no"
	snapshot.Headers["X-New"] = "injected"

	fresh := mm.Get(out.ID)
	require.NotNil(t, fresh)
	assert.Equal(t, "original", fresh.Subject)
	assert.Equal(t, "yes", fresh.Headers["X-Original"])
	_, hasNew := fresh.Headers["X-New"]
	assert.False(t, hasNew, "mutation of snapshot must not leak into stored map")
}

// TestPreviewTheme_ToggleScopedToEmailView asserts the preview-theme toggle
// appears only on the detail page's HTML tab, and the inbox + non-HTML tabs
// keep the chrome always-dark with no toggle.
func TestPreviewTheme_ToggleScopedToEmailView(t *testing.T) {
	t.Parallel()
	_, mux := newTestMailMock(t)

	rec := postJSON(t, mux, "/__hamr/mail/ingest", map[string]any{
		"Subject": "theme probe",
		"HTML":    "<p>body</p>",
	})
	var out struct{ ID string }
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	id := out.ID

	// Inbox must NOT carry the preview toggle.
	req := httptest.NewRequest(http.MethodGet, "/__hamr/mail", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	inbox := rec.Body.String()
	assert.NotContains(t, inbox, "hamr-preview-theme", "inbox must not expose the preview toggle")

	// Detail HTML tab carries the toggle, the base iframe-wrap classes, and
	// the storage key.
	req = httptest.NewRequest(http.MethodGet, "/__hamr/mail/"+id+"?tab=html", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	htmlTab := rec.Body.String()
	assert.Contains(t, htmlTab, `id="hamr-preview-theme"`)
	assert.Contains(t, htmlTab, `id="hamr-iframe-wrap"`)
	assert.Contains(t, htmlTab, "preview-light")
	assert.Contains(t, htmlTab, "__hamr_mail_preview_theme")

	// Detail non-HTML tabs don't render the toggle.
	req = httptest.NewRequest(http.MethodGet, "/__hamr/mail/"+id+"?tab=text", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.NotContains(t, rec.Body.String(), "hamr-preview-theme")
}

// TestHTMLFrame_HonorsThemeParam verifies the iframe body switches its
// color-scheme meta based on ?theme= — the mechanism that changes the
// email-view-only preview.
func TestHTMLFrame_HonorsThemeParam(t *testing.T) {
	t.Parallel()
	_, mux := newTestMailMock(t)

	rec := postJSON(t, mux, "/__hamr/mail/ingest", map[string]any{"HTML": "<p>x</p>"})
	var out struct{ ID string }
	_ = json.Unmarshal(rec.Body.Bytes(), &out)

	// Default (no theme param) → light.
	req := httptest.NewRequest(http.MethodGet, "/__hamr/mail/"+out.ID+"/html", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	body := rec.Body.String()
	assert.Contains(t, body, `color-scheme:light`)
	assert.Contains(t, body, `content="light"`)

	// ?theme=dark → dark.
	req = httptest.NewRequest(http.MethodGet, "/__hamr/mail/"+out.ID+"/html?theme=dark", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	body = rec.Body.String()
	assert.Contains(t, body, `color-scheme:dark`)
	assert.Contains(t, body, `content="dark"`)

	// Junk value falls back to light.
	req = httptest.NewRequest(http.MethodGet, "/__hamr/mail/"+out.ID+"/html?theme=garbage", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Contains(t, rec.Body.String(), `color-scheme:light`)
}

func TestDetail_DetailPageCarriesIframeSandbox(t *testing.T) {
	t.Parallel()
	_, mux := newTestMailMock(t)

	rec := postJSON(t, mux, "/__hamr/mail/ingest", map[string]any{
		"Subject": "x",
		"HTML":    "<p>hi</p>",
	})
	var out struct{ ID string }
	_ = json.Unmarshal(rec.Body.Bytes(), &out)

	req := httptest.NewRequest(http.MethodGet, "/__hamr/mail/"+out.ID, nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	// The detail page must emit a restrictive sandbox on the HTML-preview
	// iframe. Only allow-popups + allow-popups-to-escape-sandbox are granted
	// (so <a target="_blank"> clicks open as normal, un-sandboxed tabs).
	// allow-scripts / allow-same-origin / allow-forms / allow-top-navigation
	// must NEVER appear here — that's the load-bearing defense against
	// captured email HTML, and removing this assertion or widening the
	// sandbox without a security review is forbidden.
	assert.Contains(t, body, `sandbox="allow-popups allow-popups-to-escape-sandbox"`)
	assert.NotContains(t, body, "allow-scripts")
	assert.NotContains(t, body, "allow-same-origin")
	assert.NotContains(t, body, "allow-forms")
	assert.NotContains(t, body, "allow-top-navigation")
	assert.Contains(t, body, `referrerpolicy="no-referrer"`)
}

func TestDetailRoutes_RejectWrongMethod(t *testing.T) {
	t.Parallel()
	_, mux := newTestMailMock(t)

	rec := postJSON(t, mux, "/__hamr/mail/ingest", map[string]any{"Subject": "x"})
	var out struct{ ID string }
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	id := out.ID

	cases := []struct {
		method, path string
	}{
		{http.MethodGet, "/__hamr/mail/" + id + "/delete"},
		{http.MethodGet, "/__hamr/mail/" + id + "/fail"},
		{http.MethodGet, "/__hamr/mail/" + id + "/delay"},
		{http.MethodGet, "/__hamr/mail/clear"},
		{http.MethodPost, "/__hamr/mail/" + id + "/html"},
		{http.MethodPost, "/__hamr/mail/" + id + "/attachment/0"},
		{http.MethodPost, "/__hamr/mail/" + id + "/inline/logo"},
		{http.MethodPut, "/__hamr/mail"},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, c.path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusMethodNotAllowed, rec.Code,
			"%s %s", c.method, c.path)
	}
}

// TestCSRF_OriginMismatchIsRefused verifies that state-changing POSTs with an
// Origin header pointing at a different host are refused with 403. Requests
// without an Origin header (curl, tests) are unaffected — see checkSameOrigin
// for why absent Origin is allowed.
func TestCSRF_OriginMismatchIsRefused(t *testing.T) {
	t.Parallel()
	mm, mux := newTestMailMock(t)

	rec := postJSON(t, mux, "/__hamr/mail/ingest", map[string]any{"Subject": "x"})
	var out struct{ ID string }
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	id := out.ID

	paths := []string{
		"/__hamr/mail/clear",
		"/__hamr/mail/" + id + "/delete",
		"/__hamr/mail/" + id + "/fail",
		"/__hamr/mail/" + id + "/delay",
	}
	for _, p := range paths {
		req := httptest.NewRequest(http.MethodPost, p, nil)
		req.Header.Set("Origin", "http://evil.example.com")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusForbidden, rec.Code, p)
	}

	// Message should still be present (nothing mutated).
	assert.NotNil(t, mm.Get(id))
	assert.Equal(t, "delivered", mm.Get(id).Status)
}

func TestCSRF_OriginMatchIsAllowed(t *testing.T) {
	t.Parallel()
	mm, mux := newTestMailMock(t)

	rec := postJSON(t, mux, "/__hamr/mail/ingest", map[string]any{"Subject": "x"})
	var out struct{ ID string }
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	id := out.ID

	req := httptest.NewRequest(http.MethodPost, "/__hamr/mail/"+id+"/fail", strings.NewReader("note=y"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://"+req.Host)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "failed", mm.Get(id).Status)
}

func TestContentDispositionAttachment(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain ascii", "report.pdf", `attachment; filename="report.pdf"`},
		{"ascii with quote", `weird"name.pdf`, `attachment; filename="weird_name.pdf"; filename*=UTF-8''weird%22name.pdf`},
		{"unicode", "résumé.pdf", `attachment; filename="r_sum_.pdf"; filename*=UTF-8''r%C3%A9sum%C3%A9.pdf`},
		{"spaces", "my file.pdf", `attachment; filename="my file.pdf"`},
	}
	for _, c := range cases {
		got := contentDispositionAttachment(c.in)
		assert.Equal(t, c.want, got, c.name)
	}
}

func TestRewriteCID_HandlesMultipleAttrForms(t *testing.T) {
	t.Parallel()
	in := `<img src="cid:a"><img SRC='cid:b'><img srcset="cid:c 1x, cid:d 2x"><div style="background-image: url(cid:e)">x</div>`
	out := rewriteCID(in, "msg_X")
	prefix := "/__hamr/mail/msg_X/inline/"
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		assert.Contains(t, out, prefix+id, "missing %s rewrite", id)
	}
	assert.NotContains(t, out, "cid:", "at least one cid: reference survived: %s", out)
}

func TestFormatSince(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"sub-second", 500 * time.Millisecond, "0s"},
		{"negative", -5 * time.Second, "0s"},
		{"seconds", 10 * time.Second, "10s"},
		{"minutes only", 5 * time.Minute, "5m"},
		{"minutes seconds", 5*time.Minute + 30*time.Second, "5m 30s"},
		{"hours only", 3 * time.Hour, "3h"},
		{"hours minutes", 3*time.Hour + 5*time.Minute, "3h 5m"},
		{"hours drop seconds", 3*time.Hour + 30*time.Second, "3h"},
		{"days only", 2 * 24 * time.Hour, "2d"},
		{"days hours", 2*24*time.Hour + 20*time.Hour, "2d 20h"},
		{"days drop minutes", 2*24*time.Hour + 5*time.Minute, "2d"},
		{"68h example", 68*time.Hour + 29*time.Minute + 49*time.Second, "2d 20h"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, formatSince(tc.d))
		})
	}
}

func TestForceLinksToNewTab(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			"plain anchor gets target injected",
			`<a href="https://example.com">x</a>`,
			`<a target="_blank" href="https://example.com">x</a>`,
		},
		{
			"existing _self overridden",
			`<a target="_self" href="https://example.com">x</a>`,
			`<a target="_blank" href="https://example.com">x</a>`,
		},
		{
			"existing _top overridden",
			`<a href="https://example.com" target="_top">x</a>`,
			`<a target="_blank" href="https://example.com">x</a>`,
		},
		{
			"named window target overridden",
			`<a href="x" target="myframe">x</a>`,
			`<a target="_blank" href="x">x</a>`,
		},
		{
			"unquoted target overridden",
			`<a href=x target=_self>x</a>`,
			`<a target="_blank" href=x>x</a>`,
		},
		{
			"single-quoted target overridden",
			`<a href='x' target='_self'>x</a>`,
			`<a target="_blank" href='x'>x</a>`,
		},
		{
			"uppercase tag rewritten",
			`<A HREF="x">x</A>`,
			`<a target="_blank" HREF="x">x</A>`,
		},
		{
			"<area> not rewritten",
			`<area shape="rect" href="x">`,
			`<area shape="rect" href="x">`,
		},
		{
			"<abbr> not rewritten",
			`<abbr title="x">y</abbr>`,
			`<abbr title="x">y</abbr>`,
		},
		{
			"multiple anchors all rewritten",
			`<a href="a">1</a> and <a target="_self" href="b">2</a>`,
			`<a target="_blank" href="a">1</a> and <a target="_blank" href="b">2</a>`,
		},
		{
			"bare <a> tag",
			`<a>x</a>`,
			`<a target="_blank">x</a>`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, forceLinksToNewTab(tc.in))
		})
	}
}

func TestStripImgSrc_ReplacesSrcSrcsetAndStyleURL(t *testing.T) {
	t.Parallel()
	in := `<img src="https://a.example/x.png" alt="a"><img srcset="https://b.example/y.png 1x"><div style="background: url(https://c.example/z.png) center">x</div>`
	out := stripImgSrc(in)

	assert.NotContains(t, out, "a.example")
	assert.NotContains(t, out, "b.example")
	assert.NotContains(t, out, "c.example")
	assert.Contains(t, out, transparentPixel)
	// Non-image domains in non-img contexts must be left alone.
	passthrough := `<a href="https://keep.example/page">link</a>`
	assert.Equal(t, passthrough, stripImgSrc(passthrough))
}
