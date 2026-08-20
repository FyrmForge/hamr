package smsmock

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FyrmForge/hamr/pkg/sms"
)

// fakeIngest mimics the dev server's /__hamr/sms/ingest endpoint, including
// the magic-number refusals.
func fakeIngest(t *testing.T) (*httptest.Server, *[]sms.Message) {
	t.Helper()
	var got []sms.Message
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/__hamr/sms/ingest" {
			http.NotFound(w, r)
			return
		}
		var msg sms.Message
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case len(msg.To) >= 7 && msg.To[len(msg.To)-7:] == "5550001":
			w.WriteHeader(http.StatusUnprocessableEntity)
			w.Write([]byte(`{"error":"invalid_number"}`)) //nolint:errcheck
		case len(msg.To) >= 7 && msg.To[len(msg.To)-7:] == "5550002":
			w.WriteHeader(http.StatusUnprocessableEntity)
			w.Write([]byte(`{"error":"undeliverable"}`)) //nolint:errcheck
		default:
			got = append(got, msg)
			w.Write([]byte(`{"ID":"sms_abc123"}`)) //nolint:errcheck
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func TestSendSuccess(t *testing.T) {
	srv, got := fakeIngest(t)
	c := New(srv.URL + "/") // trailing slash must be tolerated

	res, err := c.Send(context.Background(), sms.Message{From: "+15551230000", To: "+15559870000", Body: "hi"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if res.ID != "sms_abc123" {
		t.Errorf("ID = %q", res.ID)
	}
	if len(*got) != 1 || (*got)[0].Body != "hi" {
		t.Errorf("server received %+v", *got)
	}
}

func TestSendMagicNumbers(t *testing.T) {
	srv, _ := fakeIngest(t)
	c := New(srv.URL)

	if _, err := c.Send(context.Background(), sms.Message{To: "+15005550001", Body: "x"}); !errors.Is(err, ErrInvalidNumber) {
		t.Errorf("err = %v, want ErrInvalidNumber", err)
	}
	if _, err := c.Send(context.Background(), sms.Message{To: "+15005550002", Body: "x"}); !errors.Is(err, ErrUndeliverable) {
		t.Errorf("err = %v, want ErrUndeliverable", err)
	}
}

func TestSendEmptyBaseURL(t *testing.T) {
	if _, err := New("").Send(context.Background(), sms.Message{To: "+15550000000"}); err == nil {
		t.Fatal("want error for empty baseURL")
	}
}
