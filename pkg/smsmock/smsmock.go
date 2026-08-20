// Package smsmock is a dev-only implementation of sms.Sender that ships
// messages to the hamr dev server's SMS inbox at /__hamr/sms.
//
// The mock does not talk to any provider API. It serializes the sms.Message to
// JSON and POSTs it to the hamr dev proxy, which stores it in an in-memory
// inbox viewable at http://<dev-proxy>/__hamr/sms.
//
// Because the wire format is JSON (not a provider API), this mock validates how
// your application uses the sms.Sender interface — it does not validate your
// production provider adapter. Catch those bugs with your provider's test
// credentials or integration tests.
//
// Typical wiring in main.go:
//
//	var sender sms.Sender
//	if os.Getenv("SMS_MOCK") == "true" {
//	    sender = smsmock.New(os.Getenv("HAMR_DEV_URL"))
//	} else {
//	    sender = myapp.NewRealSender(...)
//	}
//
// The mock client is safe for concurrent use.
//
// Failure simulation: a recipient number whose digits end in "5550001"
// (e.g. "+15005550001") causes Send to return ErrInvalidNumber without
// storing the message; one ending in "5550002" returns ErrUndeliverable.
// The suffixes use the fictional 555 exchange so real numbers can't match.
// This lets apps test error paths without configuring anything server-side.
package smsmock

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

	"github.com/FyrmForge/hamr/pkg/sms"
)

// Client is a dev-only sms.Sender that POSTs messages to the hamr dev
// server's /__hamr/sms/ingest endpoint.
type Client struct {
	baseURL string
	http    *http.Client
}

// New returns a Client that ingests messages at baseURL/__hamr/sms/ingest.
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
// recipient number. These wrap the server-side refusal so apps can test
// error paths deterministically.
var (
	ErrInvalidNumber = errors.New("smsmock: invalid recipient number")
	ErrUndeliverable = errors.New("smsmock: recipient undeliverable")
)

// Send JSON-encodes msg and POSTs it to the mock inbox. It returns the
// server-assigned Result on success, ErrInvalidNumber/ErrUndeliverable for
// magic recipient numbers, or a wrapped transport/HTTP error otherwise.
func (c *Client) Send(ctx context.Context, msg sms.Message) (*sms.Result, error) {
	if c.baseURL == "" {
		return nil, errors.New("smsmock: client has empty baseURL")
	}

	body, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("smsmock: marshal message: %w", err)
	}

	endpoint, err := url.JoinPath(c.baseURL, "/__hamr/sms/ingest")
	if err != nil {
		return nil, fmt.Errorf("smsmock: build url: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("smsmock: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("smsmock: post to %s: %w", endpoint, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	switch resp.StatusCode {
	case http.StatusOK:
		var out sms.Result
		if err := json.Unmarshal(respBody, &out); err != nil {
			return nil, fmt.Errorf("smsmock: decode response: %w", err)
		}
		return &out, nil

	case http.StatusUnprocessableEntity:
		// Server-side magic-number rejection. Body is {"error":"invalid_number"}
		// or {"error":"undeliverable"}.
		var e ingestError
		_ = json.Unmarshal(respBody, &e)
		switch e.Error {
		case "invalid_number":
			return nil, ErrInvalidNumber
		case "undeliverable":
			return nil, ErrUndeliverable
		default:
			return nil, fmt.Errorf("smsmock: server refused message: %s", e.Error)
		}

	case http.StatusRequestEntityTooLarge:
		return nil, fmt.Errorf("smsmock: message exceeds dev inbox size limit")

	default:
		return nil, fmt.Errorf("smsmock: ingest returned %d: %s", resp.StatusCode, truncate(respBody, 512))
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
