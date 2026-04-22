package devserver

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// mbox persistence format.
//
// The inbox is saved as a standard MBOXO mbox file. Each message is a real
// RFC 822 / MIME document separated by a `From ` line, so Thunderbird, mutt,
// Apple Mail, etc. can open it directly — and any text editor shows it as
// readable email.
//
// Fields not representable in standard MIME (our runtime Status, StatusNote,
// Tags, and the internal mock ID) round-trip via X-Hamr-* headers that are
// ignored by real mail clients.

const (
	xHamrID         = "X-Hamr-Id"
	xHamrStatus     = "X-Hamr-Status"
	xHamrStatusNote = "X-Hamr-Status-Note"
	xHamrReceived   = "X-Hamr-Received-At"
	xHamrTags       = "X-Hamr-Tags" // JSON-encoded map
)

// writeMboxInbox atomically rewrites path with the given messages (oldest
// first). Uses tmp + rename so a crash mid-write never leaves a half-written
// inbox.
func writeMboxInbox(path string, msgs []*mailMessage) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mailmock: mkdir persist dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".inbox.*.mbox.tmp")
	if err != nil {
		return fmt.Errorf("mailmock: create tmp: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) //nolint:errcheck

	w := bufio.NewWriter(tmp)
	for _, msg := range msgs {
		if err := writeMboxMessage(w, msg); err != nil {
			_ = tmp.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("mailmock: flush tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("mailmock: close tmp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("mailmock: rename tmp: %w", err)
	}
	return nil
}

// appendMboxMessage opens path in append-only mode and writes a single
// message. Fast path for ingest — no whole-file rewrite.
func appendMboxMessage(path string, msg *mailMessage) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mailmock: mkdir persist dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("mailmock: open for append: %w", err)
	}
	defer f.Close() //nolint:errcheck
	w := bufio.NewWriter(f)
	if err := writeMboxMessage(w, msg); err != nil {
		return err
	}
	return w.Flush()
}

// writeMboxMessage serializes one message as an mbox entry: the `From ` line,
// headers, a MIME body, and a trailing blank line separator.
func writeMboxMessage(w io.Writer, msg *mailMessage) error {
	// `From ` line (MBOXO-style). asctime with no comma.
	_, _ = fmt.Fprintf(w, "From hamr-mock@hamr.local %s\r\n", msg.ReceivedAt.Format(time.ANSIC))

	// Standard email headers.
	if msg.From.Email != "" {
		_, _ = fmt.Fprintf(w, "From: %s\r\n", formatAddress(msg.From))
	}
	if len(msg.To) > 0 {
		_, _ = fmt.Fprintf(w, "To: %s\r\n", formatAddressList(msg.To))
	}
	if len(msg.Cc) > 0 {
		_, _ = fmt.Fprintf(w, "Cc: %s\r\n", formatAddressList(msg.Cc))
	}
	if len(msg.Bcc) > 0 {
		_, _ = fmt.Fprintf(w, "Bcc: %s\r\n", formatAddressList(msg.Bcc))
	}
	if msg.ReplyTo != nil {
		_, _ = fmt.Fprintf(w, "Reply-To: %s\r\n", formatAddress(*msg.ReplyTo))
	}
	if msg.Subject != "" {
		_, _ = fmt.Fprintf(w, "Subject: %s\r\n", encodeHeaderValue(msg.Subject))
	}
	_, _ = fmt.Fprintf(w, "Date: %s\r\n", msg.ReceivedAt.Format(time.RFC1123Z))
	_, _ = fmt.Fprintf(w, "Message-ID: <%s@hamr.local>\r\n", msg.ID)
	_, _ = fmt.Fprintf(w, "MIME-Version: 1.0\r\n")

	// Custom user-supplied headers. Skip any that collide with reserved names
	// to keep the parse side unambiguous.
	headerNames := make([]string, 0, len(msg.Headers))
	for k := range msg.Headers {
		headerNames = append(headerNames, k)
	}
	sort.Strings(headerNames)
	for _, k := range headerNames {
		if isReservedHeader(k) {
			continue
		}
		_, _ = fmt.Fprintf(w, "%s: %s\r\n", k, encodeHeaderValue(msg.Headers[k]))
	}

	// hamr-specific metadata for round-tripping.
	_, _ = fmt.Fprintf(w, "%s: %s\r\n", xHamrID, msg.ID)
	_, _ = fmt.Fprintf(w, "%s: %s\r\n", xHamrStatus, msg.Status)
	if msg.StatusNote != "" {
		_, _ = fmt.Fprintf(w, "%s: %s\r\n", xHamrStatusNote, encodeHeaderValue(msg.StatusNote))
	}
	_, _ = fmt.Fprintf(w, "%s: %s\r\n", xHamrReceived, msg.ReceivedAt.UTC().Format(time.RFC3339Nano))
	if len(msg.Tags) > 0 {
		tagsJSON, _ := json.Marshal(msg.Tags)
		_, _ = fmt.Fprintf(w, "%s: %s\r\n", xHamrTags, string(tagsJSON))
	}

	// Body. The Content-Type header comes from writeMIMEBody.
	var bodyBuf bytes.Buffer
	ct, err := writeMIMEBody(&bodyBuf, msg)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(w, "Content-Type: %s\r\n", ct)
	// Header/body separator (one blank line).
	_, _ = fmt.Fprint(w, "\r\n")
	// MBOXO escape, then coerce the body to end with a single newline so the
	// mbox separator below guarantees a blank line before the next `From `.
	escaped := escapeMBoxBody(bodyBuf.String())
	if escaped != "" && !strings.HasSuffix(escaped, "\n") {
		escaped += "\n"
	}
	if _, err := io.WriteString(w, escaped); err != nil {
		return err
	}
	// Blank-line separator before the next entry's `From ` line (or EOF).
	_, _ = fmt.Fprint(w, "\n")
	return nil
}

// escapeMBoxBody applies MBOXO `>From ` escaping to lines in body. Preserves
// the original byte stream otherwise — no added or stripped trailing newlines.
func escapeMBoxBody(body string) string {
	if body == "" {
		return body
	}
	// Work in LF space; mbox readers are tolerant of LF-only.
	normalized := strings.ReplaceAll(body, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "From ") {
			lines[i] = ">" + line
		}
	}
	return strings.Join(lines, "\n")
}

