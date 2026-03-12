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
- **The validate package handles validation only** — stays decoupled from Echo for the core validators
- **No custom reflection** in the validate package

Handler flow:

```go
var f RegisterForm
c.Bind(&f)           // Echo handles types, reflection, all of it
errs := f.Validate() // our validation runs on the populated struct
```

## The `validate.Form` Type

A new `Form` type holds field rule definitions. It is used for both full form validation and HTMX per-field validation — rules are defined once.

```go
type Form struct {
    fields       []fieldDef
    renderer     FieldRenderer
    generalError string
    trim         bool
}

type fieldDef struct {
    name  string
    rules []func(string) string
    msg   string // for FMsg override
}

type FieldRenderer func(c echo.Context, field, errMsg string) error
```

### `Rule` as Type Alias

```go
type Rule = func(string) string   // alias, not named type
```

Using a type alias (not a named type) means existing validators like `validate.Required` work directly as rules with zero adaptation — no casting required.

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
    }
}
```

### Form Options

| Option | Purpose |
|---|---|
| `WithOOBRenderer(fn)` | Renderer for `ValidationHandler` — how to render per-field errors as OOB HTML |
| `WithGeneralError(msg)` | Added to error map under key `""` (empty string) when any field fails validation |
| `WithTrim(bool)` | Auto-trim whitespace from form values before validating |

Concrete option implementations:

```go
type oobRendererOption struct{ fn FieldRenderer }
func (o oobRendererOption) apply(c *formConfig) { c.renderer = o.fn }
func WithOOBRenderer(fn FieldRenderer) FormOption { return oobRendererOption{fn} }

type generalErrorOption struct{ msg string }
func (o generalErrorOption) apply(c *formConfig) { c.generalError = o.msg }
func WithGeneralError(msg string) FormOption { return generalErrorOption{msg} }

type trimOption struct{ on bool }
func (o trimOption) apply(c *formConfig) { c.trim = o.on }
func WithTrim(on bool) FormOption { return trimOption{on} }

type fieldOption struct{ def fieldDef }
func (o fieldOption) apply(c *formConfig) { c.fields = append(c.fields, o.def) }

func F(name string, rules ...func(string) string) FormOption {
    return fieldOption{fieldDef{name: name, rules: rules}}
}

func FMsg(name, msg string, rules ...func(string) string) FormOption {
    return fieldOption{fieldDef{name: name, rules: rules, msg: msg}}
}
```

### Field Definitions: `F` and `FMsg`

```go
// Default messages from each rule
validate.F("email", validate.Required, validate.Email)

