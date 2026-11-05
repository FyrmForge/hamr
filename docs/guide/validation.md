# Forms and validation

HAMR uses simple function-based validators rather than struct-tag validation — this keeps validation explicit, testable, and easy to follow in handlers. The `validate.Form` API lets you define field rules once and reuse them for both full-form validation and HTMX per-field validation.

**Package references:** [Validate](pkg/validate.md), [Middleware](pkg/middleware.md) (flash, CSRF sections)

## Form handling pattern

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
    if err := c.Bind(&f); err != nil {
        return echo.NewHTTPError(http.StatusBadRequest, "invalid form data")
    }

    if errs := h.RegisterFormRules.Validate(c); errs != nil {
        return respond.HTML(c, http.StatusUnprocessableEntity, registerForm(c, f, errs))
    }

    // ... create user
    return respond.Redirect(c, "/login")
}
```

`Validate(c)` reads the request's form values itself; it doesn't look at the bound struct. `WithTrim` therefore trims only the value being validated — `f.Name` still holds the untrimmed input, so `strings.TrimSpace` it yourself if you store it.

## Validators

Validators return a string: empty means valid, non-empty is the error message. A form rule (`validate.Rule`) is `func(string) string`. Parameterised validators like `MinLength(value, 3)` take extra arguments, so use their curried forms (`MinLen(3)`) as rules.

### Built-in validators

```go
validate.Required(value)             // non-empty check
validate.Email(value)                // email format
validate.Phone(value)                // optional +, 7-15 digits
validate.URL(value)                  // absolute http:// or https:// URL with a host
validate.MinLength(value, 3)         // at least 3 runes
validate.MaxLength(value, 100)       // at most 100 runes
validate.OneOf(value, "a", "b", "c") // allowed values
validate.IntRange(age, 18, 120)      // numeric range
validate.MinAge("1990-01-15", 18)    // YYYY-MM-DD, minimum age
validate.MaxAge("1990-01-15", 120)   // YYYY-MM-DD, maximum age
validate.PasswordStrength(password)  // checks all requirements
validate.HasUpper(value)             // at least one uppercase letter
validate.HasLower(value)             // at least one lowercase letter
validate.HasDigit(value)             // at least one digit
validate.HasSpecial(value)           // at least one special character
```

`IntRange` takes an `int`, so it isn't usable as a form rule. `validate.NormalizeURL(value)` prepends `https://` when the scheme is missing — run it before `URL` if you accept bare domains.

### Curried constructors

Curried versions return a `validate.Rule` and work directly as `Form` rules. They have no `*Msg` variants — wrap them with `WithMsg` (below):

```go
validate.MinLen(3)                   // func(string) string
validate.MaxLen(100)                 // func(string) string
validate.In("admin", "user")        // func(string) string
validate.AgeMin(18)                  // func(string) string
validate.AgeMax(120)                 // func(string) string
```

### Optional fields

Most validators reject an empty string; `MaxLength`/`MaxLen` accept it. For optional fields that must be valid when provided, wrap with `EmptyOr`:

```go
validate.Field("website", validate.EmptyOr(validate.URL))
```

### Custom messages

Every non-curried validator has a `*Msg` variant:

```go
validate.RequiredMsg(name, "Please enter your name")
validate.MinLengthMsg(password, 8, "Password must be at least 8 characters")
```

### Custom validators

Any `func(string) string` works as a `Rule`:

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

## Form API

### Defining rules

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

### Field definitions

```go
// Default messages from each rule
validate.Field("email", validate.Required, validate.Email)

// One message for any failure on the field
validate.FieldMsg("email", "Email is invalid", validate.Required, validate.Email)
```

### Error messages in three levels

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

### Form options

| Option | Purpose |
|---|---|
| `WithOOBRenderer(fn)` | Renderer for `ValidationHandler` — how to render per-field errors as OOB HTML |
| `WithGeneralError(msg)` | Added to error map under key `"general"` when any field fails |
| `WithTrim(bool)` | Trim whitespace from each field's value before its rules run (validation only — bound structs are untouched) |
| `WithShortCircuit(bool)` | Stop after first field error (default: false — validate all fields) |

### Full-form validation

```go
errs := h.ContactFormRules.Validate(c)  // map[string]string or nil
```

Per-field rules always short-circuit: `Field("email", Required, Email)` won't check `Email` if `Required` fails. Per-form short-circuit is off by default — all fields are validated.

### One-off rule chains with RunRules

For quick one-off validation outside a `Form`:

```go
errMsg := validate.RunRules(email, validate.Required, validate.Email)
```

## Context-aware rules

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

## HTMX per-field validation

The `Form` API eliminates per-field validation handlers entirely. Register one route:

```go
site.POST("/register/validate/:field", h.RegisterFormRules.ValidationHandler("field"))
```

`ValidationHandler` automatically reads the field param, gets the form value, runs that field's rules, and calls the OOB renderer. Unknown fields are silently ignored.

### OOB renderer

Define once in the form helpers:

```go title="internal/web/components/form/helpers.go"
func OOBValidator(c echo.Context, field, errMsg string) error {
    status := http.StatusOK
    if errMsg != "" {
        status = http.StatusUnprocessableEntity
    }
    return respond.HTML(c, status, FieldErrorOOB(field, errMsg))
}
```

### Per-field custom renderer

A field can override the form-level renderer (e.g. for password requirement checklists):

