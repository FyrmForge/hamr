package devserver

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
// HTTP(S) URL of the app's webhook handler for v1 snapshot events; ThinURL is
// the handler for v2 thin event notifications (empty = thin events are logged
// but not delivered). Events carrying a connected account go to ConnectURL /
// ThinConnectURL when set, the plain URLs otherwise. Secret signs all of them,
// the same way `stripe listen` uses one secret for every --forward-*-to.
type WebhookEndpoint struct {
	URL            string
	ThinURL        string
	ConnectURL     string
	ThinConnectURL string
	Secret         string
}

// target returns the URL an event is delivered to ("" = not delivered).
func (ep WebhookEndpoint) target(thin, connect bool) string {
	url, connectURL := ep.URL, ep.ConnectURL
	if thin {
		url, connectURL = ep.ThinURL, ep.ThinConnectURL
	}
	if connect && connectURL != "" {
		return connectURL
	}
	return url
}

// errStripeListening is returned by mock actions while [dev.stripe] is in
// listen mode.
var errStripeListening = errors.New("stripe is in listen mode")

// SetWebhookEndpoint configures the destinations + signing secret for events.
// Replaces any previously configured endpoint. An endpoint with an empty URL
// or Secret still records events in the event log but delivers nothing —
// useful so callers don't have to gate every fire.
func (m *StripeMock) SetWebhookEndpoint(ep WebhookEndpoint) {
	m.mu.Lock()
	m.webhookEP = ep
	m.mu.Unlock()
}

// FireEvent records and delivers a signed v1 snapshot event. The dataObject
// is the Stripe resource that triggered the event (e.g. a serialized
// CheckoutSession for checkout.session.completed); it is embedded under
// data.object exactly as Stripe would do.
//
// Returns nil when no endpoint is configured (silent drop). On delivery
// failure or non-2xx response, returns an error so callers can log/surface.
// This call is synchronous — for fire-and-forget semantics, wrap in a
// goroutine.
func (m *StripeMock) FireEvent(ctx context.Context, eventType string, dataObject map[string]any) error {
	return m.fireEvent(ctx, webhookFire{eventType: eventType, object: dataObject})
}

// fireEvent builds, logs and delivers one event. Snapshot events carry the
// object; thin events carry only a related-object reference.
func (m *StripeMock) fireEvent(ctx context.Context, f webhookFire) error {
	now := time.Now()
	ev := &stripeEvent{
		ID:      "evt_test_" + randomHex(24),
		Type:    f.eventType,
		Thin:    f.thinRelatedID != "",
		Account: f.account,
		Created: now,
	}
	var err error
	if ev.Thin {
		ev.ObjectID = f.thinRelatedID
		ev.Payload, err = buildThinEventPayload(ev.ID, f.eventType, f.thinRelatedID, now)
	} else {
		ev.ObjectID = getString(f.object, "id")
		ev.Payload, err = buildEventPayload(ev.ID, f.eventType, f.account, f.object, now)
	}
	if err != nil {
		return fmt.Errorf("stripemock: build event: %w", err)
	}
	m.recordEvent(ev)
	return m.deliverEvent(ctx, ev)
}

// deliverEvent POSTs a logged event's payload to the matching endpoint with a
// fresh signature and records the outcome on the log entry. Resending an
// event reuses this, so the app sees the same event id again — exactly what
// a Stripe dashboard resend does.
func (m *StripeMock) deliverEvent(ctx context.Context, ev *stripeEvent) error {
	m.mu.RLock()
	ep := m.webhookEP
	m.mu.RUnlock()
	target := ep.target(ev.Thin, ev.Account != "")
	if target == "" || ep.Secret == "" {
		return nil
	}
	if m.listening.Load() {
		// Signed with the mock secret, it would fail the app's check and read
		// like an app bug. Kept in the event log: switch back and resend.
		err := fmt.Errorf("%w: webhook not delivered (switch back to mock and resend)", errStripeListening)
		m.markEventDelivery(ev.ID, err)
		return err
	}

	ts := time.Now()
	sig := computeStripeSignature(ts, ev.Payload, ep.Secret)
	header := fmt.Sprintf("t=%d,v1=%s", ts.Unix(), hex.EncodeToString(sig))

	err := m.postWebhook(ctx, target, header, ev.Payload)
	m.markEventDelivery(ev.ID, err)
	return err
}

func (m *StripeMock) postWebhook(ctx context.Context, target, sigHeader string, payload []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("stripemock: build webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Stripe-Signature", sigHeader)
	req.Header.Set("User-Agent", "Stripe/1.0 (+https://stripe.com/docs/webhooks)")

	resp, err := m.webhookHTTP().Do(req)
	if err != nil {
		return fmt.Errorf("stripemock: deliver webhook to %s: %w", target, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<14))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("stripemock: webhook %s returned %d: %s",
			target, resp.StatusCode, strings.TrimSpace(string(body)))
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

// buildEventPayload constructs the JSON body of a v1 snapshot event:
//
//	{
//	  "id":         "evt_test_<hex>",
//	  "object":     "event",
//	  "api_version":"2026-08-26.dahlia",
//	  "created":    <unix>,
//	  "type":       "<event type>",
//	  "livemode":   false,
//	  "account":    "acct_..."   (only for events on a connected account)
//	  "data":       {"object": <resource>},
//	  "request":    {"id": null, "idempotency_key": null}
//	}
//
// stripe-go's webhook.ConstructEvent verifies api_version's release train
// matches its own pinned version; we set it to the mock's pinned version so
// the check always passes.
func buildEventPayload(id, eventType, account string, dataObject map[string]any, now time.Time) ([]byte, error) {
	if eventType == "" {
		return nil, fmt.Errorf("event type is required")
	}
	envelope := map[string]any{
		"id":          id,
		"object":      "event",
		"api_version": stripeAPIVersion,
		"created":     now.Unix(),
		"type":        eventType,
		"livemode":    false,
		"data":        map[string]any{"object": dataObject},
		"request":     map[string]any{"id": nil, "idempotency_key": nil},
	}
	if account != "" {
		envelope["account"] = account
	}
	return json.Marshal(envelope)
}

// buildThinEventPayload constructs a v2 thin event notification: no object,
// just a pointer at it. stripe-go's ParseEventNotification rejects
// object="event", so the envelope must say "v2.core.event". The mock only
// emits thin events about v2 accounts, so the related object is always one.
func buildThinEventPayload(id, eventType, relatedID string, now time.Time) ([]byte, error) {
	if eventType == "" {
		return nil, fmt.Errorf("event type is required")
	}
	return json.Marshal(map[string]any{
		"id":       id,
		"object":   "v2.core.event",
		"type":     eventType,
		"livemode": false,
		"created":  formatV2Time(now),
		"related_object": map[string]any{
			"id":   relatedID,
			"type": "v2.core.account",
			"url":  "/v2/core/accounts/" + relatedID,
		},
	})
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
