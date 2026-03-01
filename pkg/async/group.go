package async

import (
	"fmt"
	"log/slog"
	"sync"
)

// Fire spawns a goroutine with panic recovery. Panics logged to slog.Default().
func Fire(fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Default().Error("async.Fire: panic recovered", "panic", fmt.Sprint(r))
			}
		}()
		fn()
	}()
}

// GroupOption configures a Group.
type GroupOption func(*Group)

// WithGroupLogger sets the logger for panic recovery output.
func WithGroupLogger(l *slog.Logger) GroupOption {
	return func(g *Group) { g.logger = l }
}

// WithLimit caps concurrent goroutines via a semaphore.
func WithLimit(n int) GroupOption {
	return func(g *Group) { g.sem = make(chan struct{}, n) }
}

// Group manages fire-and-forget goroutines with panic recovery.
type Group struct {
	wg     sync.WaitGroup
	mu     sync.Mutex
	closed bool
	logger *slog.Logger
	sem    chan struct{} // nil when unlimited
}

// NewGroup creates a Group with the given options.
func NewGroup(opts ...GroupOption) *Group {
	g := &Group{logger: slog.Default()}
	for _, o := range opts {
		o(g)
	}
	return g
}

// Go spawns work; blocks if at limit; no-op after Close.
func (g *Group) Go(fn func()) {
	// Acquire semaphore before the mutex so we don't hold the lock while blocking.
	if g.sem != nil {
		g.sem <- struct{}{}
	}

	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		if g.sem != nil {
			<-g.sem
		}
		return
	}
	g.wg.Add(1)
	g.mu.Unlock()

	go func() {
		defer g.wg.Done()
		defer func() {
			if g.sem != nil {
				<-g.sem
			}
		}()
		defer func() {
			if r := recover(); r != nil {
				g.logger.Error("async.Group: panic recovered", "panic", fmt.Sprint(r))
			}
		}()
		fn()
	}()
}

// Close waits for all in-flight goroutines to finish.
func (g *Group) Close() {
	g.mu.Lock()
	g.closed = true
	g.mu.Unlock()
	g.wg.Wait()
}
