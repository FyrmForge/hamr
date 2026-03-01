package middleware

import (
	"encoding/base64"
	"encoding/json"
	"net/http"

	"github.com/FyrmForge/hamr/pkg/ctx"
	"github.com/labstack/echo/v4"
)

// FlashCookieName is the cookie name used for flash messages.
const FlashCookieName = "hamr_flash"

// FlashType categorises a flash message.
type FlashType string

const (
	FlashInfo    FlashType = "info"
	FlashSuccess FlashType = "success"
	FlashWarning FlashType = "warning"
	FlashError   FlashType = "error"
)

// FlashMessage is a one-time message shown to the user after a redirect.
type FlashMessage struct {
	Message string    `json:"message"`
	Type    FlashType `json:"type"`
}

// FlashConfig configures flash cookie behaviour.
type FlashConfig struct {
	Path   string // default: "/"
	Secure bool   // default: true
}

var flashConfigKey = ctx.NewKey[FlashConfig]("flash_config")

func (cfg FlashConfig) withDefaults() FlashConfig {
	if cfg.Path == "" {
		cfg.Path = "/"
	}
	return cfg
}

func (cfg FlashConfig) cookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     FlashCookieName,
		Value:    value,
		Path:     cfg.Path,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   cfg.Secure,
		SameSite: http.SameSiteLaxMode,
	}
}

// Flash reads a flash cookie from the request, stores it in the context, and
// clears the cookie so it is only shown once.
func Flash() echo.MiddlewareFunc {
	return FlashWithConfig(FlashConfig{Secure: true})
}

// FlashWithConfig returns flash middleware with the given config.
func FlashWithConfig(cfg FlashConfig) echo.MiddlewareFunc {
	cfg = cfg.withDefaults()

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			// Store config so SetFlash can read it.
			ctx.Set(c, flashConfigKey, cfg)

			cookie, err := c.Cookie(FlashCookieName)
			if err == nil && cookie.Value != "" {
				data, err := base64.StdEncoding.DecodeString(cookie.Value)
				if err == nil {
					var msg FlashMessage
					if json.Unmarshal(data, &msg) == nil {
						ctx.Set(c, ctx.FlashKey, any(&msg))
					}
				}

				// Clear the cookie after reading.
				c.SetCookie(cfg.cookie("", -1))
			}

			return next(c)
		}
	}
}

// SetFlash stores a flash message in a cookie for the next request.
// Uses the cookie policy from Flash middleware if present, otherwise
// falls back to secure defaults.
func SetFlash(c echo.Context, message string, flashType FlashType) {
	msg := FlashMessage{Message: message, Type: flashType}
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}

	cfg, ok := ctx.Get(c, flashConfigKey)
	if !ok {
		cfg = FlashConfig{Path: "/", Secure: true}
	}

	c.SetCookie(cfg.cookie(base64.StdEncoding.EncodeToString(data), 60))
}

// GetFlash returns the flash message from the context, or nil if none is set.
func GetFlash(c echo.Context) *FlashMessage {
	val, ok := ctx.Get(c, ctx.FlashKey)
	if !ok {
		return nil
	}
	msg, ok := val.(*FlashMessage)
	if !ok {
		return nil
	}
	return msg
}
