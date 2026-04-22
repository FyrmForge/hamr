package emailmock_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FyrmForge/hamr/pkg/email"
	"github.com/FyrmForge/hamr/pkg/emailmock"
)

func TestSend_Success(t *testing.T) {
	t.Parallel()

	var gotPath, gotCT string
	var gotBody email.Message
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotCT = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ID":"msg_abc"}`)
	}))
	defer srv.Close()

	c := emailmock.New(srv.URL)
	msg := email.Message{
		From:    email.Addr("", "app@example.com"),
		To:      []email.Address{email.Addr("Ada", "ada@example.com")},
		Subject: "hello",
		Text:    "plain body",
		HTML:    "<p>html body</p>",
	}

	res, err := c.Send(context.Background(), msg)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if res == nil || res.ID != "msg_abc" {
		t.Fatalf("result: got %+v, want ID=msg_abc", res)
	}
	if gotPath != "/__hamr/mail/ingest" {
		t.Errorf("path: got %q, want /__hamr/mail/ingest", gotPath)
	}
	if gotCT != "application/json" {
		t.Errorf("content-type: got %q", gotCT)
	}
	if gotBody.Subject != "hello" || len(gotBody.To) != 1 || gotBody.To[0].Email != "ada@example.com" {
		t.Errorf("decoded body: %+v", gotBody)
	}
}

func TestSend_TrailingSlashStripped(t *testing.T) {
	t.Parallel()

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ID":"x"}`)
	}))
	defer srv.Close()

	c := emailmock.New(srv.URL + "/")
	if _, err := c.Send(context.Background(), email.Message{Subject: "x"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotPath != "/__hamr/mail/ingest" {
		t.Errorf("path: got %q", gotPath)
	}
}

func TestSend_Bounced(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, `{"error":"bounced"}`)
	}))
	defer srv.Close()

	c := emailmock.New(srv.URL)
	_, err := c.Send(context.Background(), email.Message{To: []email.Address{email.Addr("", "bounce@example.com")}})
	if !errors.Is(err, emailmock.ErrBounced) {
		t.Fatalf("want ErrBounced, got %v", err)
	}
}

func TestSend_Rejected(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, `{"error":"rejected"}`)
	}))
	defer srv.Close()

	c := emailmock.New(srv.URL)
	_, err := c.Send(context.Background(), email.Message{})
	if !errors.Is(err, emailmock.ErrRejected) {
		t.Fatalf("want ErrRejected, got %v", err)
	}
}

func TestSend_TooLarge(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
	}))
	defer srv.Close()

	c := emailmock.New(srv.URL)
	_, err := c.Send(context.Background(), email.Message{})
	if err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("want size-limit error, got %v", err)
	}
}

func TestSend_EmptyBaseURL(t *testing.T) {
	t.Parallel()

	c := emailmock.New("")
	_, err := c.Send(context.Background(), email.Message{})
	if err == nil || !strings.Contains(err.Error(), "baseURL") {
		t.Fatalf("want baseURL error, got %v", err)
	}
}

func TestSend_ContextCancelled(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ID":"x"}`)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	c := emailmock.New(srv.URL)
	_, err := c.Send(ctx, email.Message{})
	if err == nil {
		t.Fatalf("expected error from cancelled context")
	}
}
