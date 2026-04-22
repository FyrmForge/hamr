# Email — Provider-Agnostic Sender Interface

`hamr/pkg/email` defines the `Sender` interface your application depends on for
outbound transactional email. Your app writes code against this interface;
you plug in any concrete implementation (real provider, local mock, plain SMTP)
at startup.

It is deliberately narrow. It covers the fields every major provider (SES,
Postmark, Resend, Mailgun, SMTP2GO, plain SMTP) supports. Provider-specific
features like per-recipient substitutions, templates, scheduled send, and
tracking flags live in your app's own adapter code, in `Headers`, or in `Tags`
— not in this interface.

## Quick Start

```go
import "github.com/FyrmForge/hamr/pkg/email"

func RegisterUser(ctx context.Context, sender email.Sender, newUser User) error {
    _, err := sender.Send(ctx, email.Message{
        From:    email.Addr("Acme", "hello@acme.example"),
        To:      []email.Address{email.Addr(newUser.Name, newUser.Email)},
        Subject: "Welcome to Acme",
        Text:    "Thanks for signing up.",
        HTML:    "<p>Thanks for signing up.</p>",
    })
    return err
}
```

## Interface

```go
type Sender interface {
    Send(ctx context.Context, msg Message) (*Result, error)
}
```

Implementations must be safe for concurrent use. `Send` respects context
cancellation.

## Types

```go
type Message struct {
    From        Address
    To          []Address
    Cc          []Address
    Bcc         []Address
    ReplyTo     *Address          // nil if not overriding From
    Subject     string
    Text        string            // plaintext body
    HTML        string            // html body
    Attachments []Attachment      // downloadable files
    Inline      []Attachment      // cid-referenced inline images
    Headers     map[string]string // custom (List-Unsubscribe, X-*, In-Reply-To, etc.)
    Tags        map[string]string // free-form metadata
}

type Address     struct { Name, Email string }
type Attachment  struct { Filename, ContentType string; Data []byte; ContentID string }
type Result      struct { ID string } // provider-assigned message id (may be empty)

func Addr(name, email string) Address
```

### Bodies

Set `Text`, `HTML`, or both. Adapters that produce MIME should emit
`multipart/alternative` when both are non-empty.

### Inline images

For HTML that references `<img src="cid:logo">`, set an entry in `Inline` with
`ContentID = "logo"`. The `emailmock` dev viewer rewrites these to local URLs
so you can preview. Real adapters include the inline parts in the MIME tree.

### Extensibility

The interface intentionally skips features that vary wildly between providers:

| Need | Where it goes |
|------|---------------|
| Custom SMTP-level or RFC 5322 headers | `Headers` |
| Correlation keys (user_id, campaign_id) | `Tags` |
| Templates, per-recipient substitutions | Your provider adapter |
| Scheduled send | Your provider adapter |
| Open/click tracking flags | Your provider adapter |

If your app needs provider-specific features everywhere, define a richer
interface in your app that extends `email.Sender`.

## Swap Pattern

```go
import (
    "github.com/FyrmForge/hamr/pkg/email"
    "github.com/FyrmForge/hamr/pkg/emailmock"
)

var sender email.Sender
if os.Getenv("EMAIL_MOCK") == "true" {
    sender = emailmock.New(os.Getenv("HAMR_DEV_URL")) // e.g. "http://localhost:3000"
} else {
    sender = myapp.NewRealSender(cfg) // your production adapter, satisfies email.Sender
}
```

## See Also

- [emailmock](emailmock.md) — Mock `Sender` that ships to the `hamr dev` inbox
- [dev](dev.md) — The `hamr dev` mail viewer at `/__hamr/mail`
