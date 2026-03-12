# Form Binding & Validation API

## Problem

The current validation pattern requires handlers to manually extract form values one by one, then wire them into validators with repeated field names:

```go
name := c.FormValue("name")
email := c.FormValue("email")
message := c.FormValue("message")

fieldErrors := validate.Check(
    validate.Field("name", validate.Required(name)),
    validate.Field("email", validate.Required(email)),
    validate.Field("email", validate.Email(email)),
    validate.Field("message", validate.Required(message)),
)
```

HTMX per-field validation endpoints make it worse — every field's rules get duplicated in a switch statement:

```go
func (h *handler) ValidateField(c echo.Context) error {
    field := c.Param("field")
    value := c.FormValue(field)
    switch field {
    case "name":     errMsg = validate.Required(value)
    case "email":    // duplicate chain...
    case "password": // duplicate chain...
    }
    return respond.HTML(c, status, form.FieldErrorOOB(field, errMsg))
}
```

This is tedious, error-prone, and scales poorly as forms grow.

## Proposal

Separate binding from validation, define rules once, and eliminate per-field HTMX handlers entirely.

### Core Design Decision

- **Echo's `c.Bind()` handles struct population** — type conversion, reflection, `form:` tags, all built-in
- **The validate package handles validation only** — core validators stay pure functions, `Form` type adds Echo integration
- **No custom reflection** in the validate package

Handler flow:

```go
var f RegisterForm
c.Bind(&f)           // Echo handles types, reflection, all of it
errs := f.Validate() // our validation runs on the populated struct
```

## Breaking Changes

The old `Field`, `FieldResult`, and `Check` helpers are removed. The new `Form` API replaces them entirely. The `Field` name is reused for the new field definition function.

## The `validate.Form` Type

A new `Form` type holds field rule definitions. It is used for both full form validation and HTMX per-field validation — rules are defined once.

```go
type Form struct {
    fields       []fieldDef
    renderer     FieldRenderer
    generalError string
    trim         bool
    shortCircuit bool
}

type fieldDef struct {
    name     string
    rules    []Rule
    ctxRules []CtxRule
    msg      string        // for FieldMsg override
    renderer FieldRenderer // per-field override
}

type FieldRenderer func(c echo.Context, field, errMsg string) error
```

### `Rule` and `CtxRule` Type Aliases

```go
type Rule = func(string) string              // alias, not named type
type CtxRule = func(echo.Context, string) string  // context-aware rule
```

Using type aliases (not named types) means existing validators like `validate.Required` work directly as rules with zero adaptation — no casting required.

## `FieldBuilder` — Chainable Field Definition

`Field()` and `FieldMsg()` return a `FieldBuilder` that satisfies `FormOption`. This enables chaining for per-field configuration:

```go
type FieldBuilder struct {
    def fieldDef
}

// Field defines a field with rules that use default error messages.
func Field(name string, rules ...Rule) FieldBuilder

// FieldMsg defines a field where any rule failure produces the given message.
func FieldMsg(name, msg string, rules ...Rule) FieldBuilder

// WithRenderer sets a custom renderer for this field only.
// Falls back to the form-level WithOOBRenderer if not set.
func (fb FieldBuilder) WithRenderer(fn FieldRenderer) FieldBuilder

// WithCtx adds context-aware rules to the field.
func (fb FieldBuilder) WithCtx(rules ...CtxRule) FieldBuilder
```

`FieldBuilder` implements `FormOption`, so it works directly in `NewForm`:

```go
validate.NewForm(
    validate.Field("name", validate.Required),                              // no custom renderer
    validate.Field("password", validate.Required).WithRenderer(pwRenderer), // custom renderer
)
```

## Constructor with `FormOption` Interface

Everything passed to `NewForm` implements a single `FormOption` interface — both configuration options and field definitions:

```go
type FormOption interface {
    apply(*formConfig)
}

type formConfig struct {
    fields       []fieldDef
    renderer     FieldRenderer
    generalError string
    trim         bool
    shortCircuit bool
}

func NewForm(opts ...FormOption) Form {
    var cfg formConfig
    for _, o := range opts {
        o.apply(&cfg)
    }
    return Form{
        fields:       cfg.fields,
        renderer:     cfg.renderer,
        generalError: cfg.generalError,
        trim:         cfg.trim,
        shortCircuit: cfg.shortCircuit,
    }
}
```

### Form Options

| Option | Purpose |
|---|---|
| `WithOOBRenderer(fn)` | Renderer for `ValidationHandler` — how to render per-field errors as OOB HTML |
| `WithGeneralError(msg)` | Added to error map under key `"general"` when any field fails validation |
| `WithTrim(bool)` | Auto-trim whitespace from form values before validating |
| `WithShortCircuit(bool)` | Stop validating after the first field that fails (default: false — validate all fields) |

