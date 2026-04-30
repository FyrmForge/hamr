package devserver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServicesNeedingWalk_excludesRunningPeers(t *testing.T) {
	// Apply-path filter: a running peer (any health) must not be walked,
	// because state-derived shifts already capture its actual port.
	// Walking it would diff against base and emit a second shift for the
	// same service — both records land in walks.json and the rewrite
	// map resolves them last-write-wins (fragile).
	services := []composeService{
		{Name: "db", Ports: []composePortBinding{{HostPort: 5432, Container: 5432}}},
		{Name: "redis", Ports: []composePortBinding{{HostPort: 6379, Container: 6379}}},
		{Name: "worker", Ports: nil},
	}
	running := map[string]bool{"db": true, "redis": true} // both running, regardless of health

	got := servicesNeedingWalk(services, running)
	require.Len(t, got, 1, "only worker should be eligible for walking")
	assert.Equal(t, "worker", got[0].Name)
}

func TestServicesNeedingWalk_includesAllWhenNothingRunning(t *testing.T) {
	services := []composeService{
		{Name: "db", Ports: []composePortBinding{{HostPort: 5432, Container: 5432}}},
		{Name: "redis", Ports: []composePortBinding{{HostPort: 6379, Container: 6379}}},
	}

	got := servicesNeedingWalk(services, nil)
	assert.Len(t, got, 2)
}

func TestCombinedComposeShifts_coldStartReturnsWalkOnly(t *testing.T) {
	services := []composeService{
		{Name: "db", Ports: []composePortBinding{{HostPort: 5432, Container: 5432}}},
	}
	walk := []portShift{{Service: "db", Old: 5432, New: 5433}}

	state := composeStackState{} // no Adopted, no Publishers — cold start
	got := combinedComposeShifts(services, state, walk)
	assert.Equal(t, walk, got, "cold start must pass walk shifts through unchanged")
}

func TestCombinedComposeShifts_partialMergesWalkAndPeerDrift(t *testing.T) {
	services := []composeService{
		{Name: "db", Ports: []composePortBinding{{HostPort: 5432, Container: 5432}}},
		{Name: "redis", Ports: []composePortBinding{{HostPort: 6379, Container: 6379}}},
	}
	state := composeStackState{
		Adopted: map[string]bool{"db": true},
		Publishers: []composeStackPublisher{
			{Service: "db", Container: 5432, PublishedPort: 5433}, // drifted from base 5432
		},
	}
	walk := []portShift{{Service: "redis", Old: 6379, New: 6380}}

	got := combinedComposeShifts(services, state, walk)
	require.Len(t, got, 2)
	// Walk first, peer drift appended.
	assert.Equal(t, "redis", got[0].Service)
	assert.Equal(t, "db", got[1].Service)
	assert.Equal(t, 5432, got[1].Old)
	assert.Equal(t, 5433, got[1].New)
}

func TestCombinedComposeShifts_includesUnhealthyRunningPeerDrift(t *testing.T) {
	// Running-unhealthy peer: has a publisher (the port is held), but
	// not in Adopted. Drift derivation must still emit its shift so
	// env injection points consumers at the actual port. Without this,
	// app processes would connect to the base port while the running
	// container sits elsewhere.
	services := []composeService{
		{Name: "db", Ports: []composePortBinding{{HostPort: 5432, Container: 5432}}},
	}
	state := composeStackState{
		Adopted: map[string]bool{}, // unhealthy → not adopted
		Publishers: []composeStackPublisher{
			{Service: "db", Container: 5432, PublishedPort: 5433},
		},
	}

	got := combinedComposeShifts(services, state, nil)
	require.Len(t, got, 1)
	assert.Equal(t, "db", got[0].Service)
	assert.Equal(t, 5432, got[0].Old)
	assert.Equal(t, 5433, got[0].New)
}

func TestCombinedComposeShifts_adoptedPeerOnBasePortContributesNoShift(t *testing.T) {
	services := []composeService{
		{Name: "db", Ports: []composePortBinding{{HostPort: 5432, Container: 5432}}},
		{Name: "redis", Ports: []composePortBinding{{HostPort: 6379, Container: 6379}}},
	}
	state := composeStackState{
		Adopted: map[string]bool{"db": true},
		Publishers: []composeStackPublisher{
			{Service: "db", Container: 5432, PublishedPort: 5432}, // matches base, no drift
		},
	}
	walk := []portShift{{Service: "redis", Old: 6379, New: 6380}}

	got := combinedComposeShifts(services, state, walk)
	require.Len(t, got, 1, "no peer drift, walk-only")
	assert.Equal(t, "redis", got[0].Service)
}

func TestManageComposeOverride_writesWhenCombinedNonEmpty(t *testing.T) {
	dir := t.TempDir()
	override := filepath.Join(dir, "compose.infra.override.yaml")

	services := []composeService{
		{Name: "db", Ports: []composePortBinding{{HostPort: 5432, Container: 5432}}},
	}
	state := composeStackState{
		Adopted: map[string]bool{"db": true},
		Publishers: []composeStackPublisher{
			{Service: "db", Container: 5432, PublishedPort: 5433},
		},
	}
	combined := []portShift{{Service: "db", Old: 5432, New: 5433}}

	require.NoError(t, manageComposeOverride(override, services, state, combined, nil))

	data, err := os.ReadFile(override)
	require.NoError(t, err)
	assert.Contains(t, string(data), "5433:5432", "override must encode the adopted peer's actual port")
}

