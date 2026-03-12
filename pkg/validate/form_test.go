package validate_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/FyrmForge/hamr/pkg/validate"
	"github.com/labstack/echo/v4"
)

// newCtx builds an Echo context with the given form values.
func newCtx(values map[string]string) echo.Context {
	e := echo.New()
	form := make(url.Values, len(values))
	for k, v := range values {
		form.Set(k, v)
	}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationForm)
	rec := httptest.NewRecorder()
	return e.NewContext(req, rec)
}

// newCtxWithParam builds an Echo context with form values and a route param.
func newCtxWithParam(values map[string]string, paramName, paramValue string) echo.Context {
	e := echo.New()
	form := make(url.Values, len(values))
	for k, v := range values {
		form.Set(k, v)
	}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationForm)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames(paramName)
	c.SetParamValues(paramValue)
	return c
}

// ---------------------------------------------------------------------------
// Form.Validate
// ---------------------------------------------------------------------------

func TestForm_Validate_AllPass(t *testing.T) {
	f := validate.NewForm(
		validate.Field("name", validate.Required),
		validate.Field("email", validate.Required, validate.Email),
	)

	c := newCtx(map[string]string{"name": "Alice", "email": "alice@example.com"})
	errs := f.Validate(c)
	if errs != nil {
		t.Fatalf("expected nil, got %v", errs)
	}
}

func TestForm_Validate_SomeErrors(t *testing.T) {
	f := validate.NewForm(
		validate.Field("name", validate.Required),
		validate.Field("email", validate.Required, validate.Email),
	)

	c := newCtx(map[string]string{"name": "", "email": "bad"})
	errs := f.Validate(c)
	if errs == nil {
		t.Fatal("expected errors, got nil")
	}
	if len(errs) != 2 {
		t.Fatalf("expected 2 errors, got %d: %v", len(errs), errs)
	}
	check(t, "name", errs["name"], validate.MsgRequired)
	check(t, "email", errs["email"], validate.MsgEmailInvalid)
}

func TestForm_Validate_PerFieldShortCircuit(t *testing.T) {
	// Required fails, so Email should NOT run — error should be MsgRequired,
	// not MsgEmailInvalid.
	f := validate.NewForm(
		validate.Field("email", validate.Required, validate.Email),
	)

	c := newCtx(map[string]string{"email": ""})
	errs := f.Validate(c)
	if errs == nil {
		t.Fatal("expected errors, got nil")
	}
	check(t, "short-circuit", errs["email"], validate.MsgRequired)
}

func TestForm_Validate_FieldMsg(t *testing.T) {
	f := validate.NewForm(
		validate.FieldMsg("email", "Email is invalid", validate.Required, validate.Email),
	)

	c := newCtx(map[string]string{"email": ""})
	errs := f.Validate(c)
	check(t, "field-msg", errs["email"], "Email is invalid")
}

func TestForm_Validate_WithTrim(t *testing.T) {
	f := validate.NewForm(
		validate.WithTrim(true),
		validate.Field("name", validate.Required),
	)

	c := newCtx(map[string]string{"name": "   "})
	errs := f.Validate(c)
	if errs == nil {
		t.Fatal("expected errors after trimming whitespace, got nil")
	}
	check(t, "trimmed", errs["name"], validate.MsgRequired)
}

func TestForm_Validate_GeneralError(t *testing.T) {
	f := validate.NewForm(
		validate.WithGeneralError("Fix errors below."),
		validate.Field("name", validate.Required),
	)

	c := newCtx(map[string]string{"name": ""})
	errs := f.Validate(c)
	check(t, "general", errs["general"], "Fix errors below.")
}

func TestForm_Validate_GeneralError_NotAddedOnPass(t *testing.T) {
	f := validate.NewForm(
		validate.WithGeneralError("Fix errors below."),
		validate.Field("name", validate.Required),
	)

	c := newCtx(map[string]string{"name": "Alice"})
	errs := f.Validate(c)
	if errs != nil {
		t.Fatalf("expected nil, got %v", errs)
	}
}

