package janitor

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------- test helpers ----------

// stubTask is a minimal Task implementation for testing.
type stubTask struct {
	name     string
	affected int64
	err      error
	delay    time.Duration

	mu      sync.Mutex
	calls   int
	lastCtx context.Context
}

func (s *stubTask) Name() string { return s.name }

func (s *stubTask) Run(ctx context.Context) (int64, error) {
	s.mu.Lock()
	s.calls++
	s.lastCtx = ctx
	s.mu.Unlock()

	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	return s.affected, s.err
}

func (s *stubTask) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(nopWriter{}, nil))
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }

// ---------- tests ----------

func TestNew_defaults(t *testing.T) {
	j := New(1 * time.Hour)
	assert.Equal(t, 30*time.Second, j.timeout)
	assert.Empty(t, j.tasks)
	assert.NotNil(t, j.done)
}

func TestAddTask_chaining(t *testing.T) {
	a := &stubTask{name: "a"}
	b := &stubTask{name: "b"}

	j := New(1 * time.Hour)
	ret := j.AddTask(a).AddTask(b)

	assert.Same(t, j, ret, "AddTask must return the same Janitor for chaining")
	require.Len(t, j.tasks, 2)
	assert.Equal(t, "a", j.tasks[0].Name())
	assert.Equal(t, "b", j.tasks[1].Name())
}

func TestStart_invalidInterval(t *testing.T) {
	j := New(0, WithLogger(discardLogger()))
	err := j.Start(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "interval must be positive")
}

func TestStart_invalidTimeout(t *testing.T) {
	j := New(1*time.Hour, WithTimeout(0), WithLogger(discardLogger()))
	err := j.Start(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timeout must be positive")
}

func TestStart_runImmediately(t *testing.T) {
	task := &stubTask{name: "imm", affected: 5}
	j := New(1*time.Hour,
		WithRunImmediately(true),
		WithLogger(discardLogger()),
	).AddTask(task)

	err := j.Start(context.Background())
	require.NoError(t, err)
	defer j.Stop()

	// runImmediately runs synchronously before Start returns.
	assert.Equal(t, 1, task.callCount())
}

func TestStart_tickExecution(t *testing.T) {
	task := &stubTask{name: "tick", affected: 1}
	j := New(50*time.Millisecond,
		WithLogger(discardLogger()),
	).AddTask(task)

	err := j.Start(context.Background())
	require.NoError(t, err)
	defer j.Stop()

	assert.Eventually(t, func() bool {
		return task.callCount() >= 2
	}, 500*time.Millisecond, 10*time.Millisecond)
}

func TestStop_idempotent(t *testing.T) {
	j := New(1*time.Hour, WithLogger(discardLogger()))
	require.NoError(t, j.Start(context.Background()))

	assert.NotPanics(t, func() {
		j.Stop()
		j.Stop()
	})
}

func TestTask_timeout(t *testing.T) {
	task := &stubTask{name: "slow", delay: 5 * time.Second}
	j := New(1*time.Hour,
		WithTimeout(50*time.Millisecond),
		WithRunImmediately(true),
		WithLogger(discardLogger()),
	).AddTask(task)

	err := j.Start(context.Background())
	require.NoError(t, err)
	defer j.Stop()

	assert.Equal(t, 1, task.callCount())
	task.mu.Lock()
	ctx := task.lastCtx
	task.mu.Unlock()

	// The context should have been cancelled by now.
	assert.Error(t, ctx.Err())
}

func TestTask_errorDoesNotStopOthers(t *testing.T) {
	bad := &stubTask{name: "bad", err: errors.New("boom")}
	good := &stubTask{name: "good", affected: 42}

	j := New(1*time.Hour,
		WithRunImmediately(true),
		WithLogger(discardLogger()),
	).AddTask(bad).AddTask(good)

	require.NoError(t, j.Start(context.Background()))
	defer j.Stop()

	assert.Equal(t, 1, bad.callCount())
	assert.Equal(t, 1, good.callCount())
}

func TestPreRun_called(t *testing.T) {
	task := &stubTask{name: "pr", affected: 3}
	var got atomic.Value

	j := New(1*time.Hour,
		WithRunImmediately(true),
		WithLogger(discardLogger()),
		WithPreRun(func(_ context.Context, name string) error {
			got.Store(name)
			return nil
		}),
	).AddTask(task)

	require.NoError(t, j.Start(context.Background()))
	defer j.Stop()

	assert.Equal(t, "pr", got.Load())
}

func TestPreRun_errorSkipsTask(t *testing.T) {
	skipped := &stubTask{name: "skipped", affected: 1}
	notSkipped := &stubTask{name: "kept", affected: 1}

	j := New(1*time.Hour,
		WithRunImmediately(true),
		WithLogger(discardLogger()),
		WithPreRun(func(_ context.Context, name string) error {
			if name == "skipped" {
				return errors.New("nope")
			}
			return nil
		}),
	).AddTask(skipped).AddTask(notSkipped)

	require.NoError(t, j.Start(context.Background()))
	defer j.Stop()

	assert.Equal(t, 0, skipped.callCount(), "pre-run error should skip the task")
	assert.Equal(t, 1, notSkipped.callCount(), "other tasks should still run")
}

func TestPostRun_called(t *testing.T) {
	taskErr := errors.New("task-err")
	task := &stubTask{name: "post", affected: 7, err: taskErr}

	var (
		gotName     atomic.Value
		gotAffected atomic.Int64
		gotErr      atomic.Value
	)

	j := New(1*time.Hour,
		WithRunImmediately(true),
		WithLogger(discardLogger()),
		WithPostRun(func(_ context.Context, name string, affected int64, err error) {
			gotName.Store(name)
			gotAffected.Store(affected)
			gotErr.Store(err)
		}),
	).AddTask(task)

	require.NoError(t, j.Start(context.Background()))
	defer j.Stop()

	assert.Equal(t, "post", gotName.Load())
	assert.Equal(t, int64(7), gotAffected.Load())
	assert.Equal(t, taskErr, gotErr.Load())
}

func TestPreTick_called(t *testing.T) {
	task := &stubTask{name: "pt", affected: 1}
	var called atomic.Bool

	j := New(1*time.Hour,
		WithRunImmediately(true),
		WithLogger(discardLogger()),
		WithPreTick(func(_ context.Context) error {
			called.Store(true)
			return nil
		}),
	).AddTask(task)

	require.NoError(t, j.Start(context.Background()))
	defer j.Stop()

	assert.True(t, called.Load())
	assert.Equal(t, 1, task.callCount())
}

func TestPreTick_errorSkipsTick(t *testing.T) {
	task := &stubTask{name: "pt-skip", affected: 1}

	j := New(1*time.Hour,
		WithRunImmediately(true),
		WithLogger(discardLogger()),
		WithPreTick(func(_ context.Context) error {
			return errors.New("skip tick")
		}),
	).AddTask(task)

	require.NoError(t, j.Start(context.Background()))
	defer j.Stop()

	assert.Equal(t, 0, task.callCount(), "pre-tick error should skip the entire tick")
}

func TestPostTick_called(t *testing.T) {
	task := &stubTask{name: "ptt", affected: 1}
	var called atomic.Bool

	j := New(1*time.Hour,
		WithRunImmediately(true),
		WithLogger(discardLogger()),
		WithPostTick(func(_ context.Context) {
			called.Store(true)
		}),
	).AddTask(task)

	require.NoError(t, j.Start(context.Background()))
	defer j.Stop()

	assert.True(t, called.Load())
}