// One message for any failure on the field
validate.FMsg("email", "Email is invalid", validate.Required, validate.Email)
```

## Error Message Customization — Three Levels

**Level 1 — Default messages** from each rule:

```go
validate.F("email", validate.Required, validate.Email)
// Required failure -> "This field is required"
// Email failure -> "Invalid email address"
```

**Level 2 — Field-level override** (`FMsg`): one message for any failure:

```go
validate.FMsg("email", "Email is invalid", validate.Required, validate.Email)
// Any failure -> "Email is invalid"
```

**Level 3 — Per-rule override** (`WithMsg`): override a specific rule's message:

```go
validate.F("email",
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
            validate.F("name", validate.Required, validate.MinLen(2)),
            validate.F("email",
                validate.Required,
                validate.WithMsg(validate.Email, "Please enter a valid email"),
            ),
            validate.FMsg("password", "Password does not meet requirements",
                validate.Required, validate.PasswordStrength,
            ),
            validate.F("website", validate.EmptyOr(validate.URL)),
            validate.F("role", validate.Required, validate.In("user", "admin", "editor")),
        ),
        LoginFormRules: validate.NewForm(
            validate.F("email", validate.Required, validate.Email),
            validate.F("password", validate.Required),
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

`Form.Validate(c)` loops over field definitions, calls `c.FormValue(field)` for each, runs rules with short-circuit. Returns `map[string]string` (field name to error message) or `nil`.

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
3. Runs that field's rules
4. Calls the OOB renderer set via `WithOOBRenderer`

No switch statement. No handler code needed per field.

HTMX per-field validation sends only the single field value (not the whole form). The handler validates just that one field.

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

## Generic `Rules[T]` — Typed Field Validation

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

### Mixing String and Typed Validation

For forms with mixed types, merge both error maps:

```go
func (h *handler) CreateProfile(c echo.Context) error {
    var f ProfileForm
    c.Bind(&f)

    // String validation via Form
    errs := h.ProfileFormRules.Validate(c)

    // Typed validation via Rules
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

### New Curried Constructors

| Constructor | Returns |
|---|---|
| `MinLen(n)` | `func(string) string` |
| `MaxLen(n)` | `func(string) string` |
| `In(vals...)` | `func(string) string` |
| `EmptyOr(rule)` | `func(string) string` — empty string is valid, validates if provided |
| `AgeMin(n)` | `func(string) string` |
| `AgeMax(n)` | `func(string) string` |

### New Typed Validators

| Type | Validator | Signature |
|---|---|---|
| `int` | `IntBetween(min, max)` | `func(int) string` |
| `float64` | `FloatBetween(min, max)` | `func(float64) string` |
| `bool` | `MustBeTrue` | `func(bool) string` |
| `time.Time` | `After(t)` | `func(time.Time) string` |
| `time.Time` | `Before(t)` | `func(time.Time) string` |
| `time.Time` | `Between(min, max)` | `func(time.Time) string` |

### String-Based Date Validators (for the `Form` path)

- `DateFormat(layout)` — validates string parses with layout
- `DateAfter(layout, ref)` — parses then checks after ref
- `DateBefore(layout, ref)` — parses then checks before ref

## Custom Validators

Since `Rules[T any]` accepts `...func(T) string`, any custom function works:

```go
// Inline
validate.Rules("starts_at", f.StartsAt, func(t time.Time) string {
    if t.Before(time.Now()) {
        return "Must be in the future"
    }
    return ""
})

// Curried reusable constructor
func WithinDays(days int) func(time.Time) string {
    return func(t time.Time) string {
        now := time.Now()
        if t.Before(now) || t.After(now.AddDate(0, 0, days)) {
            return fmt.Sprintf("Must be within the next %d days", days)
        }
        return ""
    }
}

// Custom string validator for F()
func noSpaces(value string) string {
    if strings.Contains(value, " ") { return "Cannot contain spaces" }
    return ""
}
validate.F("username", validate.Required, noSpaces)
```

## Dependency Injection via Constructor Closures

Rules are defined in the constructor, so they close over the handler's dependencies:

```go
func NewHandler(cfg Config, authService *service.AuthService, sm *auth.SessionManager) *handler {
    return &handler{
        RegisterFormRules: validate.NewForm(
            validate.F("role", validate.Required, validate.In(cfg.AllowedRoles...)),
            validate.F("bio", validate.MaxLen(cfg.MaxBioLength)),
            validate.F("invite_code", validate.Required, func(value string) string {
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
        @form.CSRFField(c.Get("csrf").(string))
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
            validate.F("title", validate.Required, validate.MinLen(3), validate.MaxLen(200)),
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

## Backward Compatibility

Everything existing stays unchanged:

- All existing validators (`Required`, `Email`, `Phone`, `URL`, `PasswordStrength`, `MinLength`, `MaxLength`, `OneOf`, `IntRange`, `MinAge`, `MaxAge`)
- `Field`, `Check`, `FieldResult` types/functions
- Template components (`FieldError`, `FieldErrorOOB`)
- HTMX trigger pattern (`blur` + `input[data-has-error]`)
- `*Msg` variants of all validators

## File Organization

All new files live in the existing `pkg/validate` package alongside the current code:

| File | Contents |
|---|---|
| `pkg/validate/form.go` | `Form` type, `NewForm`, `FormOption`, `F`, `FMsg`, `Validate`, `ValidationHandler`, options |
| `pkg/validate/rules.go` | `Rules[T]`, `RulesMsg[T]`, `RunRules[T]`, `WithMsg`, `WithCtx` |
| `pkg/validate/constructors.go` | Curried validators: `MinLen`, `MaxLen`, `In`, `IntBetween`, `AgeMin`, `AgeMax`, `FloatBetween`, `MustBeTrue`, `After`, `Before`, `Between`, `DateFormat`, `DateAfter`, `DateBefore`, `EmptyOr` |
| `pkg/validate/validate.go` | Untouched — existing validators stay as-is |
| `pkg/validate/messages.go` | Untouched — existing error message constants stay as-is |

## Open Questions

- Should `WithCtx` (context-aware rules that receive `echo.Context`) be included in the first version, or deferred?
- Should `Form.Validate(c)` return early on first field error, or always validate all fields?
- Should there be a `Form.ValidateMap(values map[string]string)` for testing without an Echo context?
- Should `EmptyOr` compose with typed validators, or only string validators?
