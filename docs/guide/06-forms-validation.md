# Forms & Validation

HAMR uses simple function-based validators rather than struct-tag validation — this keeps validation explicit, testable, and easy to follow in handlers. This guide covers form handling, validators, CSRF protection, flash messages, and HTMX field validation.

**Package references:** [Validate](pkg/validate.md), [Middleware](pkg/middleware.md) (flash, CSRF sections)

---

## Form Handling Pattern

The pattern is: read form values, validate each one, respond with errors or proceed.

```go
func (h *Handler) CreateUser(c echo.Context) error {
    name  := c.FormValue("name")
    email := c.FormValue("email")
    password := c.FormValue("password")

    errors := map[string]string{}
    if msg := validate.Required(name); msg != "" {
        errors["name"] = msg
    }
    if msg := validate.Email(email); msg != "" {
        errors["email"] = msg
    }
    if msg := validate.PasswordStrength(password); msg != "" {
        errors["password"] = msg
    }

    if len(errors) > 0 {
        return respond.ValidationError(c, errors)
    }

    // ... create user
    return respond.HTML(c, http.StatusOK, templates.Success())
}
```

---

## Validators

Every validator is a pure function: `func(value) string`. Empty string means valid. Non-empty string is the error message.

### Built-in Validators

```go
validate.Required(value)             // non-empty check
validate.Email(value)                // email format
validate.Phone(value)                // optional +, 7-15 digits
validate.URL(value)                  // valid absolute URL
validate.MinLength(value, 3)         // at least 3 runes
validate.MaxLength(value, 100)       // at most 100 runes
validate.OneOf(value, "a", "b", "c") // allowed values
validate.IntRange(age, 18, 120)      // numeric range
validate.MinAge("1990-01-15", 18)    // YYYY-MM-DD, minimum age
validate.PasswordStrength(password)  // checks all requirements
```

### Optional Fields

All validators reject empty strings by default. For optional fields that must be valid when provided, wrap with `EmptyOr`:

```go
if msg := validate.EmptyOr(validate.Email)(""); msg != "" {
    errors["email"] = msg // not reached — empty passes
}
```

### Custom Messages

Every validator has a `*Msg` variant:

```go
validate.RequiredMsg(name, "Please enter your name")
validate.MinLengthMsg(password, 8, "Password must be at least 8 characters")
```

### Custom Validators

Register project-specific validators:

```go
validate.Register("username", func(v string) string {
    if strings.Contains(v, " ") {
        return "Username cannot contain spaces"
    }
    return ""
})

msg := validate.Run("username", input)
```

---

## Validation Errors

`respond.ValidationError` returns a 422 response with field errors:

```go
return respond.ValidationError(c, errors)
```

- **JSON output:** `{"error": "Validation failed", "fields": {"email": "Invalid email address"}}`
- **HTML output:** renders a templ component for Out-of-Band (OOB) swap display (pass as an additional argument)

---

## CSRF Protection

Enable CSRF on your site route group:

```go
siteGroup.Use(middleware.CSRF())
```

Include the CSRF token in forms:

```html
<form method="POST" action="/register">
    <input type="hidden" name="csrf_token" value={ csrfToken }/>
    <!-- form fields -->
</form>
```

Configure with custom options:

```go
siteGroup.Use(middleware.CSRFWithConfig(middleware.CSRFConfig{
    CookieName:  "csrf",
    TokenLookup: "form:csrf_token,header:X-CSRF-Token",
    Secure:      true,
}))
```

---

## Flash Messages

One-time messages shown on the next request, stored in a cookie.

### Setup

```go
siteGroup.Use(middleware.Flash())
```

### Setting a Flash

```go
middleware.SetFlash(c, "Account created successfully", middleware.FlashSuccess)
```

Flash types: `FlashInfo`, `FlashSuccess`, `FlashWarning`, `FlashError`.

### Reading a Flash

```go
if flash := middleware.GetFlash(c); flash != nil {
    // render flash.Type and flash.Message in your template
}
```

The cookie is cleared after reading — flash messages appear exactly once.

---

## HTMX Field Validation

For inline field validation with HTMX, create a validation endpoint that returns individual field errors:

```html
<input
    name="email"
    hx-post="/validate/email"
    hx-trigger="blur"
    hx-target="#email-error"
/>
<span id="email-error"></span>
```

```go
func (h *Handler) ValidateEmail(c echo.Context) error {
    email := c.FormValue("email")
    if msg := validate.Email(email); msg != "" {
        return respond.HTML(c, http.StatusOK, templates.FieldError(msg))
    }
    return respond.HTML(c, http.StatusOK, templates.FieldError(""))
}
```

---

## Password Strength

For UI display, get individual requirement statuses:

```go
reqs := validate.CheckPasswordRequirements(password)
for _, r := range reqs {
    fmt.Printf("%s: %v\n", r.Description, r.Met)
}
```

---

## Next Steps

- [Authentication](07-authentication.md) — Login/register flow with sessions
- [Templates & Frontend](05-templates-frontend.md) — HTMX patterns for forms
