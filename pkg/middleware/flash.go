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

// Flash reads a flash cookie from the request, stores it in the context, and
// clears the cookie so it is only shown once.
func Flash() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
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
				c.SetCookie(&http.Cookie{
					Name:     FlashCookieName,
					Value:    "",
					Path:     "/",
					MaxAge:   -1,
					HttpOnly: true,
					SameSite: http.SameSiteLaxMode,
				})
			}

			return next(c)
		}
	}
}

// SetFlash stores a flash message in a cookie for the next request.
func SetFlash(c echo.Context, message string, flashType FlashType) {
	msg := FlashMessage{Message: message, Type: flashType}
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}

	c.SetCookie(&http.Cookie{
		Name:     FlashCookieName,
		Value:    base64.StdEncoding.EncodeToString(data),
		Path:     "/",
		MaxAge:   60,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
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
