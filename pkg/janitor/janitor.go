// Package janitor provides a cron-based background task runner with per-task
// schedules, timeouts, and pre/post hooks.
package janitor

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

// Task is a single unit of maintenance work executed by the Janitor.
type Task interface {
	// Name returns a human-readable identifier used for logging and hooks.
	Name() string
	// Run performs the work and returns the number of affected rows/items and
	// any error. The context carries the per-task timeout.
	Run(ctx context.Context) (int64, error)
}

// PreRunFunc is called before each task. Returning an error skips that task.
type PreRunFunc func(ctx context.Context, taskName string) error

// PostRunFunc is called after each task with its results.
type PostRunFunc func(ctx context.Context, taskName string, affected int64, taskErr error)

// PreTickFunc is called before a scheduled task execution. Returning an error
// skips that execution.
type PreTickFunc func(ctx context.Context) error

// PostTickFunc is called after a scheduled task execution completes.
type PostTickFunc func(ctx context.Context)

// scheduledTask pairs a cron expression with a Task.
type scheduledTask struct {
	schedule string
	task     Task
}

// Janitor runs Tasks on individual cron schedules.
type Janitor struct {
	mu sync.Mutex

	ctx     context.Context
	cancel  context.CancelFunc
	timeout time.Duration
	logger  *slog.Logger

	runImmediately bool

	tasks []scheduledTask

	preRun   []PreRunFunc
	postRun  []PostRunFunc
	preTick  []PreTickFunc
	postTick []PostTickFunc

	cron    *cron.Cron
	wg      sync.WaitGroup  // tracks immediate-run goroutines (cron tracks scheduled)
	running map[string]bool // task name → in-flight; guards immediate vs scheduled overlap
}

// New creates a Janitor. Options configure timeout, logger, and hooks.
func New(opts ...Option) *Janitor {
	j := &Janitor{
		timeout: 30 * time.Second,
		running: make(map[string]bool),
	}
	for _, o := range opts {
		o(j)
	}
	return j
}

// AddTask registers a task with a cron schedule expression. Supported formats
// include standard cron ("0 0 * * *") and robfig/cron descriptors
// ("@every 5m", "@daily", "@hourly").
func (j *Janitor) AddTask(schedule string, task Task) *Janitor {
	j.tasks = append(j.tasks, scheduledTask{schedule: schedule, task: task})
	return j
}

// Start validates configuration, registers all tasks with the cron scheduler,
// and starts it. The provided context controls the lifetime — cancelling it
// stops the scheduler.
func (j *Janitor) Start(ctx context.Context) error {
	if j.timeout <= 0 {
		return fmt.Errorf("janitor: timeout must be positive, got %v", j.timeout)
	}
	if j.logger == nil {
		j.logger = slog.Default()
	}

	j.mu.Lock()
	if j.cron != nil {
		j.mu.Unlock()
		return fmt.Errorf("janitor: already started")
	}
	j.ctx, j.cancel = context.WithCancel(ctx)
	// Prevent overlapping runs of the same task when schedules are shorter
	// than execution time.
	j.cron = cron.New(cron.WithChain(
		cron.SkipIfStillRunning(cron.DiscardLogger),
	))
	cronRef := j.cron
	ctxRef := j.ctx
	j.mu.Unlock()

	for _, st := range j.tasks {
		_, err := cronRef.AddFunc(st.schedule, func() {
			j.runTask(ctxRef, st.task)
		})
		if err != nil {
			j.Stop()
			return fmt.Errorf("janitor: invalid schedule %q for task %q: %w",
				st.schedule, st.task.Name(), err)
		}
	}

	cronRef.Start()

	if j.runImmediately {
		for _, st := range j.tasks {
			j.wg.Go(func() {
				j.runTask(ctxRef, st.task)
			})
		}
	}

	go func() {
		<-ctxRef.Done()
		cronRef.Stop()

		j.mu.Lock()
		if j.cron == cronRef {
			j.ctx = nil
			j.cancel = nil
			j.cron = nil
		}
		j.mu.Unlock()
	}()

	return nil
}

// Stop stops the cron scheduler and waits for in-flight tasks to return, so a
// caller can safely tear down shared resources (DB connections, etc.) once Stop
// returns. It is safe to call multiple times. Tasks observe the cancelled
// context, so well-behaved tasks return promptly.
func (j *Janitor) Stop() {
	j.mu.Lock()
	cancel := j.cancel
	cronRef := j.cron
	j.ctx = nil
	j.cancel = nil
	j.cron = nil
	j.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if cronRef != nil {
		// cron.Stop() returns a context that is Done once all in-flight
		// scheduled jobs have returned — wait on it instead of discarding it.
		<-cronRef.Stop().Done()
	}
	// Immediate-run goroutines bypass cron; drain them too.
	j.wg.Wait()
}

// acquireRun marks a task as running, returning false if it already is.
func (j *Janitor) acquireRun(name string) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.running == nil {
		j.running = make(map[string]bool)
	}
	if j.running[name] {
		return false
	}
	j.running[name] = true
	return true
}

// releaseRun clears a task's running marker.
func (j *Janitor) releaseRun(name string) {
	j.mu.Lock()
	delete(j.running, name)
	j.mu.Unlock()
}

// runTask executes a single task with pre/post hooks and timeout.
func (j *Janitor) runTask(parent context.Context, t Task) {
	name := t.Name()

	// A panicking task must not crash the process. runTask is the single
	// chokepoint for both scheduled runs (cron AddFunc) and immediate runs
	// (the WithRunImmediately goroutines bypass the cron chain, so a
	// chain-level cron.Recover would not cover them). On panic we log and
	// return; pre/post hooks for this run are skipped.
	defer func() {
		if r := recover(); r != nil {
			j.logger.Error("janitor: task panicked",
				"task", name, "panic", r, "stack", string(debug.Stack()))
		}
	}()

	// Skip if this task is already running. cron's SkipIfStillRunning only
	// guards scheduled-vs-scheduled; this also covers a WithRunImmediately run
	// overlapping the first scheduled tick (and vice versa).
	if !j.acquireRun(name) {
		return
	}
	defer j.releaseRun(name)

	for _, fn := range j.preTick {
		if err := fn(parent); err != nil {
			j.logger.Error("janitor: pre-tick hook failed, skipping task",
				"task", name, "error", err)
			return
		}
	}

	for _, fn := range j.preRun {
		if err := fn(parent, name); err != nil {
			j.logger.Error("janitor: pre-run hook failed, skipping task",
				"task", name, "error", err)
			return
		}
	}

	ctx, cancel := context.WithTimeout(parent, j.timeout)
	defer cancel()

	start := time.Now()
	affected, err := t.Run(ctx)
	elapsed := time.Since(start)

	if err != nil {
		j.logger.Error("janitor: task failed",
			"task", name, "affected", affected, "duration", elapsed, "error", err)
	} else {
		j.logger.Info("janitor: task completed",
			"task", name, "affected", affected, "duration", elapsed)
	}

	for _, fn := range j.postRun {
		fn(parent, name, affected, err)
	}

	for _, fn := range j.postTick {
		fn(parent)
	}
}
