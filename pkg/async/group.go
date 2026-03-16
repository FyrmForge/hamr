package async

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"
)

// Fire spawns a goroutine with panic recovery. Panics logged to slog.Default().
func Fire(fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Default().Error("async.Fire: panic recovered", "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
			}
		}()
		fn()
	}()
}

// GroupMetrics receives lifecycle callbacks from [Group.Go].
//
// Every Dispatched call is followed by exactly one Completed or Panicked call.
// Blocked fires at most once per Dispatched, immediately before it.
// No callbacks fire for calls to Go after Close (true no-op).
//
// WARNING: callbacks run synchronously on the Group hot path and during worker
// teardown. They are not isolated in a separate goroutine. Implementations
// must be safe for concurrent use, must not block, must not perform slow I/O,
// and must not panic. A bad implementation can stall Go(), delay or deadlock
// Close(), and hold semaphore slots longer than expected.
type GroupMetrics interface {
	Blocked()                         // semaphore contention occurred (fired after the wait completes)
	Dispatched(blocked time.Duration) // job accepted; worker goroutine about to spawn (blocked=0 if no wait)
	Completed(duration time.Duration) // job finished without panic recovery
	Panicked(duration time.Duration)  // job finished via panic recovery
}

type noopMetrics struct{}

func (noopMetrics) Blocked()                 {}
func (noopMetrics) Dispatched(time.Duration) {}
func (noopMetrics) Completed(time.Duration)  {}
func (noopMetrics) Panicked(time.Duration)   {}

// GroupOption configures a Group.
type GroupOption func(*Group)

// WithGroupLogger sets the logger for panic recovery output.
func WithGroupLogger(l *slog.Logger) GroupOption {
	return func(g *Group) { g.logger = l }
}

// WithMetrics sets the metrics observer for Group lifecycle events.
// See [GroupMetrics] for the synchronous callback warning and constraints.
func WithMetrics(m GroupMetrics) GroupOption {
	return func(g *Group) {
		if m == nil {
			return
		}
		g.metrics = m
	}
}

// WithLimit caps concurrent goroutines via a semaphore.
// Values less than 1 are clamped to 1.
func WithLimit(n int) GroupOption {
	if n < 1 {
		n = 1
	}
	return func(g *Group) { g.sem = semaphore.NewWeighted(int64(n)) }
}

// Group manages fire-and-forget goroutines with panic recovery.
type Group struct {
	wg      sync.WaitGroup
	mu      sync.Mutex
	closed  bool
	logger  *slog.Logger
	sem     *semaphore.Weighted // nil when unlimited
	metrics GroupMetrics
}

// NewGroup creates a Group with the given options.
func NewGroup(opts ...GroupOption) *Group {
	g := &Group{logger: slog.Default(), metrics: noopMetrics{}}
	for _, o := range opts {
		o(g)
	}
	return g
}

func (g *Group) loggerOrDefault() *slog.Logger {
	if g.logger != nil {
		return g.logger
	}
	return slog.Default()
}

func (g *Group) metricsOrNoop() GroupMetrics {
	if g.metrics != nil {
		return g.metrics
	}
	return noopMetrics{}
}

// Go spawns work; blocks if at limit; no-op after Close.
func (g *Group) Go(fn func()) {
	// Acquire semaphore before the mutex so we don't hold the lock while blocking.
	var blockedDur time.Duration
	var wasBlocked bool
	if g.sem != nil {
		if !g.sem.TryAcquire(1) {
			wasBlocked = true
			blockStart := time.Now()
			_ = g.sem.Acquire(context.Background(), 1) // never fails with background context
			blockedDur = time.Since(blockStart)
		}
	}

	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		if g.sem != nil {
			g.sem.Release(1)
		}
		return
	}
	logger := g.loggerOrDefault()
	metrics := g.metricsOrNoop()
	g.wg.Add(1)
	g.mu.Unlock()

	// Metrics fire only for accepted jobs — never for post-Close drops.
	if wasBlocked {
		metrics.Blocked()
	}
	metrics.Dispatched(blockedDur)

	go func() {
		defer g.wg.Done()
		defer func() {
			if g.sem != nil {
				g.sem.Release(1)
			}
		}()

		start := time.Now()
		defer func() {
			dur := time.Since(start)
			if r := recover(); r != nil {
				logger.Error("async.Group: panic recovered", "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
				metrics.Panicked(dur)
				return
			}
			metrics.Completed(dur)
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
