# Validate — Pure-Function Validators

`hamr/pkg/validate` provides pure-function validators that return `""` on success or a
human-readable error message on failure. No struct tags, no reflection. Zero framework
dependencies.

## Quick Start

```go
import "github.com/FyrmForge/hamr/pkg/validate"
```

## Design

Every validator is a plain function: `func(value) string`. Empty string means valid.
Non-empty string is the error message. This makes validators composable, testable, and
easy to use in handlers.

```go
if msg := validate.Required(name); msg != "" {
    errors["name"] = msg
}
if msg := validate.Email(email); msg != "" {
    errors["email"] = msg
}
```

## Built-in Validators

### String validators

```go
validate.Required(value)             // non-empty check
validate.Email(value)                // email format
validate.Phone(value)                // optional +, 7-15 digits
validate.URL(value)                  // valid absolute URL
validate.MinLength(value, 3)         // at least 3 runes
validate.MaxLength(value, 100)       // at most 100 runes
validate.Match(value, `^\d{4}$`)     // regex match
validate.OneOf(value, "a", "b", "c") // allowed values
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

### Password strength

```go
validate.PasswordStrength(password)  // checks all requirements
```

For UI display, get individual requirement statuses:

```go
reqs := validate.CheckPasswordRequirements(password)
for _, r := range reqs {
    fmt.Printf("%s: %v\n", r.Description, r.Met)
}
```

## Custom Messages

Every validator has a `*Msg` variant that accepts a custom error message:

```go
validate.RequiredMsg(name, "Please enter your name")
validate.EmailMsg(email, "That doesn't look like an email")
validate.MinLengthMsg(password, 8, "Password must be at least 8 characters")
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
| `MsgPatternMismatch` | "Invalid format" |
| `MsgOneOf` | "Invalid selection" |
| `MsgIntRange` | "Value out of range" |
| `MsgMinAge` | "Does not meet minimum age requirement" |
| `MsgMaxAge` | "Exceeds maximum age" |
| `MsgPasswordWeak` | "Password is too weak" |

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

## URL Normalization

```go
validate.NormalizeURL("example.com")       // "https://example.com"
validate.NormalizeURL("https://foo.com")   // "https://foo.com" (unchanged)
validate.NormalizeURL("")                  // ""
```

## Handler Pattern

```go
func (h *Handler) CreateUser(c echo.Context) error {
    name  := c.FormValue("name")
    email := c.FormValue("email")

    errors := map[string]string{}
    if msg := validate.Required(name); msg != "" {
        errors["name"] = msg
    }
    if msg := validate.Email(email); msg != "" {
        errors["email"] = msg
    }
    if len(errors) > 0 {
        return respond.ValidationError(c, errors)
    }
    // ... create user
}
```

## API Reference

```go
// String validators
func Required(value string) string
func Email(value string) string
func Phone(value string) string
func URL(value string) string
func MinLength(value string, min int) string
func MaxLength(value string, max int) string
func Match(value string, pattern string) string
func OneOf(value string, options ...string) string

// Numeric
func IntRange(value int, min, max int) string

// Date
func MinAge(birthDate string, minAge int) string
func MaxAge(birthDate string, maxAge int) string

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
func MatchMsg(value, pattern, msg string) string
func OneOfMsg(value, msg string, options ...string) string
func IntRangeMsg(value, min, max int, msg string) string
func MinAgeMsg(birthDate string, minAge int, msg string) string
func MaxAgeMsg(birthDate string, maxAge int, msg string) string
func PasswordStrengthMsg(password, msg string) string

// URL normalization
func NormalizeURL(value string) string

// Custom registry
func Register(name string, fn func(string) string)
func Run(name, value string) string
```
