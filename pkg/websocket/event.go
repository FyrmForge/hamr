package websocket

import "encoding/json"

// EventType represents the type of WebSocket event.
// No domain-specific constants are defined here — projects define their own.
type EventType string

// Event is the standard WebSocket event structure.
// Supports three delivery modes:
//  1. HTML Direct: set Target + HTML, client swaps HTML into target
//  2. HTMX Trigger: set Target + Trigger, client calls htmx.trigger()
//  3. Data Only: set Payload only, client handles via registered callback
type Event struct {
	Type    EventType       `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Target  string          `json:"target,omitempty"`  // CSS selector
	Swap    string          `json:"swap,omitempty"`    // "innerHTML" or "outerHTML"
	HTML    string          `json:"html,omitempty"`    // rendered HTML to swap in
	Trigger string          `json:"trigger,omitempty"` // HTMX event name
}

// NewEvent creates a data-only event with a JSON-marshaled payload.
func NewEvent(eventType EventType, payload any) *Event {
	data, err := json.Marshal(payload)
	if err != nil {
		data = []byte("{}")
	}
	return &Event{Type: eventType, Payload: data}
}

// NewHTMLEvent creates an event that swaps HTML into a target element (innerHTML).
func NewHTMLEvent(eventType EventType, target, html string) *Event {
	return &Event{
		Type:   eventType,
		Target: target,
		HTML:   html,
		Swap:   "innerHTML",
	}
}

// NewOuterHTMLEvent creates an event that replaces a target element entirely (outerHTML).
func NewOuterHTMLEvent(eventType EventType, target, html string) *Event {
	return &Event{
		Type:   eventType,
		Target: target,
		HTML:   html,
		Swap:   "outerHTML",
	}
}

// NewTriggerEvent creates an event that triggers an HTMX event on a target.
func NewTriggerEvent(eventType EventType, target, trigger string) *Event {
	return &Event{
		Type:    eventType,
		Target:  target,
		Trigger: trigger,
	}
}

// JSON serializes the event to JSON bytes.
func (e *Event) JSON() []byte {
	data, err := json.Marshal(e)
	if err != nil {
		return []byte(`{"type":"error"}`)
	}
	return data
}
