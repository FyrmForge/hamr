package devserver

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGraph_WaitForDeps_NoDeps(t *testing.T) {
	g := NewGraph([]WatchRule{
		{Name: "a"},
	})

	ctx := context.Background()
	err := g.WaitForDeps(ctx, "a")
	require.NoError(t, err)
}

func TestGraph_WaitForDeps_WithDeps(t *testing.T) {
	g := NewGraph([]WatchRule{
		{Name: "templ"},
		{Name: "go", Depends: StringOrSlice{"templ"}},
	})

	// Initially both are "done" (channels closed at construction).
	ctx := context.Background()
	err := g.WaitForDeps(ctx, "go")
	require.NoError(t, err)

	// Mark templ as running — go should now block.
	g.MarkRunning("templ")

	ctx2, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err = g.WaitForDeps(ctx2, "go")
	assert.ErrorIs(t, err, context.DeadlineExceeded)

	// Mark templ done — go should unblock.
	g.MarkDone("templ")
	err = g.WaitForDeps(context.Background(), "go")
	require.NoError(t, err)
}

func TestGraph_WaitForDeps_MultipleDeps(t *testing.T) {
	g := NewGraph([]WatchRule{
		{Name: "templ"},
		{Name: "css"},
		{Name: "go", Depends: StringOrSlice{"templ", "css"}},
	})

	g.MarkRunning("templ")
	g.MarkRunning("css")

	// go should block because both deps are running.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := g.WaitForDeps(ctx, "go")
	assert.ErrorIs(t, err, context.DeadlineExceeded)

	// Completing only one dep is not enough.
	g.MarkDone("templ")
	ctx2, cancel2 := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel2()
	err = g.WaitForDeps(ctx2, "go")
	assert.ErrorIs(t, err, context.DeadlineExceeded)

	// Completing both deps unblocks.
	g.MarkDone("css")
	err = g.WaitForDeps(context.Background(), "go")
	require.NoError(t, err)
}

func TestGraph_WaitForDeps_MultiLevelChain(t *testing.T) {
	g := NewGraph([]WatchRule{
		{Name: "a"},
		{Name: "b", Depends: StringOrSlice{"a"}},
		{Name: "c", Depends: StringOrSlice{"b"}},
	})

	g.MarkRunning("a")
	g.MarkRunning("b")

	// c depends on b, which depends on a.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := g.WaitForDeps(ctx, "c")
	assert.ErrorIs(t, err, context.DeadlineExceeded)

	// a done — b can proceed, but c still blocked on b.
	g.MarkDone("a")
	ctx2, cancel2 := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel2()
	err = g.WaitForDeps(ctx2, "c")
	assert.ErrorIs(t, err, context.DeadlineExceeded)

	// b done — c unblocked.
	g.MarkDone("b")
	err = g.WaitForDeps(context.Background(), "c")
	require.NoError(t, err)
}

func TestGraph_MarkDone_Idempotent(t *testing.T) {
	g := NewGraph([]WatchRule{{Name: "a"}})
	g.MarkRunning("a")
	g.MarkDone("a")
	g.MarkDone("a") // should not panic
}

func TestGraph_MarkRunning_Nonexistent(t *testing.T) {
	g := NewGraph([]WatchRule{{Name: "a"}})
	g.MarkRunning("nonexistent") // should not panic
}

func TestGraph_MarkDone_Nonexistent(t *testing.T) {
	g := NewGraph([]WatchRule{{Name: "a"}})
	g.MarkDone("nonexistent") // should not panic
}

func TestGraph_UnknownRule(t *testing.T) {
	g := NewGraph([]WatchRule{{Name: "a"}})
	err := g.WaitForDeps(context.Background(), "nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown rule")
}

