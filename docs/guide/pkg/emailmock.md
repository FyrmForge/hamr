# Emailmock — Local Email Mock for Development

`hamr/pkg/emailmock` is a dev-only implementation of
[`email.Sender`](email.md) that ships messages to the `hamr dev` server's
in-memory inbox at `/__hamr/mail`. It replaces real provider clients in
development so you can preview outbound email without API keys or network
calls.

> **Dev only.** The package has no production safety guards. Gate it behind
> an env flag (e.g. `EMAIL_MOCK=true`) in `main.go`. If someone shipped it to
> production, every outbound email would 404.

## What it does (and doesn't) test

- ✅ Validates how your app constructs `email.Message` — subject, bodies,
  recipients, attachments, inline image CIDs, custom headers.
- ✅ Lets you preview rendered HTML (sandboxed iframe) with mobile-viewport and
  images-blocked toggles.
- ❌ Does **not** validate your production adapter's MIME construction, SMTP
  handshake, or provider HTTP wire format. The mock never sees MIME. Catch
  those bugs with your provider's test environment or integration tests.

This is the deliberate tradeoff of an interface-boundary mock.

## Quick Start

Enable the mock inbox in `hamr.toml`:

```toml
[proxy]
listen = ":3000"
target = ":8080"

[dev.email]
enabled = true
```

Wire the client in `main.go`:

```go
import (
    "github.com/FyrmForge/hamr/pkg/email"
    "github.com/FyrmForge/hamr/pkg/emailmock"
)

var sender email.Sender
if os.Getenv("EMAIL_MOCK") == "true" {
    sender = emailmock.New(os.Getenv("HAMR_DEV_URL")) // e.g. "http://localhost:3000"
} else {
    sender = myapp.NewRealSender(cfg)
}
```

Your app code only ever sees `email.Sender`.

Set `EMAIL_MOCK=true` and `HAMR_DEV_URL=http://localhost:3000` in `.env` for
dev. Leave both unset (or `EMAIL_MOCK=false`) in production.

## Inbox UI

Open `http://<proxy-listen>/__hamr/mail` in a browser during `hamr dev`.

- **Inbox view** — newest-first list, substring search across subject /
  recipient / sender.
- **Detail view** — tabs for HTML render, plaintext, raw reconstruction,
  custom headers, attachments.
- **HTML tab** — sandboxed iframe, mobile viewport toggle, images-blocked
  toggle.
- **Outcome simulation** — per-message "mark failed" and "mark delayed"
  buttons for tagging captured messages as part of a test flow.
- **Clear / delete** — per-message delete, full-inbox clear.

The inbox survives app restarts triggered by file-watch rebuilds. When
persistence is enabled (default), it also survives restarts of `hamr dev`
itself — the inbox is mirrored to an mbox file at `.hamr/mail/inbox.mbox`
that reloads on startup. The file is a standard MBOXO-format email archive,
so you can also open it in Thunderbird, mutt, or Apple Mail directly.

## Failure Simulation

Any recipient whose local-part matches a magic value causes `Send` to return an
error without storing the message. Works across `To`, `Cc`, and `Bcc`.

| Magic local-part          | Returned error                  |
|---------------------------|---------------------------------|
| `bounce@anything.example` | `emailmock.ErrBounced`          |
| `reject@anything.example` | `emailmock.ErrRejected`         |

```go
_, err := sender.Send(ctx, email.Message{
    To: []email.Address{email.Addr("", "bounce@test.example")},
})
if errors.Is(err, emailmock.ErrBounced) {
    // handle bounce path
}
```

For post-hoc outcome tagging on already-captured messages, use the
"Mark failed" / "Mark delayed" buttons in the detail view.

## Config

In `hamr.toml`:

```toml
[dev.email]
enabled           = true                     # opt-in; default false
max_messages      = 500                      # ring-buffer size; oldest evicted first
max_message_bytes = 10485760                 # per-message cap; ingest returns 413 above this
persist           = true                     # default; mirror inbox to mbox file
persist_path      = ".hamr/mail/inbox.mbox"  # default
```

`[dev.email].enabled = true` requires `[proxy]` to be configured — the inbox UI
lives on the proxy mux. `hamr dev` refuses to start otherwise with an explicit
error.

### Persistence tradeoffs

- **Disk footprint.** Worst-case `max_messages * max_message_bytes` (default ~5 GB). Real usage is tiny; lower `max_messages` if you persist heavy attachments.
- **One trailing newline.** Bodies without a trailing `\n` gain one after round-trip through the mbox file. This is a deliberate tradeoff of MBOXO format — needed to guarantee a blank line before the next entry's `From ` separator.
- **Staleness.** Messages from previous sessions survive. Use the UI's "Clear inbox" button or delete the file when starting fresh.
- **Opt-out.** Set `persist = false` for an ephemeral in-memory-only inbox.

## HTTP API (for non-Go clients)

The ingest endpoint is plain HTTP, so clients in any language work:

```
POST /__hamr/mail/ingest
Content-Type: application/json

{
  "From":    {"Name":"Acme", "Email":"hello@acme.example"},
  "To":      [{"Email":"ada@example.com"}],
  "Subject": "hello",
  "Text":    "hi",
  "HTML":    "<p>hi</p>"
}

→ 200 OK  {"ID":"msg_1a2b3c..."}
→ 413     {"error":"message too large"}
→ 422     {"error":"bounced"}  or  {"error":"rejected"}
```

`Data` fields on attachments are JSON-encoded as base64.

## Security Notes

- **Sandboxed HTML preview.** The HTML tab renders inside an `<iframe sandbox="">`
  (no JS, no forms, no navigation, no same-origin) with a restrictive CSP
  (`default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'`).
  Captured HTML can't execute or phone home.
- **CSRF guard.** Mutating POST endpoints (`/clear`, `/:id/delete`,
  `/:id/fail`, `/:id/delay`) reject requests whose `Origin` header is set and
  doesn't match the request host. Requests without an `Origin` header (curl,
  tests, non-browser clients) pass through.
- **Not authenticated.** The inbox is unauthenticated by design. Do not bind
  `hamr dev`'s proxy to a public interface.

## Limitations

- **Not wire-captured MIME.** The mock receives JSON (via
  `pkg/emailmock`), not MIME from the network. The persisted mbox file and
  the "Raw" tab reconstruct MIME from stored fields — good enough to open
  in Thunderbird and to debug structure, but your production provider
  adapter's real wire format is not exercised here.
- **Single shared inbox.** No per-user segmentation; everything sent to
  this `hamr dev` lands in one inbox.
- **No webhook / open / click simulation.** Just capture-and-inspect plus
  post-hoc outcome tagging.

## API Reference

```go
func New(baseURL string) *Client
func (c *Client) Send(ctx context.Context, msg email.Message) (*email.Result, error)

var ErrBounced  = errors.New("emailmock: recipient bounced")
var ErrRejected = errors.New("emailmock: recipient rejected")
```

## See Also

- [email](email.md) — The `Sender` interface this package implements
- [dev](dev.md) — `hamr dev` overview including the mail viewer
- [stripemock](stripemock.md) — Same pattern applied to Stripe checkout
