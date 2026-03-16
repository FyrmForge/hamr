package devserver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRollingFileWriter_BasicWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.log")
	w, err := newRollingFileWriter(path, 100)
	if err != nil {
		t.Fatal(err)
	}

	_, _ = fmt.Fprintln(w, "hello world")
	_, _ = fmt.Fprintln(w, "second line")
	_ = w.Close()

	data, _ := os.ReadFile(path)
	lines := nonEmptyLines(string(data))
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), string(data))
	}
	if lines[0] != "hello world" {
		t.Errorf("line 0 = %q, want %q", lines[0], "hello world")
	}
	if lines[1] != "second line" {
		t.Errorf("line 1 = %q, want %q", lines[1], "second line")
	}
}

func TestRollingFileWriter_StripANSI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.log")
	w, err := newRollingFileWriter(path, 100)
	if err != nil {
		t.Fatal(err)
	}

	// Write colored output like prefixWriter produces.
	_, _ = fmt.Fprintf(w, "\033[36m[templ]\033[0m built OK\n")
	_, _ = fmt.Fprintf(w, "\033[1;36m[hamr dev]\033[0m \033[33mWARN\033[0m something\n")
	_ = w.Close()

	data, _ := os.ReadFile(path)
	lines := nonEmptyLines(string(data))
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	if lines[0] != "[templ] built OK" {
		t.Errorf("line 0 = %q, want %q", lines[0], "[templ] built OK")
	}
	if lines[1] != "[hamr dev] WARN something" {
		t.Errorf("line 1 = %q, want %q", lines[1], "[hamr dev] WARN something")
	}
	if strings.Contains(string(data), "\033") {
		t.Error("file still contains ANSI escape sequences")
	}
}

func TestRollingFileWriter_StripOSCSequences(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.log")
	w, err := newRollingFileWriter(path, 100)
	if err != nil {
		t.Fatal(err)
	}

	_, _ = fmt.Fprintf(w, "\033]8;;https://example.com\aopen docs\033]8;;\a\n")
	_ = w.Close()

	data, _ := os.ReadFile(path)
	lines := nonEmptyLines(string(data))
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if lines[0] != "open docs" {
		t.Errorf("line = %q, want %q", lines[0], "open docs")
	}
	if strings.Contains(string(data), "\033]8;") {
		t.Error("file still contains OSC hyperlink escape sequences")
	}
}

func TestRollingFileWriter_RollingTruncation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.log")
	maxLines := 10
	w, err := newRollingFileWriter(path, maxLines)
	if err != nil {
		t.Fatal(err)
	}

	// Write 25 lines — should trigger truncation at 2*maxLines (20).
	for i := 0; i < 25; i++ {
		_, _ = fmt.Fprintf(w, "line %d\n", i)
	}
	_ = w.Close()

	data, _ := os.ReadFile(path)
	lines := nonEmptyLines(string(data))
	if len(lines) != maxLines {
		t.Fatalf("expected %d lines after truncation, got %d", maxLines, len(lines))
	}
	// The last line should be "line 24".
	if lines[len(lines)-1] != "line 24" {
		t.Errorf("last line = %q, want %q", lines[len(lines)-1], "line 24")
	}
	// The first line should be "line 15" (25 - 10 = 15).
	if lines[0] != "line 15" {
		t.Errorf("first line = %q, want %q", lines[0], "line 15")
	}
}

func TestRollingFileWriter_PartialLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.log")
	w, err := newRollingFileWriter(path, 100)
	if err != nil {
		t.Fatal(err)
	}

	// Write a line in two parts.
	_, _ = w.Write([]byte("hello "))
	_, _ = w.Write([]byte("world\n"))
	_ = w.Close()

	data, _ := os.ReadFile(path)
	lines := nonEmptyLines(string(data))
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if lines[0] != "hello world" {
		t.Errorf("line = %q, want %q", lines[0], "hello world")
	}
}

func TestRollingFileWriter_FlushPartialOnClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.log")
	w, err := newRollingFileWriter(path, 100)
	if err != nil {
		t.Fatal(err)
	}

	// Write without trailing newline.
	_, _ = w.Write([]byte("no newline"))
	_ = w.Close()

	data, _ := os.ReadFile(path)
	lines := nonEmptyLines(string(data))
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if lines[0] != "no newline" {
		t.Errorf("line = %q, want %q", lines[0], "no newline")
	}
}

func TestRollingFileWriter_CRLFNormalization(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.log")
	w, err := newRollingFileWriter(path, 100)
	if err != nil {
		t.Fatal(err)
	}

	_, _ = w.Write([]byte("line one\r\n"))
	_, _ = w.Write([]byte("line two\r\n"))
	_ = w.Close()

	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "\r") {
		t.Error("file contains \\r characters")
	}
	lines := nonEmptyLines(string(data))
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
}

func TestRollingFileWriter_PreExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.log")

	// Create a file with 5 existing lines.
	var existing strings.Builder
	for i := 0; i < 5; i++ {
		_, _ = fmt.Fprintf(&existing, "old %d\n", i)
	}
	_ = os.WriteFile(path, []byte(existing.String()), 0o644)

	w, err := newRollingFileWriter(path, 8)
	if err != nil {
		t.Fatal(err)
	}

	// Write 5 more lines (total 10, maxLines=8).
	for i := 0; i < 5; i++ {
		_, _ = fmt.Fprintf(w, "new %d\n", i)
	}
	_ = w.Close()

	data, _ := os.ReadFile(path)
	lines := nonEmptyLines(string(data))
	if len(lines) != 8 {
		t.Fatalf("expected 8 lines, got %d: %v", len(lines), lines)
	}
	// Should have kept the last 8 of 10 total lines.
	if lines[0] != "old 2" {
		t.Errorf("first line = %q, want %q", lines[0], "old 2")
	}
	if lines[len(lines)-1] != "new 4" {
		t.Errorf("last line = %q, want %q", lines[len(lines)-1], "new 4")
	}
}

func TestRollingFileWriter_PreExistingOverLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.log")

	// Create a file with 15 lines, maxLines=5 — should truncate on open.
	var existing strings.Builder
	for i := 0; i < 15; i++ {
		_, _ = fmt.Fprintf(&existing, "line %d\n", i)
	}
	_ = os.WriteFile(path, []byte(existing.String()), 0o644)

	w, err := newRollingFileWriter(path, 5)
	if err != nil {
		t.Fatal(err)
	}
	_ = w.Close()

	data, _ := os.ReadFile(path)
	lines := nonEmptyLines(string(data))
	if len(lines) != 5 {
		t.Fatalf("expected 5 lines after open truncation, got %d", len(lines))
	}
	if lines[0] != "line 10" {
		t.Errorf("first line = %q, want %q", lines[0], "line 10")
	}
}

func TestRollingFileWriter_CreateParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "test.log")
	w, err := newRollingFileWriter(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fmt.Fprintln(w, "hello")
	_ = w.Close()

	data, _ := os.ReadFile(path)
	lines := nonEmptyLines(string(data))
	if len(lines) != 1 || lines[0] != "hello" {
		t.Errorf("unexpected content: %q", string(data))
	}
}

func TestRollingFileWriter_InvalidMaxLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.log")
	for _, maxLines := range []int{0, -1} {
		t.Run(fmt.Sprintf("%d", maxLines), func(t *testing.T) {
			_, err := newRollingFileWriter(path, maxLines)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), "greater than 0") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// nonEmptyLines splits text on newlines and discards empty trailing entries.
func nonEmptyLines(s string) []string {
	all := strings.Split(strings.TrimRight(s, "\n"), "\n")
	var out []string
	for _, l := range all {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}
