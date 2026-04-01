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

type overlapTask struct {
	name  string
	delay time.Duration

	running    atomic.Int32
	maxRunning atomic.Int32
	calls      atomic.Int32
}

func (t *overlapTask) Name() string { return t.name }

func (t *overlapTask) Run(ctx context.Context) (int64, error) {
	t.calls.Add(1)
	running := t.running.Add(1)
	for {
		max := t.maxRunning.Load()
		if running <= max {
			break
		}
		if t.maxRunning.CompareAndSwap(max, running) {
			break
		}
	}

	select {
	case <-time.After(t.delay):
	case <-ctx.Done():
		t.running.Add(-1)
		return 0, ctx.Err()
	}

	t.running.Add(-1)
	return 0, nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(nopWriter{}, nil))
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }

// ---------- tests ----------

func TestNew_defaults(t *testing.T) {
	j := New()
	assert.Equal(t, 30*time.Second, j.timeout)
	assert.Empty(t, j.tasks)
}

func TestAddTask_chaining(t *testing.T) {
	a := &stubTask{name: "a"}
	b := &stubTask{name: "b"}

	j := New()
	ret := j.AddTask("@every 1h", a).AddTask("@every 1h", b)

	assert.Same(t, j, ret, "AddTask must return the same Janitor for chaining")
	require.Len(t, j.tasks, 2)
	assert.Equal(t, "a", j.tasks[0].task.Name())
	assert.Equal(t, "b", j.tasks[1].task.Name())
}