// writeMIMEBody produces the message body as MIME and returns the Content-Type
// header value for it. Hand-rolled with explicit boundaries (simpler than
// nesting multipart.Writer). Shapes:
//
//	no body            → text/plain (empty)
//	text only          → text/plain
//	html only          → text/html
//	text + html        → multipart/alternative
//	+ inline           → wrap body in multipart/related
//	+ attachments      → wrap outermost in multipart/mixed
func writeMIMEBody(w io.Writer, msg *mailMessage) (string, error) {
	hasText := msg.Text != ""
	hasHTML := msg.HTML != ""
	hasInline := len(msg.Inline) > 0
	hasAttach := len(msg.Attach) > 0

	// Simple leaf cases (no multipart wrappers).
	switch {
	case !hasText && !hasHTML && !hasAttach && !hasInline:
		return `text/plain; charset="utf-8"`, nil
	case hasText && !hasHTML && !hasAttach && !hasInline:
		_, err := io.WriteString(w, msg.Text)
		return `text/plain; charset="utf-8"`, err
	case hasHTML && !hasText && !hasAttach && !hasInline:
		_, err := io.WriteString(w, msg.HTML)
		return `text/html; charset="utf-8"`, err
	}

	// Build inner body payload (the "related" or "alternative" or single part
	// that carries text/html plus inline). Then wrap in mixed if attachments.
	var inner bytes.Buffer
	innerCT := writeBodyAndInline(&inner, msg, hasText, hasHTML, hasInline)

	if !hasAttach {
		_, err := w.Write(inner.Bytes())
		return innerCT, err
	}

	// Outer multipart/mixed: [inner body] + attachment parts.
	mixedB := newBoundary("mix")
	_, _ = fmt.Fprintf(w, "--%s\r\n", mixedB)
	_, _ = fmt.Fprintf(w, "Content-Type: %s\r\n\r\n", innerCT)
	w.Write(inner.Bytes()) //nolint:errcheck
	_, _ = fmt.Fprintf(w, "\r\n")
	for _, a := range msg.Attach {
		writeMIMEAttachment(w, mixedB, a, false)
	}
	_, _ = fmt.Fprintf(w, "--%s--\r\n", mixedB)
	return fmt.Sprintf("multipart/mixed; boundary=%q", mixedB), nil
}