func TestManageComposeOverride_removesStaleWhenColdAndNoShifts(t *testing.T) {
	dir := t.TempDir()
	override := filepath.Join(dir, "compose.infra.override.yaml")
	require.NoError(t, os.WriteFile(override, []byte("services: {}\n"), 0o644))

	state := composeStackState{} // cold: no Adopted, no Publishers
	require.NoError(t, manageComposeOverride(override, nil, state, nil, nil))

	_, err := os.Stat(override)
	assert.ErrorIs(t, err, os.ErrNotExist, "stale override must be removed on truly cold start")
}

func TestManageComposeOverride_removesStaleWhenPartialRestartHasNoDrift(t *testing.T) {
	// Regression: a prior session walked redis to 6380 and persisted that
	// in the override. The user then stopped redis (or it crashed). On
	// the next hamr dev, db is still running on its base port (5432),
	// 6379 is free, and combined is empty (db on base contributes no
	// shift, redis isn't running so no state shift either). The stale
	// override must be removed — leaving it would force `compose up -d`
	// to recreate redis on 6380 instead of returning to 6379.
	//
	// Running peers on non-base ports always produce a state-derived
	// shift (non-empty combined), so the empty-combined branch can never
	// strand a real running mapping.
	dir := t.TempDir()
	override := filepath.Join(dir, "compose.infra.override.yaml")
	require.NoError(t, os.WriteFile(override, []byte("services:\n  redis:\n    ports:\n      - \"6380:6379\"\n"), 0o644))

	state := composeStackState{
		Adopted: map[string]bool{"db": true},
		Publishers: []composeStackPublisher{
			{Service: "db", Container: 5432, PublishedPort: 5432}, // running on base, no drift
		},
	}
	require.NoError(t, manageComposeOverride(override, nil, state, nil, nil))

	_, err := os.Stat(override)
	assert.ErrorIs(t, err, os.ErrNotExist, "stale override for stopped service must be removed when nothing drifts")
}

func TestOverrideServices_adoptedUsesPublishedPort(t *testing.T) {
	services := []composeService{
		{Name: "db", Ports: []composePortBinding{{HostPort: 5432, Container: 5432, Protocol: "tcp"}}},
	}
	state := composeStackState{
		Adopted: map[string]bool{"db": true},
		Publishers: []composeStackPublisher{
			{Service: "db", Container: 5432, PublishedPort: 5433},
		},
	}

	got := overrideServices(services, state, nil)
	require.Len(t, got, 1)
	require.Len(t, got[0].Ports, 1)
	assert.Equal(t, 5433, got[0].Ports[0].HostPort)
	// Container port must not be touched.
	assert.Equal(t, 5432, got[0].Ports[0].Container)
}

func TestOverrideServices_runningUnhealthyPeerPreservesPublishedPort(t *testing.T) {
	// db is running but unhealthy — has a publisher, not in Adopted.
	// Override must reflect the running port (5433) so `compose up -d`
	// sees a match and doesn't bounce the unhealthy container. The
	// user's signal that something needs attention is honoured; they
	// wipe to force a rebuild.
	services := []composeService{
		{Name: "db", Ports: []composePortBinding{{HostPort: 5432, Container: 5432}}},
	}
	state := composeStackState{
		Adopted: map[string]bool{}, // unhealthy
		Publishers: []composeStackPublisher{
			{Service: "db", Container: 5432, PublishedPort: 5433},
		},
	}
	// Walk also produced a result for db (since it wasn't adopted),
	// returning base. We must NOT use that — the running container is
	// authoritative.
	walked := map[string]composeService{
		"db": {Name: "db", Ports: []composePortBinding{{HostPort: 5432, Container: 5432}}},
	}

	got := overrideServices(services, state, walked)
	require.Len(t, got, 1)
	assert.Equal(t, 5433, got[0].Ports[0].HostPort, "running peer's published port wins over walk result")
}

func TestOverrideServices_walkedNonAdoptedUsesWalkResult(t *testing.T) {
	services := []composeService{
		{Name: "redis", Ports: []composePortBinding{{HostPort: 6379, Container: 6379}}},
	}
	walked := map[string]composeService{
		"redis": {Name: "redis", Ports: []composePortBinding{{HostPort: 6380, Container: 6379}}},
	}

	got := overrideServices(services, composeStackState{}, walked)
	require.Len(t, got, 1)
	require.Len(t, got[0].Ports, 1)
	assert.Equal(t, 6380, got[0].Ports[0].HostPort)
}

func TestOverrideServices_nonAdoptedNonWalkedKeepsBasePorts(t *testing.T) {
	services := []composeService{
		{Name: "worker", Ports: []composePortBinding{{HostPort: 9000, Container: 9000}}},
	}

	got := overrideServices(services, composeStackState{}, nil)
	require.Len(t, got, 1)
	assert.Equal(t, 9000, got[0].Ports[0].HostPort)
}

func TestOverrideServices_mixedAdoptedAndWalked(t *testing.T) {
	services := []composeService{
		{Name: "db", Ports: []composePortBinding{{HostPort: 5432, Container: 5432}}},
		{Name: "redis", Ports: []composePortBinding{{HostPort: 6379, Container: 6379}}},
	}
	state := composeStackState{
		Adopted: map[string]bool{"db": true},
		Publishers: []composeStackPublisher{
			{Service: "db", Container: 5432, PublishedPort: 5433},
		},
	}
	walked := map[string]composeService{
		"redis": {Name: "redis", Ports: []composePortBinding{{HostPort: 6380, Container: 6379}}},
	}

	got := overrideServices(services, state, walked)
	require.Len(t, got, 2)
	byName := make(map[string]composeService, len(got))
	for _, s := range got {
		byName[s.Name] = s
	}
	assert.Equal(t, 5433, byName["db"].Ports[0].HostPort, "adopted peer renders actual published port")
	assert.Equal(t, 6380, byName["redis"].Ports[0].HostPort, "walked service renders walk result")
}
