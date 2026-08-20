package devserver

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newSMSTestServer(t *testing.T, opts SMSMockOptions) (*SMSMock, *httptest.Server) {
	t.Helper()
	m := NewSMSMock(opts)
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return m, srv
}

func smsIngest(t *testing.T, srv *httptest.Server, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(srv.URL+"/__hamr/sms/ingest", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() }) //nolint:errcheck
	return resp
}

func TestSMSMockIngestAndList(t *testing.T) {
	m, srv := newSMSTestServer(t, SMSMockOptions{})

	resp := smsIngest(t, srv, `{"From":"+15551230000","To":"+15559870000","Body":"your code is 123456","Ref":"otp-42"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var ack map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&ack); err != nil {
		t.Fatalf("decode ack: %v", err)
	}
	if !strings.HasPrefix(ack["ID"], "sms_") {
		t.Fatalf("ID = %q, want sms_ prefix", ack["ID"])
	}

	msgs := m.List()
	if len(msgs) != 1 {
		t.Fatalf("len(List()) = %d, want 1", len(msgs))
	}
	got := msgs[0]
	if got.From != "+15551230000" || got.To != "+15559870000" || got.Body != "your code is 123456" {
		t.Errorf("stored message = %+v", got)
	}
	if got.Status != "delivered" {
		t.Errorf("Status = %q, want delivered", got.Status)
	}
	if got.Ref != "otp-42" {
		t.Errorf("Ref = %q", got.Ref)
	}
	if m.Get(got.ID) == nil {
		t.Error("Get(id) = nil")
	}
}

func TestSMSMockMagicNumbers(t *testing.T) {
	m, srv := newSMSTestServer(t, SMSMockOptions{})

	cases := []struct {
		to, wantErr string
	}{
		{"+15005550001", "invalid_number"},
		{"+1 500 555-0002", "undeliverable"},
	}
	for _, c := range cases {
		resp := smsIngest(t, srv, `{"From":"+15551230000","To":"`+c.to+`","Body":"x"}`)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("to=%q: status = %d, want 422", c.to, resp.StatusCode)
		}
		var e map[string]string
		json.NewDecoder(resp.Body).Decode(&e) //nolint:errcheck
		if e["error"] != c.wantErr {
			t.Errorf("to=%q: error = %q, want %q", c.to, e["error"], c.wantErr)
		}
	}
	if got := len(m.List()); got != 0 {
		t.Errorf("magic-number messages stored: %d", got)
	}

	// A real-looking number that merely ends in 0001 (without the 555
	// exchange) must be delivered, not swallowed by the magic match.
	resp := smsIngest(t, srv, `{"From":"+15551230000","To":"+15559870001","Body":"real recipient"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("non-magic 0001 number: status = %d, want 200", resp.StatusCode)
	}
}

func TestSMSMockEviction(t *testing.T) {
	m, srv := newSMSTestServer(t, SMSMockOptions{MaxMessages: 2})

	for _, body := range []string{"one", "two", "three"} {
		smsIngest(t, srv, `{"To":"+15550000000","Body":"`+body+`"}`)
	}
	msgs := m.List()
	if len(msgs) != 2 {
		t.Fatalf("len = %d, want 2", len(msgs))
	}
	// Newest-first: "three", "two"; "one" evicted.
	if msgs[0].Body != "three" || msgs[1].Body != "two" {
		t.Errorf("bodies = %q, %q", msgs[0].Body, msgs[1].Body)
	}
}

func TestSMSMockStatusDeleteClear(t *testing.T) {
	m, srv := newSMSTestServer(t, SMSMockOptions{})
	smsIngest(t, srv, `{"To":"+15550000000","Body":"a"}`)
	smsIngest(t, srv, `{"To":"+15550000000","Body":"b"}`)
	id := m.List()[0].ID

	if !m.SetStatus(id, "failed", "test note") {
		t.Fatal("SetStatus returned false")
	}
	if m.SetStatus(id, "delivered", "") {
		t.Error("SetStatus accepted disallowed value")
	}
	if got := m.Get(id); got.Status != "failed" || got.StatusNote != "test note" {
		t.Errorf("after SetStatus: %+v", got)
	}

	if !m.Delete(id) {
		t.Fatal("Delete returned false")
	}
	if m.Get(id) != nil {
		t.Error("message still present after Delete")
	}
	m.Clear()
	if len(m.List()) != 0 {
		t.Error("List not empty after Clear")
	}
}

func TestSMSMockPersistenceRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sms", "inbox.jsonl")
	_, srv := newSMSTestServer(t, SMSMockOptions{PersistPath: path})
	smsIngest(t, srv, `{"From":"+15551230000","To":"+15559870000","Body":"persist me"}`)

	reloaded := NewSMSMock(SMSMockOptions{PersistPath: path})
	msgs := reloaded.List()
	if len(msgs) != 1 {
		t.Fatalf("reloaded len = %d, want 1", len(msgs))
	}
	if msgs[0].Body != "persist me" || msgs[0].Status != "delivered" {
		t.Errorf("reloaded message = %+v", msgs[0])
	}
}

func TestSMSMockPersistenceSkipsCorruptLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inbox.jsonl")
	good, _ := json.Marshal(&smsMessage{ID: "sms_ok", To: "+15550000000", Body: "kept"})
	content := bytes.Join([][]byte{[]byte("{not json"), good, []byte(`{"To":"no id"}`)}, []byte("\n"))
	if err := os.WriteFile(path, append(content, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewSMSMock(SMSMockOptions{PersistPath: path})
	msgs := m.List()
	if len(msgs) != 1 || msgs[0].ID != "sms_ok" {
		t.Fatalf("loaded = %+v, want just sms_ok", msgs)
	}
}

func TestSMSMockUIPages(t *testing.T) {
	m, srv := newSMSTestServer(t, SMSMockOptions{})
	smsIngest(t, srv, `{"From":"+15551230000","To":"+15559870000","Body":"<b>escaped</b> body"}`)
	id := m.List()[0].ID

	for _, path := range []string{"/__hamr/sms", "/__hamr/sms/" + id} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close() //nolint:errcheck
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d", path, resp.StatusCode)
		}
		if strings.Contains(string(body), "<b>escaped</b>") {
			t.Errorf("GET %s: body not HTML-escaped", path)
		}
	}

	// Unknown ID → 404.
	resp, err := http.Get(srv.URL + "/__hamr/sms/sms_nope")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown id status = %d, want 404", resp.StatusCode)
	}
}
