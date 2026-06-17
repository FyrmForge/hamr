package devserver

import (
	"maps"
	"sort"
	"sync"
)

// ErrorState tracks active build/process errors in a thread-safe manner.
// The proxy reads it to decide whether to serve the error page; devserver
// writes it on build failure / success.
type ErrorState struct {
	mu       sync.RWMutex
	errors   map[string]string // rule name → output
	onChange func()
}

// NewErrorState creates an empty ErrorState.
func NewErrorState() *ErrorState {
	return &ErrorState{errors: make(map[string]string)}
}

// OnChange registers a callback that fires after Set or Clear modifies state.
func (e *ErrorState) OnChange(fn func()) {
	e.mu.Lock()
	e.onChange = fn
	e.mu.Unlock()
}

// Set records a build/process error for the given rule.
func (e *ErrorState) Set(rule, output string) {
	e.mu.Lock()
	e.errors[rule] = output
	fn := e.onChange
	e.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// Clear removes the error for the given rule.
func (e *ErrorState) Clear(rule string) {
	e.mu.Lock()
	delete(e.errors, rule)
	fn := e.onChange
	e.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// RuleNames returns the sorted names of rules with active errors.
func (e *ErrorState) RuleNames() []string {
	e.mu.RLock()
	names := make([]string, 0, len(e.errors))
	for k := range e.errors {
		names = append(names, k)
	}
	e.mu.RUnlock()
	sort.Strings(names)
	return names
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
	maps.Copy(cp, e.errors)
	e.mu.RUnlock()
	return cp
}
