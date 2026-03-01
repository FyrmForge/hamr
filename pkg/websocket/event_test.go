package websocket

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewEvent_marshalPayload(t *testing.T) {
	type payload struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}

	e := NewEvent("test:event", payload{Name: "foo", Count: 42})

	assert.Equal(t, EventType("test:event"), e.Type)

	var got payload
	require.NoError(t, json.Unmarshal(e.Payload, &got))
	assert.Equal(t, "foo", got.Name)
	assert.Equal(t, 42, got.Count)
}

func TestNewEvent_marshalFailure(t *testing.T) {
	// Channels cannot be JSON-marshaled.
	e := NewEvent("bad:event", make(chan int))

	assert.Equal(t, EventType("bad:event"), e.Type)
	assert.JSONEq(t, `{}`, string(e.Payload))
}

func TestNewHTMLEvent_fields(t *testing.T) {
	e := NewHTMLEvent("html:update", "#container", "<p>hello</p>")

	assert.Equal(t, EventType("html:update"), e.Type)
	assert.Equal(t, "#container", e.Target)
	assert.Equal(t, "<p>hello</p>", e.HTML)
	assert.Equal(t, "innerHTML", e.Swap)
}

func TestNewOuterHTMLEvent_fields(t *testing.T) {
	e := NewOuterHTMLEvent("html:replace", "#item-5", "<div>new</div>")

	assert.Equal(t, EventType("html:replace"), e.Type)
	assert.Equal(t, "#item-5", e.Target)
	assert.Equal(t, "<div>new</div>", e.HTML)
	assert.Equal(t, "outerHTML", e.Swap)
}

func TestNewTriggerEvent_fields(t *testing.T) {
	e := NewTriggerEvent("notify:refresh", "#panel", "refresh")

	assert.Equal(t, EventType("notify:refresh"), e.Type)
	assert.Equal(t, "#panel", e.Target)
	assert.Equal(t, "refresh", e.Trigger)
	assert.Empty(t, e.HTML)
	assert.Empty(t, e.Swap)
}

func TestEvent_JSON_roundtrip(t *testing.T) {
	original := NewEvent("round:trip", map[string]string{"key": "value"})

	data := original.JSON()

	var decoded Event
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, original.Type, decoded.Type)
	assert.JSONEq(t, string(original.Payload), string(decoded.Payload))
}

func TestEvent_JSON_omitsEmptyFields(t *testing.T) {
	e := NewTriggerEvent("trigger:only", "#btn", "click")
	data := e.JSON()

	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))

	assert.Contains(t, raw, "type")
	assert.Contains(t, raw, "target")
	assert.Contains(t, raw, "trigger")
	assert.NotContains(t, raw, "payload")
	assert.NotContains(t, raw, "swap")
	assert.NotContains(t, raw, "html")
}
