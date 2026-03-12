package validate

import (
	"net/http"
	"slices"
	"strings"

	"github.com/labstack/echo/v4"
)

// FieldRenderer renders a per-field validation result. Used by
// ValidationHandler to produce the HTTP response (typically an OOB swap).
type FieldRenderer func(c echo.Context, field, errMsg string) error

// ---------------------------------------------------------------------------
// Form
// ---------------------------------------------------------------------------

// Form holds field rule definitions for both full-form validation and
// HTMX per-field validation. Define rules once in the constructor; use
// Validate for full-form checks and ValidationHandler for per-field HTMX
// endpoints.
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
	msg      string        // FieldMsg override — one message for any failure
	renderer FieldRenderer // per-field renderer override
}

// Validate runs all field rules against form values from the Echo context.
// Returns a map of field name to error message, or nil if all fields pass.
//
// Per-field short-circuit is always on: rules for a single field stop at the
// first failure. Per-form short-circuit is controlled by WithShortCircuit.
func (f Form) Validate(c echo.Context) map[string]string {
	var errs map[string]string

	for _, fd := range f.fields {
		value := c.FormValue(fd.name)
		if f.trim {
			value = strings.TrimSpace(value)
		}

		if msg := f.validateField(c, fd, value); msg != "" {
			if errs == nil {
				errs = make(map[string]string, len(f.fields))
			}
			errs[fd.name] = msg
			if f.shortCircuit {
				break
			}
		}
	}

	if errs != nil && f.generalError != "" {
		errs["general"] = f.generalError
	}

	return errs
}

// ValidationHandler returns an echo.HandlerFunc that validates a single
// field. The paramName argument is the Echo route parameter name that
// contains the field name (e.g. "field" for ":field").
//
// Unknown fields are silently ignored — the handler returns an empty/no-error
// response via the renderer.
func (f Form) ValidationHandler(paramName string) echo.HandlerFunc {
	return func(c echo.Context) error {
		fieldName := c.Param(paramName)

		fd, ok := f.findField(fieldName)
		if !ok {
			// Unknown field — render empty response.
			return f.render(c, fd, fieldName, "")
		}

		value := c.FormValue(fieldName)
		if f.trim {
			value = strings.TrimSpace(value)
		}

		msg := f.validateField(c, fd, value)
		return f.render(c, fd, fieldName, msg)
	}
}

// validateField runs the rules for a single field definition and returns
// the first error message, or "".
func (f Form) validateField(c echo.Context, fd fieldDef, value string) string {
	for _, rule := range fd.rules {
		if msg := rule(value); msg != "" {
			if fd.msg != "" {
				return fd.msg
			}
			return msg
		}
	}

	for _, rule := range fd.ctxRules {
		if msg := rule(c, value); msg != "" {
			if fd.msg != "" {
				return fd.msg
			}
			return msg
		}
	}

	return ""
}

// render calls the field-level renderer if set, otherwise the form-level
// renderer. If neither is set, it returns a minimal plain-text response.
func (f Form) render(c echo.Context, fd fieldDef, fieldName, errMsg string) error {
	if fd.renderer != nil {
		return fd.renderer(c, fieldName, errMsg)
	}
	if f.renderer != nil {
		return f.renderer(c, fieldName, errMsg)
	}
	// Fallback: plain text.
	status := http.StatusOK
	if errMsg != "" {
		status = http.StatusUnprocessableEntity
	}
	return c.String(status, errMsg)
}

// findField looks up a field definition by name.
func (f Form) findField(name string) (fieldDef, bool) {
	for _, fd := range f.fields {
		if fd.name == name {
			return fd, true
		}
	}
	return fieldDef{}, false
}

// ---------------------------------------------------------------------------
// FormOption
// ---------------------------------------------------------------------------

// FormOption configures a Form via NewForm.
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

// NewForm creates a Form from the given options and field definitions.
func NewForm(opts ...FormOption) Form {
	var cfg formConfig
	for _, o := range opts {
		o.apply(&cfg)
	}
	return Form(cfg)
}

// ---------------------------------------------------------------------------
// FieldBuilder
// ---------------------------------------------------------------------------

// FieldBuilder defines a field's validation rules and satisfies FormOption.
// Use Field or FieldMsg to create one.
type FieldBuilder struct {
	def fieldDef
}

func (fb FieldBuilder) apply(c *formConfig) {
	c.fields = append(c.fields, fb.def)
}

// Field defines a field with rules that use each rule's default error message.
func Field(name string, rules ...Rule) FieldBuilder {
	return FieldBuilder{def: fieldDef{name: name, rules: rules}}
}

// FieldMsg defines a field where any rule failure produces the given message,
// regardless of which rule failed.
func FieldMsg(name, msg string, rules ...Rule) FieldBuilder {
	return FieldBuilder{def: fieldDef{name: name, rules: rules, msg: msg}}
}

// WithRenderer sets a custom renderer for this field's per-field validation.
// Falls back to the form-level WithOOBRenderer if not set.
func (fb FieldBuilder) WithRenderer(fn FieldRenderer) FieldBuilder {
	fb.def.renderer = fn
	return fb
}

// WithCtx adds context-aware rules to the field. These run after standard
// rules and only if all standard rules pass.
//
// Note: WithTrim only trims the current field's value before it is passed
// to rules. Values read from echo.Context inside a CtxRule (e.g.
// c.FormValue("other_field")) are untrimmed. Apply strings.TrimSpace
// manually if cross-field comparisons need consistent trimming.
func (fb FieldBuilder) WithCtx(rules ...CtxRule) FieldBuilder {
	fb.def.ctxRules = append(slices.Clone(fb.def.ctxRules), rules...)
	return fb
}

// ---------------------------------------------------------------------------
// Option implementations
// ---------------------------------------------------------------------------

type oobRendererOption struct{ fn FieldRenderer }

func (o oobRendererOption) apply(c *formConfig) { c.renderer = o.fn }

// WithOOBRenderer sets the default renderer used by ValidationHandler to
// produce per-field error responses.
func WithOOBRenderer(fn FieldRenderer) FormOption { return oobRendererOption{fn} }

type generalErrorOption struct{ msg string }

func (o generalErrorOption) apply(c *formConfig) { c.generalError = o.msg }

// WithGeneralError adds a message under the "general" key in the error map
// when any field fails validation. Templates display it via
// form.GetError(errs, "general").
func WithGeneralError(msg string) FormOption { return generalErrorOption{msg} }

type trimOption struct{ on bool }

func (o trimOption) apply(c *formConfig) { c.trim = o.on }

// WithTrim enables automatic whitespace trimming of form values before
// validation.
func WithTrim(on bool) FormOption { return trimOption{on} }

type shortCircuitOption struct{ on bool }

func (o shortCircuitOption) apply(c *formConfig) { c.shortCircuit = o.on }

// WithShortCircuit stops validating after the first field that fails.
// Default is false — all fields are validated and all errors returned.
func WithShortCircuit(on bool) FormOption { return shortCircuitOption{on} }
