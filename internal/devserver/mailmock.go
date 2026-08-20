package devserver

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MailMock is a dev-only email inbox. Messages arrive via
// /__hamr/mail/ingest (POST JSON), are stored in a ring buffer, and are
// viewable at /__hamr/mail on the reverse proxy. If persistPath is set, the
// inbox is mirrored to an mbox file on disk so it survives hamr dev restart.
type MailMock struct {
	maxMessages     int
	maxMessageBytes int64
	persistPath     string      // "" disables persistence
	persistErr      func(error) // callback for persist errors; nil logs via default logger

	mu       sync.RWMutex
	messages []*mailMessage // newest-last; truncated from front when over cap
}

// mailMessage is the stored form: the original payload plus metadata derived
// at ingest time. Addresses, attachments, etc. follow email.Message's JSON
// shape; we decode it loosely here to avoid coupling internal/devserver to
// pkg/email (which would create an internal→pkg import cycle in tests).
//
// Mutable state (Status, StatusNote) is protected by MailMock.mu. Accessors
// return deep copies so callers never read while another goroutine mutates.
type mailMessage struct {
	ID         string            `json:"id"`
	ReceivedAt time.Time         `json:"received_at"`
	Status     string            `json:"status"` // "delivered", "failed", "delayed"
	StatusNote string            `json:"status_note,omitempty"`
	From       mailAddress       `json:"From"`
	To         []mailAddress     `json:"To"`
	Cc         []mailAddress     `json:"Cc,omitempty"`
	Bcc        []mailAddress     `json:"Bcc,omitempty"`
	ReplyTo    *mailAddress      `json:"ReplyTo,omitempty"`
	Subject    string            `json:"Subject"`
	Text       string            `json:"Text,omitempty"`
	HTML       string            `json:"HTML,omitempty"`
	Attach     []mailAttachment  `json:"Attachments,omitempty"`
	Inline     []mailAttachment  `json:"Inline,omitempty"`
	Headers    map[string]string `json:"Headers,omitempty"`
	Tags       map[string]string `json:"Tags,omitempty"`
}

type mailAddress struct {
	Name  string `json:"Name,omitempty"`
	Email string `json:"Email"`
}

func (a mailAddress) Display() string {
	if a.Name == "" {
		return a.Email
	}
	return fmt.Sprintf("%s <%s>", a.Name, a.Email)
}

// UnmarshalJSON accepts either a bare email string ("a@b.com") or the full
// {"Name":...,"Email":...} object. The string form lets MCP mail.ingest callers
// pass the same joined-email shape that mail.list/mail.get render, instead of
// forcing the object form the schema would otherwise demand. Object senders
// (the normal /__hamr/mail ingest path) are unaffected.
func (a *mailAddress) UnmarshalJSON(data []byte) error {
	if s := strings.TrimSpace(string(data)); strings.HasPrefix(s, `"`) {
		var email string
		if err := json.Unmarshal(data, &email); err != nil {
			return err
		}
		a.Name, a.Email = "", email
		return nil
	}
	type alias mailAddress // avoid recursing into this method
	var v alias
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*a = mailAddress(v)
	return nil
}

type mailAttachment struct {
	Filename    string `json:"Filename"`
	ContentType string `json:"ContentType,omitempty"`
	Data        []byte `json:"Data"` // JSON base64-encodes []byte automatically
	ContentID   string `json:"ContentID,omitempty"`
}

// cloneMessage returns a deep copy of msg. The Data byte slices on attachments
// are shared with the original — they are write-once (set at ingest, never
// mutated) so sharing is safe and avoids copying large blobs.
func cloneMessage(msg *mailMessage) *mailMessage {
	if msg == nil {
		return nil
	}
	out := *msg // copies scalar + pointer-ish fields
	out.To = append([]mailAddress(nil), msg.To...)
	out.Cc = append([]mailAddress(nil), msg.Cc...)
	out.Bcc = append([]mailAddress(nil), msg.Bcc...)
	if msg.ReplyTo != nil {
		rt := *msg.ReplyTo
		out.ReplyTo = &rt
	}
	out.Attach = append([]mailAttachment(nil), msg.Attach...)
	out.Inline = append([]mailAttachment(nil), msg.Inline...)
	out.Headers = cloneStringMap(msg.Headers)
	out.Tags = cloneStringMap(msg.Tags)
	return &out
}

func cloneStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	maps.Copy(out, m)
	return out
}

