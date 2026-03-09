package devserver

import (
	"bytes"
	"encoding/json"
	"sync"
)

// logWriter implements io.Writer. It line-buffers output, tags each complete
// line with a rule name, appends to a shared LogBuffer, and broadcasts an SSE
// "output" event.
type logWriter struct {
	mu      sync.Mutex
	rule    string
	color   string
	buf     *LogBuffer
	broker  *SSEBroker
	partial bytes.Buffer
}

func newLogWriter(rule, color string, buf *LogBuffer, broker *SSEBroker) *logWriter {
	return &logWriter{rule: rule, color: color, buf: buf, broker: broker}
}

func (lw *logWriter) Write(p []byte) (int, error) {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	lw.partial.Write(p)
	for {
		line, err := lw.partial.ReadBytes('\n')
		if err != nil {
			// No newline found — put the partial data back.
			lw.partial.Write(line)
			break
		}
		// Trim the trailing newline for clean storage.
		text := string(bytes.TrimRight(line, "\n"))
		lw.buf.Append(LogLine{Rule: lw.rule, Text: text, Color: lw.color})
		lw.broker.Broadcast(outputEvent(lw.rule, text, lw.color))
	}
	return len(p), nil
}

// Flush broadcasts any remaining partial line.
func (lw *logWriter) Flush() {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	if lw.partial.Len() == 0 {
		return
	}
	text := lw.partial.String()
	lw.partial.Reset()
	lw.buf.Append(LogLine{Rule: lw.rule, Text: text, Color: lw.color})
	lw.broker.Broadcast(outputEvent(lw.rule, text, lw.color))
}

func outputEvent(rule, text, color string) SSEEvent {
	payload, _ := json.Marshal(LogLine{Rule: rule, Text: text, Color: color})
	return SSEEvent{Type: "output", Data: string(payload)}
}
