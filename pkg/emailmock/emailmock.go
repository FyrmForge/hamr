// Package emailmock is a dev-only implementation of email.Sender that ships
// messages to the hamr dev server's mail inbox at /__hamr/mail.
//
// The mock does not construct MIME, talk SMTP, or call any provider API. It
// serializes the email.Message to JSON and POSTs it to the hamr dev proxy,
// which stores it in an in-memory inbox viewable at http://<dev-proxy>/__hamr/mail.
//
// Because the wire format is JSON (not MIME), this mock validates how your
// application uses the email.Sender interface — it does not validate your
// production provider adapter's MIME construction or SMTP handshake. Catch
// those bugs with your provider's test environment or integration tests.
//
// Typical wiring in main.go:
//
//	var sender email.Sender
//	if os.Getenv("EMAIL_MOCK") == "true" {
//	    sender = emailmock.New(os.Getenv("HAMR_DEV_URL"))
//	} else {
//	    sender = myapp.NewRealSender(...)
//	}
//
// The mock client is safe for concurrent use.
//
// Failure simulation: any recipient whose local-part is "bounce" (e.g.
// bounce@example.com) causes Send to return ErrBounced without storing the
// message; any recipient whose local-part is "reject" returns ErrRejected.
// This lets apps test error paths without configuring anything server-side.
package emailmock

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/FyrmForge/hamr/pkg/email"
)

// Client is a dev-only email.Sender that POSTs messages to the hamr dev
// server's /__hamr/mail/ingest endpoint.
type Client struct {
	baseURL string
	http    *http.Client
}

// New returns a Client that ingests messages at baseURL/__hamr/mail/ingest.
// baseURL is typically the hamr dev proxy origin (e.g. "http://localhost:3000").
// Trailing slashes are stripped. An empty baseURL is accepted but every Send
// will fail until the client is re-configured.
func New(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

// Well-known failure outcomes returned by Send when the message hits a magic
// recipient. These wrap the server-side refusal so apps can test bounce and
// reject error paths deterministically.
var (
	ErrBounced  = errors.New("emailmock: recipient bounced")
	ErrRejected = errors.New("emailmock: recipient rejected")
)

// Send JSON-encodes msg and POSTs it to the mock inbox. It returns the
// server-assigned Result on success, ErrBounced/ErrRejected for magic
// recipients, or a wrapped transport/HTTP error otherwise.
func (c *Client) Send(ctx context.Context, msg email.Message) (*email.Result, error) {
	if c.baseURL == "" {
		return nil, errors.New("emailmock: client has empty baseURL")
	}

	body, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("emailmock: marshal message: %w", err)
	}

	endpoint, err := url.JoinPath(c.baseURL, "/__hamr/mail/ingest")
	if err != nil {
		return nil, fmt.Errorf("emailmock: build url: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("emailmock: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("emailmock: post to %s: %w", endpoint, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	// Read the response once so we can use it for both success and error paths.
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	switch resp.StatusCode {
	case http.StatusOK:
		var out email.Result
		if err := json.Unmarshal(respBody, &out); err != nil {
			return nil, fmt.Errorf("emailmock: decode response: %w", err)
		}
		return &out, nil

	case http.StatusUnprocessableEntity:
		// Server-side magic-address rejection. Body is {"error":"bounced"} or {"error":"rejected"}.
		var e ingestError
		_ = json.Unmarshal(respBody, &e)
		switch e.Error {
		case "bounced":
			return nil, ErrBounced
		case "rejected":
			return nil, ErrRejected
		default:
			return nil, fmt.Errorf("emailmock: server refused message: %s", e.Error)
		}

	case http.StatusRequestEntityTooLarge:
		return nil, fmt.Errorf("emailmock: message exceeds dev inbox size limit")

	default:
		return nil, fmt.Errorf("emailmock: ingest returned %d: %s", resp.StatusCode, truncate(respBody, 512))
	}
}

type ingestError struct {
	Error string `json:"error"`
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
