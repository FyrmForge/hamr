# Forms & Validation

HAMR uses simple function-based validators rather than struct-tag validation — this keeps validation explicit, testable, and easy to follow in handlers. The `validate.Form` API lets you define field rules once and reuse them for both full-form validation and HTMX per-field validation.

**Package references:** [Validate](pkg/validate.md), [Middleware](pkg/middleware.md) (flash, CSRF sections)

---

## Form Handling Pattern

The recommended pattern uses `validate.Form` — define rules once in the handler constructor, then validate in your handler:

```go
type handler struct {
    RegisterFormRules validate.Form
}

func NewHandler() *handler {
    return &handler{
        RegisterFormRules: validate.NewForm(
            validate.WithOOBRenderer(form.OOBValidator),
            validate.WithGeneralError("Please fix the errors below."),
            validate.WithTrim(true),
            validate.Field("name", validate.Required, validate.MinLen(2)),
            validate.Field("email", validate.Required, validate.Email),
            validate.FieldMsg("password", "Password does not meet requirements",
                validate.Required, validate.PasswordStrength,
            ),
        ),
    }
}

func (h *handler) Register(c echo.Context) error {
    var f RegisterForm
    c.Bind(&f)

    if errs := h.RegisterFormRules.Validate(c); errs != nil {
        return respond.HTML(c, http.StatusUnprocessableEntity, registerForm(c, f, errs))
    }

    // ... create user
    return respond.Redirect(c, "/login")
}
```

---

## Validators

Every validator is a pure function: `func(value) string`. Empty string means valid. Non-empty string is the error message.

### Built-in Validators

```go
validate.Required(value)             // non-empty check
validate.Email(value)                // email format
validate.Phone(value)                // 7-15 digits; + must be followed by 1-9; separators OK
validate.URL(value)                  // valid absolute URL
validate.MinLength(value, 3)         // at least 3 runes
validate.MaxLength(value, 100)       // at most 100 runes
validate.OneOf(value, "a", "b", "c") // allowed values
validate.IntRange(age, 18, 120)      // numeric range
validate.MinAge("1990-01-15", 18)    // YYYY-MM-DD, minimum age
validate.PasswordStrength(password)  // checks all requirements
```

### Curried Constructors

Curried versions return `func(string) string` and work directly as `Form` rules:

```go
validate.MinLen(3)                   // func(string) string
validate.MaxLen(100)                 // func(string) string
validate.In("admin", "user")        // func(string) string
validate.AgeMin(18)                  // func(string) string
validate.AgeMax(120)                 // func(string) string
```

### Optional Fields

All validators reject empty strings by default. For optional fields that must be valid when provided, wrap with `EmptyOr`:

```go
validate.Field("website", validate.EmptyOr(validate.URL))
```

### Custom Messages

Every validator has a `*Msg` variant:

```go
validate.RequiredMsg(name, "Please enter your name")
validate.MinLengthMsg(password, 8, "Password must be at least 8 characters")
```

### Custom Validators

Any `func(string) string` works as a rule:

```go
func noSpaces(value string) string {
    if strings.Contains(value, " ") {
        return "Cannot contain spaces"
    }
    return ""
}
validate.Field("username", validate.Required, noSpaces)
```

Or register project-specific validators by name:

```go
validate.Register("username", noSpaces)
msg := validate.Run("username", input)
```

---

## Form API

### Defining Rules

Rules are defined once using `validate.NewForm` and attached to the handler:

```go
type handler struct {
    ContactFormRules validate.Form
}

func NewHandler() *handler {
    return &handler{
        ContactFormRules: validate.NewForm(
            validate.WithOOBRenderer(form.OOBValidator),
            validate.WithTrim(true),
            validate.Field("name", validate.Required, validate.MinLen(2)),
            validate.Field("email", validate.Required, validate.Email),
            validate.Field("message", validate.Required, validate.MaxLen(1000)),
        ),
    }
}
```

### Field Definitions

```go
// Default messages from each rule
validate.Field("email", validate.Required, validate.Email)

// One message for any failure on the field
validate.FieldMsg("email", "Email is invalid", validate.Required, validate.Email)
```

### Error Message Customization — Three Levels

**Level 1 — Default messages** from each rule:

```go
validate.Field("email", validate.Required, validate.Email)
// Required failure -> "This field is required"
// Email failure -> "Invalid email address"
```

**Level 2 — Field-level override** (`FieldMsg`): one message for any failure:

```go
validate.FieldMsg("email", "Email is invalid", validate.Required, validate.Email)
// Any failure -> "Email is invalid"
```

**Level 3 — Per-rule override** (`WithMsg`): override a specific rule's message:

```go
validate.Field("email",
    validate.Required,
    validate.WithMsg(validate.Email, "Please enter a valid email"),
)
```

### Form Options

| Option | Purpose |
|---|---|
| `WithOOBRenderer(fn)` | Renderer for `ValidationHandler` — how to render per-field errors as OOB HTML |
| `WithGeneralError(msg)` | Added to error map under key `"general"` when any field fails |
| `WithTrim(bool)` | Auto-trim whitespace before validating |
| `WithShortCircuit(bool)` | Stop after first field error (default: false — validate all fields) |