// writeBodyAndInline writes the body payload (text/html, optionally wrapped
// in alternative, and optionally followed by inline images inside related)
// into w and returns its Content-Type.
func writeBodyAndInline(w io.Writer, msg *mailMessage, hasText, hasHTML, hasInline bool) string {
	// Inner "content" part: single text/html or an alternative wrapper.
	var bodyBuf bytes.Buffer
	bodyCT := writeAltOrSingle(&bodyBuf, msg.Text, msg.HTML, hasText, hasHTML)

	if !hasInline {
		w.Write(bodyBuf.Bytes()) //nolint:errcheck
		return bodyCT
	}

	// Wrap body + inline images in multipart/related.
	relB := newBoundary("rel")
	_, _ = fmt.Fprintf(w, "--%s\r\n", relB)
	_, _ = fmt.Fprintf(w, "Content-Type: %s\r\n\r\n", bodyCT)
	w.Write(bodyBuf.Bytes()) //nolint:errcheck
	_, _ = fmt.Fprintf(w, "\r\n")
	for _, a := range msg.Inline {
		writeMIMEAttachment(w, relB, a, true)
	}
	_, _ = fmt.Fprintf(w, "--%s--\r\n", relB)
	return fmt.Sprintf("multipart/related; boundary=%q", relB)
}

// writeAltOrSingle writes either a single text/plain or text/html body, or a
// multipart/alternative of both, and returns its Content-Type.
func writeAltOrSingle(w io.Writer, text, html string, hasText, hasHTML bool) string {
	switch {
	case hasText && hasHTML:
		altB := newBoundary("alt")
		_, _ = fmt.Fprintf(w, "--%s\r\nContent-Type: text/plain; charset=\"utf-8\"\r\n\r\n%s\r\n", altB, text)
		_, _ = fmt.Fprintf(w, "--%s\r\nContent-Type: text/html; charset=\"utf-8\"\r\n\r\n%s\r\n", altB, html)
		_, _ = fmt.Fprintf(w, "--%s--\r\n", altB)
		return fmt.Sprintf("multipart/alternative; boundary=%q", altB)
	case hasText:
		io.WriteString(w, text) //nolint:errcheck
		return `text/plain; charset="utf-8"`
	case hasHTML:
		io.WriteString(w, html) //nolint:errcheck
		return `text/html; charset="utf-8"`
	}
	return `text/plain; charset="utf-8"`
}

// writeMIMEAttachment writes a single MIME attachment or inline part bounded
// by the given boundary.
func writeMIMEAttachment(w io.Writer, boundary string, a mailAttachment, inline bool) {
	ct := a.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	_, _ = fmt.Fprintf(w, "--%s\r\n", boundary)
	_, _ = fmt.Fprintf(w, "Content-Type: %s\r\n", ct)
	_, _ = fmt.Fprintf(w, "Content-Transfer-Encoding: base64\r\n")
	if inline {
		_, _ = fmt.Fprintf(w, "Content-Disposition: inline; filename=%q\r\n", a.Filename)
		if a.ContentID != "" {
			_, _ = fmt.Fprintf(w, "Content-ID: <%s>\r\n", a.ContentID)
		}
	} else {
		_, _ = fmt.Fprintf(w, "Content-Disposition: attachment; filename=%q\r\n", a.Filename)
	}
	_, _ = fmt.Fprint(w, "\r\n")
	writeBase64(w, a.Data)
	_, _ = fmt.Fprint(w, "\r\n")
}

// writeBase64 writes data as base64 wrapped at 76 columns (RFC 2045).
func writeBase64(w io.Writer, data []byte) {
	const width = 76
	enc := base64.StdEncoding.EncodeToString(data)
	for i := 0; i < len(enc); i += width {
		end := i + width
		if end > len(enc) {
			end = len(enc)
		}
		_, _ = fmt.Fprintln(w, enc[i:end])
	}
}

