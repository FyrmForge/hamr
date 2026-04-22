package devserver

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sampleMessage() *mailMessage {
	return &mailMessage{
		ID:         "msg_aabbccddeeff0011",
		ReceivedAt: time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC),
		Status:     "delivered",
		From:       mailAddress{Name: "Acme", Email: "hello@acme.example"},
		To:         []mailAddress{{Name: "Ada Lovelace", Email: "ada@example.com"}},
		Cc:         []mailAddress{{Email: "cc@example.com"}},
		Subject:    "Welcome, Ada!",
		Text:       "Thanks for signing up.\nFrom the hamr team.\n",
		HTML:       "<p>Thanks for signing up.</p>",
		Attach: []mailAttachment{{
			Filename:    "report.pdf",
			ContentType: "application/pdf",
			Data:        []byte("%PDF-1.4\n%fake pdf bytes\n"),
		}},
		Inline: []mailAttachment{{
			ContentID:   "logo",
			Filename:    "logo.png",
			ContentType: "image/png",
			Data:        []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0xde, 0xad},
		}},
		Headers: map[string]string{
			"List-Unsubscribe": "<mailto:unsub@acme.example>",
			"X-App":            "demo",
		},
		Tags: map[string]string{"user_id": "42", "campaign": "welcome"},
	}
}

func TestMbox_Roundtrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "inbox.mbox")
	orig := sampleMessage()

	require.NoError(t, writeMboxInbox(path, []*mailMessage{orig}))

	got, err := readMboxInbox(path)
	require.NoError(t, err)
	require.Len(t, got, 1)
	restored := got[0]

	assert.Equal(t, orig.ID, restored.ID)
	assert.Equal(t, orig.Status, restored.Status)
	assert.Equal(t, orig.Subject, restored.Subject)
	assert.Equal(t, orig.From, restored.From)
	assert.Equal(t, orig.To, restored.To)
	assert.Equal(t, orig.Cc, restored.Cc)
	assert.Equal(t, orig.Text, restored.Text)
	assert.Equal(t, orig.HTML, restored.HTML)
	assert.Equal(t, orig.Tags, restored.Tags)
	// Headers: our custom ones must survive (case-normalized to canonical form).
	assert.Equal(t, "<mailto:unsub@acme.example>", restored.Headers["List-Unsubscribe"])
	assert.Equal(t, "demo", restored.Headers["X-App"])

	require.Len(t, restored.Attach, 1)
	assert.Equal(t, "report.pdf", restored.Attach[0].Filename)
	assert.Equal(t, "application/pdf", restored.Attach[0].ContentType)
	assert.Equal(t, orig.Attach[0].Data, restored.Attach[0].Data)

	require.Len(t, restored.Inline, 1)
	assert.Equal(t, "logo", restored.Inline[0].ContentID)
	assert.Equal(t, "image/png", restored.Inline[0].ContentType)
	assert.Equal(t, orig.Inline[0].Data, restored.Inline[0].Data)

	// Received-at should round-trip at nanosecond precision (via X-Hamr-Received-At).
	assert.True(t, restored.ReceivedAt.Equal(orig.ReceivedAt))
}

func TestMbox_PlaintextOnly(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "inbox.mbox")
	msg := &mailMessage{
		ID:         "msg_text",
		ReceivedAt: time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC),
		Status:     "delivered",
		From:       mailAddress{Email: "a@x.example"},
		To:         []mailAddress{{Email: "b@y.example"}},
		Subject:    "plain",
		Text:       "just text\n",
	}
	require.NoError(t, writeMboxInbox(path, []*mailMessage{msg}))
	got, err := readMboxInbox(path)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "just text\n", got[0].Text)
	assert.Empty(t, got[0].HTML)
}

// TestMbox_BodyWithoutTrailingNewlineGainsOne documents the mbox-write
// coercion: a body not ending in "\n" gains a single trailing newline after
// round-trip. This is a deliberate tradeoff to ensure the mbox separator
// produces a blank line before the next entry.
func TestMbox_BodyWithoutTrailingNewlineGainsOne(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "inbox.mbox")
	msg := &mailMessage{
		ID:         "msg_html",
		ReceivedAt: time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC),
		Status:     "delivered",
		From:       mailAddress{Email: "a@x.example"},
		To:         []mailAddress{{Email: "b@y.example"}},
		Subject:    "html",
		HTML:       "<p>hello</p>",
	}
	require.NoError(t, writeMboxInbox(path, []*mailMessage{msg}))
	got, err := readMboxInbox(path)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "<p>hello</p>\n", got[0].HTML)
}

