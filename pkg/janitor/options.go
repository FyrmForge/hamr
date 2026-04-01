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

// WithPreTick appends a pre-execution hook. Multiple hooks run in order;
// the first error skips the execution.
func WithPreTick(fn PreTickFunc) Option {
	return func(j *Janitor) { j.preTick = append(j.preTick, fn) }
}

// WithPostTick appends a post-execution hook. Multiple hooks run in order.
func WithPostTick(fn PostTickFunc) Option {
	return func(j *Janitor) { j.postTick = append(j.postTick, fn) }
}

// WithRunImmediately causes all registered tasks to execute once as soon as
// Start is called, in addition to their regular cron schedules.
func WithRunImmediately() Option {
	return func(j *Janitor) { j.runImmediately = true }
}