func formatAddress(a mailAddress) string {
	addr := mail.Address{Name: a.Name, Address: a.Email}
	return addr.String()
}

func formatAddressList(list []mailAddress) string {
	parts := make([]string, len(list))
	for i, a := range list {
		parts[i] = formatAddress(a)
	}
	return strings.Join(parts, ", ")
}

// encodeHeaderValue applies RFC 2047 Q-encoding when the value has non-ASCII
// or CR/LF. Plain ASCII values are returned as-is.
func encodeHeaderValue(s string) string {
	for _, r := range s {
		if r > 0x7F || r == '\r' || r == '\n' {
			return mime.QEncoding.Encode("utf-8", s)
		}
	}
	return s
}

// newBoundary returns a unique MIME boundary with the given short tag prefix.
func newBoundary(tag string) string {
	return fmt.Sprintf("=_hamr_%s_%d", tag, time.Now().UnixNano())
}

var reservedHeaders = map[string]bool{
	// Email headers we generate ourselves.
	"from": true, "to": true, "cc": true, "bcc": true, "reply-to": true,
	"subject": true, "date": true, "message-id": true, "mime-version": true,
	"content-type": true, "content-transfer-encoding": true,
	// Our own metadata namespace.
	"x-hamr-id": true, "x-hamr-status": true, "x-hamr-status-note": true,
	"x-hamr-received-at": true, "x-hamr-tags": true,
}

func isReservedHeader(name string) bool {
	return reservedHeaders[strings.ToLower(name)]
}

// --- reader ---

// readMboxInbox parses path as an mbox file and returns the stored messages
// (oldest first). Corrupt messages are skipped with a best-effort attempt to
// recover the next message boundary. Returns (nil, nil) if path does not
// exist.
func readMboxInbox(path string) ([]*mailMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("mailmock: read mbox: %w", err)
	}
	if len(data) == 0 {
		return nil, nil
	}

	// Split on lines starting with "From " at column 0 preceded by a blank
	// line. The first message doesn't need a preceding blank line.
	var msgs []*mailMessage
	for _, raw := range splitMboxEntries(data) {
		msg, err := parseMboxEntry(raw)
		if err != nil {
			// Corrupt entry — skip, don't abort the whole load.
			continue
		}
		if msg != nil {
			msgs = append(msgs, msg)
		}
	}
	return msgs, nil
}

// splitMboxEntries cuts an mbox blob into per-message byte slices. Each entry
// includes the header-opening `From ` line through to (but not including) the
// next `From ` line.
func splitMboxEntries(data []byte) [][]byte {
	var entries [][]byte
	var current bytes.Buffer
	var prevBlank = true // a virtual blank line at start so leading `From ` starts an entry

	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		isFromLine := bytes.HasPrefix(line, []byte("From "))
		if isFromLine && prevBlank {
			if current.Len() > 0 {
				cp := make([]byte, current.Len())
				copy(cp, current.Bytes())
				entries = append(entries, cp)
			}
			current.Reset()
		}
		current.Write(line)
		current.WriteByte('\n')
		prevBlank = len(line) == 0
	}
	if current.Len() > 0 {
		entries = append(entries, current.Bytes())
	}
	return entries
}

