package htmx

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// Request helpers
// ---------------------------------------------------------------------------

func TestIsHTMX(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set(HeaderRequest, "true")
	assert.True(t, IsHTMX(r))
}

func TestIsHTMX_false(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	assert.False(t, IsHTMX(r))
}

func TestIsHTMX_wrongValue(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set(HeaderRequest, "yes")
	assert.False(t, IsHTMX(r))
}

func TestIsBoosted(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set(HeaderBoosted, "true")
	assert.True(t, IsBoosted(r))
}

func TestIsBoosted_false(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	assert.False(t, IsBoosted(r))
}

func TestGetTrigger(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set(HeaderTrigger, "btn-submit")
	assert.Equal(t, "btn-submit", GetTrigger(r))
}

func TestGetTrigger_empty(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	assert.Equal(t, "", GetTrigger(r))
}

func TestGetTarget(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set(HeaderTarget, "#main")
	assert.Equal(t, "#main", GetTarget(r))
}

func TestGetTarget_empty(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	assert.Equal(t, "", GetTarget(r))
}

// ---------------------------------------------------------------------------
// Response helpers
// ---------------------------------------------------------------------------

func TestRedirect(t *testing.T) {
	w := httptest.NewRecorder()
	Redirect(w, "/dashboard")
	assert.Equal(t, "/dashboard", w.Header().Get(HeaderRedirect))
}

func TestTrigger_single(t *testing.T) {
	w := httptest.NewRecorder()
	Trigger(w, "closeModal")
	assert.Equal(t, "closeModal", w.Header().Get(HeaderTriggerResponse))
}

func TestTrigger_multiple(t *testing.T) {
	w := httptest.NewRecorder()
	Trigger(w, "closeModal", "refreshList")
	assert.Equal(t, "closeModal, refreshList", w.Header().Get(HeaderTriggerResponse))
}

func TestTrigger_json(t *testing.T) {
	w := httptest.NewRecorder()
	Trigger(w, `{"showMsg":"hi"}`)
	assert.Equal(t, `{"showMsg":"hi"}`, w.Header().Get(HeaderTriggerResponse))
}

func TestTriggerAfterSettle(t *testing.T) {
	w := httptest.NewRecorder()
	TriggerAfterSettle(w, "evt1", "evt2")
	assert.Equal(t, "evt1, evt2", w.Header().Get(HeaderTriggerAfterSettle))
}

func TestTriggerAfterSwap(t *testing.T) {
	w := httptest.NewRecorder()
	TriggerAfterSwap(w, "swapped")
	assert.Equal(t, "swapped", w.Header().Get(HeaderTriggerAfterSwap))
}

func TestReswap(t *testing.T) {
	w := httptest.NewRecorder()
	Reswap(w, "outerHTML")
	assert.Equal(t, "outerHTML", w.Header().Get(HeaderReswap))
}

func TestRetarget(t *testing.T) {
	w := httptest.NewRecorder()
	Retarget(w, "#sidebar")
	assert.Equal(t, "#sidebar", w.Header().Get(HeaderRetarget))
}

func TestRefresh(t *testing.T) {
	w := httptest.NewRecorder()
	Refresh(w)
	assert.Equal(t, "true", w.Header().Get(HeaderRefresh))
}

func TestPushURL(t *testing.T) {
	w := httptest.NewRecorder()
	PushURL(w, "/new-url")
	assert.Equal(t, "/new-url", w.Header().Get(HeaderPushURL))
}

func TestReplaceURL(t *testing.T) {
	w := httptest.NewRecorder()
	ReplaceURL(w, "/replaced")
	assert.Equal(t, "/replaced", w.Header().Get(HeaderReplaceURL))
}
