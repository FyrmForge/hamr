package async_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FyrmForge/hamr/pkg/async"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// All
// ---------------------------------------------------------------------------

func TestAll_happyPath(t *testing.T) {
	// Each goroutine writes to its own variable. This is safe because
	// All calls wg.Wait before returning, providing happens-before.
	var a, b, c int
	err := async.All(context.Background(),
		func(ctx context.Context) error { a = 1; return nil },
		func(ctx context.Context) error { b = 2; return nil },
		func(ctx context.Context) error { c = 3; return nil },
	)
	require.NoError(t, err)
	assert.Equal(t, 1, a)
	assert.Equal(t, 2, b)
	assert.Equal(t, 3, c)
}

func TestAll_empty(t *testing.T) {
	err := async.All(context.Background())
	assert.NoError(t, err)
}

func TestAll_firstErrorCancels(t *testing.T) {
	sentinel := errors.New("boom")
	var cancelled atomic.Bool

	err := async.All(context.Background(),
		func(ctx context.Context) error {
			return sentinel
		},
		func(ctx context.Context) error {
			<-ctx.Done()
			cancelled.Store(true)
			return ctx.Err()
		},
	)
	require.Error(t, err)
	// All goroutines have finished by the time All returns (wg.Wait),
	// so the cancelled flag is already set — no sleep needed.
	assert.True(t, cancelled.Load(), "second fn should observe cancelled context")
}

func TestAll_errorPassthrough(t *testing.T) {
	sentinel := errors.New("sentinel")
	err := async.All(context.Background(),
		func(ctx context.Context) error { return sentinel },
	)
	assert.True(t, errors.Is(err, sentinel), "error should pass through unwrapped")
}

func TestAll_panicRecovery(t *testing.T) {
	err := async.All(context.Background(),
		func(ctx context.Context) error { panic("kaboom") },
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "panic")
}

func TestAll_parentContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := async.All(ctx,
		func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
	)
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled))
}

func TestAll_mixedTypes(t *testing.T) {
	var (
		name    string
		age     int
		balance float64
	)
	err := async.All(context.Background(),
		func(ctx context.Context) error { name = "alice"; return nil },
		func(ctx context.Context) error { age = 30; return nil },
		func(ctx context.Context) error { balance = 99.50; return nil },
	)
	require.NoError(t, err)
	assert.Equal(t, "alice", name)
	assert.Equal(t, 30, age)
	assert.Equal(t, 99.50, balance)
}

// ---------------------------------------------------------------------------
// Settle
// ---------------------------------------------------------------------------

func TestSettle_allSucceed(t *testing.T) {
	var a, b string
	errs := async.Settle(context.Background(),
		func(ctx context.Context) error { a = "x"; return nil },
		func(ctx context.Context) error { b = "y"; return nil },
	)
	require.Len(t, errs, 2)
	assert.NoError(t, errs[0])
	assert.NoError(t, errs[1])
	assert.Equal(t, "x", a)
	assert.Equal(t, "y", b)
}

func TestSettle_mixedResults(t *testing.T) {
	sentinel := errors.New("fail")
	var ok1, ok2 bool
	errs := async.Settle(context.Background(),
		func(ctx context.Context) error { ok1 = true; return nil },
		func(ctx context.Context) error { return sentinel },
		func(ctx context.Context) error { ok2 = true; return nil },
	)
	require.Len(t, errs, 3)
	assert.NoError(t, errs[0])
	assert.True(t, errors.Is(errs[1], sentinel))
	assert.NoError(t, errs[2])
	assert.True(t, ok1)
	assert.True(t, ok2)
}

func TestSettle_noShortCircuit(t *testing.T) {
	var counter atomic.Int32
	errs := async.Settle(context.Background(),
		func(ctx context.Context) error {
			counter.Add(1)
			return errors.New("err")
		},
		func(ctx context.Context) error {
			counter.Add(1)
			return nil
		},
		func(ctx context.Context) error {
			counter.Add(1)
			return nil
		},
	)
	require.Len(t, errs, 3)
	assert.Equal(t, int32(3), counter.Load(), "all fns should run despite error")
}