// MailMockOptions configures a MailMock at construction.
type MailMockOptions struct {
	MaxMessages     int         // default 500
	MaxMessageBytes int64       // default 10 MiB
	PersistPath     string      // "" disables persistence
	OnPersistError  func(error) // invoked on disk write/read errors; nil is silent
}

// NewMailMock returns a MailMock with the given options. If PersistPath is
// non-empty, any existing inbox at that path is loaded and subsequent changes
// are mirrored there. Load failures are reported via OnPersistError (if set)
// but never fatal — the in-memory inbox starts empty.
func NewMailMock(opts MailMockOptions) *MailMock {
	if opts.MaxMessages <= 0 {
		opts.MaxMessages = 500
	}
	if opts.MaxMessageBytes <= 0 {
		opts.MaxMessageBytes = 10 * 1024 * 1024
	}
	m := &MailMock{
		maxMessages:     opts.MaxMessages,
		maxMessageBytes: opts.MaxMessageBytes,
		persistPath:     opts.PersistPath,
		persistErr:      opts.OnPersistError,
	}
	if m.persistPath != "" {
		m.loadFromDisk()
	}
	return m
}

// loadFromDisk reads the persisted inbox and populates memory. Corrupt entries
// are skipped; a missing file is not an error. If more than maxMessages were
// persisted, the oldest are dropped AND the file is rewritten to match.
func (m *MailMock) loadFromDisk() {
	msgs, err := readMboxInbox(m.persistPath)
	if err != nil {
		m.reportPersistErr(err)
		return
	}
	if len(msgs) == 0 {
		return
	}
	// Oldest first. If over cap, drop oldest and rewrite file.
	trimmed := false
	if len(msgs) > m.maxMessages {
		msgs = msgs[len(msgs)-m.maxMessages:]
		trimmed = true
	}
	m.messages = msgs
	if trimmed {
		if err := writeMboxInbox(m.persistPath, m.messages); err != nil {
			m.reportPersistErr(err)
		}
	}
}

func (m *MailMock) reportPersistErr(err error) {
	if m.persistErr != nil {
		m.persistErr(err)
	}
}

// RegisterRoutes mounts both the UI and the ingest endpoint on mux. Do not
// register twice on the same mux — http.ServeMux panics on duplicate patterns.
func (m *MailMock) RegisterRoutes(mux *http.ServeMux) {
	m.RegisterUIRoutes(mux)
	m.RegisterIngestRoutes(mux)
}

// RegisterUIRoutes mounts the human-facing inbox UI. Split from the ingest
// sink so the two can live on separate listeners (see `hamr mock-serve`).
func (m *MailMock) RegisterUIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/__hamr/mail", guardUnsafe(m.handleInboxOrDetail))
	mux.HandleFunc("/__hamr/mail/", guardUnsafe(m.handleInboxOrDetail))
}

// RegisterIngestRoutes mounts the SMTP capture sink. handleIngest is
// server-to-server (no browser Origin) — intentionally NOT origin-guarded;
// see guardUnsafe.
func (m *MailMock) RegisterIngestRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/__hamr/mail/ingest", m.handleIngest)
}

// handleIngest accepts POST application/json of an email.Message shape and
// appends it to the inbox. Returns {id} on success.
//
// Magic-recipient failure simulation:
//   - any recipient whose local-part equals "bounce"  → 422 {"error":"bounced"}
//   - any recipient whose local-part equals "reject"  → 422 {"error":"rejected"}
//
// Recipients are checked across To, Cc, Bcc. Matching addresses short-circuit
// storage so apps can test error paths deterministically.
func (m *MailMock) handleIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Enforce byte cap before fully reading the body.
	r.Body = http.MaxBytesReader(w, r.Body, m.maxMessageBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "message too large"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read body: " + err.Error()})
		return
	}

	var msg mailMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return
	}

	// Magic-address rejection runs before storage. Checked across To, Cc, Bcc.
	for _, group := range [][]mailAddress{msg.To, msg.Cc, msg.Bcc} {
		for _, a := range group {
			switch localPart(a.Email) {
			case "bounce":
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "bounced"})
				return
			case "reject":
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "rejected"})
				return
			}
		}
	}

	// Server-assigned fields take precedence over anything a caller sent.
	msg.ID = newMessageID()
	msg.ReceivedAt = time.Now()
	msg.Status = "delivered"

	m.append(&msg)

	writeJSON(w, http.StatusOK, map[string]string{"ID": msg.ID})
}

