package stripemock_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/FyrmForge/hamr/pkg/stripemock"
)

func TestCreateCheckoutSession_startsRequiresAction(t *testing.T) {
	c := stripemock.New("http://localhost:8080", "GBP")

	sess, err := c.CreateCheckoutSession(stripemock.CheckoutRequest{
		LineItems: []stripemock.LineItem{{Name: "Item", Amount: 100, Quantity: 1}},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.HasPrefix(sess.ID, "cs_mock_") {
		t.Errorf("unexpected session id: %q", sess.ID)
	}
	if sess.URL != "http://localhost:8080/dev/stripe?session="+sess.ID {
		t.Errorf("unexpected url: %q", sess.URL)
	}

	result, err := c.GetSessionResult(sess.ID)
	if err != nil {
		t.Fatalf("get result: %v", err)
	}
	if result.Status != stripemock.StatusRequiresAction {
		t.Errorf("expected requires_action, got %q", result.Status)
	}
}

func TestCreateCheckoutSession_isolatesCallerMutations(t *testing.T) {
	c := stripemock.New("http://x", "GBP")

	items := []stripemock.LineItem{{Name: "Original", Amount: 100, Quantity: 1}}
	meta := map[string]string{"k": "v1"}
	sess, _ := c.CreateCheckoutSession(stripemock.CheckoutRequest{
		LineItems: items,
		Metadata:  meta,
	})

	// Caller mutates the originals after the call.
	items[0].Name = "Mutated"
	meta["k"] = "v2"

	stored, _ := c.GetRequest(sess.ID)
	if stored.LineItems[0].Name != "Original" {
		t.Errorf("LineItems leaked caller mutation: %q", stored.LineItems[0].Name)
	}
	if stored.Metadata["k"] != "v1" {
		t.Errorf("Metadata leaked caller mutation: %q", stored.Metadata["k"])
	}
}

func TestSetOutcome_updatesStatus(t *testing.T) {
	c := stripemock.New("http://x", "GBP")
	sess, _ := c.CreateCheckoutSession(stripemock.CheckoutRequest{
		LineItems: []stripemock.LineItem{{Name: "x", Amount: 1, Quantity: 1}},
	})

	if err := c.SetOutcome(sess.ID, stripemock.StatusPaid); err != nil {
		t.Fatalf("set outcome: %v", err)
	}
	result, _ := c.GetSessionResult(sess.ID)
	if result.Status != stripemock.StatusPaid {
		t.Errorf("expected paid, got %q", result.Status)
	}
}

func TestGetSessionResult_returnsDetachedCopy(t *testing.T) {
	c := stripemock.New("http://x", "GBP")
	sess, _ := c.CreateCheckoutSession(stripemock.CheckoutRequest{
		LineItems: []stripemock.LineItem{{Name: "x", Amount: 1, Quantity: 1}},
		Metadata:  map[string]string{"k": "v"},
	})

	r1, _ := c.GetSessionResult(sess.ID)

	// Mutating the returned copy must not affect stored state.
	r1.Status = stripemock.StatusPaid
	r1.Metadata["k"] = "tampered"

	r2, _ := c.GetSessionResult(sess.ID)
	if r2.Status != stripemock.StatusRequiresAction {
		t.Errorf("returned pointer leaks into stored state: status=%q", r2.Status)
	}
	if r2.Metadata["k"] != "v" {
		t.Errorf("returned metadata leaks into stored state: %q", r2.Metadata["k"])
	}
}

func TestGetSessionResult_unknownSession(t *testing.T) {
	c := stripemock.New("http://x", "GBP")
	if _, err := c.GetSessionResult("nope"); err == nil {
		t.Error("expected error for unknown session")
	}
}

func TestMount_checkoutPageRendersAndCompleteRedirects(t *testing.T) {
	e := echo.New()
	c := stripemock.New("http://test", "GBP")
	stripemock.Mount(e, c)

	sess, _ := c.CreateCheckoutSession(stripemock.CheckoutRequest{
		LineItems:  []stripemock.LineItem{{Name: "Widget", Amount: 250, Quantity: 2}},
		SuccessURL: "/success",
		CancelURL:  "/cancel",
	})

	// GET /dev/stripe
	req := httptest.NewRequest(http.MethodGet, "/dev/stripe?session="+sess.ID, nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("checkout page status: got %d, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Widget") {
		t.Error("checkout page did not render line item name")
	}

	// POST /dev/stripe/complete with paid → redirect to success URL
	form := url.Values{"session": {sess.ID}, "outcome": {"paid"}}
	req = httptest.NewRequest(http.MethodPost, "/dev/stripe/complete", strings.NewReader(form.Encode()))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationForm)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("complete status: got %d, want 303", rec.Code)
	}
	if got := rec.Header().Get(echo.HeaderLocation); got != "/success" {
		t.Errorf("redirect: got %q, want /success", got)
	}

	result, _ := c.GetSessionResult(sess.ID)
	if result.Status != stripemock.StatusPaid {
		t.Errorf("status after complete: got %q, want paid", result.Status)
	}
}

func TestMount_rendersConfiguredCurrencySymbol(t *testing.T) {
	cases := []struct {
		currency string
		amount   int64
		want     string
	}{
		{"GBP", 1000, "£10.00"},
		{"USD", 1000, "$10.00"},
		{"EUR", 1000, "€10.00"},
		{"JPY", 1000, "¥1000"},
		{"XYZ", 1000, "XYZ 10.00"}, // fallback for unknown
	}
	for _, tc := range cases {
		t.Run(tc.currency, func(t *testing.T) {
			e := echo.New()
			c := stripemock.New("http://test", tc.currency)
			stripemock.Mount(e, c)

			sess, _ := c.CreateCheckoutSession(stripemock.CheckoutRequest{
				LineItems: []stripemock.LineItem{{Name: "item", Amount: tc.amount, Quantity: 1}},
			})

			req := httptest.NewRequest(http.MethodGet, "/dev/stripe?session="+sess.ID, nil)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Errorf("body did not contain %q", tc.want)
			}
		})
	}
}

func TestMount_completeInvalidOutcome(t *testing.T) {
	e := echo.New()
	c := stripemock.New("http://test", "GBP")
	stripemock.Mount(e, c)

	sess, _ := c.CreateCheckoutSession(stripemock.CheckoutRequest{
		LineItems: []stripemock.LineItem{{Name: "x", Amount: 1, Quantity: 1}},
	})

	form := url.Values{"session": {sess.ID}, "outcome": {"bogus"}}
	req := httptest.NewRequest(http.MethodPost, "/dev/stripe/complete", strings.NewReader(form.Encode()))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationForm)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}
