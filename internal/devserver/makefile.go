package devserver

import (
	"bufio"
	"errors"
	"io/fs"
	"os"
	"strings"
)

// MakefileTargetsFromPath reads the Makefile at path and returns its
// declared target names in the order they appear. Pattern rules
// (containing '%'), variables (lines with ':=' / '+=' / '?='), comments,
// and recipe lines (starting with a tab) are ignored. The function does
// not parse includes or expand variables — the dev TUI's run overlay
// just needs the human-visible target list, not full make semantics.
//
// Returns ([]string{}, nil) when the file does not exist so callers can
// gate UI on a non-error empty list.
func MakefileTargetsFromPath(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var targets []string
	seen := make(map[string]struct{})
	sc := bufio.NewScanner(f)
	// Allow long lines (default 64KiB max token is plenty in practice but
	// some Makefiles inline big lists).
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		// Recipe lines start with a tab — never targets.
		if strings.HasPrefix(line, "\t") {
			continue
		}
		// Strip inline comments (anything from an unescaped '#' onwards).
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Variable assignments — `:=`, `::=`, `+=`, `?=`, plain `=`.
		// Detect by checking for the assignment operator before the first
		// ':' if any. The cheap test: if the line contains '=' and the
		// '=' appears before the first ':' (or there's no ':'), treat as
		// variable. Also catch `:=` explicitly.
		if isAssignment(line) {
			continue
		}
		colon := strings.Index(line, ":")
		if colon <= 0 {
			continue
		}
		head := strings.TrimSpace(line[:colon])
		if head == "" {
			continue
		}
		// Skip pattern rules and special targets like .PHONY (we still
		// want documented phony targets, but `.PHONY:` itself is not a
		// runnable target).
		if strings.ContainsAny(head, "%$") {
			continue
		}
		// A target line may declare multiple space-separated targets:
		//   build clean: deps
		for _, name := range strings.Fields(head) {
			if strings.HasPrefix(name, ".") {
				// Special targets (.PHONY, .DEFAULT_GOAL, .SUFFIXES, ...)
				continue
			}
			if _, dup := seen[name]; dup {
				continue
			}
			seen[name] = struct{}{}
			targets = append(targets, name)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return targets, nil
}

// isAssignment reports whether line is a make variable assignment rather
// than a target declaration. A target's ':' always precedes any '=', so
// if '=' appears first (or no ':' exists alongside '='), it's a var.
func isAssignment(line string) bool {
	eq := strings.IndexAny(line, "=")
	if eq < 0 {
		return false
	}
	// `:=`, `::=`, `?=`, `+=`, `!=` all end in '=' with a sigil before.
	// Look at the byte immediately before '=' for any of those sigils.
	if eq > 0 {
		switch line[eq-1] {
		case ':', '?', '+', '!':
			return true
		}
	}
	colon := strings.Index(line, ":")
	if colon < 0 || eq < colon {
		return true
	}
	return false
}