// parseMboxEntry parses one mbox entry (starting with `From `) into a stored
// message. Returns nil, nil if the entry has no usable headers (e.g. empty).
func parseMboxEntry(raw []byte) (*mailMessage, error) {
	// Strip the leading `From ` line.
	nl := bytes.IndexByte(raw, '\n')
	if nl < 0 {
		return nil, fmt.Errorf("mbox entry missing newline")
	}
	body := raw[nl+1:]

	// Reverse MBOXO escaping: `>From ` at line start → `From `.
	body = unescapeMBox(body)

	m, err := mail.ReadMessage(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("parse message: %w", err)
	}

	msg := &mailMessage{
		Headers: map[string]string{},
		Tags:    map[string]string{},
	}

	// Pull standard addresses.
	if v := m.Header.Get("From"); v != "" {
		if a, err := mail.ParseAddress(v); err == nil {
			msg.From = mailAddress{Name: a.Name, Email: a.Address}
		}
	}
	msg.To = parseAddressListHeader(m.Header.Get("To"))
	msg.Cc = parseAddressListHeader(m.Header.Get("Cc"))
	msg.Bcc = parseAddressListHeader(m.Header.Get("Bcc"))
	if v := m.Header.Get("Reply-To"); v != "" {
		if a, err := mail.ParseAddress(v); err == nil {
			rt := mailAddress{Name: a.Name, Email: a.Address}
			msg.ReplyTo = &rt
		}
	}
	dec := &mime.WordDecoder{}
	subj, _ := dec.DecodeHeader(m.Header.Get("Subject"))
	msg.Subject = subj

	// Hamr metadata.
	msg.ID = m.Header.Get(xHamrID)
	if msg.ID == "" {
		// Fallback: generate if missing (old files).
		msg.ID = newMessageID()
	}
	msg.Status = m.Header.Get(xHamrStatus)
	if msg.Status == "" {
		msg.Status = "delivered"
	}
	if note, _ := dec.DecodeHeader(m.Header.Get(xHamrStatusNote)); note != "" {
		msg.StatusNote = note
	}
	if v := m.Header.Get(xHamrReceived); v != "" {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			msg.ReceivedAt = t
		}
	}
	if msg.ReceivedAt.IsZero() {
		if v := m.Header.Get("Date"); v != "" {
			if t, err := mail.ParseDate(v); err == nil {
				msg.ReceivedAt = t
			}
		}
	}
	if msg.ReceivedAt.IsZero() {
		msg.ReceivedAt = time.Now()
	}
	if v := m.Header.Get(xHamrTags); v != "" {
		_ = json.Unmarshal([]byte(v), &msg.Tags)
	}

	// Non-reserved headers go into Headers.
	for k, vs := range m.Header {
		if isReservedHeader(k) || len(vs) == 0 {
			continue
		}
		decoded, _ := dec.DecodeHeader(vs[0])
		msg.Headers[k] = decoded
	}
	if len(msg.Headers) == 0 {
		msg.Headers = nil
	}
	if len(msg.Tags) == 0 {
		msg.Tags = nil
	}

	// Body / attachments.
	ctHeader := m.Header.Get("Content-Type")
	if ctHeader == "" {
		ctHeader = "text/plain"
	}
	ct, params, err := mime.ParseMediaType(ctHeader)
	if err != nil {
		return msg, nil // tolerate malformed content-type; leave body empty
	}
	if err := parseMIMEInto(msg, ct, params, m.Body); err != nil {
		// Best-effort: don't drop the message over a body parse error.
		_ = err
	}

	return msg, nil
}

// parseMIMEInto walks a MIME tree and fills Text/HTML/Attach/Inline on msg.
func parseMIMEInto(msg *mailMessage, ct string, params map[string]string, body io.Reader) error {
	switch {
	case strings.HasPrefix(ct, "multipart/"):
		boundary := params["boundary"]
		if boundary == "" {
			return fmt.Errorf("multipart missing boundary")
		}
		mr := multipart.NewReader(body, boundary)
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			partCT := part.Header.Get("Content-Type")
			if partCT == "" {
				partCT = "text/plain"
			}
			pct, pparams, perr := mime.ParseMediaType(partCT)
			if perr != nil {
				pct = "application/octet-stream"
				pparams = map[string]string{}
			}
			if err := readPart(msg, pct, pparams, part); err != nil {
				return err
			}
		}
	case ct == "text/plain":
		b, err := io.ReadAll(body)
		if err != nil {
			return err
		}
		msg.Text = trimMboxSeparator(string(b))
		return nil
	case ct == "text/html":
		b, err := io.ReadAll(body)
		if err != nil {
			return err
		}
		msg.HTML = trimMboxSeparator(string(b))
		return nil
	default:
		// Single-part non-text body — unusual for dev email. Ignore.
		return nil
	}
}

// trimMboxSeparator removes the single trailing "\n" the mbox writer inserts
// between entries. We strip exactly one newline (if present) — any additional
// trailing newlines are part of the original body and must be preserved.
func trimMboxSeparator(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if strings.HasSuffix(s, "\n") {
		return s[:len(s)-1]
	}
	return s
}

