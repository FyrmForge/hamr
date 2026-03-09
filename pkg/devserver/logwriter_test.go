package devserver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestLogWriter(rule string) (*logWriter, *LogBuffer, *SSEBroker) {
	buf := NewLogBuffer(100)
	broker := NewSSEBroker(nil, nil, nil)
	lw := newLogWriter(rule, "", buf, broker)
	return lw, buf, broker
}

func TestLogWriter_CompleteLine(t *testing.T) {
	lw, buf, _ := newTestLogWriter("go")

	n, err := lw.Write([]byte("hello world\n"))
	require.NoError(t, err)
	assert.Equal(t, 12, n)

	lines := buf.Lines()
	require.Len(t, lines, 1)
	assert.Equal(t, "go", lines[0].Rule)
	assert.Equal(t, "hello world", lines[0].Text)
}

func TestLogWriter_PartialLine(t *testing.T) {
	lw, buf, _ := newTestLogWriter("templ")

	_, _ = lw.Write([]byte("part"))
	assert.Empty(t, buf.Lines(), "partial line should not be flushed yet")

	_, _ = lw.Write([]byte("ial complete\n"))
	lines := buf.Lines()
	require.Len(t, lines, 1)
	assert.Equal(t, "partial complete", lines[0].Text)
}

func TestLogWriter_Flush(t *testing.T) {
	lw, buf, _ := newTestLogWriter("css")

	_, _ = lw.Write([]byte("no newline"))
	assert.Empty(t, buf.Lines())

	lw.Flush()
	lines := buf.Lines()
	require.Len(t, lines, 1)
	assert.Equal(t, "no newline", lines[0].Text)
}

func TestLogWriter_FlushEmpty(t *testing.T) {
	lw, buf, _ := newTestLogWriter("x")
	lw.Flush()
	assert.Empty(t, buf.Lines())
}

func TestLogWriter_MultiLine(t *testing.T) {
	lw, buf, _ := newTestLogWriter("build")

	_, _ = lw.Write([]byte("line1\nline2\nline3\n"))

	lines := buf.Lines()
	require.Len(t, lines, 3)
	assert.Equal(t, "line1", lines[0].Text)
	assert.Equal(t, "line2", lines[1].Text)
	assert.Equal(t, "line3", lines[2].Text)
}