func TestForm_Validate_ShortCircuit(t *testing.T) {
	f := validate.NewForm(
		validate.WithShortCircuit(true),
		validate.Field("name", validate.Required),
		validate.Field("email", validate.Required),
	)

	c := newCtx(map[string]string{"name": "", "email": ""})
	errs := f.Validate(c)
	if errs == nil {
		t.Fatal("expected errors, got nil")
	}
	// Only first field should appear.
	if len(errs) != 1 {
		t.Fatalf("expected 1 error with short-circuit, got %d: %v", len(errs), errs)
	}
	check(t, "first-field", errs["name"], validate.MsgRequired)
}

func TestForm_Validate_ShortCircuit_WithGeneralError(t *testing.T) {
	f := validate.NewForm(
		validate.WithShortCircuit(true),
		validate.WithGeneralError("Fix errors below."),
		validate.Field("name", validate.Required),
		validate.Field("email", validate.Required),
	)

	c := newCtx(map[string]string{"name": "", "email": ""})
	errs := f.Validate(c)
	if errs == nil {
		t.Fatal("expected errors, got nil")
	}
	// Field error + general error.
	if len(errs) != 2 {
		t.Fatalf("expected 2 entries (field + general), got %d: %v", len(errs), errs)
	}
	check(t, "first-field", errs["name"], validate.MsgRequired)
	check(t, "general", errs["general"], "Fix errors below.")
}

func TestForm_Validate_WithCtx(t *testing.T) {
	f := validate.NewForm(
		validate.Field("password", validate.Required).
			WithCtx(func(c echo.Context, value string) string {
				if value != c.FormValue("password_confirm") {
					return "Passwords do not match"
				}
				return ""
			}),
	)

	// Matching passwords.
	c := newCtx(map[string]string{"password": "secret", "password_confirm": "secret"})
	errs := f.Validate(c)
	if errs != nil {
		t.Fatalf("expected nil, got %v", errs)
	}

	// Mismatched passwords.
	c = newCtx(map[string]string{"password": "secret", "password_confirm": "other"})
	errs = f.Validate(c)
	check(t, "mismatch", errs["password"], "Passwords do not match")
}

func TestForm_Validate_CtxRulesSkippedWhenStandardRuleFails(t *testing.T) {
	ctxRuleCalled := false
	f := validate.NewForm(
		validate.Field("password", validate.Required).
			WithCtx(func(c echo.Context, value string) string {
				ctxRuleCalled = true
				return "should not run"
			}),
	)

	c := newCtx(map[string]string{"password": ""})
	errs := f.Validate(c)
	if errs == nil {
		t.Fatal("expected errors, got nil")
	}
	check(t, "standard-rule", errs["password"], validate.MsgRequired)
	if ctxRuleCalled {
		t.Error("CtxRule should not run when standard rules fail")
	}
}

func TestForm_Validate_FieldMsgWithCtx(t *testing.T) {
	f := validate.NewForm(
		validate.FieldMsg("password", "Password is invalid", validate.Required).
			WithCtx(func(c echo.Context, value string) string {
				if value != c.FormValue("password_confirm") {
					return "Passwords do not match"
				}
				return ""
			}),
	)

	// FieldMsg should override the CtxRule error message.
	c := newCtx(map[string]string{"password": "secret", "password_confirm": "other"})
	errs := f.Validate(c)
	check(t, "field-msg-overrides-ctx", errs["password"], "Password is invalid")
}

func TestForm_Validate_NoFields(t *testing.T) {
	f := validate.NewForm()

	c := newCtx(map[string]string{"anything": "value"})
	errs := f.Validate(c)
	if errs != nil {
		t.Fatalf("expected nil for empty form, got %v", errs)
	}
}

