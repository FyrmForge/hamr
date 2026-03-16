package devserver

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
)

// ansiEscape matches common ANSI escape sequences, including CSI (colors,
// cursor control like \x1b[?25h) and OSC sequences such as terminal
// hyperlinks.
var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)`)

// rollingFileWriter is an io.Writer that appends plain-text lines to a file,
// stripping ANSI escape codes and maintaining a maximum line count by
// truncating old lines when the file grows too large.
type rollingFileWriter struct {
	mu        sync.Mutex
	file      *os.File
	path      string
	maxLines  int
	lineCount int
	partial   bytes.Buffer
}

// newRollingFileWriter creates a rolling file writer. It creates the parent
// directory if needed, opens the file for appending, and counts any existing
// lines so truncation math is correct from the start.
func newRollingFileWriter(path string, maxLines int) (*rollingFileWriter, error) {
	if maxLines <= 0 {
		return nil, fmt.Errorf("maxLines must be greater than 0")
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	// Count existing lines (if the file already exists).
	lineCount := 0
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		lineCount = bytes.Count(data, []byte{'\n'})
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}

	w := &rollingFileWriter{
		file:      f,
		path:      path,
		maxLines:  maxLines,
		lineCount: lineCount,
	}

	// If the existing file already exceeds the limit, truncate immediately.
	if lineCount > maxLines {
		w.truncate()
	}

	return w, nil
}

// Write receives raw bytes (possibly with ANSI codes), strips escape
// sequences, line-buffers the input, and appends complete lines to the file.
func (w *rollingFileWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Strip ANSI escape codes.
	clean := ansiEscape.ReplaceAll(p, nil)
	w.partial.Write(clean)

	for {
		line, err := w.partial.ReadBytes('\n')
		if err != nil {
			// No newline found — put the partial data back.
			w.partial.Write(line)
			break
		}

		// Normalize \r\n → \n (line already ends with \n from ReadBytes).
		line = bytes.TrimRight(line, "\r\n")
		line = append(line, '\n')

		if w.file == nil {
			break
		}
		if _, werr := w.file.Write(line); werr != nil {
			return len(p), werr
		}
		w.lineCount++
	}

	if w.lineCount > w.maxLines*2 {
		w.truncate()
	}

	return len(p), nil
}

// Close flushes any remaining partial line and closes the file.
func (w *rollingFileWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return nil
	}

	if w.partial.Len() > 0 {
		text := bytes.TrimRight(w.partial.Bytes(), "\r\n")
		line := make([]byte, len(text)+1)
		copy(line, text)
		line[len(line)-1] = '\n'
		_, _ = w.file.Write(line)
		w.partial.Reset()
		w.lineCount++
	}

	if w.lineCount > w.maxLines {
		w.truncate()
	}

	if w.file == nil {
		return nil
	}
	return w.file.Close()
}

// truncate reads the file, keeps only the last maxLines lines, and rewrites it.
// Must be called with w.mu held.
func (w *rollingFileWriter) truncate() {
	if w.file == nil {
		return
	}
	// Sync before reading to ensure all written data is on disk.
	_ = w.file.Sync()

	data, err := os.ReadFile(w.path)
	if err != nil {
		return
	}

	lines := bytes.Split(data, []byte{'\n'})
	// bytes.Split on trailing \n produces an empty final element — drop it.
	if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}

	if len(lines) <= w.maxLines {
		w.lineCount = len(lines)
		return
	}

	keep := lines[len(lines)-w.maxLines:]

	var buf bytes.Buffer
	for _, l := range keep {
		buf.Write(l)
		buf.WriteByte('\n')
	}

	// Close the current file handle, rewrite, and reopen for append.
	_ = w.file.Close()

	if err := os.WriteFile(w.path, buf.Bytes(), 0o644); err != nil {
		// Rewrite failed — reopen the original file and skip truncation.
		w.file, _ = os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		return
	}
	w.lineCount = w.maxLines

	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		// Reopen failed — nil out the file handle; Write() checks for this.
		w.file = nil
		return
	}
	w.file = f
}
