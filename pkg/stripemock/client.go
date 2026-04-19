package stripemock

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
)

// Client is an in-memory Stripe mock. Sessions live in memory for the
// lifetime of the process; outcomes are set via the dev UI.
type Client struct {
	mu       sync.RWMutex
	sessions map[string]*session
	baseURL  string
	currency string
}

type session struct {
	request CheckoutRequest
	result  *PaymentResult
}

// New returns a mock client.
//
// baseURL is used to construct the dev-UI redirect URL returned from
// CreateCheckoutSession (i.e. what the app tells the user to visit in place of
// a real Stripe checkout).
//
// currency is the ISO 4217 code (uppercase, e.g. "GBP", "USD", "JPY") used to
// render line-item amounts on the dev page. Unknown codes render as
// "<CODE> 0.00"; zero-decimal currencies (JPY) are rendered without decimals.
func New(baseURL, currency string) *Client {
	return &Client{
		sessions: make(map[string]*session),
		baseURL:  baseURL,
		currency: currency,
	}
}

// Currency returns the ISO 4217 code this client was configured with.
func (c *Client) Currency() string {
	return c.currency
}

// CreateCheckoutSession stores the request and returns a CheckoutSession whose
// URL points at the local dev UI. The session starts in StatusRequiresAction
// until the developer picks an outcome in the browser. The request's
// LineItems and Metadata are deep-copied, so later mutations by the caller
// don't affect the stored session.
func (c *Client) CreateCheckoutSession(req CheckoutRequest) (*CheckoutSession, error) {
	id := "cs_mock_" + randomID(16)
	pi := "pi_mock_" + randomID(16)

	stored := req
	stored.LineItems = append([]LineItem(nil), req.LineItems...)
	stored.Metadata = cloneMeta(req.Metadata)

	c.mu.Lock()
	c.sessions[id] = &session{
		request: stored,
		result: &PaymentResult{
			SessionID:       id,
			PaymentIntentID: pi,
			Status:          StatusRequiresAction,
			Metadata:        cloneMeta(req.Metadata),
		},
	}
	c.mu.Unlock()

	return &CheckoutSession{
		ID:              id,
		URL:             fmt.Sprintf("%s/dev/stripe?session=%s", c.baseURL, id),
		PaymentIntentID: pi,
	}, nil
}

func cloneMeta(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// GetSessionResult returns the current outcome of a session as a detached
// copy — mutating the returned value does not affect stored state.
func (c *Client) GetSessionResult(sessionID string) (*PaymentResult, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	s, ok := c.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("stripemock: session %q not found", sessionID)
	}
	out := *s.result
	out.Metadata = cloneMeta(s.result.Metadata)
	return &out, nil
}

// SetOutcome updates the stored outcome of a session. Called by the dev UI
// when the developer clicks an outcome button.
func (c *Client) SetOutcome(sessionID string, status PaymentStatus) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	s, ok := c.sessions[sessionID]
	if !ok {
		return fmt.Errorf("stripemock: session %q not found", sessionID)
	}
	s.result.Status = status
	return nil
}

// GetRequest returns the original checkout request for a session as a
// detached copy. Used by the dev UI to render line items.
func (c *Client) GetRequest(sessionID string) (*CheckoutRequest, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	s, ok := c.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("stripemock: session %q not found", sessionID)
	}
	out := s.request
	out.LineItems = append([]LineItem(nil), s.request.LineItems...)
	out.Metadata = cloneMeta(s.request.Metadata)
	return &out, nil
}

// SuccessURL returns the success redirect URL the caller provided when
// creating the session, or "/" if the session is unknown.
func (c *Client) SuccessURL(sessionID string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	s, ok := c.sessions[sessionID]
	if !ok {
		return "/"
	}
	return s.request.SuccessURL
}

// CancelURL returns the cancel redirect URL the caller provided when creating
// the session, or "/" if the session is unknown.
func (c *Client) CancelURL(sessionID string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	s, ok := c.sessions[sessionID]
	if !ok {
		return "/"
	}
	return s.request.CancelURL
}

func randomID(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
