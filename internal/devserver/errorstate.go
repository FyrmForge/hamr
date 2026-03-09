package devserver

import "sync"

// ErrorState tracks active build/process errors in a thread-safe manner.
// The proxy reads it to decide whether to serve the error page; devserver
// writes it on build failure / success.
type ErrorState struct {
	mu     sync.RWMutex
	errors map[string]string // rule name → output
}

// NewErrorState creates an empty ErrorState.
func NewErrorState() *ErrorState {
	return &ErrorState{errors: make(map[string]string)}
}

// Set records a build/process error for the given rule.
func (e *ErrorState) Set(rule, output string) {
	e.mu.Lock()
	e.errors[rule] = output
	e.mu.Unlock()
}

// Clear removes the error for the given rule.
func (e *ErrorState) Clear(rule string) {
	e.mu.Lock()
	delete(e.errors, rule)
	e.mu.Unlock()
}

// HasErrors returns true if any rule has an active error.
func (e *ErrorState) HasErrors() bool {
	e.mu.RLock()
	has := len(e.errors) > 0
	e.mu.RUnlock()
	return has
}

// Snapshot returns a copy of the current errors map.
func (e *ErrorState) Snapshot() map[string]string {
	e.mu.RLock()
	cp := make(map[string]string, len(e.errors))
	for k, v := range e.errors {
		cp[k] = v
	}
	e.mu.RUnlock()
	return cp
}