### Short-Circuit Behavior

Two levels of short-circuiting:

- **Per-field (always on):** Rules for a single field stop at the first failure. `Field("email", Required, Email)` won't check `Email` if `Required` fails. One error per field.
- **Per-form (configurable):** `WithShortCircuit(true)` stops after the first field with an error. Default is `false` — validate all fields and return all errors.

The return type is always `map[string]string` — per-field short-circuit means at most one error per field name.

## Error Message Customization — Three Levels

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

Where `WithMsg` returns a new `func(string) string`:

```go
func WithMsg(rule func(string) string, msg string) func(string) string {
    return func(value string) string {
        if result := rule(value); result != "" {
            return msg
        }
        return ""
    }
}
```

## `RunRules` — Standalone Rule Chain

For quick one-off validation without a `Form`:

```go
func RunRules(value string, rules ...Rule) string {
    for _, r := range rules {
        if msg := r(value); msg != "" {
            return msg
        }
    }
    return ""
}
```

Usage:

```go
errMsg := validate.RunRules(email, validate.Required, validate.Email)
```

## Handler Pattern — Rules in Constructor

Rules are defined on the handler struct, initialized in the constructor. Field names are exported so route registration can access `ValidationHandler`:

```go
type handler struct {
    authService       *service.AuthService
    sessionManager    *auth.SessionManager
    RegisterFormRules validate.Form
    LoginFormRules    validate.Form
}

func NewHandler(authService *service.AuthService, sm *auth.SessionManager) *handler {
    return &handler{
        authService:    authService,
        sessionManager: sm,
        RegisterFormRules: validate.NewForm(
            validate.WithOOBRenderer(form.OOBValidator),
            validate.WithGeneralError("Please fix the errors below and try again."),
            validate.WithTrim(true),
            validate.Field("name", validate.Required, validate.MinLen(2)),
            validate.Field("email",
                validate.Required,
                validate.WithMsg(validate.Email, "Please enter a valid email"),
            ),
            validate.FieldMsg("password", "Password does not meet requirements",
                validate.Required, validate.PasswordStrength,
            ),
            validate.Field("website", validate.EmptyOr(validate.URL)),
            validate.Field("role", validate.Required, validate.In("user", "admin", "editor")),
        ),
        LoginFormRules: validate.NewForm(
            validate.Field("email", validate.Required, validate.Email),
            validate.Field("password", validate.Required),
        ),
    }
}
```

## Route Registration — One Line for HTMX Validation

```go
h := auth.NewHandler(authService, sm)

authGroup.POST("/register", h.Register)
authGroup.POST("/register/validate/:field", h.RegisterFormRules.ValidationHandler("field"))
authGroup.POST("/login", h.Login)
authGroup.POST("/login/validate/:field", h.LoginFormRules.ValidationHandler("field"))
```

`ValidationHandler("field")` takes the param name as an argument so it is not hardcoded. It returns `echo.HandlerFunc`.

**Unknown field handling**: `ValidationHandler` silently ignores field names that are not defined in the form — it returns an empty/no-error response. This prevents information leakage and avoids validating fields the form doesn't own.

## Full Form Validation

`Form.Validate(c)` loops over field definitions, calls `c.FormValue(field)` for each, runs rules with per-field short-circuit. Returns `map[string]string` (field name to error message) or `nil`.

```go
func (h *handler) Register(c echo.Context) error {
    var f RegisterForm
    c.Bind(&f)
    f.Email = strings.ToLower(f.Email)

    if errs := h.RegisterFormRules.Validate(c); errs != nil {
        return respond.HTML(c, http.StatusUnprocessableEntity, registerForm(c, f, errs))
    }

    user, err := h.authService.Register(c.Request().Context(), f.Email, f.Password, f.Name)
    if err != nil {
        if errors.Is(err, service.ErrEmailTaken) {
            return respond.HTML(c, 422, registerForm(c, f, map[string]string{
                "email": "An account with this email already exists",
            }))
        }
        return echo.NewHTTPError(500, "Registration failed")
    }
    // ...
}
```

## HTMX Per-Field Validation — Zero Code

`ValidationHandler` does everything:

1. Reads `:field` param
2. Gets `c.FormValue(field)`
3. Runs that field's rules (including `CtxRule`s)
4. Calls the field's renderer (or falls back to form-level `WithOOBRenderer`)

No switch statement. No handler code needed per field.

HTMX per-field validation sends only the single field value (not the whole form). The handler validates just that one field.

### Per-Field Custom Renderer

A field can override the form-level OOB renderer. Useful for fields like passwords that need a custom error component (e.g. a requirements checklist):

