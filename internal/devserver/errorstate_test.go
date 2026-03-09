package devserver

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestErrorState_SetAndSnapshot(t *testing.T) {
	es := NewErrorState()
	es.Set("go", "compile error")
	es.Set("templ", "template error")

	snap := es.Snapshot()
	assert.Equal(t, "compile error", snap["go"])
	assert.Equal(t, "template error", snap["templ"])
	assert.Len(t, snap, 2)
}

func TestErrorState_Clear(t *testing.T) {
	es := NewErrorState()
	es.Set("go", "error")
	es.Set("templ", "error")
	es.Clear("go")

	snap := es.Snapshot()
	assert.Len(t, snap, 1)
	assert.Equal(t, "error", snap["templ"])
	_, ok := snap["go"]
	assert.False(t, ok)
}

func TestErrorState_HasErrors(t *testing.T) {
	es := NewErrorState()
	assert.False(t, es.HasErrors())

	es.Set("go", "error")
	assert.True(t, es.HasErrors())

	es.Clear("go")
	assert.False(t, es.HasErrors())
}
