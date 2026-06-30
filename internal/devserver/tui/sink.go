package tui

import (
	"bytes"
	"io"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
)

// LogLineMsg carries one fully-formed line from a subprocess or the dev
// runner's slog handler. The bubbletea model appends it to the viewport.
type LogLineMsg string

// DockerLogLineMsg carries one line from `docker compose logs -f` for a
// specific compose entry. The model routes these to a per-entry buffer
// so Tab can cycle between hamr logs and one log view per docker stack.
type DockerLogLineMsg struct {
	Name string
	Line string
}

// Sink is an io.Writer that emits one tea.Msg per newline-terminated line.
// toMsg builds the message from each line, so the same line-batching logic
// serves both the runner's combined log (LogLineMsg) and a docker stack's
// tagged log (DockerLogLineMsg) — see NewSink and NewDockerSink.
//
// One Sink can fan in stdout, stderr, and the runner's slog writer because
// the only thing it does with each line is post a tea.Msg — colors and
// prefixes are preserved upstream by prefixWriter.
type Sink struct {
	mu    sync.Mutex
	buf   []byte
	send  func(tea.Msg)
	toMsg func(string) tea.Msg
}

// NewSink returns a sink emitting LogLineMsg per line. It drops lines until
// Bind is called; the dev runtime binds the program right after constructing
// it, so the drop window is bounded by the few lines emitted between sink
// creation and program start — acceptable for a demo runtime.
func NewSink() *Sink {
	return &Sink{toMsg: func(s string) tea.Msg { return LogLineMsg(s) }}
}

// NewDockerSink returns a sink scoped to one docker compose stack, emitting
// DockerLogLineMsg tagged with the stack's name from hamr.toml
// (`[[dev.docker_compose]] name = ...`). The runtime binds the program
// shortly after construction.
func NewDockerSink(name string) *Sink {
	return &Sink{toMsg: func(s string) tea.Msg { return DockerLogLineMsg{Name: name, Line: s} }}
}

// Bind attaches the bubbletea program. After this call, every complete line
// is delivered as the sink's message type.
func (s *Sink) Bind(p *tea.Program) {
	s.bindSender(func(m tea.Msg) { p.Send(m) })
}

// bindSender lets tests substitute a plain function for the program's
// Send. Production callers should use Bind.
func (s *Sink) bindSender(send func(tea.Msg)) {
	s.mu.Lock()
	s.send = send
	s.mu.Unlock()
}

func (s *Sink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.buf = append(s.buf, p...)
	for {
		i := bytes.IndexByte(s.buf, '\n')
		if i < 0 {
			break
		}
		// Trim trailing CR so prefixWriter's "\r\n" terminators don't leave
		// stray carriage returns inside the viewport's wrapped lines.
		line := s.buf[:i]
		if n := len(line); n > 0 && line[n-1] == '\r' {
			line = line[:n-1]
		}
		out := string(line)
		s.buf = s.buf[i+1:]
		if s.send != nil {
			s.send(s.toMsg(out))
		}
	}
	return len(p), nil
}

var _ io.Writer = (*Sink)(nil)