// append adds msg to the inbox, evicting the oldest message if the buffer is
// full. Maintains newest-last ordering to make UI iteration natural.
//
// Persistence: if at cap, the whole file is rewritten (eviction); otherwise
// the new message is appended to the mbox file via O_APPEND (fast path).
func (m *MailMock) append(msg *mailMessage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	evicted := false
	if len(m.messages) >= m.maxMessages {
		m.messages = m.messages[1:] // drop oldest
		evicted = true
	}
	m.messages = append(m.messages, msg)

	if m.persistPath == "" {
		return
	}
	if evicted {
		if err := writeMboxInbox(m.persistPath, m.messages); err != nil {
			m.reportPersistErr(err)
		}
		return
	}
	if err := appendMboxMessage(m.persistPath, msg); err != nil {
		m.reportPersistErr(err)
	}
}

// List returns a newest-first snapshot of the inbox. Messages are deep-copied
// so callers can read fields without racing concurrent mutators (SetStatus).
func (m *MailMock) List() []*mailMessage {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*mailMessage, len(m.messages))
	for i, msg := range m.messages {
		out[len(m.messages)-1-i] = cloneMessage(msg)
	}
	return out
}

// Get returns a deep copy of the message with the given id, or nil if not
// found. The copy decouples callers from concurrent mutation.
func (m *MailMock) Get(id string) *mailMessage {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, msg := range m.messages {
		if msg.ID == id {
			return cloneMessage(msg)
		}
	}
	return nil
}

// Delete removes a single message by id. Returns true if it existed.
func (m *MailMock) Delete(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, msg := range m.messages {
		if msg.ID == id {
			m.messages = append(m.messages[:i], m.messages[i+1:]...)
			if m.persistPath != "" {
				if err := writeMboxInbox(m.persistPath, m.messages); err != nil {
					m.reportPersistErr(err)
				}
			}
			return true
		}
	}
	return false
}

// Clear empties the inbox.
func (m *MailMock) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = nil
	if m.persistPath != "" {
		if err := writeMboxInbox(m.persistPath, nil); err != nil {
			m.reportPersistErr(err)
		}
	}
}

