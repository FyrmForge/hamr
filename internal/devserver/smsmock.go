package devserver

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// SMSMock is a dev-only SMS inbox. Messages arrive via /__hamr/sms/ingest
// (POST JSON), are stored in a ring buffer, and are viewable at /__hamr/sms
// on the reverse proxy. If persistPath is set, the inbox is mirrored to a
// JSONL file on disk so it survives hamr dev restart.
type SMSMock struct {
	maxMessages int
	persistPath string      // "" disables persistence
	persistErr  func(error) // callback for persist errors; nil is silent

	mu       sync.RWMutex
	messages []*smsMessage // newest-last; truncated from front when over cap
}

// smsIngestMaxBytes caps a single ingest body. SMS bodies are tiny; the cap
// only guards against a runaway client.
const smsIngestMaxBytes = 64 * 1024

// smsMessage is the stored form: the sms.Message payload plus metadata derived
// at ingest time. Field JSON names match sms.Message's default marshaling so
// the smsmock client can POST a Message verbatim (decoded loosely here to
// avoid an internal→pkg import cycle in tests, mirroring mailMessage).
//
// Mutable state (Status, StatusNote) is protected by SMSMock.mu. Accessors
// return copies so callers never read while another goroutine mutates.
type smsMessage struct {
	ID         string            `json:"id"`
	ReceivedAt time.Time         `json:"received_at"`
	Status     string            `json:"status"` // "delivered", "failed", "delayed"
	StatusNote string            `json:"status_note,omitempty"`
	From       string `json:"From"`
	To         string `json:"To"`
	Body       string `json:"Body"`
	Ref        string `json:"Ref,omitempty"`
}

func cloneSMSMessage(msg *smsMessage) *smsMessage {
	if msg == nil {
		return nil
	}
	out := *msg
	return &out
}

// SMSMockOptions configures an SMSMock at construction.
type SMSMockOptions struct {
	MaxMessages    int         // default 500
	PersistPath    string      // "" disables persistence
	OnPersistError func(error) // invoked on disk write/read errors; nil is silent
}

// NewSMSMock returns an SMSMock with the given options. If PersistPath is
// non-empty, any existing inbox at that path is loaded and subsequent changes
// are mirrored there. Load failures are reported via OnPersistError (if set)
// but never fatal — the in-memory inbox starts empty.
func NewSMSMock(opts SMSMockOptions) *SMSMock {
	if opts.MaxMessages <= 0 {
		opts.MaxMessages = 500
	}
	m := &SMSMock{
		maxMessages: opts.MaxMessages,
		persistPath: opts.PersistPath,
		persistErr:  opts.OnPersistError,
	}
	if m.persistPath != "" {
		m.loadFromDisk()
	}
	return m
}

func (m *SMSMock) reportPersistErr(err error) {
	if m.persistErr != nil {
		m.persistErr(err)
	}
}

// --- persistence (JSONL: one message per line, oldest first) ---

// loadFromDisk reads the persisted inbox and populates memory. Corrupt lines
// are skipped; a missing file is not an error. If more than maxMessages were
// persisted, the oldest are dropped AND the file is rewritten to match.
func (m *SMSMock) loadFromDisk() {
	f, err := os.Open(m.persistPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			m.reportPersistErr(fmt.Errorf("sms inbox: open %s: %w", m.persistPath, err))
		}
		return
	}
	defer f.Close() //nolint:errcheck

	var msgs []*smsMessage
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), smsIngestMaxBytes+4096)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var msg smsMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil || msg.ID == "" {
			continue // corrupt line: skip
		}
		msgs = append(msgs, &msg)
	}
	if err := sc.Err(); err != nil {
		m.reportPersistErr(fmt.Errorf("sms inbox: read %s: %w", m.persistPath, err))
	}
	if len(msgs) == 0 {
		return
	}
	trimmed := false
	if len(msgs) > m.maxMessages {
		msgs = msgs[len(msgs)-m.maxMessages:]
		trimmed = true
	}
	m.messages = msgs
	if trimmed {
		if err := writeSMSInbox(m.persistPath, m.messages); err != nil {
			m.reportPersistErr(err)
		}
	}
}