```go
validate.Field("password", validate.Required, validate.PasswordStrength).
    WithRenderer(form.PasswordRequirementsRenderer)
```

The renderer signature is the same: `func(c echo.Context, field, errMsg string) error`. The field renderer is checked first; if nil, the form-level renderer is used.

### OOB Renderer

Defined once in form helpers:

```go
// internal/web/components/form/helpers.go
func OOBValidator(c echo.Context, field, errMsg string) error {
    status := http.StatusOK
    if errMsg != "" {
        status = http.StatusUnprocessableEntity
    }
    return respond.HTML(c, status, FieldErrorOOB(field, errMsg))
}
```

### Error Clear Flow

The existing HTMX trigger pattern handles error clearing automatically:

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

## Context-Aware Rules (`WithCtx`)

For rules that need the Echo context (e.g. checking other form values, request headers):

```go
validate.Field("password_confirm",
    validate.Required,
).WithCtx(func(c echo.Context, value string) string {
    if value != c.FormValue("password") {
        return "Passwords do not match"
    }
    return ""
})
```

Context-aware rules run after standard rules (and only if all standard rules pass). They follow the same per-field short-circuit behavior.

**Important boundary**: `CtxRule` has access to `echo.Context` but should still be used for pure validation logic (comparing fields, checking headers). Anything needing DB queries or async operations belongs in the handler after validation.

## Generic `Rules[T]` — Typed Field Validation (Phase 3)

For fields that are already typed (after `c.Bind`), generic rules handle non-string types:

```go
func Rules[T any](name string, value T, rules ...func(T) string) FieldResult
func RulesMsg[T any](name string, value T, msg string, rules ...func(T) string) FieldResult
func RunRules[T any](value T, rules ...func(T) string) string
```

Go infers `T` from the value:

```go
validate.Rules("name", f.Name, validate.Required)         // T = string
validate.Rules("age", f.Age, validate.IntBetween(13, 120)) // T = int
validate.Rules("agree", f.Agree, validate.MustBeTrue)      // T = bool
```

### Generic `EmptyOr[T]` (Phase 3)

For typed validators, a generic `EmptyOr` checks the zero value:

```go
func EmptyOrTyped[T comparable](rule func(T) string) func(T) string {
    return func(v T) string {
        var zero T
        if v == zero { return "" }
        return rule(v)
    }
}
```

### Mixing String and Typed Validation

For forms with mixed types, merge both error maps:

```go
func (h *handler) CreateProfile(c echo.Context) error {
    var f ProfileForm
    c.Bind(&f)

    // String validation via Form
    errs := h.ProfileFormRules.Validate(c)

    // Typed validation via generic Rules
    typedErrs := validate.Check(
        validate.Rules("age", f.Age, validate.IntBetween(13, 120)),
        validate.Rules("score", f.Score, validate.FloatBetween(0.0, 100.0)),
        validate.Rules("agree", f.Agree, validate.MustBeTrue),
    )

    if typedErrs != nil {
        if errs == nil {
            errs = typedErrs
        } else {
            maps.Copy(errs, typedErrs)
        }
    }

    if errs != nil {
        return respond.HTML(c, 422, profileForm(c, f, errs))
    }
    // ...
}
```

## Built-in Validators

### Existing (unchanged, work directly as rules)

- `Required`, `Email`, `Phone`, `URL`, `PasswordStrength`

### Curried Constructors (Phase 1)

| Constructor | Returns |
|---|---|
| `MinLen(n)` | `func(string) string` |
| `MaxLen(n)` | `func(string) string` |
| `In(vals...)` | `func(string) string` |
| `AgeMin(n)` | `func(string) string` |
| `AgeMax(n)` | `func(string) string` |

`EmptyOr` already exists as a curried constructor and works directly as a rule composer.

### Typed Validators (Phase 3)

| Type | Validator | Signature |
|---|---|---|
| `int` | `IntBetween(min, max)` | `func(int) string` |
| `float64` | `FloatBetween(min, max)` | `func(float64) string` |
| `bool` | `MustBeTrue` | `func(bool) string` |
| `time.Time` | `After(t)` | `func(time.Time) string` |
| `time.Time` | `Before(t)` | `func(time.Time) string` |
| `time.Time` | `Between(min, max)` | `func(time.Time) string` |

### String-Based Date Validators (Phase 3)

- `DateFormat(layout)` — validates string parses with layout
- `DateAfter(layout, ref)` — parses then checks after ref
- `DateBefore(layout, ref)` — parses then checks before ref

## Custom Validators

Since rules are just `func(string) string`, any custom function works:

