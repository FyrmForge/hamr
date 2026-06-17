package sync

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingStore is a FileStorage that records Save'd keys and contents.
type recordingStore struct {
	saved map[string][]byte
}

func (s *recordingStore) Save(_ context.Context, path string, r io.Reader) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.saved[path] = b
	return nil
}
func (s *recordingStore) Open(context.Context, string) (io.ReadCloser, error)  { return nil, nil }
func (s *recordingStore) Delete(context.Context, string) error                 { return nil }
func (s *recordingStore) Exists(context.Context, string) (bool, error)         { return false, nil }
func (s *recordingStore) List(context.Context, string) ([]string, error)       { return nil, nil }

// TestSyncExistingFiles covers the case fsnotify misses: a directory that
// appears with files already inside it. All contained files must be synced,
// keyed relative to the watch root, with .gitkeep skipped.
func TestSyncExistingFiles(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "images", "icons")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "a.png"), []byte("AAA"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "b.png"), []byte("BBB"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(sub, ".gitkeep"), []byte(""), 0o644))

	store := &recordingStore{saved: map[string][]byte{}}
	syncExistingFiles(context.Background(), store, root, filepath.Join(root, "images"))

	keys := make([]string, 0, len(store.saved))
	for k := range store.saved {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	assert.Equal(t, []string{"images/icons/a.png", "images/icons/b.png"}, keys)
	assert.Equal(t, []byte("AAA"), store.saved["images/icons/a.png"])
}
