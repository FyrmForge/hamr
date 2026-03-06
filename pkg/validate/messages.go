// Package validate provides pure-function validators that return "" on success
// or a human-readable error message string on failure.
package validate

// Exported message constants used by the built-in validators.
// Override individual messages by using the *Msg function variants.
const (
	MsgRequired        = "This field is required"
	MsgEmailInvalid    = "Invalid email address"
	MsgPhoneInvalid    = "Invalid phone number"
	MsgURLInvalid      = "Invalid URL"
	MsgMinLength       = "Too short"
	MsgMaxLength       = "Too long"
	MsgOneOf           = "Invalid selection"
	MsgIntRange        = "Value out of range"
	MsgMinAge          = "Does not meet minimum age requirement"
	MsgMaxAge          = "Exceeds maximum age"
	MsgPasswordWeak    = "Password is too weak"
	MsgValidatorNotFound = "Unknown validator"
)
