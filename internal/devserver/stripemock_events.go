package devserver

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"strings"
	"time"
)

// stripeEventLimit caps the event log. ponytail: in memory only (a hamr dev
// restart clears it) and oldest entries drop off past this; persist or page
// it if a long session needs older events.
const stripeEventLimit = 200

// stripeEvent is one entry in the mock's event log: every event the mock has
// emitted, snapshot or thin, with the exact payload so a resend replays the
// same event id the app already saw.
type stripeEvent struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Thin      bool            `json:"thin,omitempty"`
	Account   string          `json:"account,omitempty"`   // connected account the event happened on
	ObjectID  string          `json:"object_id,omitempty"` // data.object.id, or the thin event's related object
	Created   time.Time       `json:"created"`
	Payload   json.RawMessage `json:"payload"`
	Attempts  int             `json:"attempts"`
	Delivered bool            `json:"delivered"`
	LastError string          `json:"last_error,omitempty"`
}

// recordEvent appends ev to the log, trimming the oldest past the cap.
func (m *StripeMock) recordEvent(ev *stripeEvent) {
	m.mu.Lock()
	m.events = append(m.events, ev)
	if over := len(m.events) - stripeEventLimit; over > 0 {
		m.events = append([]*stripeEvent(nil), m.events[over:]...)
	}
	m.mu.Unlock()
}

// markEventDelivery records one delivery attempt's outcome.
func (m *StripeMock) markEventDelivery(id string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ev := range m.events {
		if ev.ID != id {
			continue
		}
		ev.Attempts++
		ev.Delivered = err == nil
		ev.LastError = ""
		if err != nil {
			ev.LastError = err.Error()
		}
		return
	}
}

// resendEvent redelivers a logged event by id, synchronously.
func (m *StripeMock) resendEvent(ctx context.Context, id string) error {
	m.mu.RLock()
	var found *stripeEvent
	for _, ev := range m.events {
		if ev.ID == id {
			c := *ev
			found = &c
			break
		}
	}
	m.mu.RUnlock()
	if found == nil {
		return stripeErr(http.StatusNotFound, "event not found")
	}
	return m.deliverEvent(ctx, found)
}

// fireThinAsync delivers v2 thin event notifications about one v2 account.
func (m *StripeMock) fireThinAsync(accountID string, eventTypes ...string) {
	fires := make([]webhookFire, len(eventTypes))
	for i, t := range eventTypes {
		fires[i] = webhookFire{eventType: t, thinRelatedID: accountID}
	}
	m.fireEventsAsync(fires, "account", accountID)
}

// formatV2Time renders a v2 API timestamp: RFC 3339, UTC, millisecond
// precision. v1 objects use unix seconds instead.
func formatV2Time(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

// --- idempotency ---

// idemLimit bounds the idempotency cache. ponytail: the whole cache is
// dropped when full rather than evicting by age; Stripe keeps keys for 24h,
// which no dev session gets near.
const idemLimit = 1000

// idemEntry is one idempotency key's recorded response. done closes once the
// first request finishes, so a concurrent retry waits and replays instead of
// running the side effect twice.
type idemEntry struct {
	done   chan struct{}
	status int
	header http.Header
	body   []byte
}

// stripeRouter is the subset of *http.ServeMux the route registrars use, so
// RegisterAPIRoutes can wrap every API route in idempotency handling.
type stripeRouter interface {
	HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request))
}

type idempotentRouter struct {
	mux  *http.ServeMux
	mock *StripeMock
}

func (r idempotentRouter) HandleFunc(pattern string, h func(http.ResponseWriter, *http.Request)) {
	r.mux.HandleFunc(pattern, r.mock.idempotent(h))
}

// idempotent replays the recorded response for a POST that repeats an
// Idempotency-Key, like Stripe does. Keys are scoped by Stripe-Account and
// path. Not persisted: a hamr dev restart forgets them.
func (m *StripeMock) idempotent(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
		if r.Method != http.MethodPost || key == "" {
			h(w, r)
			return
		}
		scoped := r.Header.Get("Stripe-Account") + " " + r.URL.Path + " " + key

		m.idemMu.Lock()
		e, seen := m.idem[scoped]
		if !seen {
			if len(m.idem) >= idemLimit {
				m.idem = map[string]*idemEntry{}
			}
			e = &idemEntry{done: make(chan struct{})}
			m.idem[scoped] = e
		}
		m.idemMu.Unlock()

		if seen {
			<-e.done
			maps.Copy(w.Header(), e.header)
			w.Header().Set("Idempotent-Replayed", "true")
			w.WriteHeader(e.status)
			w.Write(e.body) //nolint:errcheck
			return
		}

		// A panicking handler must not leave e.done open: every retry with
		// this key would block on it forever. Drop the entry so the retry
		// runs the handler again instead of replaying a half response.
		completed := false
		defer func() {
			if !completed {
				m.idemMu.Lock()
				if m.idem[scoped] == e {
					delete(m.idem, scoped)
				}
				m.idemMu.Unlock()
				e.status = http.StatusInternalServerError
			}
			close(e.done)
		}()
		rec := &recordingWriter{ResponseWriter: w, status: http.StatusOK}
		h(rec, r)
		e.status, e.header, e.body = rec.status, w.Header().Clone(), rec.buf.Bytes()
		completed = true
	}
}

// recordingWriter tees a response so it can be replayed.
type recordingWriter struct {
	http.ResponseWriter
	status int
	buf    bytes.Buffer
}

func (w *recordingWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *recordingWriter) Write(b []byte) (int, error) {
	w.buf.Write(b)
	return w.ResponseWriter.Write(b)
}
