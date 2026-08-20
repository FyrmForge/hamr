# Smsmock — Local SMS Mock for Development

`hamr/pkg/smsmock` is a dev-only implementation of [`sms.Sender`](sms.md)
that ships messages to the `hamr dev` server's in-memory inbox at
`/__hamr/sms`. It replaces real provider clients in development so you can
inspect outbound SMS without API keys or network calls.

> **Dev only.** The package has no production safety guards. Gate it behind
> an env flag (e.g. `SMS_MOCK=true`) in `main.go`. If someone shipped it to
> production, every outbound SMS would 404.

Like [`emailmock`](emailmock.md), this is an interface-boundary mock: it
validates how your app constructs `sms.Message`, not your production
provider adapter's wire format.

## Quick Start

Enable the mock inbox in `hamr.toml`:

```toml
[proxy]
listen = ":3000"
target = ":8080"

[dev.sms]
enabled = true
```

Wire the client in `main.go`:

```go
import (
    "github.com/FyrmForge/hamr/pkg/sms"
    "github.com/FyrmForge/hamr/pkg/smsmock"
)

var sender sms.Sender
if os.Getenv("SMS_MOCK") == "true" {
    sender = smsmock.New(os.Getenv("HAMR_DEV_URL")) // e.g. "http://localhost:3000"
} else {
    sender = myapp.NewRealSender(cfg)
}
```

Set `SMS_MOCK=true` in `.env` for dev (`HAMR_DEV_URL` is injected by
`hamr dev` when the mock is enabled). Leave it unset in production.

## Inbox UI

Open `http://<proxy-listen>/__hamr/sms` in a browser during `hamr dev`.
The `hamr dev` panel also shows an "Open SMS inbox" shortcut button.

- **Inbox view** — newest-first list, substring search across from / to / body.
- **Detail view** — full body, correlation ref, character count.
- **Outcome simulation** — per-message "mark failed" and "mark delayed"
  buttons for tagging captured messages as part of a test flow.
- **Clear / delete** — per-message delete, full-inbox clear.

The inbox survives app restarts triggered by file-watch rebuilds. When
persistence is enabled (default), it also survives restarts of `hamr dev`
itself — the inbox is mirrored to a JSONL file at `.hamr/sms/inbox.jsonl`
(one JSON message per line) that reloads on startup.

## Failure Simulation

A recipient number whose digits end in a magic suffix causes `Send` to return
an error without storing the message. The suffixes use the fictional 555
exchange so real recipient numbers can't match. Formatting characters are
ignored, so `+1 500 555-0001` matches.

| Magic suffix | Example        | Returned error            |
|--------------|----------------|---------------------------|
| `5550001`    | `+15005550001` | `smsmock.ErrInvalidNumber`|
| `5550002`    | `+15005550002` | `smsmock.ErrUndeliverable`|

```go
_, err := sender.Send(ctx, sms.Message{To: "+15005550001", Body: "hi"})
if errors.Is(err, smsmock.ErrInvalidNumber) {
    // handle invalid-number path
}
```

For post-hoc outcome tagging on already-captured messages, use the
"Mark failed" / "Mark delayed" buttons in the detail view.

## Config

In `hamr.toml`:

```toml
[dev.sms]
enabled      = true                     # opt-in; default false
max_messages = 500                      # ring-buffer size; oldest evicted first
persist      = true                     # default; mirror inbox to JSONL file
persist_path = ".hamr/sms/inbox.jsonl"  # default
```

`[dev.sms].enabled = true` requires `[proxy]` to be configured — the inbox UI
lives on the proxy mux. `hamr dev` refuses to start otherwise with an explicit
error. Ingest bodies are capped at 64 KiB (413 above that).

## HTTP API (for non-Go clients)

The ingest endpoint is plain HTTP, so clients in any language work:

```
POST /__hamr/sms/ingest
Content-Type: application/json

{
  "From": "+15551230000",
  "To":   "+15559870000",
  "Body": "your code is 123456"
}

→ 200 OK  {"ID":"sms_1a2b3c..."}
→ 413     {"error":"message too large"}
→ 422     {"error":"invalid_number"}  or  {"error":"undeliverable"}
```

## Security Notes

- **CSRF guard.** Mutating POST endpoints (`/clear`, `/:id/delete`,
  `/:id/fail`, `/:id/delay`) reject requests whose `Origin` header is set and
  doesn't match the request host. Requests without an `Origin` header (curl,
  tests, non-browser clients) pass through.
- **Not authenticated.** The inbox is unauthenticated by design. Do not bind
  `hamr dev`'s proxy to a public interface.

## Limitations

- **Single shared inbox.** No per-user segmentation; everything sent to
  this `hamr dev` lands in one inbox.
- **Text only.** No MMS media, delivery receipts, or inbound-reply
  simulation — just capture-and-inspect plus post-hoc outcome tagging.

## API Reference

```go
func New(baseURL string) *Client
func (c *Client) Send(ctx context.Context, msg sms.Message) (*sms.Result, error)

var ErrInvalidNumber = errors.New("smsmock: invalid recipient number")
var ErrUndeliverable = errors.New("smsmock: recipient undeliverable")
```

## See Also

- [sms](sms.md) — The `Sender` interface this package implements
- [emailmock](emailmock.md) — Same `hamr dev`-hosted mock pattern, applied to email
- [mock-serve](mock-serve.md) — Running the mocks standalone in a container
