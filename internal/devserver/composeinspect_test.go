package devserver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHostPortKey_normalisesLoopbackVariants(t *testing.T) {
	for _, host := range []string{"", "0.0.0.0", "[::]", "::", "127.0.0.1", "localhost"} {
		assert.Equal(t, "localhost:5432", hostPortKey(host, 5432), "host=%q", host)
	}
	assert.Equal(t, "192.168.1.10:5432", hostPortKey("192.168.1.10", 5432))
}

func TestCanonComposeHost(t *testing.T) {
	assert.Equal(t, "", canonComposeHost(""))
	assert.Equal(t, "", canonComposeHost("0.0.0.0"))
	assert.Equal(t, "", canonComposeHost("127.0.0.1"))
	assert.Equal(t, "", canonComposeHost("localhost"))
	assert.Equal(t, "192.168.1.10", canonComposeHost("192.168.1.10"))
}

func TestDecodeComposePS_arrayForm(t *testing.T) {
	raw := []byte(`[
		{"Service":"db","State":"running","Health":"healthy","Publishers":[{"URL":"0.0.0.0","TargetPort":5432,"PublishedPort":5433,"Protocol":"tcp"}]},
		{"Service":"redis","State":"exited","Health":"","Publishers":[]}
	]`)
	got, err := decodeComposePS(raw)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "db", got[0].Service)
	assert.Equal(t, "running", got[0].State)
	assert.Equal(t, "healthy", got[0].Health)
	require.Len(t, got[0].Publishers, 1)
	assert.Equal(t, 5433, got[0].Publishers[0].PublishedPort)
}

func TestDecodeComposePS_ndjsonForm(t *testing.T) {
	raw := []byte(`{"Service":"db","State":"running","Publishers":[{"URL":"127.0.0.1","TargetPort":5432,"PublishedPort":5432,"Protocol":"tcp"}]}
{"Service":"redis","State":"running","Health":"healthy","Publishers":[]}`)
	got, err := decodeComposePS(raw)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "db", got[0].Service)
	assert.Equal(t, "redis", got[1].Service)
}

func TestDecodeComposePS_emptyInput(t *testing.T) {
	got, err := decodeComposePS(nil)
	require.NoError(t, err)
	assert.Nil(t, got)

	got, err = decodeComposePS([]byte("   \n\n  "))
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestIsAdopted(t *testing.T) {
	tests := []struct {
		name string
		e    composePSEntry
		want bool
	}{
		{"running healthy", composePSEntry{State: "running", Health: "healthy"}, true},
		{"running no healthcheck", composePSEntry{State: "running", Health: ""}, true},
		{"running unhealthy", composePSEntry{State: "running", Health: "unhealthy"}, false},
		{"running starting", composePSEntry{State: "running", Health: "starting"}, false},
		{"exited", composePSEntry{State: "exited", Health: ""}, false},
		{"restarting", composePSEntry{State: "restarting", Health: ""}, false},
		{"running mixed case", composePSEntry{State: "Running", Health: "Healthy"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isAdopted(tt.e))
		})
	}
}

func TestInterpretComposePS_buildsAdoptedAndOwned(t *testing.T) {
	entries := []composePSEntry{
		{
			Service: "db",
			State:   "running",
			Health:  "healthy",
			Publishers: []composePSPublisher{
				{URL: "0.0.0.0", TargetPort: 5432, PublishedPort: 5433, Protocol: "tcp"},
			},
		},
		{
			Service: "redis",
			State:   "running",
			Health:  "starting",
			Publishers: []composePSPublisher{
				{URL: "127.0.0.1", TargetPort: 6379, PublishedPort: 6379, Protocol: "tcp"},
			},
		},
		{
			Service:    "worker",
			State:      "exited",
			Publishers: nil,
		},
	}
	got := interpretComposePS(entries)

	// Adopted set: only db (redis is starting → fail readiness;
	// worker is exited).
	assert.Equal(t, map[string]bool{"db": true}, got.Adopted)

	// Owned ports: published ports from every running entry, regardless
	// of health (the walk should still avoid colliding with a starting
	// container that's holding a port).
	assert.True(t, got.Owned["localhost:5433"])
	assert.True(t, got.Owned["localhost:6379"])

	// Publishers carries one entry per publisher row.
	require.Len(t, got.Publishers, 2)
}

func TestInterpretComposePS_excludesExitedContainerPublishers(t *testing.T) {
	// If compose ps were ever to report Publishers for exited
	// containers (or we accidentally pass --all), they must not bleed
	// into Owned or Publishers — the port isn't actually held.
	entries := []composePSEntry{
		{
			Service: "db",
			State:   "exited",
			Publishers: []composePSPublisher{
				{URL: "127.0.0.1", TargetPort: 5432, PublishedPort: 5432, Protocol: "tcp"},
			},
		},
	}
	got := interpretComposePS(entries)
	assert.Empty(t, got.Adopted, "exited container does not adopt")
	assert.Empty(t, got.Owned, "exited container's publishers must not enter the owned set")
	assert.Empty(t, got.Publishers, "exited container's publishers must not enter the publisher list")
}

