# Validate — Pure-Function Validators & Form API

`hamr/pkg/validate` provides pure-function validators that return `""` on success or a
human-readable error message on failure. The `Form` API lets you define field rules once
and reuse them for both full-form validation and HTMX per-field validation.

## Quick Start

```go
import "github.com/FyrmForge/hamr/pkg/validate"
```

## Design

Every validator is a plain function: `func(value) string`. Empty string means valid.
Non-empty string is the error message. This makes validators composable, testable, and
easy to use in handlers.

The `Form` type collects field definitions and provides `Validate(c)` for full-form
validation and `ValidationHandler(paramName)` for automatic HTMX per-field endpoints.

## Built-in Validators

### String validators

```go
validate.Required(value)             // non-empty check
validate.Email(value)                // email format
validate.Phone(value)                // 7-15 digits; + must be followed by 1-9; separators OK
validate.URL(value)                  // absolute http/https URL (rejects javascript:, data:, etc.)
validate.MinLength(value, 3)         // at least 3 runes
validate.MaxLength(value, 100)       // at most 100 runes
validate.OneOf(value, "a", "b", "c") // allowed values
```

### Curried constructors

Curried versions return `func(string) string` and work directly as `Form` rules:

```go
validate.MinLen(3)                   // func(string) string
validate.MaxLen(100)                 // func(string) string
validate.In("admin", "user")        // func(string) string
validate.AgeMin(18)                  // func(string) string
validate.AgeMax(120)                 // func(string) string
```

### Numeric validators

```go
validate.IntRange(age, 18, 120)
```

### Date validators

```go
validate.MinAge("1990-01-15", 18)  // YYYY-MM-DD, at least 18 years old
validate.MaxAge("1990-01-15", 65)  // at most 65 years old
```

### Character-class validators

Individual checks for composable password rules:

```go
validate.HasUpper(value)    // contains uppercase letter
validate.HasLower(value)    // contains lowercase letter
validate.HasDigit(value)    // contains digit
validate.HasSpecial(value)  // contains special character (punctuation/symbol)
```

Compose them with `Field()` for custom password policies:

```go
// Relaxed: just length + digit
validate.Field("password", validate.Required, validate.MinLen(12), validate.HasDigit)

// Strict (equivalent to PasswordStrength):
validate.Field("password", validate.Required, validate.MinLen(8),
    validate.HasUpper, validate.HasLower, validate.HasDigit, validate.HasSpecial)
```

### Password strength

```go
validate.PasswordStrength(password)  // checks all requirements (8+ chars, upper, lower, digit, special)
```

For UI display, get individual requirement statuses:

```go
reqs := validate.CheckPasswordRequirements(password)
for _, r := range reqs {
    fmt.Printf("%s: %v\n", r.Description, r.Met)
}
```

## Empty-Input Behavior

All validators (except `Required`) reject empty strings by default. If a field is optional
but must be valid when provided, wrap the validator with `EmptyOr`:

```go
validate.EmptyOr(validate.Email)("")      // "" (pass)
validate.EmptyOr(validate.Email)("   ")   // "" (pass)
validate.EmptyOr(validate.Email)("bad")   // "Invalid email address"
validate.EmptyOr(validate.Email)("a@b.c") // "" (pass)
```

## Custom Messages

Every validator has a `*Msg` variant that accepts a custom error message:

```go
validate.RequiredMsg(name, "Please enter your name")
validate.EmailMsg(email, "That doesn't look like an email")
validate.MinLengthMsg(password, 8, "Password must be at least 8 characters")
```

## Rule Message Override

`WithMsg` wraps a rule to replace its error message:

```go
validate.WithMsg(validate.Email, "Please enter a valid email")
```

## RunRules — Standalone Rule Chain

Execute rules in order, returning the first error message:

```go
errMsg := validate.RunRules(email, validate.Required, validate.Email)
// Returns "" if all pass, or first error message
```

## Default Messages

All default messages are exported constants in `messages.go`:

| Constant | Value |
|----------|-------|
| `MsgRequired` | "This field is required" |
| `MsgEmailInvalid` | "Invalid email address" |
| `MsgPhoneInvalid` | "Invalid phone number" |
| `MsgURLInvalid` | "Invalid URL" |
| `MsgMinLength` | "Too short" |
| `MsgMaxLength` | "Too long" |
| `MsgOneOf` | "Invalid selection" |
| `MsgIntRange` | "Value out of range" |
| `MsgMinAge` | "Does not meet minimum age requirement" |
| `MsgMaxAge` | "Exceeds maximum age" |
| `MsgPasswordWeak` | "Password is too weak" |
| `MsgHasUpper` | "Must contain an uppercase letter" |
| `MsgHasLower` | "Must contain a lowercase letter" |
| `MsgHasDigit` | "Must contain a digit" |
| `MsgHasSpecial` | "Must contain a special character" |

## Custom Validator Registry

Register project-specific validators and run them by name:

```go
validate.Register("username", func(v string) string {
    if strings.Contains(v, " ") {
        return "Username cannot contain spaces"
    }
    return ""
})

msg := validate.Run("username", input)
```

## Normalization

```go
validate.NormalizeURL("example.com")       // "https://example.com"
validate.NormalizeURL("https://foo.com")   // "https://foo.com" (unchanged)
validate.NormalizeURL("")                  // ""
```

`NormalizePhone` strips the punctuation people type into phone fields —
whitespace, hyphens and Unicode dash punctuation, dots, slashes and brackets.
`Phone` calls it internally, so you only need it directly when you want to
store a number in a canonical form:

```go
validate.NormalizePhone("07700 900123")        // "07700900123"
validate.NormalizePhone("(415) 555-1234")      // "4155551234"
validate.NormalizePhone("+44 7700 900123")     // "+447700900123"
validate.NormalizePhone("abc")                 // "abc" (unchanged)
```

