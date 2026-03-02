package e2e

import (
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/stretchr/testify/assert"
)

// AssertElementExists verifies that selector matches an element on the page.
func AssertElementExists(t *testing.T, page *rod.Page, selector string) {
	t.Helper()

	cfg := configForTest(t)

	el, err := page.Timeout(cfg.Timeout).Element(selector)
	assert.NoError(t, err, "element %q should exist", selector)
	assert.NotNil(t, el, "element %q should not be nil", selector)
}

// AssertElementNotExists verifies that selector does not match any element on
// the page.
func AssertElementNotExists(t *testing.T, page *rod.Page, selector string) {
	t.Helper()

	_, err := page.Timeout(500 * time.Millisecond).Element(selector)
	assert.Error(t, err, "element %q should not exist", selector)
}

// AssertElementNotVisible verifies that selector either does not exist or is
// not visible on the page.
func AssertElementNotVisible(t *testing.T, page *rod.Page, selector string) {
	t.Helper()

	cfg := configForTest(t)

	el, err := page.Timeout(cfg.Timeout).Element(selector)
	if err != nil {
		return // element doesn't exist — not visible
	}

	visible, err := el.Visible()
	if err != nil {
		return // can't determine visibility — treat as not visible
	}
	assert.False(t, visible, "element %q should not be visible", selector)
}

// AssertElementContainsText verifies that the text content of selector
// contains the given substring.
func AssertElementContainsText(t *testing.T, page *rod.Page, selector, text string) {
	t.Helper()

	cfg := configForTest(t)

	el, err := page.Timeout(cfg.Timeout).Element(selector)
	if !assert.NoError(t, err, "element %q should exist", selector) {
		return
	}

	got, err := el.Text()
	if !assert.NoError(t, err, "failed to get text of %q", selector) {
		return
	}
	assert.Contains(t, got, text, "element %q text should contain %q", selector, text)
}

// AssertElementHasClass verifies that the element matching selector has the
// given CSS class.
func AssertElementHasClass(t *testing.T, page *rod.Page, selector, class string) {
	t.Helper()

	cfg := configForTest(t)

	el, err := page.Timeout(cfg.Timeout).Element(selector)
	if !assert.NoError(t, err, "element %q should exist", selector) {
		return
	}

	classAttr, err := el.Attribute("class")
	if !assert.NoError(t, err, "failed to get class attribute of %q", selector) {
		return
	}

	classes := ""
	if classAttr != nil {
		classes = *classAttr
	}
	assert.Contains(t, " "+classes+" ", " "+class+" ",
		"element %q should have class %q, got %q", selector, class, classes)
}

// AssertElementCount verifies that exactly expected elements match selector.
func AssertElementCount(t *testing.T, page *rod.Page, selector string, expected int) {
	t.Helper()

	cfg := configForTest(t)

	els, err := page.Timeout(cfg.Timeout).Elements(selector)
	if !assert.NoError(t, err, "failed to query elements %q", selector) {
		return
	}
	assert.Equal(t, expected, len(els),
		"expected %d elements matching %q, got %d", expected, selector, len(els))
}

// AssertURL verifies that the page URL equals expected.
func AssertURL(t *testing.T, page *rod.Page, expected string) {
	t.Helper()

	info, err := page.Info()
	if !assert.NoError(t, err, "failed to get page info") {
		return
	}
	assert.Equal(t, expected, info.URL, "page URL should match")
}

// AssertURLContains verifies that the page URL contains substring.
func AssertURLContains(t *testing.T, page *rod.Page, substring string) {
	t.Helper()

	info, err := page.Info()
	if !assert.NoError(t, err, "failed to get page info") {
		return
	}
	assert.Contains(t, info.URL, substring, "page URL should contain %q", substring)
}
