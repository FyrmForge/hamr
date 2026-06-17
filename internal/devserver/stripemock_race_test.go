package devserver

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestStripeMock_ConcurrentRetrieveWhileCompleting is a regression test for
// the unlocked-read class fixed by the clone-on-read change. Before the fix,
// retrievePaymentIntent (and the dashboard/page renderers) read shared state —
// including the m.charges map via serializePaymentIntent — after releasing
// m.mu, while handlePaymentIntentComplete wrote m.charges under Lock. That is
// a concurrent map read+write: a fatal runtime error in production and a
// data race flagged by `go test -race` (which `make test` runs).
//
// The test drives a reader goroutine hammering the API retrieve + dashboard
// snapshot across many PIs while a writer goroutine completes each one
// (synthesising a Charge and mutating m.charges). It asserts nothing beyond
// "no race / no panic"; its value is entirely in running under -race.
func TestStripeMock_ConcurrentRetrieveWhileCompleting(t *testing.T) {
	mock, srv, _ := newFullStripeStack(t, "")

	const n = 40
	ids := make([]string, n)
	mock.mu.Lock()
	for i := range ids {
		id := fmt.Sprintf("pi_test_race_%02d", i)
		ids[i] = id
		mock.paymentIntents[id] = &stripePaymentIntent{
			ID:                 id,
			Amount:             2000,
			Currency:           "gbp",
			Status:             "requires_payment_method",
			CaptureMethod:      "automatic",
			ConfirmationMethod: "automatic",
			ClientSecret:       id + "_secret",
			Created:            time.Now(),
		}
	}
	mock.mu.Unlock()

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Reader: continuously retrieve every PI (reads m.charges via serialize)
	// and render the dashboard (snapshots every map) until the writer is done.
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			for _, id := range ids {
				drain(http.Get(srv.URL + "/v1/payment_intents/" + id))
			}
			drain(http.Get(srv.URL + "/__hamr/stripe"))
		}
	})

	// Writer: complete each PI, which creates a Charge and writes m.charges
	// under Lock — the mutation that races the reader pre-fix.
	wg.Go(func() {
		defer close(stop)
		for _, id := range ids {
			form := url.Values{"id": {id}, "outcome": {"succeed"}}
			req, err := http.NewRequest(http.MethodPost,
				srv.URL+"/__hamr/stripe/payment_intent/complete",
				strings.NewReader(form.Encode()))
			if err != nil {
				t.Errorf("build complete request: %v", err)
				return
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			drain(http.DefaultClient.Do(req))
		}
	})

	wg.Wait()
}

// drain consumes and closes an HTTP response, ignoring transport errors —
// the race test only cares that the server didn't crash, not the payloads.
func drain(resp *http.Response, err error) {
	if err != nil || resp == nil {
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}
