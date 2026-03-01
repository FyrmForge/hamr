// Package client provides an HTTP client for inter-service communication.
//
// It automatically propagates X-Request-ID and X-Subject-ID headers from
// the incoming request context, ensuring traceability and auth propagation
// across service boundaries.
//
//	billing := client.New(
//	    client.WithBaseURL(cfg.BillingServiceURL),
//	    client.WithTimeout(5 * time.Second),
//	)
//
//	ctx := client.EchoContext(c) // propagates request ID + subject ID
//	invoice, err := client.Get[dto.Invoice](ctx, billing, "/invoices/"+id)
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type contextKey string

const (
	requestIDCtxKey contextKey = "hamr_client_request_id"
	subjectIDCtxKey contextKey = "hamr_client_subject_id"
)

const (
	headerRequestID = "X-Request-ID"
	headerSubjectID = "X-Subject-ID"
)

// Client is an HTTP client for inter-service communication.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL sets the base URL for all requests.
func WithBaseURL(url string) Option {
	return func(c *Client) {
		c.baseURL = strings.TrimRight(url, "/")
	}
}

// WithTimeout sets the HTTP client timeout.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		c.httpClient.Timeout = d
	}
}

// WithHTTPClient replaces the underlying http.Client.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		c.httpClient = hc
	}
}

// New creates a new Client with the given options.
func New(opts ...Option) *Client {
	c := &Client{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// WithRequestID returns a context carrying the given request ID for propagation.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDCtxKey, id)
}

// WithSubjectID returns a context carrying the given subject ID for propagation.
func WithSubjectID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, subjectIDCtxKey, id)
}

// Do executes an HTTP request, propagating X-Request-ID and X-Subject-ID
// headers from the context.
func (c *Client) Do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("client: create request: %w", err)
	}

	if id, ok := ctx.Value(requestIDCtxKey).(string); ok && id != "" {
		req.Header.Set(headerRequestID, id)
	}
	if id, ok := ctx.Value(subjectIDCtxKey).(string); ok && id != "" {
		req.Header.Set(headerSubjectID, id)
	}

	req.Header.Set("Content-Type", "application/json")

	return c.httpClient.Do(req)
}

// Get performs a GET request and JSON-decodes the response into T.
func Get[T any](ctx context.Context, c *Client, path string) (T, error) {
	return doRequest[T](ctx, c, http.MethodGet, path, nil)
}

// Post performs a POST request with a JSON body and decodes the response into T.
func Post[T any](ctx context.Context, c *Client, path string, body any) (T, error) {
	return doWithBody[T](ctx, c, http.MethodPost, path, body)
}

// Put performs a PUT request with a JSON body and decodes the response into T.
func Put[T any](ctx context.Context, c *Client, path string, body any) (T, error) {
	return doWithBody[T](ctx, c, http.MethodPut, path, body)
}

// Delete performs a DELETE request and JSON-decodes the response into T.
func Delete[T any](ctx context.Context, c *Client, path string) (T, error) {
	return doRequest[T](ctx, c, http.MethodDelete, path, nil)
}

func doRequest[T any](ctx context.Context, c *Client, method, path string, body io.Reader) (T, error) {
	var result T

	resp, err := c.Do(ctx, method, path, body)
	if err != nil {
		return result, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return result, &ResponseError{
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
			Body:       respBody,
		}
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return result, fmt.Errorf("client: decode response: %w", err)
	}
	return result, nil
}

func doWithBody[T any](ctx context.Context, c *Client, method, path string, body any) (T, error) {
	var result T

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return result, fmt.Errorf("client: encode body: %w", err)
	}

	return doRequest[T](ctx, c, method, path, &buf)
}

// ResponseError represents a non-2xx HTTP response.
type ResponseError struct {
	StatusCode int
	Status     string
	Body       []byte
}

func (e *ResponseError) Error() string {
	return fmt.Sprintf("client: unexpected status %s", e.Status)
}
