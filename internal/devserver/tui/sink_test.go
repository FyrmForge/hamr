package tui

import (
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// captureSink wires a test sender that records every LogLineMsg.
func captureSink() (*Sink, *[]string, *sync.Mutex) {
	var mu sync.Mutex
	var lines []string
	s := NewSink()
	s.bindSender(func(m tea.Msg) {
		if line, ok := m.(LogLineMsg); ok {
			mu.Lock()
			lines = append(lines, string(line))
			mu.Unlock()
		}
	})
	return s, &lines, &mu
}

func TestSink_EmitsCompleteLinesOnly(t *testing.T) {
	s, lines, mu := captureSink()

	if _, err := s.Write([]byte("partial line without newline")); err != nil {
		t.Fatalf("write: %v", err)
	}
	mu.Lock()
	got := len(*lines)
	mu.Unlock()
	if got != 0 {
		t.Fatalf("incomplete write must not emit, got %d lines", got)
	}

	if _, err := s.Write([]byte("\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*lines) != 1 || (*lines)[0] != "partial line without newline" {
		t.Fatalf("after newline expected one buffered line, got %v", *lines)
	}
}

func TestSink_StripsTrailingCarriageReturn(t *testing.T) {
	// prefixWriter emits "\r\n"; the trailing CR should not leak into the
	// viewport or it ends up rendered as a stray glyph.
	s, lines, mu := captureSink()

	if _, err := s.Write([]byte("hello\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*lines) != 1 || (*lines)[0] != "hello" {
		t.Fatalf("expected stripped 'hello', got %q", *lines)
	}
}

func TestSink_SplitsBatchedLines(t *testing.T) {
	s, lines, mu := captureSink()

	payload := "build start\nbuild ok\nrunning\n"
	if _, err := s.Write([]byte(payload)); err != nil {
		t.Fatalf("write: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{"build start", "build ok", "running"}
	if strings.Join(*lines, "|") != strings.Join(want, "|") {
		t.Fatalf("want %v, got %v", want, *lines)
	}
}

func TestSink_AccumulatesAcrossWrites(t *testing.T) {
	s, lines, mu := captureSink()

	if _, err := s.Write([]byte("hel")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := s.Write([]byte("lo wor")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := s.Write([]byte("ld\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(*lines) != 1 || (*lines)[0] != "hello world" {
		t.Fatalf("expected single reassembled line, got %v", *lines)
	}
}

func TestSink_DropsBeforeBind(t *testing.T) {
	// Lines written before Bind are dropped by design — the runtime binds
	// immediately so the window is tiny, and we'd rather drop than panic.
	s := NewSink()
	if _, err := s.Write([]byte("ignored\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Now bind and write a fresh line.
	var got []string
	s.bindSender(func(m tea.Msg) {
		got = append(got, string(m.(LogLineMsg)))
	})
	if _, err := s.Write([]byte("delivered\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if len(got) != 1 || got[0] != "delivered" {
		t.Fatalf("post-bind line should be delivered, got %v", got)
	}
}

func TestSink_ReturnsFullByteCount(t *testing.T) {
	s, _, _ := captureSink()
	in := []byte("first\nsecond\nthird")
	n, err := s.Write(in)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if n != len(in) {
		t.Fatalf("Write must return len(p)=%d, got %d", len(in), n)
	}
}

// captureDockerSink is the docker-tagged equivalent of captureSink.
func captureDockerSink(name string) (*DockerSink, *[]DockerLogLineMsg, *sync.Mutex) {
	var mu sync.Mutex
	var lines []DockerLogLineMsg
	s := NewDockerSink(name)
	s.bindSender(func(m tea.Msg) {
		if line, ok := m.(DockerLogLineMsg); ok {
			mu.Lock()
			lines = append(lines, line)
			mu.Unlock()
		}
	})
	return s, &lines, &mu
}

func TestDockerSink_TagsLinesWithStackName(t *testing.T) {
	s, lines, mu := captureDockerSink("infra")

	if _, err := s.Write([]byte("postgres ready\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*lines) != 1 {
		t.Fatalf("expected one line, got %d", len(*lines))
	}
	got := (*lines)[0]
	if got.Name != "infra" {
		t.Fatalf("Name should match the sink's stack: got %q", got.Name)
	}
	if got.Line != "postgres ready" {
		t.Fatalf("Line: got %q", got.Line)
	}
}

func TestDockerSink_TwoSinksDontCrossBuffers(t *testing.T) {
	a, aLines, aMu := captureDockerSink("infra")
	b, bLines, bMu := captureDockerSink("stripe")

	if _, err := a.Write([]byte("a-line\n")); err != nil {
		t.Fatalf("a write: %v", err)
	}
	if _, err := b.Write([]byte("b-line\n")); err != nil {
		t.Fatalf("b write: %v", err)
	}

	aMu.Lock()
	bMu.Lock()
	defer aMu.Unlock()
	defer bMu.Unlock()

	if len(*aLines) != 1 || (*aLines)[0].Line != "a-line" || (*aLines)[0].Name != "infra" {
		t.Fatalf("infra sink picked up the wrong lines: %v", *aLines)
	}
	if len(*bLines) != 1 || (*bLines)[0].Line != "b-line" || (*bLines)[0].Name != "stripe" {
		t.Fatalf("stripe sink picked up the wrong lines: %v", *bLines)
	}
}