```go
validate.Field("password", validate.Required, validate.PasswordStrength).
    WithRenderer(form.PasswordRequirementsRenderer)
```

### Error clear flow

The HTMX trigger pattern handles error clearing automatically:

```templ
<input
    name="email"
    hx-post="/register/validate/email"
    hx-trigger="blur, hamr:revalidate"
    hx-swap="none"
/>
@form.FieldError("email", form.GetError(errs, "email"))
```

1. User types bad email, leaves field -> `blur` fires -> OOB swap with `data-has-error="true"`
2. User corrects -> each keystroke is seen by the listener in `frontend/static/js/main.js` -> it sees `data-has-error="true"` -> 300ms after the last keystroke it fires `hamr:revalidate` -> passes -> OOB swap with empty span, `data-has-error="false"`
3. Once `data-has-error="false"`, the listener returns early and no further requests are made until the next `blur`

`hamr:revalidate` is a plain custom DOM event; htmx listens for it because it is
named in `hx-trigger`. The "only while already in error" condition lives in
`frontend/static/js/main.js`, not in an `hx-trigger` `[...]` filter, because htmx compiles
those filters with `Function()` — which requires `'unsafe-eval'` in the CSP. The
listener pairs an input with its error span by id convention: `name="email"` ->
`id="error-email"`. Set `data-hamr-watch="other_field"` to gate on a different
field's error span instead (see cross-field validation below).

### Cross-field validation (HTMX)

`WithCtx` rules can read other form fields via `c.FormValue("other_field")`. An input inside a `<form>` already sends every field of that form on a POST (htmx includes the enclosing form for non-GET requests), so nothing extra is needed there. When the other field lives outside that form, pull it in with `hx-include`:

```html
<input
    type="password"
    name="password_confirm"
    hx-post="/register/validate/password_confirm"
    hx-include="[name='password']"
    hx-trigger="blur, hamr:revalidate"
    hx-swap="none"
/>
```

If the field is neither in the enclosing form nor included, `c.FormValue("password")` inside the `CtxRule` returns `""` and the comparison silently misbehaves (an empty `password_confirm` typically passes a `value != ""` check, masking the mismatch).

The validation is also asymmetric: editing `password_confirm` re-runs the comparison, but editing `password` does not re-validate `password_confirm`. To keep the confirm error in sync, mirror the wiring on the password input — point its trigger at the confirm validator, include the confirm field if it's outside the form, and set `data-hamr-watch` so typing in `password` is gated on the *confirm* field's error rather than its own:

```html
<input
    type="password"
    name="password"
    hx-post="/register/validate/password_confirm"
    hx-include="[name='password_confirm']"
    data-hamr-watch="password_confirm"
    hx-trigger="hamr:revalidate"
    hx-swap="none"
/>
```

If you don't want this round-tripping, skip the cross-trigger and rely on the full-form `Validate(c)` at submit time to surface the mismatch.

## CSRF protection

The scaffold enables CSRF on the site route group:

```go title="internal/web/server.go"
site.Use(hamrmw.CSRFWithConfig(hamrmw.CSRFConfig{Secure: !deps.DevMode}))
```

(`hamrmw` is the scaffold's import alias for `pkg/middleware`.) `hamrmw.CSRF()` is the same with `Secure: true`. The defaults are cookie `csrf` and `TokenLookup: "form:csrf_token,header:X-CSRF-Token"`.

Render the token in every form with the scaffold's helper (`internal/web/components/form/fields.templ`), which writes a hidden `csrf_token` input:

```templ title="internal/web/handler/auth/register/register.templ"
<form hx-post="/register" hx-target="#register-form" hx-swap="outerHTML" id="register-form">
    @form.CSRFField(c)
    <!-- form fields -->
</form>
```

The layout's inline script copies the token into an `X-CSRF-Token` header for htmx requests made outside a form, and makes htmx swap `422` responses so re-rendered forms and OOB field errors reach the page. Keep both parts if you replace the layout — see [HTMX setup in the layout](templates.md#htmx-setup-in-the-layout).

## Flash messages

One-time messages shown on the next request, stored in a cookie.

### Setup

```go title="internal/web/server.go"
site.Use(hamrmw.FlashWithConfig(hamrmw.FlashConfig{Secure: !deps.DevMode}))
```

`hamrmw.Flash()` is the same with `Secure: true`. In handler packages the scaffold imports `pkg/middleware` unaliased, so the helpers below are `middleware.SetFlash` / `middleware.GetFlash`.

### Setting a flash

```go
middleware.SetFlash(c, "Account created successfully", middleware.FlashSuccess)
```

Flash types: `FlashInfo`, `FlashSuccess`, `FlashWarning`, `FlashError`.

### Reading a flash

```go
if flash := middleware.GetFlash(c); flash != nil {
    // render flash.Type and flash.Message in your template
}
```

The cookie is cleared after reading — flash messages appear exactly once.

## Password strength

For UI display, get individual requirement statuses:

```go
reqs := validate.CheckPasswordRequirements(password)
for _, r := range reqs {
    fmt.Printf("%s: %v\n", r.Description, r.Met)
}
```

## Dependencies in rules via constructor closures

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

**Important boundary**: plain rules are `func(string) string`, and `WithCtx` rules get only the Echo context (for reading other form values — see [Context-Aware Rules](#context-aware-rules)). Anything needing DB queries or services belongs in the handler after validation.
