package tui

import "testing"

func newModelForTabs(stacks ...string) *Model {
	m := NewModel(NewHotkeySource())
	if len(stacks) > 0 {
		m.setDockerTabs(stacks)
	}
	return m
}

func TestModel_StartsOnHamrTab(t *testing.T) {
	m := newModelForTabs()
	if m.viewMode != 0 {
		t.Fatalf("zero value should be hamr tab (mode 0), got %d", m.viewMode)
	}
}

func TestModel_CycleTabHamrOnly_NoOp(t *testing.T) {
	m := newModelForTabs() // no docker entries
	m.cycleTab(true)
	if m.viewMode != 0 {
		t.Fatalf("with only the hamr tab, Tab must not change viewMode, got %d", m.viewMode)
	}
	m.cycleTab(false)
	if m.viewMode != 0 {
		t.Fatalf("with only the hamr tab, Shift+Tab must not change viewMode, got %d", m.viewMode)
	}
}

func TestModel_CycleTabForwardWraps(t *testing.T) {
	m := newModelForTabs("infra", "stripe")
	steps := []int{1, 2, 0, 1, 2, 0}
	for i, want := range steps {
		m.cycleTab(true)
		if m.viewMode != want {
			t.Fatalf("step %d: want viewMode=%d, got %d", i, want, m.viewMode)
		}
	}
}

func TestModel_CycleTabBackwardWraps(t *testing.T) {
	m := newModelForTabs("infra", "stripe")
	// from 0, Shift+Tab should land on the last docker tab
	m.cycleTab(false)
	if m.viewMode != 2 {
		t.Fatalf("Shift+Tab from hamr should jump to last docker tab, got %d", m.viewMode)
	}
	m.cycleTab(false)
	if m.viewMode != 1 {
		t.Fatalf("Shift+Tab again should land on first docker tab, got %d", m.viewMode)
	}
	m.cycleTab(false)
	if m.viewMode != 0 {
		t.Fatalf("Shift+Tab again should return to hamr, got %d", m.viewMode)
	}
}

func TestModel_AppendRoutesByActiveTab(t *testing.T) {
	m := newModelForTabs("infra")

	m.appendHamrLog("hamr line")
	m.appendDockerLog("infra", "infra line")

	if got := m.hamrLogs; len(got) != 1 || got[0] != "hamr line" {
		t.Fatalf("hamr buffer: got %v", got)
	}
	if got := m.dockerLogs["infra"]; len(got) != 1 || got[0] != "infra line" {
		t.Fatalf("infra buffer: got %v", got)
	}
}

func TestModel_DockerLogBufferedBeforeRegistration(t *testing.T) {
	// Lines may arrive before RegisterDockerStacks (race between the
	// follower starting and the model receiving dockerStacksMsg). They
	// must still land in the named buffer so they appear once the tab
	// is registered and the user cycles to it.
	m := newModelForTabs() // no tabs registered yet

	m.appendDockerLog("infra", "early line")
	m.setDockerTabs([]string{"infra"})

	if got := m.dockerLogs["infra"]; len(got) != 1 || got[0] != "early line" {
		t.Fatalf("expected early line preserved across registration, got %v", got)
	}
}

func TestModel_SetDockerTabsResetsModeWhenTabRemoved(t *testing.T) {
	m := newModelForTabs("infra", "stripe")
	m.viewMode = 2 // viewing "stripe"

	// Config reload removes "stripe".
	m.setDockerTabs([]string{"infra"})

	if m.viewMode != 0 {
		t.Fatalf("removing the active docker stack should reset to hamr tab, got %d", m.viewMode)
	}
}

func TestModel_CurrentLogsTracksMode(t *testing.T) {
	m := newModelForTabs("infra")
	m.appendHamrLog("h")
	m.appendDockerLog("infra", "i")

	if got := m.currentLogs(); len(got) != 1 || got[0] != "h" {
		t.Fatalf("hamr-mode currentLogs: got %v", got)
	}
	m.viewMode = 1
	if got := m.currentLogs(); len(got) != 1 || got[0] != "i" {
		t.Fatalf("infra-mode currentLogs: got %v", got)
	}
}