func TestInterpretComposePS_skipsEmptyServiceAndUnpublishedPorts(t *testing.T) {
	entries := []composePSEntry{
		{Service: "", State: "running", Publishers: nil},
		{
			Service: "db",
			State:   "running",
			Publishers: []composePSPublisher{
				{URL: "127.0.0.1", TargetPort: 5432, PublishedPort: 0}, // unpublished
				{URL: "127.0.0.1", TargetPort: 5432, PublishedPort: 5432},
			},
		},
	}
	got := interpretComposePS(entries)
	assert.Equal(t, map[string]bool{"db": true}, got.Adopted)
	assert.Equal(t, map[string]bool{"localhost:5432": true}, got.Owned)
	require.Len(t, got.Publishers, 1)
	assert.Equal(t, 5432, got.Publishers[0].PublishedPort)
}

func TestExpectedServiceNames(t *testing.T) {
	services := []composeService{{Name: "db"}, {Name: "redis"}, {Name: "worker"}}

	t.Run("explicit dc.Services wins", func(t *testing.T) {
		dc := &DockerCompose{Services: []string{"db", "redis"}}
		assert.Equal(t, []string{"db", "redis"}, expectedServiceNames(dc, services))
	})

	t.Run("empty dc.Services falls back to all in compose", func(t *testing.T) {
		dc := &DockerCompose{}
		assert.Equal(t, []string{"db", "redis", "worker"}, expectedServiceNames(dc, services))
	})

	t.Run("empty dc.Services excludes profile-gated services", func(t *testing.T) {
		// Regression: compose files using `profiles:` declare services
		// that compose only starts when the profile is enabled. hamr
		// doesn't pass --profile, so those services aren't running and
		// must not be in the expected set — otherwise allServicesAdopted
		// returns false forever and adoption is permanently disabled.
		profiled := []composeService{
			{Name: "db"},
			{Name: "redis"},
			{Name: "seed", Profiles: []string{"tools"}},
			{Name: "shell", Profiles: []string{"tools", "debug"}},
		}
		dc := &DockerCompose{}
		assert.Equal(t, []string{"db", "redis"}, expectedServiceNames(dc, profiled))
	})

	t.Run("explicit dc.Services overrides profile filter", func(t *testing.T) {
		// `compose up -d <name>` starts a named service regardless of
		// profile. If the user opts in by name, trust them.
		profiled := []composeService{
			{Name: "db"},
			{Name: "seed", Profiles: []string{"tools"}},
		}
		dc := &DockerCompose{Services: []string{"db", "seed"}}
		assert.Equal(t, []string{"db", "seed"}, expectedServiceNames(dc, profiled))
	})
}

func TestAllServicesAdopted(t *testing.T) {
	adopted := map[string]bool{"db": true, "redis": true}
	assert.True(t, allServicesAdopted([]string{"db", "redis"}, adopted))
	assert.True(t, allServicesAdopted([]string{"db"}, adopted))
	assert.False(t, allServicesAdopted([]string{"db", "redis", "worker"}, adopted))
	assert.True(t, allServicesAdopted(nil, adopted))
}

func TestStateShiftsForServices_capturesDriftFromBase(t *testing.T) {
	services := []composeService{
		{
			Name: "db",
			Ports: []composePortBinding{
				{HostPort: 5432, Container: 5432, Protocol: "tcp"},
			},
		},
		{
			Name: "redis",
			Ports: []composePortBinding{
				{HostPort: 6379, Container: 6379, Protocol: "tcp"},
			},
		},
	}
	publishers := []composeStackPublisher{
		{Service: "db", Container: 5432, PublishedPort: 5433, Protocol: "tcp"},
		{Service: "redis", Container: 6379, PublishedPort: 6379, Protocol: "tcp"},
	}

	shifts := stateShiftsForServices(services, publishers, nil)
	require.Len(t, shifts, 1, "only db drifted")
	assert.Equal(t, "db", shifts[0].Service)
	assert.Equal(t, 5432, shifts[0].Old)
	assert.Equal(t, 5433, shifts[0].New)
}

func TestStateShiftsForServices_scopedByOnlyFilter(t *testing.T) {
	services := []composeService{
		{Name: "db", Ports: []composePortBinding{{HostPort: 5432, Container: 5432}}},
		{Name: "redis", Ports: []composePortBinding{{HostPort: 6379, Container: 6379}}},
	}
	publishers := []composeStackPublisher{
		{Service: "db", Container: 5432, PublishedPort: 5433},
		{Service: "redis", Container: 6379, PublishedPort: 6380},
	}

	only := map[string]bool{"db": true}
	shifts := stateShiftsForServices(services, publishers, only)
	require.Len(t, shifts, 1)
	assert.Equal(t, "db", shifts[0].Service)
}