// writeSMSInbox atomically rewrites the whole inbox file (oldest first).
// A nil/empty slice writes an empty file.
func writeSMSInbox(path string, msgs []*smsMessage) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("sms inbox: mkdir: %w", err)
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("sms inbox: create %s: %w", tmp, err)
	}
	enc := json.NewEncoder(f)
	for _, msg := range msgs {
		if err := enc.Encode(msg); err != nil {
			f.Close()      //nolint:errcheck
			os.Remove(tmp) //nolint:errcheck
			return fmt.Errorf("sms inbox: encode: %w", err)
		}
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp) //nolint:errcheck
		return fmt.Errorf("sms inbox: close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp) //nolint:errcheck
		return fmt.Errorf("sms inbox: rename: %w", err)
	}
	return nil
}

// appendSMSMessage appends one message line via O_APPEND (fast path).
func appendSMSMessage(path string, msg *smsMessage) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("sms inbox: mkdir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("sms inbox: open %s: %w", path, err)
	}
	defer f.Close() //nolint:errcheck
	if err := json.NewEncoder(f).Encode(msg); err != nil {
		return fmt.Errorf("sms inbox: append: %w", err)
	}
	return nil
}

// --- routes ---

// RegisterRoutes mounts both the UI and the ingest endpoint on mux. Do not
// register twice on the same mux — http.ServeMux panics on duplicate patterns.
func (m *SMSMock) RegisterRoutes(mux *http.ServeMux) {
	m.RegisterUIRoutes(mux)
	m.RegisterIngestRoutes(mux)
}

// RegisterUIRoutes mounts the human-facing inbox UI. Split from the ingest
// sink so the two can live on separate listeners (see `hamr mock-serve`).
func (m *SMSMock) RegisterUIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/__hamr/sms", guardUnsafe(m.handleInboxOrDetail))
	mux.HandleFunc("/__hamr/sms/", guardUnsafe(m.handleInboxOrDetail))
}

// RegisterIngestRoutes mounts the capture sink. handleIngest is
// server-to-server (no browser Origin) — intentionally NOT origin-guarded;
// see guardUnsafe.
func (m *SMSMock) RegisterIngestRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/__hamr/sms/ingest", m.handleIngest)
}

// handleIngest accepts POST application/json of an sms.Message shape and
// appends it to the inbox. Returns {ID} on success.
//
// Magic-recipient failure simulation (digits-only suffix of To):
//   - ends in "5550001" → 422 {"error":"invalid_number"}
//   - ends in "5550002" → 422 {"error":"undeliverable"}
//
// Matching numbers short-circuit storage so apps can test error paths
// deterministically.
func (m *SMSMock) handleIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, smsIngestMaxBytes)
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

	var msg smsMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return
	}

	if reason := smsMagicNumberRefusal(msg.To); reason != "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": reason})
		return
	}

	// Server-assigned fields take precedence over anything a caller sent.
	msg.ID = newSMSMessageID()
	msg.ReceivedAt = time.Now()
	msg.Status = "delivered"

	m.append(&msg)

	writeJSON(w, http.StatusOK, map[string]string{"ID": msg.ID})
}

// smsMagicNumberRefusal returns the refusal reason for magic recipient
// numbers, or "" for a deliverable number. The suffixes include the
// fictional 555 exchange (never assigned in NANP) so real recipient numbers
// that merely end in 0001/0002 are not swallowed. Formatting characters are
// ignored: "+1 500 555-0001" matches the "5550001" suffix.
func smsMagicNumberRefusal(to string) string {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, to)
	switch {
	case strings.HasSuffix(digits, "5550001"):
		return "invalid_number"
	case strings.HasSuffix(digits, "5550002"):
		return "undeliverable"
	}
	return ""
}

func newSMSMessageID() string {
	return "sms_" + strings.TrimPrefix(newMessageID(), "msg_")
}

