package devserver

import "sync"

// LogLine is a single line of process output tagged with its rule name.
type LogLine struct {
	Rule  string `json:"rule"`
	Text  string `json:"text"`
	Color string `json:"color,omitempty"`
}

// LogBuffer is a thread-safe ring buffer that keeps the last N log lines.
type LogBuffer struct {
	mu    sync.Mutex
	lines []LogLine
	max   int
}

// NewLogBuffer creates a new LogBuffer capped at max lines.
func NewLogBuffer(max int) *LogBuffer {
	return &LogBuffer{
		lines: make([]LogLine, 0, max),
		max:   max,
	}
}

// Append adds a line to the buffer, trimming the oldest if over capacity.
func (lb *LogBuffer) Append(line LogLine) {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	lb.lines = append(lb.lines, line)
	if len(lb.lines) > lb.max {
		// Drop oldest entries.
		excess := len(lb.lines) - lb.max
		copy(lb.lines, lb.lines[excess:])
		lb.lines = lb.lines[:lb.max]
	}
}

// Lines returns a copy of all buffered log lines.
func (lb *LogBuffer) Lines() []LogLine {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	out := make([]LogLine, len(lb.lines))
	copy(out, lb.lines)
	return out
}