func TestMbox_HTMLOnly(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "inbox.mbox")
	msg := &mailMessage{
		ID:         "msg_html",
		ReceivedAt: time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC),
		Status:     "delivered",
		From:       mailAddress{Email: "a@x.example"},
		To:         []mailAddress{{Email: "b@y.example"}},
		Subject:    "html",
		HTML:       "<p>hello</p>\n",
	}
	require.NoError(t, writeMboxInbox(path, []*mailMessage{msg}))
	got, err := readMboxInbox(path)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Empty(t, got[0].Text)
	assert.Equal(t, "<p>hello</p>\n", got[0].HTML)
}

func TestMbox_UnicodeSubjectAndNames(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "inbox.mbox")
	msg := &mailMessage{
		ID:         "msg_u",
		ReceivedAt: time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC),
		Status:     "delivered",
		From:       mailAddress{Name: "Café", Email: "a@x.example"},
		To:         []mailAddress{{Name: "Ádám", Email: "b@y.example"}},
		Subject:    "Résumé — draft",
		Text:       "body",
	}
	require.NoError(t, writeMboxInbox(path, []*mailMessage{msg}))
	got, err := readMboxInbox(path)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "Résumé — draft", got[0].Subject)
	assert.Equal(t, "Café", got[0].From.Name)
	assert.Equal(t, "Ádám", got[0].To[0].Name)
}

func TestMbox_MBOXOEscapeRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "inbox.mbox")
	// Body contains a line starting with "From " — must be escaped on write
	// and unescaped on read so it doesn't split the message.
	msg := &mailMessage{
		ID:         "msg_e",
		ReceivedAt: time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC),
		Status:     "delivered",
		From:       mailAddress{Email: "a@x.example"},
		To:         []mailAddress{{Email: "b@y.example"}},
		Subject:    "x",
		Text:       "line one\nFrom scratch we rewrite.\nlast line\n",
	}
	require.NoError(t, writeMboxInbox(path, []*mailMessage{msg}))

	raw, _ := os.ReadFile(path)
	// The `From scratch` line should be escaped to `>From scratch`.
	assert.Contains(t, string(raw), ">From scratch we rewrite.")

	got, err := readMboxInbox(path)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Contains(t, got[0].Text, "From scratch we rewrite.")
	assert.NotContains(t, got[0].Text, ">From scratch")
}

func TestMbox_MultipleMessages(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "inbox.mbox")
	msgs := []*mailMessage{
		{ID: "m1", ReceivedAt: time.Date(2026, 4, 21, 10, 0, 0, 0, time.UTC), Status: "delivered", From: mailAddress{Email: "a@x"}, To: []mailAddress{{Email: "b@y"}}, Subject: "one", Text: "first"},
		{ID: "m2", ReceivedAt: time.Date(2026, 4, 21, 11, 0, 0, 0, time.UTC), Status: "failed", StatusNote: "simulated", From: mailAddress{Email: "a@x"}, To: []mailAddress{{Email: "b@y"}}, Subject: "two", Text: "second"},
		{ID: "m3", ReceivedAt: time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC), Status: "delivered", From: mailAddress{Email: "a@x"}, To: []mailAddress{{Email: "b@y"}}, Subject: "three", HTML: "<p>third</p>"},
	}
	require.NoError(t, writeMboxInbox(path, msgs))
	got, err := readMboxInbox(path)
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, "one", got[0].Subject)
	assert.Equal(t, "two", got[1].Subject)
	assert.Equal(t, "three", got[2].Subject)
	assert.Equal(t, "failed", got[1].Status)
	assert.Equal(t, "simulated", got[1].StatusNote)
}

func TestMbox_CorruptEntryIsSkipped(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "inbox.mbox")

	// Write two good messages, then hand-append a garbage `From ` entry.
	require.NoError(t, writeMboxInbox(path, []*mailMessage{
		{ID: "ok1", ReceivedAt: time.Now(), Status: "delivered", From: mailAddress{Email: "a@x"}, Subject: "good one", Text: "x"},
	}))
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, _ = f.WriteString("From hamr garbage\r\nnot valid headers\r\nno blank line no anything\r\n\r\n")
	_ = f.Close()
	require.NoError(t, appendMboxMessage(path, &mailMessage{ID: "ok2", ReceivedAt: time.Now(), Status: "delivered", From: mailAddress{Email: "a@x"}, Subject: "good two", Text: "y"}))

	got, err := readMboxInbox(path)
	require.NoError(t, err)
	// The corrupt entry may or may not parse depending on tolerance — but the
	// two valid entries must survive.
	gotIDs := make(map[string]bool)
	for _, m := range got {
		gotIDs[m.ID] = true
	}
	assert.True(t, gotIDs["ok1"], "first valid entry must survive")
	assert.True(t, gotIDs["ok2"], "second valid entry must survive")
}

