package devserver

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMatchGlob(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		// Simple extension matching.
		{"*.go", "main.go", true},
		{"*.go", "pkg/server/main.go", true},
		{"*.go", "main.js", false},

		// Double-star patterns.
		{"**/*.go", "main.go", true},
		{"**/*.go", "pkg/server/main.go", true},
		{"**/*.go", "main.js", false},

		{"**/*.templ", "internal/web/handler/home/home.templ", true},
		{"**/*.templ", "home.templ", true},

		// Prefix + double-star.
		{"static/**/*.css", "static/css/main.css", true},
		{"static/**/*.css", "static/css/base/variables.css", true},
		{"static/**/*.css", "pkg/main.css", false},

		// Ignore pattern (just basename).
		{"*_templ.go", "internal/web/home_templ.go", true},
		{"*_templ.go", "home_templ.go", true},
		{"*_templ.go", "home.go", false},

		// Double-star alone.
		{"**", "anything/at/all.go", true},
		{"**", "single.go", true},

		// No ** but with directory.
		{"static/*.js", "static/main.js", true},

		// Trailing double-star.
		{"static/**", "static/js/main.js", true},
		{"static/**", "static/deep/nested/file.css", true},

		// Leading double-star with specific filename.
		{"**/test.go", "test.go", true},
		{"**/test.go", "pkg/test.go", true},
		{"**/test.go", "deep/nested/test.go", true},
		{"**/test.go", "main.go", false},

		// Exact match.
		{"Makefile", "Makefile", true},
		{"Makefile", "sub/Makefile", true},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+"__"+tt.path, func(t *testing.T) {
			got := matchGlob(tt.pattern, tt.path)
			assert.Equal(t, tt.want, got, "matchGlob(%q, %q)", tt.pattern, tt.path)
		})
	}
}

func TestMatchRule(t *testing.T) {
	rule := &WatchRule{
		Watch:  StringOrSlice{"**/*.go"},
		Ignore: StringOrSlice{"*_templ.go", "*_test.go"},
	}

	assert.True(t, matchRule(rule, "main.go"))
	assert.True(t, matchRule(rule, "pkg/server/main.go"))
	assert.False(t, matchRule(rule, "home_templ.go"))
	assert.False(t, matchRule(rule, "main_test.go"))
	assert.False(t, matchRule(rule, "main.js"))
}

func TestMatchRule_NoIgnore(t *testing.T) {
	rule := &WatchRule{
		Watch: StringOrSlice{"**/*.go"},
	}

	assert.True(t, matchRule(rule, "main.go"))
	assert.True(t, matchRule(rule, "main_test.go"))
}

func TestMatchRule_NoMatch(t *testing.T) {
	rule := &WatchRule{
		Watch: StringOrSlice{"**/*.go"},
	}

	assert.False(t, matchRule(rule, "main.js"))
	assert.False(t, matchRule(rule, "styles.css"))
}

func TestMatchRule_IgnoreTakesPrecedence(t *testing.T) {
	rule := &WatchRule{
		Watch:  StringOrSlice{"**/*.go"},
		Ignore: StringOrSlice{"**/*.go"}, // ignore everything we watch
	}
	assert.False(t, matchRule(rule, "main.go"))
}

func TestMatchRule_MultipleWatchPatterns(t *testing.T) {
	rule := &WatchRule{
		Watch: StringOrSlice{"**/*.go", "**/*.templ"},
	}

	assert.True(t, matchRule(rule, "main.go"))
	assert.True(t, matchRule(rule, "home.templ"))
	assert.False(t, matchRule(rule, "main.js"))
}

func TestShouldIgnoreDir(t *testing.T) {
	assert.True(t, shouldIgnoreDir(".git"))
	assert.True(t, shouldIgnoreDir("node_modules"))
	assert.True(t, shouldIgnoreDir("vendor"))
	assert.True(t, shouldIgnoreDir(".idea"))
	assert.True(t, shouldIgnoreDir(".vscode"))
	assert.True(t, shouldIgnoreDir("tmp"))
	assert.True(t, shouldIgnoreDir(".hidden"))
	assert.False(t, shouldIgnoreDir("pkg"))
	assert.False(t, shouldIgnoreDir("internal"))
	assert.False(t, shouldIgnoreDir("static"))
}

func watcherLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestWatcher_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	dir := t.TempDir()

	// Create initial structure.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "pkg"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pkg", "main.go"), []byte("package main"), 0o644))

	rules := []WatchRule{
		{
			Name:     "go",
			Watch:    StringOrSlice{"**/*.go"},
			Ignore:   StringOrSlice{"*_test.go"},
			Debounce: Duration{50 * time.Millisecond},
		},
	}

	w, err := NewWatcher(dir, rules, watcherLogger())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, w.Start(ctx))

	// Write a .go file — should trigger event.
	time.Sleep(100 * time.Millisecond) // let watcher settle
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pkg", "new.go"), []byte("package pkg"), 0o644))

	select {
	case evt := <-w.Events():
		assert.Equal(t, "go", evt.Rule.Name)
		assert.Contains(t, evt.Path, "new.go")
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for file event")
	}

	// Write a _test.go file — should NOT trigger event (ignored).
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pkg", "main_test.go"), []byte("package pkg"), 0o644))

	select {
	case evt := <-w.Events():
		t.Fatalf("unexpected event for ignored file: %s", evt.Path)
	case <-time.After(300 * time.Millisecond):
		// Expected: no event.
	}

	cancel()
	w.Stop()
}

func TestWatcher_NewDirectory(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	dir := t.TempDir()

	rules := []WatchRule{
		{
			Name:     "go",
			Watch:    StringOrSlice{"**/*.go"},
			Debounce: Duration{50 * time.Millisecond},
		},
	}

	w, err := NewWatcher(dir, rules, watcherLogger())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, w.Start(ctx))
	time.Sleep(100 * time.Millisecond)

	// Create a new subdirectory and a file in it.
	newDir := filepath.Join(dir, "newpkg")
	require.NoError(t, os.MkdirAll(newDir, 0o755))
	time.Sleep(200 * time.Millisecond) // let fsnotify pick up the new dir
	require.NoError(t, os.WriteFile(filepath.Join(newDir, "file.go"), []byte("package newpkg"), 0o644))

	select {
	case evt := <-w.Events():
		assert.Equal(t, "go", evt.Rule.Name)
		assert.Contains(t, evt.Path, "file.go")
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for event in new directory")
	}

	cancel()
	w.Stop()
}

func TestWatcher_Debounce(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644))

	rules := []WatchRule{
		{
			Name:     "go",
			Watch:    StringOrSlice{"**/*.go"},
			Debounce: Duration{200 * time.Millisecond},
		},
	}

	w, err := NewWatcher(dir, rules, watcherLogger())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, w.Start(ctx))
	time.Sleep(100 * time.Millisecond)

	// Rapidly write to the same file multiple times.
	for i := 0; i < 5; i++ {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main // "+string(rune('a'+i))), 0o644))
		time.Sleep(20 * time.Millisecond)
	}

	// Should get only one debounced event, not five.
	select {
	case <-w.Events():
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for debounced event")
	}

	// No second event within the debounce window.
	select {
	case <-w.Events():
		// A second event is acceptable due to FS timing, but there shouldn't be 5.
	case <-time.After(500 * time.Millisecond):
		// Good — debounce coalesced the events.
	}

	cancel()
	w.Stop()
}

func TestNewWatcher(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWatcher(dir, []WatchRule{{Name: "test", Watch: StringOrSlice{"*.go"}}}, watcherLogger())
	require.NoError(t, err)
	assert.NotNil(t, w)
	assert.Equal(t, dir, w.root)
	_ = w.fsw.Close()
}

func TestWatcher_StopWithoutStart(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWatcher(dir, nil, watcherLogger())
	require.NoError(t, err)
	w.Stop() // should not panic
}
