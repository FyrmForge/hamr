package middleware

import (
	"net/http"
	"strings"

	"github.com/FyrmForge/hamr/pkg/ctx"
	"github.com/FyrmForge/hamr/pkg/i18n"
	"github.com/labstack/echo/v4"
	"golang.org/x/text/language"
)

// LocaleConfig configures locale middleware.
type LocaleConfig struct {
	Bundle         *i18n.Bundle
	CookieName     string                         // default: "hamr_locale"
	CookieMaxAge   int                            // default: 31536000 (1 year)
	CookieSecure   bool
	DefaultLocale  string                         // from bundle if empty
	UserLocaleFunc func(c echo.Context) string    // optional: load from user preference
}

func (cfg *LocaleConfig) defaults() {
	if cfg.CookieName == "" {
		cfg.CookieName = "hamr_locale"
	}
	if cfg.CookieMaxAge == 0 {
		cfg.CookieMaxAge = 60 * 60 * 24 * 365
	}
	if cfg.DefaultLocale == "" && cfg.Bundle != nil {
		cfg.DefaultLocale = cfg.Bundle.DefaultLocale()
	}
}

// LocaleFromPath returns pre-router middleware that extracts the locale from
// the first URL path segment. Used for public/SEO pages where the locale
// appears in the URL (e.g. /en/about, /fr/contact).
//
// IMPORTANT: Register with e.Pre(), not e.Use(), because the locale prefix
// must be stripped before the Echo router matches a route:
//
//	e.Pre(middleware.LocaleFromPath(cfg))
//
// If the first segment is a supported locale, it is stripped from the path and
// the translator is injected into the context. If no valid locale prefix is
// found, the request is redirected to /{defaultLocale}/... with a 301 (GET) or
// 307 (POST).
func LocaleFromPath(cfg LocaleConfig) echo.MiddlewareFunc {
	cfg.defaults()

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			path := c.Request().URL.Path

			// Extract first path segment.
			trimmed := strings.TrimPrefix(path, "/")
			segment, rest, _ := strings.Cut(trimmed, "/")

			if cfg.Bundle.HasLocale(segment) {
				// Strip locale prefix — rewrite both URL.Path and
				// RawPath so the Echo router sees the stripped path.
				newPath := "/" + rest
				c.Request().URL.Path = newPath
				c.Request().URL.RawPath = newPath

				setLocaleContext(c, cfg, segment)
				return next(c)
			}

			// No valid locale prefix — redirect to default.
			dest := "/" + cfg.DefaultLocale + path
			if qs := c.Request().URL.RawQuery; qs != "" {
				dest += "?" + qs
			}
			if c.Request().Method == http.MethodGet || c.Request().Method == http.MethodHead {
				return c.Redirect(http.StatusMovedPermanently, dest)
			}
			return c.Redirect(http.StatusTemporaryRedirect, dest)
		}
	}
}

// LocaleFromPreference returns middleware that resolves the locale from user
// preferences: UserLocaleFunc > cookie > Accept-Language header > default.
// Used for authenticated/dashboard pages.
func LocaleFromPreference(cfg LocaleConfig) echo.MiddlewareFunc {
	cfg.defaults()

	// Build language matcher from supported locales.
	supported := cfg.Bundle.SupportedLocales()
	tags := make([]language.Tag, len(supported))
	for i, s := range supported {
		tags[i] = language.Make(s)
	}
	matcher := language.NewMatcher(tags)

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			locale := ""

			// 1. User preference function.
			if cfg.UserLocaleFunc != nil {
				if pref := cfg.UserLocaleFunc(c); pref != "" {
					if resolved, ok := cfg.Bundle.ResolveLocale(pref); ok {
						locale = resolved
					}
				}
			}

			// 2. Cookie.
			if locale == "" {
				if cookie, err := c.Cookie(cfg.CookieName); err == nil && cookie.Value != "" {
					if resolved, ok := cfg.Bundle.ResolveLocale(cookie.Value); ok {
						locale = resolved
					}
				}
			}

			// 3. Accept-Language header.
			if locale == "" {
				accept := c.Request().Header.Get("Accept-Language")
				if accept != "" {
					tags, _, err := language.ParseAcceptLanguage(accept)
					if err == nil && len(tags) > 0 {
						tag, _, _ := matcher.Match(tags...)
						// Try the full matched tag first (e.g. "fr-CA"),
						// then fall back to the base language (e.g. "fr").
						candidate := tag.String()
						if cfg.Bundle.HasLocale(candidate) {
							locale = candidate
						} else {
							base, _ := tag.Base()
							if cfg.Bundle.HasLocale(base.String()) {
								locale = base.String()
							}
						}
					}
				}
			}

			// 4. Default.
			if locale == "" {
				locale = cfg.DefaultLocale
			}

			setLocaleContext(c, cfg, locale)
			return next(c)
		}
	}
}

func setLocaleContext(c echo.Context, cfg LocaleConfig, locale string) {
	tr := cfg.Bundle.Translator(locale)
	ctx.Set(c, ctx.TranslatorKey, any(tr))
	ctx.Set(c, ctx.LocaleKey, locale)

	// Only set cookie when it differs from the current value.
	existing, err := c.Cookie(cfg.CookieName)
	if err != nil || existing.Value != locale {
		c.SetCookie(&http.Cookie{
			Name:     cfg.CookieName,
			Value:    locale,
			Path:     "/",
			MaxAge:   cfg.CookieMaxAge,
			HttpOnly: true,
			Secure:   cfg.CookieSecure,
			SameSite: http.SameSiteLaxMode,
		})
	}
}

// GetLocale is a convenience helper that returns the locale from the context.
func GetLocale(c echo.Context) string {
	return i18n.LocaleFromContext(c)
}

// GetDirection is a convenience helper that returns the text direction from
// the context's translator.
func GetDirection(c echo.Context) string {
	return i18n.DirectionFromContext(c)
}