// SetStatus marks a stored message as having a particular outcome. Used by the
// UI to simulate post-hoc bounce/delay on an already-captured message.
// Allowed values: "failed", "delayed" ("delivered" is the implicit default set
// at ingest and cannot be re-applied here). Unknown values are rejected.
//
// Persistence: rewrites the whole mbox file (status is in the headers).
func (m *MailMock) SetStatus(id, status, note string) bool {
	switch status {
	case "failed", "delayed":
	default:
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, msg := range m.messages {
		if msg.ID == id {
			msg.Status = status
			msg.StatusNote = note
			if m.persistPath != "" {
				if err := writeMboxInbox(m.persistPath, m.messages); err != nil {
					m.reportPersistErr(err)
				}
			}
			return true
		}
	}
	return false
}

// handleInboxOrDetail dispatches /__hamr/mail, /__hamr/mail/, and all deeper
// paths. When both RegisterUIRoutes and RegisterIngestRoutes share a mux, the
// /__hamr/mail/ingest exact pattern takes precedence over the subtree. In
// split-port mode (uiMux only has RegisterUIRoutes), /__hamr/mail/ingest
// requests still reach this handler via the subtree — they resolve as an
// unknown message ID and return 404, so ingest is not processed.
func (m *MailMock) handleInboxOrDetail(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/__hamr/mail")
	p = strings.TrimPrefix(p, "/")

	switch p {
	case "", "/":
		if r.Method == http.MethodGet {
			m.handleInbox(w, r)
			return
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)

	case "clear":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		m.Clear()
		http.Redirect(w, r, "/__hamr/mail", http.StatusSeeOther)

	default:
		// /__hamr/mail/<id>[/...]
		parts := strings.SplitN(p, "/", 3)
		id := parts[0]
		msg := m.Get(id)
		if msg == nil {
			http.Error(w, "message not found", http.StatusNotFound)
			return
		}

		if len(parts) == 1 {
			if r.Method == http.MethodGet {
				m.handleDetail(w, r, msg)
				return
			}
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		sub := parts[1]
		tail := ""
		if len(parts) > 2 {
			tail = parts[2]
		}

		switch sub {
		case "html":
			if r.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			m.handleHTMLFrame(w, r, msg)
		case "attachment":
			if r.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			m.handleAttachment(w, r, msg, tail)
		case "inline":
			if r.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			m.handleInline(w, r, msg, tail)
		case "delete":
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			m.Delete(id)
			http.Redirect(w, r, "/__hamr/mail", http.StatusSeeOther)
		case "fail":
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			note := r.FormValue("note")
			if note == "" {
				note = "marked failed via dev UI"
			}
			m.SetStatus(id, "failed", note)
			http.Redirect(w, r, "/__hamr/mail/"+id, http.StatusSeeOther)
		case "delay":
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			secsStr := r.FormValue("seconds")
			secs, _ := strconv.Atoi(secsStr)
			if secs <= 0 {
				secs = 30
			}
			m.SetStatus(id, "delayed", fmt.Sprintf("delayed %ds via dev UI", secs))
			http.Redirect(w, r, "/__hamr/mail/"+id, http.StatusSeeOther)
		default:
			http.NotFound(w, r)
		}
	}
}

func (m *MailMock) handleAttachment(w http.ResponseWriter, _ *http.Request, msg *mailMessage, tail string) {
	idx, err := strconv.Atoi(tail)
	if err != nil || idx < 0 || idx >= len(msg.Attach) {
		http.NotFound(w, nil)
		return
	}
	a := msg.Attach[idx]
	ct := a.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", contentDispositionAttachment(a.Filename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Write(a.Data) //nolint:errcheck
}

func (m *MailMock) handleInline(w http.ResponseWriter, _ *http.Request, msg *mailMessage, tail string) {
	for _, a := range msg.Inline {
		if a.ContentID == tail {
			ct := a.ContentType
			if ct == "" {
				ct = "application/octet-stream"
			}
			w.Header().Set("Content-Type", ct)
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("Cache-Control", "no-store")
			// Inline content is served on the dev-proxy origin and can't use
			// Content-Disposition: attachment (it's referenced via cid: in img
			// tags). A message could carry an inline part with Content-Type
			// text/html and a script payload — the sandbox CSP makes the browser
			// treat the response as an isolated, script-disabled document, so
			// even an HTML inline part can't run code on the app's origin.
			w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
			w.Write(a.Data) //nolint:errcheck
			return
		}
	}
	http.NotFound(w, nil)
}

// contentDispositionAttachment builds an RFC 6266 Content-Disposition header.
// mime.FormatMediaType handles the RFC 5987 ext-value encoding; when it emits
// the authoritative filename* form (non-ASCII or control chars in name) we
// prepend a sanitized plain-ASCII filename as a fallback for legacy parsers.
// Keep in sync with the copy in pkg/storage/storage.go (importing pkg/storage
// here would pull the AWS SDK into the dev binary).
func contentDispositionAttachment(name string) string {
	name = strings.ToValidUTF8(name, "_")
	if name == "" {
		return "attachment"
	}
	cd := mime.FormatMediaType("attachment", map[string]string{"filename": name})
	if cd == "" {
		return "attachment"
	}
	if !strings.Contains(cd, "filename*") {
		return cd
	}
	ascii := strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7E || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	return mime.FormatMediaType("attachment", map[string]string{"filename": ascii}) + strings.TrimPrefix(cd, "attachment")
}

// checkSameOrigin rejects state-changing POSTs whose Origin header is present
// and does not match the request's Host. Absent Origin (curl, tests,
// non-browser clients) is allowed — the mock is localhost-only and the check
// exists only to blunt drive-by CSRF from malicious pages opened in the same
// browser during `hamr dev`.
func checkSameOrigin(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host != r.Host {
		http.Error(w, "cross-origin request refused", http.StatusForbidden)
		return false
	}
	return true
}

// guardUnsafe wraps a dev-mock handler so state-changing requests (anything but
// a safe method) are rejected when their Origin is present and cross-origin —
// the drive-by-CSRF defense, declared once at registration instead of being
// re-derived inside every handler body. Safe methods pass straight through, so
// GET page renders and the GET/POST multiplexing handlers are unaffected.
//
// Routes that must NOT be guarded are simply registered without this wrapper:
// the server-to-server /v1 Stripe API (hit by the app's SDK, never a browser)
// and the /__hamr/mail/ingest and /__hamr/sms/ingest sinks. Keeping those carve-outs at the
// registration site makes them visible rather than buried in handler logic.
func guardUnsafe(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if !checkSameOrigin(w, r) {
				return
			}
		}
		h(w, r)
	}
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func newMessageID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "msg_" + hex.EncodeToString(b)
}

func localPart(email string) string {
	before, _, ok := strings.Cut(email, "@")
	if !ok {
		return strings.ToLower(email)
	}
	return strings.ToLower(before)
}
