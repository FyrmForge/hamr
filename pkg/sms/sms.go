// Package sms defines a provider-agnostic Sender interface for transactional
// SMS and the value types that flow through it.
//
// The interface is intentionally narrow. It covers the fields every major
// provider (Twilio, Vonage, MessageBird, AWS SNS, Plivo) supports: a sender, a
// recipient, and a text body. Provider-specific features (alphanumeric sender
// IDs, MMS media, delivery callbacks, messaging services) live in your app's
// own adapter code — not in this interface.
//
// Typical wiring:
//
//	var sender sms.Sender
//	if os.Getenv("SMS_MOCK") == "true" {
//	    sender = smsmock.New(os.Getenv("HAMR_DEV_URL"))
//	} else {
//	    sender = myapp.NewTwilioSender(twilioSID, twilioToken)
//	}
//
// Both the mock and any real adapter satisfy sms.Sender, so the rest of the
// app depends only on this package.
package sms

import "context"

// Sender sends an SMS through some backend (real provider, mock, etc.).
// Implementations must be safe for concurrent use.
type Sender interface {
	Send(ctx context.Context, msg Message) (*Result, error)
}

// Message is the input to Sender.Send.
type Message struct {
	From string // sender: E.164 number ("+15551230000") or short code
	To   string // recipient: E.164 number
	Body string // message text
	Ref  string // correlation reference (Vonage client-ref, MessageBird reference); adapters for providers without one may ignore it
}

// Result describes a successfully-sent message.
type Result struct {
	ID string // provider-assigned message id (empty if the backend does not return one)
}