func TestModel_ClearActiveLogOnlyClearsActiveBuffer(t *testing.T) {
	m := newModelForTabs("infra")
	m.appendHamrLog("h")
	m.appendDockerLog("infra", "i")

	m.viewMode = 1 // viewing infra
	m.clearActiveLog()

	if got := m.dockerLogs["infra"]; len(got) != 0 {
		t.Fatalf("infra buffer should be cleared, got %v", got)
	}
	if got := m.hamrLogs; len(got) != 1 || got[0] != "h" {
		t.Fatalf("hamr buffer must be untouched when clearing infra, got %v", got)
	}
}

func TestModel_ActiveTabTitleAndIconChange(t *testing.T) {
	m := newModelForTabs("infra", "stripe")

	if title := m.activeTabTitle(); title != "hamr dev" {
		t.Fatalf("hamr tab title: want 'hamr dev', got %q", title)
	}

	m.viewMode = 1
	if title := m.activeTabTitle(); title != "infra" {
		t.Fatalf("docker tab 1 title: want 'infra', got %q", title)
	}
	m.viewMode = 2
	if title := m.activeTabTitle(); title != "stripe" {
		t.Fatalf("docker tab 2 title: want 'stripe', got %q", title)
	}
}

func TestModel_SearchSurvivesTabReorder(t *testing.T) {
	// Regression test: searches used to be keyed by viewMode (int).
	// When a config reload re-ordered docker compose entries, the
	// old int-key would point at a different stack, silently
	// misaligning queries. Searches are now keyed by stable name so
	// the per-tab state follows the tab.
	m := newModelForTabs("infra", "stripe")

	// Commit a search on the infra tab.
	m.viewMode = 1
	infraSearch := m.activeSearch()
	infraSearch.open()
	for _, r := range "err" {
		infraSearch.appendRune(r)
	}
	infraSearch.commit(nil)

	// Commit a different search on the stripe tab.
	m.viewMode = 2
	stripeSearch := m.activeSearch()
	stripeSearch.open()
	for _, r := range "warn" {
		stripeSearch.appendRune(r)
	}
	stripeSearch.commit(nil)

	// Config reload re-orders the docker compose entries — stripe is
	// now first, infra second.
	m.setDockerTabs([]string{"stripe", "infra"})

	// viewMode 1 should now resolve to stripe's search; viewMode 2 to
	// infra's. If the keying drifted, the queries would swap.
	m.viewMode = 1
	if got := m.activeSearch().query; got != "warn" {
		t.Fatalf("after reorder, viewMode=1 (stripe) should hold 'warn', got %q", got)
	}
	m.viewMode = 2
	if got := m.activeSearch().query; got != "err" {
		t.Fatalf("after reorder, viewMode=2 (infra) should hold 'err', got %q", got)
	}
}

func TestModel_AppendDockerLogUpdatesSearchByName(t *testing.T) {
	// New lines arriving on a background docker tab should keep its
	// search counter fresh — keyed by stack name, not view-mode int.
	m := newModelForTabs("infra")

	// Open + commit a search on the infra tab.
	m.viewMode = 1
	s := m.activeSearch()
	s.open()
	for _, r := range "boom" {
		s.appendRune(r)
	}
	s.commit(nil)
	if len(s.matches) != 0 {
		t.Fatalf("setup: matches should start empty, got %d", len(s.matches))
	}

	// Switch to the hamr tab and let a new line arrive on infra.
	m.viewMode = 0
	m.appendDockerLog("infra", "system boom level: high")

	// The search owned by "infra" should have picked up the new hit
	// even though we're not currently viewing that tab.
	if len(s.matches) != 1 {
		t.Fatalf("background tab search should recompute, got %d matches", len(s.matches))
	}
}
