package i18n

import (
	"github.com/FyrmForge/hamr/pkg/ctx"
	"github.com/labstack/echo/v4"
)

// FromContext retrieves the Translator stored in the Echo context.
// Panics if no translator has been set (middleware not applied).
func FromContext(c echo.Context) *Translator {
	return ctx.MustGetAs[*Translator](c, ctx.TranslatorKey)
}

// LocaleFromContext returns the locale string from the Echo context.
func LocaleFromContext(c echo.Context) string {
	v, ok := ctx.Get(c, ctx.LocaleKey)
	if !ok {
		return ""
	}
	return v
}

// DirectionFromContext returns the text direction from the context's translator.
func DirectionFromContext(c echo.Context) string {
	t, ok := ctx.GetAs[*Translator](c, ctx.TranslatorKey)
	if !ok {
		return "ltr"
	}
	return t.Direction()
}
