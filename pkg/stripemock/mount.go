package stripemock

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/FyrmForge/hamr/pkg/respond"
)

// Mount registers the mock Stripe dev UI on the given Echo instance:
//
//	GET  /dev/stripe           → renders the checkout page for ?session=<id>
//	POST /dev/stripe/complete  → sets the outcome and redirects to success/cancel
//
// The caller is responsible for only mounting this in dev (e.g. behind a
// STRIPE_MOCK env flag in main.go). Mounting in production will serve a fake
// Stripe page to real users.
func Mount(e *echo.Echo, client *Client) {
	h := &handler{client: client}
	e.GET("/dev/stripe", h.checkoutPage)
	e.POST("/dev/stripe/complete", h.complete)
}

type handler struct {
	client *Client
}

func (h *handler) checkoutPage(c echo.Context) error {
	sessionID := c.QueryParam("session")
	if sessionID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "missing session param")
	}

	req, err := h.client.GetRequest(sessionID)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "session not found")
	}

	return respond.HTML(c, http.StatusOK, checkoutPage(sessionID, h.client.Currency(), req))
}

func (h *handler) complete(c echo.Context) error {
	sessionID := c.FormValue("session")
	outcome := c.FormValue("outcome")

	if sessionID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "missing session")
	}

	var status PaymentStatus
	switch outcome {
	case "paid":
		status = StatusPaid
	case "failed":
		status = StatusFailed
	case "cancelled":
		status = StatusCancelled
	default:
		return echo.NewHTTPError(http.StatusBadRequest, "invalid outcome")
	}

	if err := h.client.SetOutcome(sessionID, status); err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "session not found")
	}

	if status == StatusPaid {
		return respond.Redirect(c, h.client.SuccessURL(sessionID))
	}
	return respond.Redirect(c, h.client.CancelURL(sessionID))
}
