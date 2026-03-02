package e2e

import (
	"testing"
	"time"

	"github.com/go-rod/rod"
)

// WaitForHTMXIdle waits until no elements carry the htmx-request class,
// indicating all in-flight HTMX requests have settled. If htmx is not loaded
// on the page the function returns immediately.
func WaitForHTMXIdle(t *testing.T, page *rod.Page, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		result, err := page.Eval(`() => {
			if (typeof htmx === 'undefined') return true;
			return document.querySelector('.htmx-request') === null;
		}`)
		if err == nil && result.Value.Bool() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("e2e: WaitForHTMXIdle timed out after %v", timeout)
}

// WaitForHTMXSwap waits until the HTML content of the element matching
// selector changes from its initial value, indicating an HTMX swap occurred.
func WaitForHTMXSwap(t *testing.T, page *rod.Page, selector string, timeout time.Duration) {
	t.Helper()

	cfg := configForTest(t)

	el, err := page.Timeout(cfg.Timeout).Element(selector)
	if err != nil {
		t.Errorf("e2e: WaitForHTMXSwap: element %q not found: %v", selector, err)
		return
	}

	initial, err := el.HTML()
	if err != nil {
		t.Errorf("e2e: WaitForHTMXSwap: failed to get initial HTML of %q: %v", selector, err)
		return
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		current, err := el.HTML()
		if err == nil && current != initial {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("e2e: WaitForHTMXSwap timed out waiting for %q content to change after %v", selector, timeout)
}

// ClickAndWaitHTMX clicks the element matching selector and waits for all
// HTMX requests to settle.
func ClickAndWaitHTMX(t *testing.T, page *rod.Page, selector string, timeout time.Duration) {
	t.Helper()

	Click(t, page, selector)
	WaitForHTMXIdle(t, page, timeout)
}
