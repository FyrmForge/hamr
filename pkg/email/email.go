// Package email defines a provider-agnostic Sender interface for transactional
// email and the value types that flow through it.
//
// The interface is intentionally narrow. It covers the fields every major
// provider (SES, Postmark, Resend, Mailgun, SMTP2GO, plain SMTP) supports:
// recipients, subject, text+html bodies, attachments, inline images, custom
// headers, and free-form tags for correlation. Provider-specific features
// (templates, per-recipient substitutions, scheduled send, tracking flags)
// live either in custom headers, tags, or in your app's own adapter code —
// not in this interface.
//
// Typical wiring:
//
//	var sender email.Sender
//	if os.Getenv("EMAIL_MOCK") == "true" {
//	    sender = emailmock.New(os.Getenv("HAMR_DEV_URL"))
//	} else {
//	    sender = myapp.NewResendSender(resendAPIKey)
//	}
//
// Both the mock and any real adapter satisfy email.Sender, so the rest of the
// app depends only on this package.
package email

import "context"

// Sender sends an email through some backend (real provider, mock, SMTP, etc.).
// Implementations must be safe for concurrent use.
type Sender interface {
	Send(ctx context.Context, msg Message) (*Result, error)
}

// Message is the input to Sender.Send. Exactly one of Text or HTML should be
// set for single-part messages; setting both produces a multipart/alternative.
type Message struct {
	From        Address
	To          []Address
	Cc          []Address
	Bcc         []Address
	ReplyTo     *Address // nil if not overriding From
	Subject     string
	Text        string            // plaintext body
	HTML        string            // html body
	Attachments []Attachment      // downloadable files
	Inline      []Attachment      // inline images; ContentID must be set and referenced as cid:<ContentID> in HTML
	Headers     map[string]string // custom headers (List-Unsubscribe, X-*, In-Reply-To, etc.)
	Tags        map[string]string // free-form metadata for provider correlation
}

// Address is a single RFC 5322 mailbox. Name is optional.
type Address struct {
	Name  string
	Email string
}

// Addr constructs an Address. Pass an empty name for a bare email.
func Addr(name, email string) Address {
	return Address{Name: name, Email: email}
}

// Attachment is a file attached to a Message. For inline images referenced
// from HTML via cid:, set ContentID and place the Attachment in Message.Inline.
type Attachment struct {
	Filename    string
	ContentType string // RFC 2046 media type; defaults to application/octet-stream if empty
	Data        []byte
	ContentID   string // only meaningful for Message.Inline entries
}

// Result describes a successfully-sent message.
type Result struct {
	ID string // provider-assigned message id (empty if the backend does not return one)
}
