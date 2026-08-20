# SMS — Provider-Agnostic Sender Interface

`hamr/pkg/sms` defines a narrow `Sender` interface for outbound transactional
SMS and the value types that flow through it. Apps depend on this interface;
concrete implementations (a real provider adapter, [`smsmock`](smsmock.md))
plug in at startup.

The interface covers what every major provider (Twilio, Vonage, MessageBird,
AWS SNS, Plivo) supports: a sender, a recipient, and a text body.
Provider-specific features (alphanumeric sender IDs, MMS media, delivery
callbacks, messaging services) live in your app's own adapter code — not in
this interface.

## Types

```go
type Sender interface {
    Send(ctx context.Context, msg Message) (*Result, error) // concurrency-safe
}

type Message struct {
    From string            // sender: E.164 number ("+15551230000") or short code
    To   string            // recipient: E.164 number
    Body string            // message text
    Ref  string // correlation reference (Vonage client-ref, MessageBird reference); adapters for providers without one may ignore it
}

type Result struct {
    ID string // provider-assigned message id (empty if none)
}
```

## Typical wiring

```go
var sender sms.Sender
if os.Getenv("SMS_MOCK") == "true" {
    sender = smsmock.New(os.Getenv("HAMR_DEV_URL"))
} else {
    sender = myapp.NewTwilioSender(twilioSID, twilioToken)
}
```

## See Also

- [smsmock](smsmock.md) — Dev-only implementation with an inbox viewer at `/__hamr/sms`
- [email](email.md) — The same interface pattern, for outbound email