func TestGraph_TopologicalOrder(t *testing.T) {
	g := NewGraph([]WatchRule{
		{Name: "go", Depends: StringOrSlice{"templ"}},
		{Name: "templ"},
		{Name: "css"},
	})

	order := g.TopologicalOrder()
	require.Len(t, order, 3)

	// templ must come before go.
	templIdx, goIdx := -1, -1
	for i, name := range order {
		if name == "templ" {
			templIdx = i
		}
		if name == "go" {
			goIdx = i
		}
	}
	assert.Less(t, templIdx, goIdx, "templ should come before go")
}

func TestGraph_TopologicalOrder_Diamond(t *testing.T) {
	g := NewGraph([]WatchRule{
		{Name: "a"},
		{Name: "b", Depends: StringOrSlice{"a"}},
		{Name: "c", Depends: StringOrSlice{"a"}},
		{Name: "d", Depends: StringOrSlice{"b", "c"}},
	})

	order := g.TopologicalOrder()
	require.Len(t, order, 4)

	idx := make(map[string]int, 4)
	for i, name := range order {
		idx[name] = i
	}

	assert.Less(t, idx["a"], idx["b"])
	assert.Less(t, idx["a"], idx["c"])
	assert.Less(t, idx["b"], idx["d"])
	assert.Less(t, idx["c"], idx["d"])
}

func TestGraph_TopologicalOrder_NoDeps(t *testing.T) {
	g := NewGraph([]WatchRule{
		{Name: "a"},
		{Name: "b"},
	})

	order := g.TopologicalOrder()
	require.Len(t, order, 2)
}

func TestGraph_ConcurrentWait(t *testing.T) {
	g := NewGraph([]WatchRule{
		{Name: "templ"},
		{Name: "go", Depends: StringOrSlice{"templ"}},
		{Name: "css", Depends: StringOrSlice{"templ"}},
	})

	g.MarkRunning("templ")

	done := make(chan string, 2)

	go func() {
		_ = g.WaitForDeps(context.Background(), "go")
		done <- "go"
	}()
	go func() {
		_ = g.WaitForDeps(context.Background(), "css")
		done <- "css"
	}()

	// Neither should complete yet.
	select {
	case name := <-done:
		t.Fatalf("unexpected completion of %s before templ done", name)
	case <-time.After(50 * time.Millisecond):
	}

	g.MarkDone("templ")

	// Both should complete quickly.
	completed := make(map[string]bool)
	for i := 0; i < 2; i++ {
		select {
		case name := <-done:
			completed[name] = true
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for rule to complete")
		}
	}
	assert.True(t, completed["go"], "go should have completed")
	assert.True(t, completed["css"], "css should have completed")
}

func TestGraph_MarkRunning_ResetsForNewCycle(t *testing.T) {
	g := NewGraph([]WatchRule{
		{Name: "templ"},
		{Name: "go", Depends: StringOrSlice{"templ"}},
	})

	// First cycle: mark running then done.
	g.MarkRunning("templ")
	g.MarkDone("templ")
	err := g.WaitForDeps(context.Background(), "go")
	require.NoError(t, err)

	// Second cycle: mark running again — go should block again.
	g.MarkRunning("templ")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err = g.WaitForDeps(ctx, "go")
	assert.ErrorIs(t, err, context.DeadlineExceeded)

	g.MarkDone("templ")
	err = g.WaitForDeps(context.Background(), "go")
	require.NoError(t, err)
}

func TestGraph_MarkRunning_WhileAlreadyRunning_DoesNotOrphanWaiters(t *testing.T) {
	g := NewGraph([]WatchRule{
		{Name: "templ"},
		{Name: "go", Depends: StringOrSlice{"templ"}},
	})

	g.MarkRunning("templ")

	done := make(chan error, 1)
	go func() {
		done <- g.WaitForDeps(context.Background(), "go")
	}()

	// Calling MarkRunning again while already running should not replace the
	// channel that existing waiters are blocked on.
	g.MarkRunning("templ")
	g.MarkDone("templ")

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("waiter was orphaned by repeated MarkRunning call")
	}
}