func TestSettle_panicRecovery(t *testing.T) {
	var ok bool
	errs := async.Settle(context.Background(),
		func(ctx context.Context) error { panic("oops") },
		func(ctx context.Context) error { ok = true; return nil },
	)
	require.Len(t, errs, 2)
	require.Error(t, errs[0])
	assert.Contains(t, errs[0].Error(), "panic")
	assert.NoError(t, errs[1])
	assert.True(t, ok)
}

func TestSettle_empty(t *testing.T) {
	errs := async.Settle(context.Background())
	assert.Equal(t, []error{}, errs)
}

// ---------------------------------------------------------------------------
// Map
// ---------------------------------------------------------------------------

func TestMap_happyPath(t *testing.T) {
	got, err := async.Map(context.Background(), []int{1, 2, 3},
		func(ctx context.Context, v int) (int, error) { return v * 2, nil },
	)
	require.NoError(t, err)
	assert.Equal(t, []int{2, 4, 6}, got)
}

func TestMap_empty(t *testing.T) {
	got, err := async.Map(context.Background(), []int{},
		func(ctx context.Context, v int) (int, error) { return v, nil },
	)
	require.NoError(t, err)
	assert.Equal(t, []int{}, got)
}

func TestMap_firstError(t *testing.T) {
	sentinel := errors.New("bad")
	got, err := async.Map(context.Background(), []int{1, 2, 3},
		func(ctx context.Context, v int) (int, error) {
			if v == 2 {
				return 0, sentinel
			}
			<-ctx.Done()
			return 0, ctx.Err()
		},
	)
	assert.Nil(t, got)
	assert.True(t, errors.Is(err, sentinel))
}

// ---------------------------------------------------------------------------
// Fire
// ---------------------------------------------------------------------------

func TestFire_runs(t *testing.T) {
	var done atomic.Bool
	async.Fire(func() { done.Store(true) })
	assert.Eventually(t, done.Load, time.Second, 5*time.Millisecond)
}

func TestFire_panicDoesNotCrash(t *testing.T) {
	async.Fire(func() { panic("fire-panic") })
	// If we get here, the panic was recovered.
	time.Sleep(20 * time.Millisecond)
}

// ---------------------------------------------------------------------------
// Group
// ---------------------------------------------------------------------------

func TestGroup_lifecycle(t *testing.T) {
	var counter atomic.Int32
	g := async.NewGroup()
	for range 5 {
		g.Go(func() { counter.Add(1) })
	}
	g.Close()
	assert.Equal(t, int32(5), counter.Load())
}

func TestGroup_panicRecovery(t *testing.T) {
	g := async.NewGroup()
	g.Go(func() { panic("group-panic") })
	g.Close() // must not deadlock
}

func TestGroup_goAfterClose(t *testing.T) {
	var ran atomic.Bool
	g := async.NewGroup()
	g.Close()
	g.Go(func() { ran.Store(true) })
	time.Sleep(20 * time.Millisecond)
	assert.False(t, ran.Load(), "fn should not run after Close")
}

func TestGroup_concurrentGo(t *testing.T) {
	var counter atomic.Int32
	g := async.NewGroup()

	var (
		starter sync.WaitGroup
		called  sync.WaitGroup
	)
	starter.Add(20)
	called.Add(20)
	for range 20 {
		go func() {
			starter.Done()
			starter.Wait()
			g.Go(func() { counter.Add(1) })
			called.Done()
		}()
	}
	called.Wait() // all Go calls have returned
	g.Close()
	assert.Equal(t, int32(20), counter.Load())
}

func TestGroup_withLimit(t *testing.T) {
	var (
		running    atomic.Int32
		maxRunning atomic.Int32
	)

	g := async.NewGroup(async.WithLimit(2))
	for range 5 {
		g.Go(func() {
			cur := running.Add(1)
			// Update max running — simple CAS loop.
			for {
				old := maxRunning.Load()
				if cur <= old || maxRunning.CompareAndSwap(old, cur) {
					break
				}
			}
			time.Sleep(30 * time.Millisecond)
			running.Add(-1)
		})
	}
	g.Close()
	assert.LessOrEqual(t, maxRunning.Load(), int32(2), "concurrent goroutines should not exceed limit")
}