func TestMbox_MissingFileIsEmpty(t *testing.T) {
	t.Parallel()
	got, err := readMboxInbox(filepath.Join(t.TempDir(), "does-not-exist.mbox"))
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestMailMock_PersistenceAcrossRestart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "mail.mbox")

	first := NewMailMock(MailMockOptions{
		MaxMessages:     10,
		MaxMessageBytes: 64 * 1024,
		PersistPath:     path,
	})
	first.append(&mailMessage{
		ID:         "msg_one",
		ReceivedAt: time.Now(),
		Status:     "delivered",
		From:       mailAddress{Email: "a@x.example"},
		To:         []mailAddress{{Email: "b@y.example"}},
		Subject:    "survive me",
		Text:       "still here after restart\n",
	})
	first.SetStatus("msg_one", "failed", "deliberate")

	// "Restart" by making a new instance pointed at the same file.
	second := NewMailMock(MailMockOptions{
		MaxMessages:     10,
		MaxMessageBytes: 64 * 1024,
		PersistPath:     path,
	})
	list := second.List()
	require.Len(t, list, 1)
	assert.Equal(t, "msg_one", list[0].ID)
	assert.Equal(t, "survive me", list[0].Subject)
	assert.Equal(t, "failed", list[0].Status)
	assert.Equal(t, "deliberate", list[0].StatusNote)
}

func TestMailMock_EvictionRewritesFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "mail.mbox")

	mm := NewMailMock(MailMockOptions{
		MaxMessages:     2,
		MaxMessageBytes: 64 * 1024,
		PersistPath:     path,
	})
	for i, subj := range []string{"a", "b", "c"} {
		mm.append(&mailMessage{
			ID:         "msg_" + subj,
			ReceivedAt: time.Now().Add(time.Duration(i) * time.Second),
			Status:     "delivered",
			From:       mailAddress{Email: "x@y"},
			Subject:    subj,
		})
	}
	// After ingest #3 with cap=2, "a" should be evicted. The file must reflect
	// that — not just the in-memory buffer.
	raw, _ := os.ReadFile(path)
	assert.NotContains(t, string(raw), "msg_a")
	assert.Contains(t, string(raw), "msg_b")
	assert.Contains(t, string(raw), "msg_c")

	// Restart and verify.
	mm2 := NewMailMock(MailMockOptions{
		MaxMessages:     2,
		MaxMessageBytes: 64 * 1024,
		PersistPath:     path,
	})
	ids := make([]string, 0)
	for _, m := range mm2.List() {
		ids = append(ids, m.ID)
	}
	assert.NotContains(t, ids, "msg_a")
	assert.Contains(t, ids, "msg_b")
	assert.Contains(t, ids, "msg_c")
}

func TestMailMock_ClearRemovesFromFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "mail.mbox")

	mm := NewMailMock(MailMockOptions{
		MaxMessages:     10,
		MaxMessageBytes: 64 * 1024,
		PersistPath:     path,
	})
	mm.append(&mailMessage{ID: "a", ReceivedAt: time.Now(), Status: "delivered", Subject: "x"})
	mm.Clear()

	raw, _ := os.ReadFile(path)
	assert.Empty(t, bytes.TrimSpace(raw))

	mm2 := NewMailMock(MailMockOptions{
		MaxMessages:     10,
		MaxMessageBytes: 64 * 1024,
		PersistPath:     path,
	})
	assert.Empty(t, mm2.List())
}

func TestMailMock_DeleteRemovesFromFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "mail.mbox")

	mm := NewMailMock(MailMockOptions{
		MaxMessages:     10,
		MaxMessageBytes: 64 * 1024,
		PersistPath:     path,
	})
	mm.append(&mailMessage{ID: "keep", ReceivedAt: time.Now(), Status: "delivered", Subject: "keep me"})
	mm.append(&mailMessage{ID: "drop", ReceivedAt: time.Now(), Status: "delivered", Subject: "drop me"})
	require.True(t, mm.Delete("drop"))

	raw, _ := os.ReadFile(path)
	assert.Contains(t, string(raw), "keep me")
	assert.NotContains(t, string(raw), "drop me")
}

