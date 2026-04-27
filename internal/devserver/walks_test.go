package devserver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteWalks_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	records := []portShiftRecord{
		{Kind: "proxy", Old: 3000, New: 3001},
		{Kind: "app", Old: 8080, New: 8081},
		{Kind: "compose", ComposeName: "deps", Service: "postgres", HostIP: "127.0.0.1", Old: 5432, New: 5433},
	}
	require.NoError(t, writeWalks(dir, records))

	got, err := readWalks(dir)
	require.NoError(t, err)
	assert.Equal(t, records, got)
}

func TestWriteWalks_EmptyRemovesStaleFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, writeWalks(dir, []portShiftRecord{
		{Kind: "proxy", Old: 3000, New: 3001},
	}))
	require.FileExists(t, filepath.Join(dir, walksFilePath))

	require.NoError(t, writeWalks(dir, nil))
	_, err := os.Stat(filepath.Join(dir, walksFilePath))
	assert.ErrorIs(t, err, os.ErrNotExist, "empty shifts must remove stale walks.json so hamr env doesn't replay yesterday's data")
}

func TestReadWalks_MissingFileReturnsNil(t *testing.T) {
	dir := t.TempDir()
	got, err := readWalks(dir)
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestShiftsToMap_DropsIdentityEntries(t *testing.T) {
	records := []portShiftRecord{
		{Kind: "proxy", Old: 3000, New: 3000}, // identity, dropped
		{Kind: "app", Old: 8080, New: 8081},
		{Kind: "compose", Service: "postgres", Old: 5432, New: 5433},
	}
	got := shiftsToMap(records)
	assert.Equal(t, portShifts{8080: 8081, 5432: 5433}, got)
}

func TestBuildWalkRecords(t *testing.T) {
	t.Run("only emits entries that actually shifted", func(t *testing.T) {
		shifts := []portShift{
			{Service: "postgres", Old: 5432, New: 5433, HostIP: "127.0.0.1"},
			{Service: "rustfs", Old: 9000, New: 9000}, // identity, dropped
		}
		entryMap := map[int]string{0: "deps", 1: "deps"}
		got := buildWalkRecords(3000, 3001, 8080, 8080, shifts, entryMap)
		require.Len(t, got, 2)
		assert.Equal(t, portShiftRecord{Kind: "proxy", Old: 3000, New: 3001}, got[0])
		assert.Equal(t, portShiftRecord{Kind: "compose", ComposeName: "deps", Service: "postgres", HostIP: "127.0.0.1", Old: 5432, New: 5433}, got[1])
	})

	t.Run("zero ports are skipped", func(t *testing.T) {
		// noProxy / no-app-port case — both are 0 and shouldn't produce records.
		got := buildWalkRecords(0, 0, 0, 0, nil, nil)
		assert.Empty(t, got)
	})
}
