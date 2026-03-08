// Package templint provides a static linter for .templ files.
//
// It checks for common issues such as inline control flow (which templ silently
// drops), missing accessibility attributes, and style anti-patterns.
package templint

import "fmt"

// Severity indicates the severity level of a diagnostic.
type Severity int

const (
	// Warning indicates a non-critical issue.
	Warning Severity = iota
	// Error indicates a critical issue.
	Error
)

// String returns the string representation of a Severity.
func (s Severity) String() string {
	switch s {
	case Warning:
		return "warning"
	case Error:
		return "error"
	default:
		return "unknown"
	}
}

// ParseSeverity converts a string to a Severity.
func ParseSeverity(s string) (Severity, error) {
	switch s {
	case "warning":
		return Warning, nil
	case "error":
		return Error, nil
	default:
		return Warning, fmt.Errorf("unknown severity: %q", s)
	}
}

// Diagnostic represents a single lint finding.
type Diagnostic struct {
	File     string
	Line     int
	Col      int
	Rule     string
	Severity Severity
	Message  string
}

// String returns a formatted diagnostic string.
func (d Diagnostic) String() string {
	return fmt.Sprintf("%s:%d:%d: %s[%s] %s", d.File, d.Line, d.Col, d.Severity, d.Rule, d.Message)
}

// Rule is the interface that all lint rules must implement.
type Rule interface {
	ID() string
	Check(filename string, lines []string) []Diagnostic
}