### Full Form Validation

```go
errs := h.ContactFormRules.Validate(c)  // map[string]string or nil
```

Per-field rules always short-circuit: `Field("email", Required, Email)` won't check `Email` if `Required` fails. Per-form short-circuit is off by default — all fields are validated.

### RunRules — Standalone Rule Chain

For quick one-off validation outside a `Form`:

```go
errMsg := validate.RunRules(email, validate.Required, validate.Email)
```

---

## Context-Aware Rules

For rules that need the Echo context (e.g. cross-field comparisons):

```go
validate.Field("password_confirm", validate.Required).
    WithCtx(func(c echo.Context, value string) string {
        if value != c.FormValue("password") {
            return "Passwords do not match"
        }
        return ""
    })
```

Context-aware rules run after standard rules and only if all standard rules pass.

`WithTrim` only trims the field being validated. Other values read inside a `CtxRule` (e.g. `c.FormValue("password")`) are untrimmed — apply `strings.TrimSpace` manually if cross-field comparisons need it.

When this rule is exposed via HTMX per-field validation, the other field must be sent in the request — see [Cross-Field Validation (HTMX)](#cross-field-validation-htmx) below.

---

## HTMX Per-Field Validation

The `Form` API eliminates per-field validation handlers entirely. Register one route:

```go
authGroup.POST("/register/validate/:field", h.RegisterFormRules.ValidationHandler("field"))
```

`ValidationHandler` automatically reads the field param, gets the form value, runs that field's rules, and calls the OOB renderer. Unknown fields are silently ignored.

### OOB Renderer

Define once in form helpers:

```go
func OOBValidator(c echo.Context, field, errMsg string) error {
    status := http.StatusOK
    if errMsg != "" {
        status = http.StatusUnprocessableEntity
    }
    return respond.HTML(c, status, FieldErrorOOB(field, errMsg))
}
```

### Per-Field Custom Renderer

A field can override the form-level renderer (e.g. for password requirement checklists):

```go
validate.Field("password", validate.Required, validate.PasswordStrength).
    WithRenderer(form.PasswordRequirementsRenderer)
```

### Error Clear Flow

The HTMX trigger pattern handles error clearing automatically:

```html
<input
    name="email"
    hx-post="/register/validate/email"
    hx-trigger="blur, input[this.closest('div').querySelector('[data-has-error=true]')] delay:300ms"
    hx-swap="none"
/>
@form.FieldError("email", form.GetError(errs, "email"))
```

1. User types bad email, leaves field -> `blur` fires -> OOB swap with `data-has-error="true"`
2. User corrects -> `input` fires (because `data-has-error="true"` matches) -> after 300ms -> passes -> OOB swap with empty span, `data-has-error="false"`
3. Once `data-has-error="false"`, input trigger stops firing on keystrokes

### Cross-Field Validation (HTMX)

`WithCtx` rules can read other form fields via `c.FormValue("other_field")` — but per-field HTMX requests, by default, only send the value of the input that triggered them. To make other fields available, pull them in with `hx-include`:

```html
<input
    type="password"
    name="password_confirm"
    hx-post="/register/validate/password_confirm"
    hx-include="[name='password']"
    hx-trigger="blur, input[this.closest('.form-group').querySelector('[data-has-error=true]')] delay:300ms"
    hx-swap="none"
/>
```

Without `hx-include`, `c.FormValue("password")` inside the `CtxRule` returns `""` and the comparison silently misbehaves (an empty `password_confirm` typically passes a `value != ""` check, masking the mismatch).

The validation is also asymmetric: editing `password_confirm` re-runs the comparison, but editing `password` does not re-validate `password_confirm`. To keep the confirm error in sync, mirror the wiring on the password input — point its trigger at the confirm validator and include the confirm field:

```html
<input
    type="password"
    name="password"
    hx-post="/register/validate/password_confirm"
    hx-include="[name='password_confirm']"
    hx-trigger="input[this.closest('form').querySelector('[name=password_confirm][data-has-error=true]')] delay:300ms"
    hx-swap="none"
/>
```

If you don't want this round-tripping, skip the cross-trigger and rely on the full-form `Validate(c)` at submit time to surface the mismatch.

---

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

## Password Strength

For UI display, get individual requirement statuses:

```go
reqs := validate.CheckPasswordRequirements(password)
for _, r := range reqs {
    fmt.Printf("%s: %v\n", r.Description, r.Met)
}
```

---

## Dependency Injection via Constructor Closures

Rules defined in the constructor close over handler dependencies:

```go
func NewHandler(cfg Config) *handler {
    return &handler{
        FormRules: validate.NewForm(
            validate.Field("role", validate.Required, validate.In(cfg.AllowedRoles...)),
            validate.Field("bio", validate.MaxLen(cfg.MaxBioLength)),
        ),
    }
}
```

**Important boundary**: Rules are `func(string) string` — no `context.Context`. Anything needing DB queries or request-scoped context belongs in the handler after validation.

---

## Next Steps

- [Authentication](07-authentication.md) — Login/register flow with sessions
- [Templates & Frontend](05-templates-frontend.md) — HTMX patterns for forms