// append adds msg to the inbox, evicting the oldest message if the buffer is
// full. Maintains newest-last ordering.
//
// Persistence: if at cap, the whole file is rewritten (eviction); otherwise
// the new message is appended to the JSONL file via O_APPEND (fast path).
func (m *SMSMock) append(msg *smsMessage) {
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
		if err := writeSMSInbox(m.persistPath, m.messages); err != nil {
			m.reportPersistErr(err)
		}
		return
	}
	if err := appendSMSMessage(m.persistPath, msg); err != nil {
		m.reportPersistErr(err)
	}
}

// List returns a newest-first snapshot of the inbox. Messages are copied so
// callers can read fields without racing concurrent mutators (SetStatus).
func (m *SMSMock) List() []*smsMessage {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*smsMessage, len(m.messages))
	for i, msg := range m.messages {
		out[len(m.messages)-1-i] = cloneSMSMessage(msg)
	}
	return out
}

// Get returns a copy of the message with the given id, or nil if not found.
func (m *SMSMock) Get(id string) *smsMessage {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, msg := range m.messages {
		if msg.ID == id {
			return cloneSMSMessage(msg)
		}
	}
	return nil
}

// Delete removes a single message by id. Returns true if it existed.
func (m *SMSMock) Delete(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, msg := range m.messages {
		if msg.ID == id {
			m.messages = append(m.messages[:i], m.messages[i+1:]...)
			if m.persistPath != "" {
				if err := writeSMSInbox(m.persistPath, m.messages); err != nil {
					m.reportPersistErr(err)
				}
			}
			return true
		}
	}
	return false
}

// Clear empties the inbox.
func (m *SMSMock) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = nil
	if m.persistPath != "" {
		if err := writeSMSInbox(m.persistPath, nil); err != nil {
			m.reportPersistErr(err)
		}
	}
}

// SetStatus marks a stored message as having a particular outcome. Used by
// the UI to simulate post-hoc failure/delay on an already-captured message.
// Allowed values: "failed", "delayed" ("delivered" is the implicit default
// set at ingest and cannot be re-applied here). Unknown values are rejected.
//
// Persistence: rewrites the whole JSONL file.
func (m *SMSMock) SetStatus(id, status, note string) bool {
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
				if err := writeSMSInbox(m.persistPath, m.messages); err != nil {
					m.reportPersistErr(err)
				}
			}
			return true
		}
	}
	return false
}

// handleInboxOrDetail dispatches /__hamr/sms, /__hamr/sms/, and all deeper
// paths. When both RegisterUIRoutes and RegisterIngestRoutes share a mux, the
// /__hamr/sms/ingest exact pattern takes precedence over the subtree. In
// split-port mode (uiMux only has RegisterUIRoutes), /__hamr/sms/ingest
// requests still reach this handler via the subtree — they resolve as an
// unknown message ID and return 404, so ingest is not processed.
func (m *SMSMock) handleInboxOrDetail(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/__hamr/sms")
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
		http.Redirect(w, r, "/__hamr/sms", http.StatusSeeOther)

	default:
		// /__hamr/sms/<id>[/<action>]
		id, action, _ := strings.Cut(p, "/")
		msg := m.Get(id)
		if msg == nil {
			http.Error(w, "message not found", http.StatusNotFound)
			return
		}

		switch action {
		case "":
			if r.Method == http.MethodGet {
				m.handleDetail(w, r, msg)
				return
			}
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		case "delete":
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			m.Delete(id)
			http.Redirect(w, r, "/__hamr/sms", http.StatusSeeOther)
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
			http.Redirect(w, r, "/__hamr/sms/"+id, http.StatusSeeOther)
		case "delay":
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			secs, _ := strconv.Atoi(r.FormValue("seconds"))
			if secs <= 0 {
				secs = 30
			}
			m.SetStatus(id, "delayed", fmt.Sprintf("delayed %ds via dev UI", secs))
			http.Redirect(w, r, "/__hamr/sms/"+id, http.StatusSeeOther)
		default:
			http.NotFound(w, r)
		}
	}
}