func TestForm_Validate_EmptyOrInForm(t *testing.T) {
	f := validate.NewForm(
		validate.Field("website", validate.EmptyOr(validate.URL)),
	)

	// Empty value should pass.
	c := newCtx(map[string]string{"website": ""})
	errs := f.Validate(c)
	if errs != nil {
		t.Fatalf("expected nil for empty optional field, got %v", errs)
	}

	// Invalid value should fail.
	c = newCtx(map[string]string{"website": "not-a-url"})
	errs = f.Validate(c)
	if errs == nil {
		t.Fatal("expected errors for invalid optional field, got nil")
	}
	check(t, "invalid-url", errs["website"], validate.MsgURLInvalid)

	// Valid value should pass.
	c = newCtx(map[string]string{"website": "https://example.com"})
	errs = f.Validate(c)
	if errs != nil {
		t.Fatalf("expected nil for valid optional field, got %v", errs)
	}
}

// ---------------------------------------------------------------------------
// Form.ValidationHandler
// ---------------------------------------------------------------------------

func TestForm_ValidationHandler_ValidField(t *testing.T) {
	var rendered string
	renderer := func(c echo.Context, field, errMsg string) error {
		rendered = field + ":" + errMsg
		return c.String(http.StatusOK, errMsg)
	}

	f := validate.NewForm(
		validate.WithOOBRenderer(renderer),
		validate.Field("email", validate.Required, validate.Email),
	)

	c := newCtxWithParam(map[string]string{"email": "alice@example.com"}, "field", "email")
	handler := f.ValidationHandler("field")
	if err := handler(c); err != nil {
		t.Fatal(err)
	}
	check(t, "rendered", rendered, "email:")
}

func TestForm_ValidationHandler_InvalidField(t *testing.T) {
	var rendered string
	renderer := func(c echo.Context, field, errMsg string) error {
		rendered = field + ":" + errMsg
		status := http.StatusOK
		if errMsg != "" {
			status = http.StatusUnprocessableEntity
		}
		return c.String(status, errMsg)
	}

	f := validate.NewForm(
		validate.WithOOBRenderer(renderer),
		validate.Field("email", validate.Required, validate.Email),
	)

	c := newCtxWithParam(map[string]string{"email": "bad"}, "field", "email")
	handler := f.ValidationHandler("field")
	if err := handler(c); err != nil {
		t.Fatal(err)
	}
	check(t, "rendered", rendered, "email:"+validate.MsgEmailInvalid)
}

func TestForm_ValidationHandler_UnknownField(t *testing.T) {
	var rendered string
	renderer := func(c echo.Context, field, errMsg string) error {
		rendered = field + ":" + errMsg
		return c.String(http.StatusOK, errMsg)
	}

	f := validate.NewForm(
		validate.WithOOBRenderer(renderer),
		validate.Field("email", validate.Required),
	)

	c := newCtxWithParam(map[string]string{"unknown": "value"}, "field", "unknown")
	handler := f.ValidationHandler("field")
	if err := handler(c); err != nil {
		t.Fatal(err)
	}
	check(t, "empty-response", rendered, "unknown:")
}

func TestForm_ValidationHandler_PerFieldRenderer(t *testing.T) {
	formRendered := false
	formRenderer := func(c echo.Context, field, errMsg string) error {
		formRendered = true
		return c.String(http.StatusOK, errMsg)
	}

	var fieldRendered string
	fieldRenderer := func(c echo.Context, field, errMsg string) error {
		fieldRendered = field + ":" + errMsg
		return c.String(http.StatusOK, errMsg)
	}

	f := validate.NewForm(
		validate.WithOOBRenderer(formRenderer),
		validate.Field("name", validate.Required),
		validate.Field("password", validate.Required).WithRenderer(fieldRenderer),
	)

	// Validate password — should use field renderer, not form renderer.
	c := newCtxWithParam(map[string]string{"password": ""}, "field", "password")
	handler := f.ValidationHandler("field")
	if err := handler(c); err != nil {
		t.Fatal(err)
	}
	if formRendered {
		t.Error("form renderer should not have been called for password")
	}
	check(t, "field-renderer", fieldRendered, "password:"+validate.MsgRequired)
}

func TestForm_ValidationHandler_FallbackPlainText(t *testing.T) {
	// No renderer set at all — should fall back to plain text.
	f := validate.NewForm(
		validate.Field("email", validate.Required),
	)

	c := newCtxWithParam(map[string]string{"email": ""}, "field", "email")
	rec := c.Response().Writer.(*httptest.ResponseRecorder)
	handler := f.ValidationHandler("field")
	if err := handler(c); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422, got %d", rec.Code)
	}
}

