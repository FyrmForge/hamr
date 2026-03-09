package devserver

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLogBuffer_Append(t *testing.T) {
	lb := NewLogBuffer(10)
	lb.Append(LogLine{Rule: "go", Text: "building..."})
	lb.Append(LogLine{Rule: "go", Text: "done"})

	lines := lb.Lines()
	assert.Len(t, lines, 2)
	assert.Equal(t, LogLine{Rule: "go", Text: "building..."}, lines[0])
	assert.Equal(t, LogLine{Rule: "go", Text: "done"}, lines[1])
}

func TestLogBuffer_Cap(t *testing.T) {
	lb := NewLogBuffer(3)
	lb.Append(LogLine{Rule: "a", Text: "1"})
	lb.Append(LogLine{Rule: "b", Text: "2"})
	lb.Append(LogLine{Rule: "c", Text: "3"})
	lb.Append(LogLine{Rule: "d", Text: "4"})

	lines := lb.Lines()
	assert.Len(t, lines, 3)
	// Oldest ("a","1") should have been trimmed.
	assert.Equal(t, "b", lines[0].Rule)
	assert.Equal(t, "c", lines[1].Rule)
	assert.Equal(t, "d", lines[2].Rule)
}

func TestLogBuffer_Empty(t *testing.T) {
	lb := NewLogBuffer(10)
	lines := lb.Lines()
	assert.Empty(t, lines)
	assert.NotNil(t, lines)
}

func TestLogBuffer_LinesCopy(t *testing.T) {
	lb := NewLogBuffer(10)
	lb.Append(LogLine{Rule: "x", Text: "hello"})
	lines := lb.Lines()
	lines[0].Text = "modified"
	// Original should be unaffected.
	assert.Equal(t, "hello", lb.Lines()[0].Text)
}
