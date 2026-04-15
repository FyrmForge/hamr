package server

import (
	"io"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLayeredFS_primaryTakesPriority(t *testing.T) {
	primary := fstest.MapFS{
		"css/app.css": &fstest.MapFile{Data: []byte("primary")},
	}
	secondary := fstest.MapFS{
		"css/app.css": &fstest.MapFile{Data: []byte("secondary")},
	}

	lfs := newLayeredFS(primary, secondary)

	f, err := lfs.Open("css/app.css")
	require.NoError(t, err)
	defer f.Close() //nolint:errcheck

	data, err := io.ReadAll(f)
	require.NoError(t, err)
	assert.Equal(t, "primary", string(data))
}

func TestLayeredFS_fallsBackToSecondary(t *testing.T) {
	primary := fstest.MapFS{}
	secondary := fstest.MapFS{
		"js/main.js": &fstest.MapFile{Data: []byte("fallback")},
	}

	lfs := newLayeredFS(primary, secondary)

	f, err := lfs.Open("js/main.js")
	require.NoError(t, err)
	defer f.Close() //nolint:errcheck

	data, err := io.ReadAll(f)
	require.NoError(t, err)
	assert.Equal(t, "fallback", string(data))
}

func TestLayeredFS_notFoundInEither(t *testing.T) {
	lfs := newLayeredFS(fstest.MapFS{}, fstest.MapFS{})

	_, err := lfs.Open("nope.css")
	assert.Error(t, err)
}
