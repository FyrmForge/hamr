package janitor

import (
	"log/slog"
	"time"
)

// Option configures a Janitor.
type Option func(*Janitor)

// WithTimeout sets the per-task context timeout (default 30s).
func WithTimeout(d time.Duration) Option {
	return func(j *Janitor) { j.timeout = d }
}

// WithRunImmediately runs all tasks once synchronously on Start before the
// first ticker tick.
func WithRunImmediately(run bool) Option {
	return func(j *Janitor) { j.runImmediately = run }
}

// WithLogger sets the structured logger used for task execution logging.
// A nil logger is resolved to slog.Default() at Start time.
func WithLogger(l *slog.Logger) Option {
	return func(j *Janitor) { j.logger = l }
}

// WithPreRun appends a per-task pre-run hook. Multiple hooks run in order;
// the first error skips the task.
func WithPreRun(fn PreRunFunc) Option {
	return func(j *Janitor) { j.preRun = append(j.preRun, fn) }
}

// WithPostRun appends a per-task post-run hook. Multiple hooks run in order.
func WithPostRun(fn PostRunFunc) Option {
	return func(j *Janitor) { j.postRun = append(j.postRun, fn) }
}

// WithPreTick appends a per-tick pre-tick hook. Multiple hooks run in order;
// the first error skips the entire tick.
func WithPreTick(fn PreTickFunc) Option {
	return func(j *Janitor) { j.preTick = append(j.preTick, fn) }
}

// WithPostTick appends a per-tick post-tick hook. Multiple hooks run in order.
func WithPostTick(fn PostTickFunc) Option {
	return func(j *Janitor) { j.postTick = append(j.postTick, fn) }
}
