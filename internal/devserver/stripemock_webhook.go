package devserver

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// WebhookEndpoint is where signed events are delivered. URL is the absolute
// HTTP(S) URL of the app's webhook handler; Secret is the shared signing
// secret used to compute the Stripe-Signature header.
type WebhookEndpoint struct {
	URL    string
	Secret string
}

// SetWebhookEndpoint configures the destination + signing secret for events
// fired via FireEvent. Replaces any previously configured endpoint. An
// endpoint with empty URL or Secret silently drops events at FireEvent time
// — useful so callers don't have to gate every fire.
func (m *StripeMock) SetWebhookEndpoint(ep WebhookEndpoint) {
	m.mu.Lock()
	m.webhookEP = ep
	m.mu.Unlock()
}

// FireEvent delivers a signed Stripe webhook to the configured endpoint.
// The dataObject is the Stripe resource that triggered the event (e.g. a
// serialized CheckoutSession for a checkout.session.completed event); it is
// embedded under data.object exactly as Stripe would do.
//
// Returns nil when no endpoint is configured (silent drop). On delivery
// failure or non-2xx response, returns an error so callers can log/surface.
// This call is synchronous — for fire-and-forget semantics, wrap in a
// goroutine.
func (m *StripeMock) FireEvent(ctx context.Context, eventType string, dataObject map[string]any) error {
	m.mu.RLock()
	ep := m.webhookEP
	m.mu.RUnlock()
	if ep.URL == "" || ep.Secret == "" {
		return nil
	}

	payload, err := buildEventPayload(eventType, dataObject)
	if err != nil {
		return fmt.Errorf("stripemock: build event: %w", err)
	}

	ts := time.Now()
	sig := computeStripeSignature(ts, payload, ep.Secret)
	header := fmt.Sprintf("t=%d,v1=%s", ts.Unix(), hex.EncodeToString(sig))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.URL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("stripemock: build webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Stripe-Signature", header)
	req.Header.Set("User-Agent", "Stripe/1.0 (+https://stripe.com/docs/webhooks)")

	resp, err := m.webhookHTTP().Do(req)
	if err != nil {
		return fmt.Errorf("stripemock: deliver webhook to %s: %w", ep.URL, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<14))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("stripemock: webhook %s returned %d: %s",
			ep.URL, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// webhookHTTP returns the HTTP client used for outbound webhook delivery.
// Lazily created with a short timeout so a hung app handler doesn't stall
// the dev UI request that triggered the event.
func (m *StripeMock) webhookHTTP() *http.Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.whClient == nil {
		m.whClient = &http.Client{Timeout: 10 * time.Second}
	}
	return m.whClient
}

// buildEventPayload constructs the JSON body of a Stripe webhook event:
//
//	{
//	  "id":         "evt_test_<hex>",
//	  "object":     "event",
//	  "api_version":"2025-08-27.basil",
//	  "created":    <unix>,
//	  "type":       "<event type>",
//	  "livemode":   false,
//	  "data":       {"object": <resource>},
//	  "request":    {"id": null, "idempotency_key": null}
//	}
//
// stripe-go's webhook.ConstructEvent verifies api_version's release train
// matches its own pinned version; we set it to the mock's pinned version so
// the check always passes.
func buildEventPayload(eventType string, dataObject map[string]any) ([]byte, error) {
	if eventType == "" {
		return nil, fmt.Errorf("event type is required")
	}
	envelope := map[string]any{
		"id":          "evt_test_" + randomHex(24),
		"object":      "event",
		"api_version": stripeAPIVersion,
		"created":     time.Now().Unix(),
		"type":        eventType,
		"livemode":    false,
		"data":        map[string]any{"object": dataObject},
		"request":     map[string]any{"id": nil, "idempotency_key": nil},
	}
	return json.Marshal(envelope)
}

// computeStripeSignature mirrors stripe-go/webhook.ComputeSignature:
// HMAC-SHA256(secret, "<unix_ts>.<payload>"). Hex-encoded by the caller.
func computeStripeSignature(t time.Time, payload []byte, secret string) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%d.", t.Unix())
	mac.Write(payload) //nolint:errcheck
	return mac.Sum(nil)
}

// rewriteWebhookURLForAppPort returns rawURL with its port swapped from
// originalAppPort to actualAppPort when the URL points at a localhost
// address on the original port. Used by the dev runner so that when
// [dev].port_walk shifts the spawned-app port (e.g. 8080 → 8081), the
// Stripe mock fires webhooks at the new port instead of the stale value
// the user wrote in hamr.toml.
//
// URLs that don't match (different host, different port, missing port,
// unparseable) come back unchanged — users who configured an exotic
// webhook URL (ngrok, public proxy, non-app target) keep what they set.
func rewriteWebhookURLForAppPort(rawURL string, originalAppPort, actualAppPort int) string {
	if rawURL == "" || originalAppPort == actualAppPort {
		return rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	if !isLocalHostname(u.Hostname()) {
		return rawURL
	}
	port := u.Port()
	if port == "" {
		// No explicit port — scheme default (e.g. :80 / :443) doesn't
		// match a dev app port the user configured, so leave alone.
		return rawURL
	}
	portInt, err := strconv.Atoi(port)
	if err != nil || portInt != originalAppPort {
		return rawURL
	}
	u.Host = net.JoinHostPort(u.Hostname(), strconv.Itoa(actualAppPort))
	return u.String()
}

// isLocalHostname reports whether host is a loopback name we recognise.
// IPv6 ::1 is normalised by url.URL.Hostname() (brackets removed).
func isLocalHostname(host string) bool {
	switch host {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}