`NormalizePhone` does not infer country-specific trunk rules; bracketed digits
are kept as digits after their brackets are stripped. Characters outside the
separator set are left alone, so normalizing a non-phone string still yields
something `Phone` rejects.

## Form API

### Defining a Form

```go
formRules := validate.NewForm(
    validate.WithOOBRenderer(form.OOBValidator),
    validate.WithGeneralError("Please fix the errors below."),
    validate.WithTrim(true),
    validate.Field("name", validate.Required, validate.MinLen(2)),
    validate.Field("email", validate.Required, validate.Email),
    validate.FieldMsg("password", "Password does not meet requirements",
        validate.Required, validate.PasswordStrength,
    ),
)
```

### Form Options

| Option | Purpose |
|---|---|
| `WithOOBRenderer(fn)` | Default renderer for `ValidationHandler` |
| `WithGeneralError(msg)` | Added under `"general"` key when any field fails |
| `WithTrim(bool)` | Auto-trim whitespace before validating |
| `WithShortCircuit(bool)` | Stop after first field error (default: false) |

### Field Definitions

```go
// Default messages from each rule
validate.Field("email", validate.Required, validate.Email)

// One message for any failure
validate.FieldMsg("email", "Email is invalid", validate.Required, validate.Email)

// Per-rule message override
validate.Field("email",
    validate.Required,
    validate.WithMsg(validate.Email, "Please enter a valid email"),
)
```

### Per-Field Custom Renderer

```go
validate.Field("password", validate.Required).WithRenderer(customRenderer)
```

### Context-Aware Rules

```go
validate.Field("password_confirm", validate.Required).
    WithCtx(func(c echo.Context, value string) string {
        if value != c.FormValue("password") {
            return "Passwords do not match"
        }
        return ""
    })
```

`CtxRule`s run after standard rules and only if all standard rules pass. `WithTrim` only trims the field being validated — values read via `c.FormValue` inside a `CtxRule` are untrimmed.

When exposed via `ValidationHandler`, the cross-referenced field must be sent in the per-field request via `hx-include` — see [Forms & Validation › Cross-Field Validation (HTMX)](../06-forms-validation.md#cross-field-validation-htmx).

### Full Form Validation

```go
errs := formRules.Validate(c)  // map[string]string or nil
```

### HTMX Per-Field Validation

```go
// One route handles all fields:
group.POST("/register/validate/:field", h.RegisterFormRules.ValidationHandler("field"))
```

Unknown fields return an empty/no-error response.

### Short-Circuit Behavior

- **Per-field (always on):** Rules for a single field stop at first failure.
- **Per-form (configurable):** `WithShortCircuit(true)` stops after first field error. Default is off.

## API Reference

```go
// Type aliases
type Rule = func(string) string
type CtxRule = func(echo.Context, string) string

// Rule helpers
func WithMsg(rule Rule, msg string) Rule
func RunRules(value string, rules ...Rule) string

// Higher-order helpers
func EmptyOr(fn func(string) string) func(string) string

// String validators
func Required(value string) string
func Email(value string) string
func Phone(value string) string   // normalizes separators first; 7-15 digits; + then 1-9
func URL(value string) string
func MinLength(value string, min int) string
func MaxLength(value string, max int) string
func OneOf(value string, options ...string) string

// Curried constructors (return func(string) string)
func MinLen(n int) Rule
func MaxLen(n int) Rule
func In(options ...string) Rule
func AgeMin(minAge int) Rule
func AgeMax(maxAge int) Rule

// Numeric
func IntRange(value int, min, max int) string

// Date
func MinAge(birthDate string, minAge int) string
func MaxAge(birthDate string, maxAge int) string

// Character-class validators
func HasUpper(value string) string
func HasLower(value string) string
func HasDigit(value string) string
func HasSpecial(value string) string

// Password
func PasswordStrength(password string) string
func CheckPasswordRequirements(password string) []PasswordRequirement

// *Msg variants (custom messages)
func RequiredMsg(value, msg string) string
func EmailMsg(value, msg string) string
func PhoneMsg(value, msg string) string
func URLMsg(value, msg string) string
func MinLengthMsg(value string, min int, msg string) string
func MaxLengthMsg(value string, max int, msg string) string
func OneOfMsg(value, msg string, options ...string) string
func IntRangeMsg(value, min, max int, msg string) string
func MinAgeMsg(birthDate string, minAge int, msg string) string
func MaxAgeMsg(birthDate string, maxAge int, msg string) string
func PasswordStrengthMsg(password, msg string) string
func HasUpperMsg(value, msg string) string
func HasLowerMsg(value, msg string) string
func HasDigitMsg(value, msg string) string
func HasSpecialMsg(value, msg string) string

// Normalization
func NormalizeURL(value string) string
func NormalizePhone(value string) string

// Custom registry
func Register(name string, fn func(string) string)
func Run(name, value string) string

// Form API
type FieldRenderer func(c echo.Context, field, errMsg string) error
type FormOption interface{ apply(*formConfig) }

func NewForm(opts ...FormOption) Form
func WithOOBRenderer(fn FieldRenderer) FormOption
func WithGeneralError(msg string) FormOption
func WithTrim(on bool) FormOption
func WithShortCircuit(on bool) FormOption

func Field(name string, rules ...Rule) FieldBuilder
func FieldMsg(name, msg string, rules ...Rule) FieldBuilder
func (fb FieldBuilder) WithRenderer(fn FieldRenderer) FieldBuilder
func (fb FieldBuilder) WithCtx(rules ...CtxRule) FieldBuilder

func (f Form) Validate(c echo.Context) map[string]string
func (f Form) ValidationHandler(paramName string) echo.HandlerFunc
```
