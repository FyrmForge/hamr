// Package janitor provides a periodic background task runner with a chainable
// API, per-task timeouts, and pre/post hooks at both per-task and per-tick
// levels.
package janitor

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
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

// PreTickFunc is called before any tasks in a tick. Returning an error skips
// the entire tick.
type PreTickFunc func(ctx context.Context) error

// PostTickFunc is called after all tasks in a tick have run.
type PostTickFunc func(ctx context.Context)

// Janitor runs a set of Tasks on a fixed interval.
type Janitor struct {
	interval       time.Duration
	timeout        time.Duration
	runImmediately bool
	logger         *slog.Logger

	tasks []Task

	preRun   []PreRunFunc
	postRun  []PostRunFunc
	preTick  []PreTickFunc
	postTick []PostTickFunc

	done chan struct{}
	stop sync.Once
}

// New creates a Janitor that ticks at the given interval. Options configure
// timeout, logger, hooks, and other behaviour. The returned pointer supports
// method chaining via AddTask.
func New(interval time.Duration, opts ...Option) *Janitor {
	j := &Janitor{
		interval: interval,
		timeout:  30 * time.Second,
		done:     make(chan struct{}),
	}
	for _, o := range opts {
		o(j)
	}
	return j
}

// AddTask appends a task and returns the Janitor for chaining.
func (j *Janitor) AddTask(task Task) *Janitor {
	j.tasks = append(j.tasks, task)
	return j
}

// Start validates configuration, optionally runs tasks immediately, and spawns
// the background ticker goroutine. It returns an error if the configuration is
// invalid.
func (j *Janitor) Start() error {
	if j.interval <= 0 {
		return fmt.Errorf("janitor: interval must be positive, got %v", j.interval)
	}
	if j.timeout <= 0 {
		return fmt.Errorf("janitor: timeout must be positive, got %v", j.timeout)
	}
	if j.logger == nil {
		j.logger = slog.Default()
	}

	if j.runImmediately {
		j.runTick()
	}

	go func() {
		ticker := time.NewTicker(j.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				j.runTick()
			case <-j.done:
				return
			}
		}
	}()

	return nil
}

// Stop signals the background goroutine to exit. It is safe to call multiple
// times.
func (j *Janitor) Stop() {
	j.stop.Do(func() { close(j.done) })
}

// runTick executes one full tick cycle: preTick hooks, all tasks, postTick hooks.
func (j *Janitor) runTick() {
	ctx := context.Background()

	for _, fn := range j.preTick {
		if err := fn(ctx); err != nil {
			j.logger.Error("janitor: pre-tick hook failed, skipping tick", "error", err)
			return
		}
	}

	for _, t := range j.tasks {
		j.runTask(ctx, t)
	}

	for _, fn := range j.postTick {
		fn(ctx)
	}
}

// runTask executes a single task with its pre/post hooks and timeout.
func (j *Janitor) runTask(parent context.Context, t Task) {
	name := t.Name()

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
}