// readPart handles a single MIME part (text/html leaf, nested multipart, or
// attachment/inline) and appends to msg.
func readPart(msg *mailMessage, ct string, params map[string]string, part *multipart.Part) error {
	// Detect whether this is an attachment/inline.
	disposition := strings.ToLower(part.Header.Get("Content-Disposition"))
	isAttach := strings.HasPrefix(disposition, "attachment")
	isInline := strings.HasPrefix(disposition, "inline") || part.Header.Get("Content-ID") != ""

	switch {
	case strings.HasPrefix(ct, "multipart/"):
		boundary := params["boundary"]
		if boundary == "" {
			return nil
		}
		mr := multipart.NewReader(part, boundary)
		for {
			sub, err := mr.NextPart()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			subCT := sub.Header.Get("Content-Type")
			if subCT == "" {
				subCT = "text/plain"
			}
			sct, sparams, serr := mime.ParseMediaType(subCT)
			if serr != nil {
				sct = "application/octet-stream"
				sparams = map[string]string{}
			}
			if err := readPart(msg, sct, sparams, sub); err != nil {
				return err
			}
		}
	case isAttach || isInline:
		data, err := decodeBody(part)
		if err != nil {
			return err
		}
		filename, _ := parseDispositionFilename(part.Header.Get("Content-Disposition"))
		if filename == "" {
			filename = params["name"]
		}
		a := mailAttachment{
			Filename:    filename,
			ContentType: ct,
			Data:        data,
		}
		if cid := part.Header.Get("Content-ID"); cid != "" {
			a.ContentID = strings.Trim(cid, "<>")
		}
		if isInline {
			msg.Inline = append(msg.Inline, a)
		} else {
			msg.Attach = append(msg.Attach, a)
		}
		return nil
	case ct == "text/plain":
		data, err := decodeBody(part)
		if err != nil {
			return err
		}
		// Multipart leaves preserve bytes verbatim — no mbox separator to trim.
		if msg.Text == "" {
			msg.Text = string(data)
		}
		return nil
	case ct == "text/html":
		data, err := decodeBody(part)
		if err != nil {
			return err
		}
		if msg.HTML == "" {
			msg.HTML = string(data)
		}
		return nil
	default:
		// Unknown text-ish part — ignore.
		return nil
	}
}

func decodeBody(part *multipart.Part) ([]byte, error) {
	data, err := io.ReadAll(part)
	if err != nil {
		return nil, err
	}
	enc := strings.ToLower(part.Header.Get("Content-Transfer-Encoding"))
	if enc == "base64" {
		// mime/multipart strips trailing whitespace but leaves base64 chars.
		return base64.StdEncoding.DecodeString(stripBase64Whitespace(string(data)))
	}
	return data, nil
}

func stripBase64Whitespace(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "")
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, " ", "")
	return s
}

func parseDispositionFilename(disp string) (string, error) {
	if disp == "" {
		return "", nil
	}
	_, params, err := mime.ParseMediaType(disp)
	if err != nil {
		return "", err
	}
	return params["filename"], nil
}

func parseAddressListHeader(v string) []mailAddress {
	if v == "" {
		return nil
	}
	addrs, err := mail.ParseAddressList(v)
	if err != nil {
		return nil
	}
	out := make([]mailAddress, len(addrs))
	for i, a := range addrs {
		out[i] = mailAddress{Name: a.Name, Email: a.Address}
	}
	return out
}

// unescapeMBox reverses MBOXO `>From ` escaping applied at write time.
func unescapeMBox(body []byte) []byte {
	var out bytes.Buffer
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	first := true
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) > 0 && line[0] == '>' {
			rest := line[1:]
			if bytes.HasPrefix(rest, []byte("From ")) || bytes.HasPrefix(rest, []byte(">From ")) {
				line = rest
			}
		}
		if !first {
			out.WriteByte('\n')
		}
		first = false
		out.Write(line)
	}
	if !first {
		out.WriteByte('\n')
	}
	return out.Bytes()
}
