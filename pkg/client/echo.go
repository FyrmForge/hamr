package client

import (
	"context"

	"github.com/FyrmForge/hamr/pkg/ctx"
	"github.com/labstack/echo/v4"
)

// EchoContext extracts the request ID and subject ID from an Echo context
// and returns a stdlib context with those values set for header propagation.
//
// Use this in Echo handlers before making inter-service calls:
//
//	func (h *Handler) GetInvoice(c echo.Context) error {
//	    svcCtx := client.EchoContext(c)
//	    invoice, err := client.Get[dto.Invoice](svcCtx, h.billing, "/invoices/"+id)
//	    ...
//	}
func EchoContext(c echo.Context) context.Context {
	stdCtx := c.Request().Context()

	if id, ok := ctx.Get(c, ctx.RequestIDKey); ok {
		stdCtx = context.WithValue(stdCtx, requestIDCtxKey, id)
	}
	if id, ok := ctx.Get(c, ctx.SubjectIDKey); ok {
		stdCtx = context.WithValue(stdCtx, subjectIDCtxKey, id)
	}

	return stdCtx
}
