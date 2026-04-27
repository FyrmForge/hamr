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

// Sink is an io.Writer that emits LogLineMsg per newline-terminated line.
// One Sink can fan in stdout, stderr, and the runner's slog writer because
// the only thing it does with each line is post a tea.Msg — colors and
// prefixes are preserved upstream by prefixWriter.
type Sink struct {
	mu   sync.Mutex
	buf  []byte
	send func(tea.Msg)
}

// NewSink returns a sink that drops lines until Bind is called. The dev
// runtime binds the program right after constructing it, so the drop window
// is bounded by the few lines emitted between sink creation and program
// start — acceptable for a demo runtime.
func NewSink() *Sink {
	return &Sink{}
}

// Bind attaches the bubbletea program. After this call, every complete line
// is delivered as a LogLineMsg.
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
			s.send(LogLineMsg(out))
		}
	}
	return len(p), nil
}

var _ io.Writer = (*Sink)(nil)

// DockerSink is an io.Writer for one docker compose stack's logs. Each
// complete line becomes a DockerLogLineMsg tagged with the stack's name
// from hamr.toml (`[[dev.docker_compose]] name = ...`). Sink wraps this
// — they share the line-batching logic and just emit different message
// types.
type DockerSink struct {
	mu   sync.Mutex
	buf  []byte
	send func(tea.Msg)
	name string
}

// NewDockerSink returns a sink scoped to one compose stack name. The
// runtime binds the program shortly after construction.
func NewDockerSink(name string) *DockerSink {
	return &DockerSink{name: name}
}

// Bind attaches the bubbletea program. After this call, every complete
// line is delivered as a DockerLogLineMsg.
func (s *DockerSink) Bind(p *tea.Program) {
	s.bindSender(func(m tea.Msg) { p.Send(m) })
}

func (s *DockerSink) bindSender(send func(tea.Msg)) {
	s.mu.Lock()
	s.send = send
	s.mu.Unlock()
}

func (s *DockerSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.buf = append(s.buf, p...)
	for {
		i := bytes.IndexByte(s.buf, '\n')
		if i < 0 {
			break
		}
		line := s.buf[:i]
		if n := len(line); n > 0 && line[n-1] == '\r' {
			line = line[:n-1]
		}
		out := string(line)
		s.buf = s.buf[i+1:]
		if s.send != nil {
			s.send(DockerLogLineMsg{Name: s.name, Line: out})
		}
	}
	return len(p), nil
}

var _ io.Writer = (*DockerSink)(nil)