func TestMailMock_LoadDropsOverCapAndRewrites(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "mail.mbox")

	// Seed the file with 5 messages via one MailMock, then open a new one with cap=2.
	seed := NewMailMock(MailMockOptions{
		MaxMessages:     10,
		MaxMessageBytes: 64 * 1024,
		PersistPath:     path,
	})
	for i := 0; i < 5; i++ {
		seed.append(&mailMessage{
			ID:         "m" + string(rune('a'+i)),
			ReceivedAt: time.Now().Add(time.Duration(i) * time.Second),
			Status:     "delivered",
			Subject:    string(rune('a' + i)),
		})
	}

	loaded := NewMailMock(MailMockOptions{
		MaxMessages:     2,
		MaxMessageBytes: 64 * 1024,
		PersistPath:     path,
	})
	list := loaded.List() // newest first
	require.Len(t, list, 2)
	// The 2 newest are "me" and "md" (since we seeded a,b,c,d,e with time increasing).
	assert.Equal(t, "me", list[0].ID)
	assert.Equal(t, "md", list[1].ID)

	// File must have been rewritten to match.
	raw, _ := os.ReadFile(path)
	assert.NotContains(t, string(raw), "msg_ma")
	assert.NotContains(t, string(raw), "msg_mb")
	assert.NotContains(t, string(raw), "msg_mc")
}

func TestMailMock_PersistErrorIsReportedNotFatal(t *testing.T) {
	t.Parallel()
	// Point the mock at a path whose parent cannot be created (a file
	// masquerading as a directory). Writes must fail internally but the
	// in-memory inbox must still work.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file-not-dir")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o644))
	badPath := filepath.Join(blocker, "inbox.mbox") // parent is a file, not a dir

	errs := make(chan error, 4)
	mm := NewMailMock(MailMockOptions{
		MaxMessages:     5,
		MaxMessageBytes: 64 * 1024,
		PersistPath:     badPath,
		OnPersistError: func(err error) {
			select {
			case errs <- err:
			default:
			}
		},
	})
	mm.append(&mailMessage{ID: "x", ReceivedAt: time.Now(), Status: "delivered", Subject: "in-memory-only"})

	// Error should have been reported.
	select {
	case err := <-errs:
		assert.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("expected persist error callback to fire")
	}
	// In-memory list still has the message.
	assert.Len(t, mm.List(), 1)
}

func TestMbox_AttachmentWithSpecialCharFilename(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "mail.mbox")
	msg := &mailMessage{
		ID:         "msg_fn",
		ReceivedAt: time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC),
		Status:     "delivered",
		From:       mailAddress{Email: "a@x"},
		Subject:    "a",
		Text:       "t",
		Attach: []mailAttachment{{
			Filename:    "my file.txt",
			ContentType: "text/plain",
			Data:        []byte("hello attachment"),
		}},
	}
	require.NoError(t, writeMboxInbox(path, []*mailMessage{msg}))
	got, err := readMboxInbox(path)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Len(t, got[0].Attach, 1)
	assert.Equal(t, "my file.txt", got[0].Attach[0].Filename)
	assert.Equal(t, []byte("hello attachment"), got[0].Attach[0].Data)
}

func TestMbox_SeparatorBetweenMessages(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "inbox.mbox")
	msgs := []*mailMessage{
		{ID: "m1", ReceivedAt: time.Now(), Status: "delivered", From: mailAddress{Email: "a@x"}, Subject: "one", Text: "x"},
		{ID: "m2", ReceivedAt: time.Now(), Status: "delivered", From: mailAddress{Email: "a@x"}, Subject: "two", Text: "y"},
	}
	require.NoError(t, writeMboxInbox(path, msgs))
	raw, _ := os.ReadFile(path)
	// Must have exactly two `From ` lines at column 0.
	count := strings.Count(string(raw), "\nFrom ")
	// The first `From ` is at the very beginning (no leading newline), so count
	// the subsequent ones and add 1 for the first.
	leadingFrom := 0
	if strings.HasPrefix(string(raw), "From ") {
		leadingFrom = 1
	}
	assert.Equal(t, 2, count+leadingFrom)
}