func TestResolvedPortsForService_singleBindingMatchesByContainer(t *testing.T) {
	bindings := []composePortBinding{{HostPort: 5432, Container: 5432}}
	publishers := []composeStackPublisher{
		{Service: "db", Container: 5432, PublishedPort: 5433},
	}
	got := resolvedPortsForService("db", bindings, publishers)
	assert.Equal(t, map[int]int{0: 5433}, got)
}

func TestResolvedPortsForService_singleBindingFallsBackWhenContainerMismatched(t *testing.T) {
	// Compose ps reports TargetPort=0 (older compose / unreliable
	// field). With one binding and one publisher, the sort-order
	// fallback pairs them anyway so adopted-peer ports still resolve.
	bindings := []composePortBinding{{HostPort: 5432, Container: 5432}}
	publishers := []composeStackPublisher{
		{Service: "db", Container: 0, PublishedPort: 5433},
	}
	got := resolvedPortsForService("db", bindings, publishers)
	assert.Equal(t, map[int]int{0: 5433}, got)
}

func TestResolvedPortsForService_multipleDistinctContainers(t *testing.T) {
	bindings := []composePortBinding{
		{HostPort: 5432, Container: 5432},
		{HostPort: 6379, Container: 6379},
	}
	publishers := []composeStackPublisher{
		{Service: "db", Container: 5432, PublishedPort: 5433},
		{Service: "db", Container: 6379, PublishedPort: 6380},
	}
	got := resolvedPortsForService("db", bindings, publishers)
	assert.Equal(t, map[int]int{0: 5433, 1: 6380}, got)
}

func TestResolvedPortsForService_collidingContainerPortsPairBySortOrder(t *testing.T) {
	// Both bindings publish container 5432, on different host ports.
	// Compose ps reports two publishers on container 5432. Pair them
	// by ascending HostPort vs ascending PublishedPort so the result
	// is deterministic.
	bindings := []composePortBinding{
		{HostPort: 5432, Container: 5432},
		{HostPort: 5433, Container: 5432},
	}
	publishers := []composeStackPublisher{
		{Service: "db", Container: 5432, PublishedPort: 5500},
		{Service: "db", Container: 5432, PublishedPort: 5501},
	}
	got := resolvedPortsForService("db", bindings, publishers)
	assert.Equal(t, map[int]int{0: 5500, 1: 5501}, got)
}

func TestResolvedPortsForService_mismatchedCountsLeavesUnpaired(t *testing.T) {
	bindings := []composePortBinding{
		{HostPort: 5432, Container: 5432},
		{HostPort: 5433, Container: 5432},
	}
	// Only one publisher for two ambiguous bindings — refuse to guess.
	publishers := []composeStackPublisher{
		{Service: "db", Container: 5432, PublishedPort: 5500},
	}
	got := resolvedPortsForService("db", bindings, publishers)
	assert.Empty(t, got, "ambiguous bindings + count mismatch must leave bindings unpaired")
}

func TestResolvedPortsForService_skipsRandomHostPortBindings(t *testing.T) {
	bindings := []composePortBinding{
		{HostPort: 0, Container: 5432}, // random — no baseline
		{HostPort: 6379, Container: 6379},
	}
	publishers := []composeStackPublisher{
		{Service: "db", Container: 5432, PublishedPort: 49234},
		{Service: "db", Container: 6379, PublishedPort: 6380},
	}
	got := resolvedPortsForService("db", bindings, publishers)
	require.Len(t, got, 1)
	assert.Equal(t, 6380, got[1])
	_, hasRandom := got[0]
	assert.False(t, hasRandom, "random-port binding must be excluded from resolution")
}

func TestResolvedPortsForService_ignoresOtherServices(t *testing.T) {
	bindings := []composePortBinding{{HostPort: 5432, Container: 5432}}
	publishers := []composeStackPublisher{
		{Service: "redis", Container: 6379, PublishedPort: 6379},
	}
	got := resolvedPortsForService("db", bindings, publishers)
	assert.Empty(t, got)
}

func TestStateShiftsForServices_skipsRandomBaseHostPort(t *testing.T) {
	services := []composeService{
		{Name: "db", Ports: []composePortBinding{{HostPort: 0, Container: 5432}}},
	}
	publishers := []composeStackPublisher{
		{Service: "db", Container: 5432, PublishedPort: 49234},
	}

	shifts := stateShiftsForServices(services, publishers, nil)
	assert.Empty(t, shifts, "random host port has no declared baseline to diff against")
}