```go
// Inline custom rule
func noSpaces(value string) string {
    if strings.Contains(value, " ") { return "Cannot contain spaces" }
    return ""
}
validate.Field("username", validate.Required, noSpaces)

// Curried reusable constructor
func Matches(pattern *regexp.Regexp, msg string) func(string) string {
    return func(value string) string {
        if !pattern.MatchString(value) {
            return msg
        }
        return ""
    }
}
```

## Dependency Injection via Constructor Closures

Rules are defined in the constructor, so they close over the handler's dependencies:

```go
func NewHandler(cfg Config, authService *service.AuthService, sm *auth.SessionManager) *handler {
    return &handler{
        RegisterFormRules: validate.NewForm(
            validate.Field("role", validate.Required, validate.In(cfg.AllowedRoles...)),
            validate.Field("bio", validate.MaxLen(cfg.MaxBioLength)),
            validate.Field("invite_code", validate.Required, func(value string) string {
                if !authService.ValidInviteCode(value) {
                    return "Invalid invite code"
                }
                return ""
            }),
        ),
    }
}
```

**Important boundary**: Rules are `func(T) string` — no `context.Context`. Anything needing DB queries or request-scoped context belongs in the handler after validation:

- **Pure validation** (format, length, range, static checks) -> `Form` rules, can use config/deps from constructor
- **Business validation** (uniqueness, DB lookups, anything needing context) -> handler logic after validation

## Templ Integration

The templ component receives the form struct and errors map:

```templ
templ registerForm(c echo.Context, f RegisterForm, errs map[string]string) {
    <form id="register-form" hx-post="/register" hx-swap="outerHTML" ...>
        @form.CSRFField(c)
        <input name="name" value={ f.Name } />
        @form.FieldError("name", form.GetError(errs, "name"))
        // ... more fields
    </form>
}
```

## Comprehensive Example

```go
// 1. FORM STRUCT
type BookingForm struct {
    Title    string    `form:"title"`
    StartsAt time.Time `form:"starts_at"`
}

// 2. HANDLER WITH RULES
type handler struct {
    bookingService   *service.BookingService
    BookingFormRules validate.Form
}

func NewHandler(bookingService *service.BookingService) *handler {
    return &handler{
        bookingService: bookingService,
        BookingFormRules: validate.NewForm(
            validate.WithOOBRenderer(form.OOBValidator),
            validate.WithGeneralError("Please fix the errors below."),
            validate.WithTrim(true),
            validate.Field("title", validate.Required, validate.MinLen(3), validate.MaxLen(200)),
        ),
    }
}

// 3. ROUTES
h := booking.NewHandler(bookingService)
bookingGroup.POST("/create", h.Create)
bookingGroup.POST("/create/validate/:field", h.BookingFormRules.ValidationHandler("field"))

// 4. HANDLER
func (h *handler) Create(c echo.Context) error {
    var f BookingForm
    c.Bind(&f)

    errs := h.BookingFormRules.Validate(c)

    typedErrs := validate.Check(
        validate.Rules("starts_at", f.StartsAt,
            validate.After(time.Now()),
            validate.Before(time.Now().AddDate(0, 0, 14)),
        ),
    )

    if typedErrs != nil {
        if errs == nil {
            errs = typedErrs
        } else {
            maps.Copy(errs, typedErrs)
        }
    }

    if errs != nil {
        return respond.HTML(c, 422, bookingForm(c, f, errs))
    }

    booking, err := h.bookingService.Create(c.Request().Context(), f.Title, f.StartsAt)
    // ...
}
```

## Implementation Phases

### Phase 1 — Core Form API

| File | Contents |
|---|---|
| `pkg/validate/form.go` | `Form` type, `NewForm`, `FormOption`, `FieldBuilder`, `Field`, `FieldMsg`, `Validate`, `ValidationHandler`, all options (`WithOOBRenderer`, `WithGeneralError`, `WithTrim`, `WithShortCircuit`), `FieldRenderer` |
| `pkg/validate/rules.go` | `Rule` type alias, `CtxRule` type alias, `WithMsg`, `RunRules` |
| `pkg/validate/constructors.go` | Curried validators: `MinLen`, `MaxLen`, `In`, `AgeMin`, `AgeMax` |

Also removes old `Field`, `FieldResult`, `Check` from `validate.go`.

### Phase 2 — Typed Validators (future)

| File | Contents |
|---|---|
| `pkg/validate/constructors.go` | `IntBetween`, `FloatBetween`, `MustBeTrue`, `After`, `Before`, `Between`, `DateFormat`, `DateAfter`, `DateBefore` |

### Phase 3 — Generic Rules (future)

| File | Contents |
|---|---|
| `pkg/validate/rules.go` | `Rules[T]`, `RulesMsg[T]`, generic `RunRules[T]`, `EmptyOrTyped[T]`, `Check` |