func TestForm_ValidationHandler_WithTrim(t *testing.T) {
	var rendered string
	renderer := func(c echo.Context, field, errMsg string) error {
		rendered = errMsg
		return c.String(http.StatusOK, errMsg)
	}

	f := validate.NewForm(
		validate.WithOOBRenderer(renderer),
		validate.WithTrim(true),
		validate.Field("name", validate.Required),
	)

	c := newCtxWithParam(map[string]string{"name": "   "}, "field", "name")
	handler := f.ValidationHandler("field")
	if err := handler(c); err != nil {
		t.Fatal(err)
	}
	check(t, "trimmed-error", rendered, validate.MsgRequired)
}

func TestForm_ValidationHandler_WithCtx(t *testing.T) {
	var rendered string
	renderer := func(c echo.Context, field, errMsg string) error {
		rendered = field + ":" + errMsg
		return c.String(http.StatusOK, errMsg)
	}

	f := validate.NewForm(
		validate.WithOOBRenderer(renderer),
		validate.Field("password_confirm", validate.Required).
			WithCtx(func(c echo.Context, value string) string {
				if value != c.FormValue("password") {
					return "Passwords do not match"
				}
				return ""
			}),
	)

	// Matching — should render empty error.
	c := newCtxWithParam(
		map[string]string{"password_confirm": "secret", "password": "secret"},
		"field", "password_confirm",
	)
	handler := f.ValidationHandler("field")
	if err := handler(c); err != nil {
		t.Fatal(err)
	}
	check(t, "matching", rendered, "password_confirm:")

	// Mismatched — should render error.
	c = newCtxWithParam(
		map[string]string{"password_confirm": "other", "password": "secret"},
		"field", "password_confirm",
	)
	rendered = ""
	if err := handler(c); err != nil {
		t.Fatal(err)
	}
	check(t, "mismatch", rendered, "password_confirm:Passwords do not match")
}

// ---------------------------------------------------------------------------
// FieldBuilder chaining
// ---------------------------------------------------------------------------

func TestFieldBuilder_Chaining(t *testing.T) {
	// Ensure chaining works without panics and produces a valid form.
	renderer := func(c echo.Context, field, errMsg string) error {
		return c.String(http.StatusOK, errMsg)
	}

	f := validate.NewForm(
		validate.Field("password", validate.Required).
			WithRenderer(renderer).
			WithCtx(func(c echo.Context, value string) string {
				return ""
			}),
	)

	c := newCtx(map[string]string{"password": "secret"})
	errs := f.Validate(c)
	if errs != nil {
		t.Fatalf("expected nil, got %v", errs)
	}
}

// ---------------------------------------------------------------------------
// WithMsg integration with Form
// ---------------------------------------------------------------------------

func TestForm_WithMsg(t *testing.T) {
	f := validate.NewForm(
		validate.Field("email",
			validate.Required,
			validate.WithMsg(validate.Email, "Please enter a valid email"),
		),
	)

	c := newCtx(map[string]string{"email": "bad"})
	errs := f.Validate(c)
	check(t, "custom-rule-msg", errs["email"], "Please enter a valid email")
}

// ---------------------------------------------------------------------------
// Curried constructors in Form
// ---------------------------------------------------------------------------

func TestForm_CurriedConstructors(t *testing.T) {
	f := validate.NewForm(
		validate.Field("name", validate.Required, validate.MinLen(3)),
		validate.Field("bio", validate.MaxLen(10)),
		validate.Field("role", validate.Required, validate.In("admin", "user")),
	)

	c := newCtx(map[string]string{"name": "Al", "bio": "This is way too long!", "role": "guest"})
	errs := f.Validate(c)
	if errs == nil {
		t.Fatal("expected errors, got nil")
	}
	check(t, "min-len", errs["name"], validate.MsgMinLength)
	check(t, "max-len", errs["bio"], validate.MsgMaxLength)
	check(t, "in", errs["role"], validate.MsgOneOf)
}