func TestStart_invalidTimeout(t *testing.T) {
	j := New(WithTimeout(0), WithLogger(discardLogger()))
	err := j.Start(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timeout must be positive")
}

func TestStart_invalidSchedule(t *testing.T) {
	task := &stubTask{name: "bad-sched"}
	j := New(WithLogger(discardLogger())).AddTask("not-a-cron", task)

	err := j.Start(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid schedule")
}

func TestStart_alreadyStarted(t *testing.T) {
	task := &stubTask{name: "once"}
	j := New(WithLogger(discardLogger())).AddTask("@every 1s", task)

	require.NoError(t, j.Start(context.Background()))
	defer j.Stop()

	err := j.Start(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already started")
}

func TestStart_tickExecution(t *testing.T) {
	task := &stubTask{name: "tick", affected: 1}
	j := New(
		WithLogger(discardLogger()),
	).AddTask("@every 1s", task)

	err := j.Start(context.Background())
	require.NoError(t, err)
	defer j.Stop()

	assert.Eventually(t, func() bool {
		return task.callCount() >= 2
	}, 5*time.Second, 100*time.Millisecond)
}

func TestStop_idempotent(t *testing.T) {
	j := New(WithLogger(discardLogger()))
	require.NoError(t, j.Start(context.Background()))

	assert.NotPanics(t, func() {
		j.Stop()
		j.Stop()
	})
}

func TestTask_timeout(t *testing.T) {
	task := &stubTask{name: "slow", delay: 5 * time.Second}
	j := New(
		WithTimeout(50*time.Millisecond),
		WithLogger(discardLogger()),
	).AddTask("@every 1s", task)

	err := j.Start(context.Background())
	require.NoError(t, err)
	defer j.Stop()

	assert.Eventually(t, func() bool {
		return task.callCount() >= 1
	}, 3*time.Second, 100*time.Millisecond)

	task.mu.Lock()
	ctx := task.lastCtx
	task.mu.Unlock()

	// Wait for the timeout to fire.
	time.Sleep(100 * time.Millisecond)
	assert.Error(t, ctx.Err())
}

func TestTask_noOverlapForSameSchedule(t *testing.T) {
	task := &overlapTask{name: "no-overlap", delay: 1500 * time.Millisecond}
	j := New(
		WithLogger(discardLogger()),
	).AddTask("@every 1s", task)

	require.NoError(t, j.Start(context.Background()))
	defer j.Stop()

	assert.Eventually(t, func() bool {
		return task.calls.Load() >= 2
	}, 6*time.Second, 100*time.Millisecond)
	assert.Equal(t, int32(1), task.maxRunning.Load())
}

func TestTask_errorDoesNotStopOthers(t *testing.T) {
	bad := &stubTask{name: "bad", err: errors.New("boom")}
	good := &stubTask{name: "good", affected: 42}

	j := New(
		WithLogger(discardLogger()),
	).AddTask("@every 1s", bad).AddTask("@every 1s", good)

	require.NoError(t, j.Start(context.Background()))
	defer j.Stop()

	assert.Eventually(t, func() bool {
		return bad.callCount() >= 1 && good.callCount() >= 1
	}, 5*time.Second, 100*time.Millisecond)
}

func TestPreRun_called(t *testing.T) {
	task := &stubTask{name: "pr", affected: 3}
	var got atomic.Value

	j := New(
		WithLogger(discardLogger()),
		WithPreRun(func(_ context.Context, name string) error {
			got.Store(name)
			return nil
		}),
	).AddTask("@every 1s", task)

	require.NoError(t, j.Start(context.Background()))
	defer j.Stop()

	assert.Eventually(t, func() bool {
		return got.Load() == "pr"
	}, 3*time.Second, 100*time.Millisecond)
}

func TestPreRun_errorSkipsTask(t *testing.T) {
	skipped := &stubTask{name: "skipped", affected: 1}

	j := New(
		WithLogger(discardLogger()),
		WithPreRun(func(_ context.Context, name string) error {
			if name == "skipped" {
				return errors.New("nope")
			}
			return nil
		}),
	).AddTask("@every 1s", skipped)

	require.NoError(t, j.Start(context.Background()))
	defer j.Stop()

	// Give enough time for the cron to fire.
	time.Sleep(2 * time.Second)

	assert.Equal(t, 0, skipped.callCount(), "pre-run error should skip the task")
}

func TestPostRun_called(t *testing.T) {
	taskErr := errors.New("task-err")
	task := &stubTask{name: "post", affected: 7, err: taskErr}

	var (
		gotName     atomic.Value
		gotAffected atomic.Int64
		gotErr      atomic.Value
	)

	j := New(
		WithLogger(discardLogger()),
		WithPostRun(func(_ context.Context, name string, affected int64, err error) {
			gotName.Store(name)
			gotAffected.Store(affected)
			gotErr.Store(err)
		}),
	).AddTask("@every 1s", task)

	require.NoError(t, j.Start(context.Background()))
	defer j.Stop()

	assert.Eventually(t, func() bool {
		return gotName.Load() == "post"
	}, 3*time.Second, 100*time.Millisecond)

	assert.Equal(t, int64(7), gotAffected.Load())
	assert.Equal(t, taskErr, gotErr.Load())
}

func TestPreTick_called(t *testing.T) {
	task := &stubTask{name: "pt", affected: 1}
	var called atomic.Bool

	j := New(
		WithLogger(discardLogger()),
		WithPreTick(func(_ context.Context) error {
			called.Store(true)
			return nil
		}),
	).AddTask("@every 1s", task)

	require.NoError(t, j.Start(context.Background()))
	defer j.Stop()

	assert.Eventually(t, func() bool {
		return called.Load() && task.callCount() >= 1
	}, 3*time.Second, 100*time.Millisecond)
}

func TestPreTick_errorSkipsExecution(t *testing.T) {
	task := &stubTask{name: "pt-skip", affected: 1}

	j := New(
		WithLogger(discardLogger()),
		WithPreTick(func(_ context.Context) error {
			return errors.New("skip tick")
		}),
	).AddTask("@every 1s", task)

	require.NoError(t, j.Start(context.Background()))
	defer j.Stop()

	time.Sleep(2 * time.Second)
	assert.Equal(t, 0, task.callCount(), "pre-tick error should skip the task")
}

func TestPostTick_called(t *testing.T) {
	task := &stubTask{name: "ptt", affected: 1}
	var called atomic.Bool

	j := New(
		WithLogger(discardLogger()),
		WithPostTick(func(_ context.Context) {
			called.Store(true)
		}),
	).AddTask("@every 1s", task)

	require.NoError(t, j.Start(context.Background()))
	defer j.Stop()

	assert.Eventually(t, called.Load, 3*time.Second, 100*time.Millisecond)
}

func TestRunImmediately_firesOnStart(t *testing.T) {
	task := &stubTask{name: "imm", affected: 5}
	j := New(
		WithLogger(discardLogger()),
		WithRunImmediately(),
		// Use a very long cron interval so the only execution comes from the
		// immediate run, not the scheduler.
	).AddTask("0 0 1 1 *", task)

	require.NoError(t, j.Start(context.Background()))
	defer j.Stop()

	assert.Eventually(t, func() bool {
		return task.callCount() >= 1
	}, 3*time.Second, 50*time.Millisecond)
}

func TestRunImmediately_hooksStillApply(t *testing.T) {
	task := &stubTask{name: "imm-hook", affected: 2}

	var (
		preRan  atomic.Bool
		postRan atomic.Bool
	)

	j := New(
		WithLogger(discardLogger()),
		WithRunImmediately(),
		WithPreRun(func(_ context.Context, name string) error {
			preRan.Store(true)
			return nil
		}),
		WithPostRun(func(_ context.Context, name string, affected int64, err error) {
			postRan.Store(true)
		}),
	).AddTask("0 0 1 1 *", task)

	require.NoError(t, j.Start(context.Background()))
	defer j.Stop()

	assert.Eventually(t, func() bool {
		return preRan.Load() && postRan.Load() && task.callCount() >= 1
	}, 3*time.Second, 50*time.Millisecond)
}

func TestRunImmediately_multipleTasks(t *testing.T) {
	a := &stubTask{name: "imm-a", affected: 1}
	b := &stubTask{name: "imm-b", affected: 2}

	j := New(
		WithLogger(discardLogger()),
		WithRunImmediately(),
	).AddTask("0 0 1 1 *", a).AddTask("0 0 1 1 *", b)

	require.NoError(t, j.Start(context.Background()))
	defer j.Stop()

	assert.Eventually(t, func() bool {
		return a.callCount() >= 1 && b.callCount() >= 1
	}, 3*time.Second, 50*time.Millisecond)
}

func TestRunImmediately_preTickCalled(t *testing.T) {
	task := &stubTask{name: "imm-pt", affected: 1}

	var (
		preTicked atomic.Bool
		postTicked atomic.Bool
	)

	j := New(
		WithLogger(discardLogger()),
		WithRunImmediately(),
		WithPreTick(func(_ context.Context) error {
			preTicked.Store(true)
			return nil
		}),
		WithPostTick(func(_ context.Context) {
			postTicked.Store(true)
		}),
	).AddTask("0 0 1 1 *", task)

	require.NoError(t, j.Start(context.Background()))
	defer j.Stop()

	assert.Eventually(t, func() bool {
		return preTicked.Load() && postTicked.Load() && task.callCount() >= 1
	}, 3*time.Second, 50*time.Millisecond)
}

func TestRunImmediately_preTickErrorSkipsTask(t *testing.T) {
	task := &stubTask{name: "imm-pt-skip", affected: 1}

	j := New(
		WithLogger(discardLogger()),
		WithRunImmediately(),
		WithPreTick(func(_ context.Context) error {
			return errors.New("block it")
		}),
	).AddTask("0 0 1 1 *", task)

	require.NoError(t, j.Start(context.Background()))
	defer j.Stop()

	time.Sleep(500 * time.Millisecond)
	assert.Equal(t, 0, task.callCount(), "pre-tick error should skip immediate task")
}

func TestWithoutRunImmediately_doesNotFireOnStart(t *testing.T) {
	task := &stubTask{name: "no-imm", affected: 1}
	j := New(
		WithLogger(discardLogger()),
		// Distant cron schedule, no WithRunImmediately.
	).AddTask("0 0 1 1 *", task)

	require.NoError(t, j.Start(context.Background()))
	defer j.Stop()

	time.Sleep(500 * time.Millisecond)
	assert.Equal(t, 0, task.callCount(), "task should not run without WithRunImmediately")
}

func TestContextCancellation(t *testing.T) {
	task := &stubTask{name: "ctx-cancel", affected: 1}
	ctx, cancel := context.WithCancel(context.Background())

	j := New(
		WithLogger(discardLogger()),
	).AddTask("@every 1s", task)

	require.NoError(t, j.Start(ctx))

	assert.Eventually(t, func() bool {
		return task.callCount() >= 1
	}, 3*time.Second, 100*time.Millisecond)

	cancel()
	time.Sleep(500 * time.Millisecond)

	countAfterCancel := task.callCount()
	time.Sleep(2 * time.Second)
	assert.Equal(t, countAfterCancel, task.callCount(),
		"no more executions should happen after context cancellation")
}
