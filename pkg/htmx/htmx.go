// Package htmx provides request detection and response header helpers for htmx.
//
// All functions operate on standard net/http types with no framework dependency.
package htmx

import (
	"net/http"
	"strings"
)

// Request headers sent by htmx.
const (
	HeaderRequest        = "HX-Request"
	HeaderBoosted        = "HX-Boosted"
	HeaderTrigger        = "HX-Trigger"
	HeaderTarget         = "HX-Target"
	HeaderCurrentURL     = "HX-Current-URL"
	HeaderPrompt         = "HX-Prompt"
	HeaderHistoryRestore = "HX-History-Restore-Request"
)

// Response headers consumed by htmx.
const (
	HeaderRedirect          = "HX-Redirect"
	HeaderTriggerResponse   = "HX-Trigger"
	HeaderTriggerAfterSettle = "HX-Trigger-After-Settle"
	HeaderTriggerAfterSwap  = "HX-Trigger-After-Swap"
	HeaderReswap            = "HX-Reswap"
	HeaderRetarget          = "HX-Retarget"
	HeaderRefresh           = "HX-Refresh"
	HeaderPushURL           = "HX-Push-Url"
	HeaderReplaceURL        = "HX-Replace-Url"
)

// IsHTMX reports whether the request was made by htmx.
func IsHTMX(r *http.Request) bool {
	return r.Header.Get(HeaderRequest) == "true"
}

// IsBoosted reports whether the request is a boosted navigation.
func IsBoosted(r *http.Request) bool {
	return r.Header.Get(HeaderBoosted) == "true"
}

// GetTrigger returns the HX-Trigger request header value.
func GetTrigger(r *http.Request) string {
	return r.Header.Get(HeaderTrigger)
}

// GetTarget returns the HX-Target request header value.
func GetTarget(r *http.Request) string {
	return r.Header.Get(HeaderTarget)
}

// Redirect sets the HX-Redirect response header.
func Redirect(w http.ResponseWriter, url string) {
	w.Header().Set(HeaderRedirect, url)
}

// Trigger sets the HX-Trigger response header with one or more events.
func Trigger(w http.ResponseWriter, events ...string) {
	w.Header().Set(HeaderTriggerResponse, strings.Join(events, ", "))
}

// TriggerAfterSettle sets the HX-Trigger-After-Settle response header.
func TriggerAfterSettle(w http.ResponseWriter, events ...string) {
	w.Header().Set(HeaderTriggerAfterSettle, strings.Join(events, ", "))
}

// TriggerAfterSwap sets the HX-Trigger-After-Swap response header.
func TriggerAfterSwap(w http.ResponseWriter, events ...string) {
	w.Header().Set(HeaderTriggerAfterSwap, strings.Join(events, ", "))
}

// Reswap sets the HX-Reswap response header.
func Reswap(w http.ResponseWriter, strategy string) {
	w.Header().Set(HeaderReswap, strategy)
}

// Retarget sets the HX-Retarget response header.
func Retarget(w http.ResponseWriter, selector string) {
	w.Header().Set(HeaderRetarget, selector)
}

// Refresh sets the HX-Refresh response header to "true".
func Refresh(w http.ResponseWriter) {
	w.Header().Set(HeaderRefresh, "true")
}

// PushURL sets the HX-Push-Url response header.
func PushURL(w http.ResponseWriter, url string) {
	w.Header().Set(HeaderPushURL, url)
}

// ReplaceURL sets the HX-Replace-Url response header.
func ReplaceURL(w http.ResponseWriter, url string) {
	w.Header().Set(HeaderReplaceURL, url)
}
