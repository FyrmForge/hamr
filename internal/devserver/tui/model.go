package tui

import (
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/FyrmForge/hamr/internal/devserver"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Reserved rows for the chrome: status bar (1) + hint bar (1). The view
// renders status, the viewport, and hint joined by single newlines, so
// the resulting frame is exactly m.height rows tall — important because
// bubbletea's diff-based render won't clear a previously-painted row
// that the new frame doesn't cover, which is why a closing modal used
// to leave the hint bar's row blank.
const chromeRows = 2

// scrollKeyMap is the viewport key map we install in place of the bubbles
// default. The defaults bind d/b/f/u/j/k for half/page navigation, which
// would either collide (d = wipe) or surprise users who haven't read
// vim/less docs. We keep arrow keys for line-by-line scroll and
// PgUp/PgDn for page scroll, and disable the rest by handing back empty
// bindings.
func scrollKeyMap() viewport.KeyMap {
	return viewport.KeyMap{
		PageDown:     key.NewBinding(key.WithKeys("pgdown")),
		PageUp:       key.NewBinding(key.WithKeys("pgup")),
		HalfPageUp:   key.NewBinding(),
		HalfPageDown: key.NewBinding(),
		Up:           key.NewBinding(key.WithKeys("up")),
		Down:         key.NewBinding(key.WithKeys("down")),
	}
}

// errorChangedMsg is emitted by the runtime when the dev server's
// ErrorState changes so the model can refresh the status line.
type errorChangedMsg struct {
	rules []string
}

// actionsReadyMsg fires once Runner.Run has constructed its DevActions
// object. The model captures it for wipe dispatch.
type actionsReadyMsg struct {
	actions *devserver.DevActions
}

// wipeFinishedMsg closes the wipe modal once the goroutine returns.
type wipeFinishedMsg struct{}

// versionStatusMsg updates the persistent version indicator on the status
// bar. Mirrors devserver.StatusBar's SetVersionStatus contract.
type versionStatusMsg struct {
	status devserver.VersionStatus
	msg    string
}

// versionLabelMsg updates the version label shown on the right of the
// status bar (e.g. "v0.6.7").
type versionLabelMsg struct {
	label string
}

// versionUpdateIfOKMsg promotes status to VersionUpdate only if the
// current state is VersionOK — same guard as
// StatusBar.SetVersionUpdateIfOK so a background release-check doesn't
// overwrite a more important indicator.
type versionUpdateIfOKMsg struct {
	msg string
}

// dockerStacksMsg sets the ordered list of docker compose tab names.
// The runtime sends one of these every time runDevTUILoop registers a
// fresh config — config reload may have added or removed entries.
type dockerStacksMsg struct {
	names []string
}

// proxyURLMsg publishes the actual reachable proxy URL once the runner
// has bound its listener. The model renders this at the end of the left
// status-bar cluster so the user always sees what hamr dev picked, even
// when [dev].port_walk shifted the listener off the configured default.
type proxyURLMsg struct {
	url string
}

// Model is the top-level bubbletea model for hamr dev --tui.
type Model struct {
	width   int
	height  int
	ready   bool
	view    viewport.Model
	maxLogs int

	// viewMode selects which buffer the viewport renders.
	//   0          → hamrLogs
	//   1..N       → dockerLogs[dockerTabs[viewMode-1]]
	// Tab cycles forward through the list, Shift+Tab back.
	viewMode   int
	hamrLogs   []string
	dockerTabs []string            // ordered (config order) compose names
	dockerLogs map[string][]string // capped per entry

	hotkeys *HotkeySource
	actions atomic.Pointer[devserver.DevActions]

	wipe wipeState
	help helpState

	// Search state lives per-tab. Keyed by stable identifiers so a
	// config reload that re-orders compose entries can't misalign a
	// search with the wrong buffer: hamrSearch is the single hamr-tab
	// state; dockerSearches is keyed by the compose entry name.
	hamrSearch     *searchState
	dockerSearches map[string]*searchState

	errors []string // current rule names with errors (for status bar)

	versionStatus devserver.VersionStatus
	versionMsg    string // e.g. "CLI is ahead of scaffold (cli v1 proj v2)"
	versionLabel  string // e.g. "v0.6.7"
	proxyURL      string // e.g. "http://localhost:3001"; empty until proxy binds
}

// NewModel constructs a model that pushes hotkeys to the given source.
func NewModel(hotkeys *HotkeySource) *Model {
	return &Model{
		hotkeys:        hotkeys,
		maxLogs:        5000, // bounded per buffer to keep memory predictable
		dockerLogs:     make(map[string][]string),
		dockerSearches: make(map[string]*searchState),
	}
}

// activeSearch returns the search state for the current tab, lazily
// allocating it. Per-tab so cycling away and back preserves the query
// and match cursor; keyed by stable identifier (hamr → singleton,
// docker → compose name) so config reloads that re-order entries
// can't misalign state with the wrong buffer.
func (m *Model) activeSearch() *searchState {
	if m.viewMode == 0 {
		if m.hamrSearch == nil {
			m.hamrSearch = &searchState{}
		}
		return m.hamrSearch
	}
	ix := m.viewMode - 1
	if ix < 0 || ix >= len(m.dockerTabs) {
		// Out-of-range tab — fall back to the hamr search to keep the
		// model in a consistent state. Caller-side validation should
		// have prevented this.
		if m.hamrSearch == nil {
			m.hamrSearch = &searchState{}
		}
		return m.hamrSearch
	}
	name := m.dockerTabs[ix]
	s, ok := m.dockerSearches[name]
	if !ok {
		s = &searchState{}
		m.dockerSearches[name] = s
	}
	return s
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		viewportH := msg.Height - chromeRows
		if viewportH < 1 {
			viewportH = 1
		}
		if !m.ready {
			m.view = viewport.New(msg.Width, viewportH)
			m.view.KeyMap = scrollKeyMap()
			m.setViewContent(strings.Join(m.currentLogs(), "\n"))
			m.view.GotoBottom()
			m.ready = true
		} else {
			atBottom := m.view.AtBottom()
			m.view.Width = msg.Width
			m.view.Height = viewportH
			m.refreshViewport()
			if atBottom {
				m.view.GotoBottom()
			}
		}
		return m, nil

	case LogLineMsg:
		m.appendHamrLog(string(msg))
		return m, nil

	case DockerLogLineMsg:
		m.appendDockerLog(msg.Name, msg.Line)
		return m, nil

	case dockerStacksMsg:
		m.setDockerTabs(msg.names)
		return m, nil

	case errorChangedMsg:
		m.errors = msg.rules
		return m, nil

	case actionsReadyMsg:
		m.actions.Store(msg.actions)
		return m, nil

	case wipeFinishedMsg:
		m.wipe.finish()
		return m, nil

	case versionStatusMsg:
		m.versionStatus = msg.status
		m.versionMsg = msg.msg
		return m, nil

	case versionLabelMsg:
		m.versionLabel = msg.label
		return m, nil

	case versionUpdateIfOKMsg:
		if m.versionStatus == devserver.VersionOK {
			m.versionStatus = devserver.VersionUpdate
			m.versionMsg = msg.msg
		}
		return m, nil

	case proxyURLMsg:
		m.proxyURL = msg.url
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		// Modal overlays own input — wheel under a modal would otherwise
		// scroll the log pane behind it, which is disorienting.
		if m.help.active() || m.wipe.active() {
			return m, nil
		}
		if !m.ready {
			return m, nil
		}
		var cmd tea.Cmd
		m.view, cmd = m.view.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Help is the outermost modal — always closeable on any key, never
	// blocks ctrl+c. Take it before wipe so '?' inside a wipe doesn't
	// accidentally toggle help.
	if m.help.active() {
		if key == "ctrl+c" {
			m.hotkeys.Send(devserver.HotkeyQuit)
			return m, nil
		}
		m.help.handleKey(key)
		return m, nil
	}

	// Wipe modal owns the keyboard while open. Ctrl+C is the one
	// exception — a wipe can take up to 120s (compose down -v + up -d),
	// so the user must always be able to bail out gracefully without
	// waiting for it to finish.
	if m.wipe.active() {
		if key == "ctrl+c" {
			m.hotkeys.Send(devserver.HotkeyQuit)
			return m, nil
		}
		decision := m.wipe.handleKey(key)
		if decision.trigger {
			return m, m.dispatchWipe(decision.triggerIx)
		}
		return m, nil
	}

	// While the user is typing a search query, all key input is the
	// query — no other binding can fire. Ctrl+C still quits.
	if s := m.activeSearch(); s.prompting() {
		return m.handleSearchPrompt(s, msg)
	}

	// Active (committed) search adds n/N navigation, esc clears, f
	// toggles the filter view, and `/` re-opens; everything else falls
	// through to the normal map so r/o/c/d/Tab still work mid-search.
	if s := m.activeSearch(); s.stage == searchActive {
		switch key {
		case "n":
			s.next()
			m.onSearchCursorChange()
			return m, nil
		case "N":
			s.prev()
			m.onSearchCursorChange()
			return m, nil
		case "f":
			s.toggleFilter()
			m.refreshViewport()
			m.onSearchCursorChange()
			return m, nil
		case "esc":
			s.cancel()
			m.refreshViewport()
			return m, nil
		}
	}

	switch key {
	case "ctrl+c", "q":
		// Send through the hotkey channel so the runner shuts down
		// gracefully (stops compose, kills children). The bubbletea
		// program will be quit by the runtime once Runner.Run returns.
		m.hotkeys.Send(devserver.HotkeyQuit)
		return m, nil
	case "r":
		m.hotkeys.Send(devserver.HotkeyRebuild)
		return m, nil
	case "o":
		m.hotkeys.Send(devserver.HotkeyOpenBrowser)
		return m, nil
	case "c":
		// Clear the active tab's buffer in place; never forward to the
		// runner (its HotkeyClearTerminal handler writes raw escape
		// codes that would corrupt the bubbletea frame).
		m.clearActiveLog()
		return m, nil
	case "d":
		actions := m.actions.Load()
		if actions == nil {
			// Runner hasn't booted yet — silently ignore.
			return m, nil
		}
		composes := actions.DockerComposes()
		if len(composes) == 0 {
			m.appendHamrLog("[hamr:tui] no docker compose entries to wipe")
			return m, nil
		}
		m.wipe.open(composes)
		return m, nil
	case "?":
		m.help.toggle()
		return m, nil
	case "/":
		m.activeSearch().open()
		m.refreshViewport()
		return m, nil
	case "tab":
		m.cycleTab(true)
		return m, nil
	case "shift+tab":
		m.cycleTab(false)
		return m, nil
	case "enter":
		if m.ready {
			m.view.GotoBottom()
		}
		return m, nil
	}

	// Forward navigation keys to the viewport so PageUp/PageDown/Home/End
	// scroll the log pane.
	if m.ready {
		var cmd tea.Cmd
		m.view, cmd = m.view.Update(msg)
		return m, cmd
	}
	return m, nil
}

// setViewContent wraps content to the current viewport width before
// handing it to the bubbles viewport. Centralising the wrap here keeps
// every render path (initial paint, refresh, tab cycle, clear) in
// sync — bubbletea's viewport doesn't soft-wrap, so without this long
// log lines get clipped at the right edge.
func (m *Model) setViewContent(content string) {
	m.view.SetContent(wrapForView(content, m.view.Width))
}

// appendCapped pushes a line into a bounded buffer, dropping the oldest
// entries once the limit is reached.
func appendCapped(buf []string, line string, max int) []string {
	buf = append(buf, line)
	if len(buf) > max {
		buf = buf[len(buf)-max:]
	}
	return buf
}

// appendHamrLog records a line into the hamr buffer, refreshing any
// active search's match list, and repaints the viewport when the hamr
// tab is the one currently visible. Background tabs still update
// their search counter so cycling back surfaces an accurate "[k/n]".
func (m *Model) appendHamrLog(line string) {
	m.hamrLogs = appendCapped(m.hamrLogs, line, m.maxLogs)
	if s := m.hamrSearch; s != nil && s.active() {
		s.recompute(m.hamrLogs)
	}
	if m.viewMode == 0 {
		m.refreshViewport()
	}
}

// appendDockerLog records a line into the named docker buffer with
// the same active/background semantics as appendHamrLog. A line for
// an unregistered tab is still buffered: once dockerStacksMsg
// announces the name, the existing content shows up on Tab cycle.
func (m *Model) appendDockerLog(name, line string) {
	m.dockerLogs[name] = appendCapped(m.dockerLogs[name], line, m.maxLogs)
	if s, ok := m.dockerSearches[name]; ok && s.active() {
		s.recompute(m.dockerLogs[name])
	}
	if ix := m.dockerTabIndex(name); ix >= 0 && m.viewMode == ix+1 {
		m.refreshViewport()
	}
}

// dockerTabIndex returns the position of name inside dockerTabs or -1
// if not registered.
func (m *Model) dockerTabIndex(name string) int {
	for i, n := range m.dockerTabs {
		if n == name {
			return i
		}
	}
	return -1
}

// setDockerTabs replaces the tab list. If the active tab points at a
// stack that's no longer present (config reload removed it), the view
// resets to the hamr tab.
func (m *Model) setDockerTabs(names []string) {
	m.dockerTabs = append(m.dockerTabs[:0], names...)
	maxMode := len(m.dockerTabs)
	if m.viewMode > maxMode {
		m.viewMode = 0
		m.refreshViewport()
	}
}

// currentLogs returns the buffer the viewport should be rendering for
// the active tab.
func (m *Model) currentLogs() []string {
	if m.viewMode == 0 {
		return m.hamrLogs
	}
	ix := m.viewMode - 1
	if ix < 0 || ix >= len(m.dockerTabs) {
		return nil
	}
	return m.dockerLogs[m.dockerTabs[ix]]
}

// refreshViewport repaints the viewport with the active tab's contents,
// preserving scroll-bottom-tracking semantics: only auto-scroll when the
// user is already at the bottom, so PgUp scrollback isn't yanked away.
// When a search is in progress (prompting or active) on the tab the
// matches are rendered with highlight ANSI and bottom-tracking is
// suspended — searching the bottom of the buffer shouldn't tug the
// cursor back to the latest line on every keystroke.
func (m *Model) refreshViewport() {
	if !m.ready {
		return
	}
	atBottom := m.view.AtBottom()
	logs := m.currentLogs()
	if s := m.activeSearch(); s.active() {
		m.setViewContent(renderHighlights(logs, s))
		return
	}
	m.setViewContent(strings.Join(logs, "\n"))
	if atBottom {
		m.view.GotoBottom()
	}
}

// handleSearchPrompt routes keystrokes while the user is typing a
// query. Each rune mutates the query and immediately triggers a live
// recompute + viewport refresh + scroll-to-first-match — no waiting
// for Enter to "see" results. Enter just locks the query in so n/N
// start navigating instead of being treated as more query input;
// matches are already there. Esc cancels; Ctrl+C still quits the dev
// server. Other control keys are intentional no-ops.
func (m *Model) handleSearchPrompt(s *searchState, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		m.hotkeys.Send(devserver.HotkeyQuit)
		return m, nil
	case tea.KeyEnter:
		s.commit(m.currentLogs())
		m.refreshViewport()
		m.onSearchCursorChange()
		return m, nil
	case tea.KeyEsc:
		s.cancel()
		m.refreshViewport()
		return m, nil
	case tea.KeyBackspace:
		s.backspace()
		m.applyLiveSearch(s)
		return m, nil
	case tea.KeySpace:
		s.appendRune(' ')
		m.applyLiveSearch(s)
		return m, nil
	case tea.KeyRunes:
		for _, r := range msg.Runes {
			if r >= 0x20 && r != 0x7f {
				s.appendRune(r)
			}
		}
		m.applyLiveSearch(s)
		return m, nil
	}
	return m, nil
}

// applyLiveSearch is the per-keystroke pipeline: recompute matches
// against the current buffer, refresh the viewport (so highlights
// follow the typed prefix), and scroll to the first match. While
// prompting the cursor always sits at hit 0 (incremental search
// always shows the earliest occurrence).
func (m *Model) applyLiveSearch(s *searchState) {
	s.recompute(m.currentLogs())
	m.refreshViewport()
	if !m.ready || len(s.matches) == 0 {
		return
	}
	cur := s.currentMatch()
	target := cur.line - m.view.Height/2
	if target < 0 {
		target = 0
	}
	m.view.SetYOffset(target)
}

// onSearchCursorChange repositions the viewport so the current match
// is centred (or at least visible) and re-renders highlights — the
// "current" hit needs the brighter style than the others. In filter
// mode the "line" we centre on is the row index inside the filtered
// view, not the original buffer line.
func (m *Model) onSearchCursorChange() {
	m.refreshViewport()
	if !m.ready {
		return
	}
	s := m.activeSearch()
	if !s.active() || len(s.matches) == 0 {
		return
	}
	var line int
	if s.filtering {
		line = s.filteredCursorLine()
	} else {
		line = s.currentMatch().line
	}
	target := line - m.view.Height/2
	if target < 0 {
		target = 0
	}
	m.view.SetYOffset(target)
}

// renderHighlights returns a renderable view of logs given the search
// state. With filtering off, every line is emitted with match ANSI
// inserted in place; with filtering on, only lines that contain at
// least one match are emitted. Lines without any matches pass through
// unchanged in the inline path so the cost is bounded by the match
// count, not the buffer size.
func renderHighlights(logs []string, s *searchState) string {
	if s == nil || s.stage == searchClosed {
		return strings.Join(logs, "\n")
	}
	if s.filtering && s.stage == searchActive {
		return renderFiltered(logs, s)
	}
	return renderInline(logs, s)
}

func renderInline(logs []string, s *searchState) string {
	if len(s.matches) == 0 {
		return strings.Join(logs, "\n")
	}
	byLine := groupMatchesByLine(s.matches)
	cur := s.currentMatch()

	var b strings.Builder
	for i, line := range logs {
		if i > 0 {
			b.WriteByte('\n')
		}
		writeLineWithHighlights(&b, line, byLine[i], cur)
	}
	return b.String()
}

// renderFiltered emits one rendered line per matched line in the
// buffer, preserving the buffer's top-to-bottom order. Lines without
// matches are omitted entirely. The cursor's source line is
// highlighted with the brighter "current" style same as inline mode.
func renderFiltered(logs []string, s *searchState) string {
	if len(s.matches) == 0 {
		return ""
	}
	byLine := groupMatchesByLine(s.matches)
	cur := s.currentMatch()
	order := s.matchedLineOrder()

	var b strings.Builder
	for i, lineIx := range order {
		if i > 0 {
			b.WriteByte('\n')
		}
		if lineIx < 0 || lineIx >= len(logs) {
			continue
		}
		writeLineWithHighlights(&b, logs[lineIx], byLine[lineIx], cur)
	}
	return b.String()
}

func groupMatchesByLine(matches []searchMatch) map[int][]searchMatch {
	out := make(map[int][]searchMatch, len(matches))
	for _, m := range matches {
		out[m.line] = append(out[m.line], m)
	}
	return out
}

func writeLineWithHighlights(b *strings.Builder, line string, ms []searchMatch, cur searchMatch) {
	if len(ms) == 0 {
		b.WriteString(line)
		return
	}
	prev := 0
	for _, mt := range ms {
		if mt.start > len(line) {
			break
		}
		end := mt.end
		if end > len(line) {
			end = len(line)
		}
		b.WriteString(line[prev:mt.start])
		text := line[mt.start:end]
		if mt.line == cur.line && mt.start == cur.start {
			b.WriteString(searchCurrent.Render(text))
		} else {
			b.WriteString(searchHighlight.Render(text))
		}
		prev = end
	}
	b.WriteString(line[prev:])
}

// clearActiveLog empties the buffer for the currently visible tab and
// resets the viewport.
func (m *Model) clearActiveLog() {
	if m.viewMode == 0 {
		m.hamrLogs = m.hamrLogs[:0]
	} else if ix := m.viewMode - 1; ix >= 0 && ix < len(m.dockerTabs) {
		m.dockerLogs[m.dockerTabs[ix]] = nil
	}
	if m.ready {
		m.setViewContent("")
	}
}

// cycleTab advances the viewMode forward or backward through the tab
// list. Switching to a tab always jumps to the bottom of its buffer —
// preserving per-tab scroll position is a nice-to-have we can revisit.
// If the destination tab has an active search, refreshViewport applies
// its highlights and we don't auto-scroll, so the prior cursor match
// stays in view.
func (m *Model) cycleTab(forward bool) {
	total := 1 + len(m.dockerTabs)
	if total <= 1 {
		return
	}
	if forward {
		m.viewMode = (m.viewMode + 1) % total
	} else {
		m.viewMode = (m.viewMode - 1 + total) % total
	}
	if !m.ready {
		return
	}
	if s := m.activeSearch(); s.active() {
		m.refreshViewport()
		m.onSearchCursorChange()
		return
	}
	m.setViewContent(strings.Join(m.currentLogs(), "\n"))
	m.view.GotoBottom()
}

// dispatchWipe runs the wipe in a goroutine so the bubbletea Update loop
// stays responsive, then sends wipeFinishedMsg back to close the modal.
func (m *Model) dispatchWipe(ix int) tea.Cmd {
	actions := m.actions.Load()
	if actions == nil {
		m.wipe.finish()
		return nil
	}
	composes := actions.DockerComposes()
	if ix < 0 || ix >= len(composes) {
		m.wipe.finish()
		return nil
	}
	dc := composes[ix]
	return func() tea.Msg {
		actions.DockerWipe(&dc, "")
		return wipeFinishedMsg{}
	}
}

// View implements tea.Model.
func (m *Model) View() string {
	if !m.ready {
		return ""
	}

	var b strings.Builder
	b.WriteString(m.statusBar())
	b.WriteString("\n")
	b.WriteString(m.view.View())
	b.WriteString("\n")
	b.WriteString(m.hintBar())

	if m.help.active() {
		return overlay(b.String(), m.helpView(), m.width, m.height)
	}
	if m.wipe.active() {
		return overlay(b.String(), m.wipeView(), m.width, m.height)
	}
	return b.String()
}

func (m *Model) helpView() string {
	var lines []string
	lines = append(lines, modalTitle.Render("Keybindings"))
	lines = append(lines, "")

	// Compute the longest key column so the descriptions line up.
	keyW := 0
	for _, e := range helpEntries {
		if w := lipgloss.Width(e.keys); w > keyW {
			keyW = w
		}
	}

	for _, e := range helpEntries {
		if e.keys == "" && e.desc == "" {
			lines = append(lines, "")
			continue
		}
		pad := strings.Repeat(" ", keyW-lipgloss.Width(e.keys))
		lines = append(lines, "  "+statusKey.Render(e.keys)+pad+"   "+statusDim.Render(e.desc))
	}
	lines = append(lines, "")
	lines = append(lines, statusDim.Render("any key closes"))
	return modalStyle.Render(strings.Join(lines, "\n"))
}

// hammerStyle returns the style for the 🔨 emoji using the same priority
// the legacy ANSI bar applies: red errors > yellow dev/mismatch/update >
// green ok.
func (m *Model) hammerStyle() lipgloss.Style {
	if len(m.errors) > 0 {
		return hammerErr
	}
	switch m.versionStatus {
	case devserver.VersionDev, devserver.VersionMismatch, devserver.VersionUpdate:
		return hammerWarn
	}
	return hammerOK
}

// versionTag returns the right-aligned version indicator and its visible
// width. Mirrors the legacy bar: VER tag on mismatch, "label ➜ latest" on
// update, plain label otherwise (DEV string for dev builds).
func (m *Model) versionTag() (string, int) {
	switch {
	case m.versionStatus == devserver.VersionMismatch && m.versionMsg != "":
		return statusWarn.Render(m.versionMsg), lipgloss.Width(m.versionMsg)
	case m.versionStatus == devserver.VersionUpdate && m.versionMsg != "":
		text := m.versionLabel + " ➜ " + m.versionMsg
		return statusWarn.Render(text), lipgloss.Width(text)
	case m.versionStatus == devserver.VersionDev:
		return statusWarn.Render("DEV"), 3
	default:
		if m.versionLabel != "" {
			return statusVer.Render(m.versionLabel), lipgloss.Width(m.versionLabel)
		}
	}
	return "", 0
}

// barPad renders a run of spaces with the status-bar background. Used
// between segments and to fill the gap to full width — every visible
// space on the bar has to render with the bg style explicitly, otherwise
// the inner segments' ESC[0m terminators leave terminal-default bg in
// the gaps even when the whole string is wrapped in an outer style.
func barPad(n int) string {
	if n <= 0 {
		return ""
	}
	return statusBarStyle.Render(strings.Repeat(" ", n))
}

// hintPad does the same for the hint bar, which uses a slightly
// different foreground default than the status bar.
func hintPad(n int) string {
	if n <= 0 {
		return ""
	}
	return hintBarStyle.Render(strings.Repeat(" ", n))
}

// searchHintBar swaps the regular keybinding row for a search prompt
// while the user is typing or has a query committed. Layout (live
// search updates [k/n] per keystroke; ↩ "done" locks in the query
// for n/N navigation rather than starting the search):
//
//	prompting: /<query>_  [k/n]              ↩ done  esc cancel
//	prompting (no hits): /<query>_  [no matches]    ↩ done  esc cancel
//	active:    /<query>  [k/n]      n next  N prev  f filter  esc clear
//	active filtered: /<query>  [k/n]    n next  N prev  f unfilter  esc clear
//
// Same width-fill rule as the regular hint bar so the bottom row stays
// solid colour edge-to-edge.
func (m *Model) searchHintBar(s *searchState) string {
	var leftContent string
	// Live counter is shown in both stages now — typing recomputes
	// matches per keystroke so the [k/n] indicator updates as the user
	// narrows the query.
	var counter string
	switch {
	case s.query == "":
		counter = ""
	case len(s.matches) == 0:
		counter = "  [no matches]"
	default:
		counter = fmt.Sprintf("  [%d/%d]", s.cursor+1, len(s.matches))
	}
	leftContent = hintPad(1) +
		statusKey.Render("/") +
		statusDim.Render(s.query)
	if s.stage == searchPrompting {
		// Trailing "_" is a faux cursor — minimal, no need to wire a
		// real blinking cursor for v1.
		leftContent += statusKey.Render("_")
	}
	leftContent += statusDim.Render(counter)

	var right string
	if s.stage == searchPrompting {
		right = statusKey.Render("↩") + statusDim.Render(" done  ") +
			statusKey.Render("esc") + statusDim.Render(" cancel")
	} else {
		filterLabel := " filter"
		if s.filtering {
			filterLabel = " unfilter"
		}
		right = statusKey.Render("n") + statusDim.Render(" next  ") +
			statusKey.Render("N") + statusDim.Render(" prev  ") +
			statusKey.Render("f") + statusDim.Render(filterLabel) + statusDim.Render("  ") +
			statusKey.Render("esc") + statusDim.Render(" clear")
	}

	leftW := lipgloss.Width(leftContent)
	rightW := lipgloss.Width(right)
	const trail = 1
	gap := m.width - leftW - rightW - trail
	if gap < 1 {
		return leftContent + hintPad(m.width-leftW)
	}
	return leftContent + hintPad(gap) + right + hintPad(trail)
}

// activeTabIcon returns the styled icon glyph for the current tab.
func (m *Model) activeTabIcon() string {
	if m.viewMode == 0 {
		return m.hammerStyle().Render(" 🔨 ")
	}
	return dockerIcon.Render(" 🐳 ")
}

// activeTabTitle returns the bold title text for the current tab.
func (m *Model) activeTabTitle() string {
	if m.viewMode == 0 {
		return "hamr dev"
	}
	ix := m.viewMode - 1
	if ix < 0 || ix >= len(m.dockerTabs) {
		return "hamr dev"
	}
	return m.dockerTabs[ix]
}

// tabPosition returns "[k/n]" for the active tab, or "" when there's
// only the hamr tab. Gives the user a hint that Tab cycles when more
// than one buffer exists.
func (m *Model) tabPosition() string {
	if len(m.dockerTabs) == 0 {
		return ""
	}
	return fmt.Sprintf("[%d/%d]", m.viewMode+1, 1+len(m.dockerTabs))
}

func (m *Model) statusBar() string {
	if m.width <= 0 {
		return ""
	}

	parts := []string{
		m.activeTabIcon(),
		statusKey.Render(m.activeTabTitle()),
	}
	if pos := m.tabPosition(); pos != "" {
		parts = append(parts, barPad(2), statusDim.Render(pos))
	}
	parts = append(parts, barPad(2), statusLabel.Render("•"), barPad(2))
	if len(m.errors) == 0 {
		parts = append(parts, statusOK.Render("OK"))
	} else {
		parts = append(parts, statusErr.Render(fmt.Sprintf("ERR: %s", strings.Join(m.errors, ", "))))
	}
	// Proxy URL renders at the end of the left cluster (after OK/ERR) so
	// it's always visible — particularly useful when [dev].port_walk
	// shifted the listener off the configured default.
	if m.proxyURL != "" {
		parts = append(parts, barPad(2), statusDim.Render(m.proxyURL))
	}
	left := strings.Join(parts, "")

	right, rightW := m.versionTag()
	// Trailing space inside the right tag region keeps the version label
	// off the very last column.
	if right != "" {
		right += barPad(1)
		rightW++
	}

	leftW := lipgloss.Width(left)
	gap := m.width - leftW - rightW
	if gap < 1 {
		gap = m.width - leftW
		if gap < 0 {
			gap = 0
		}
		right = ""
	}
	return left + barPad(gap) + right
}

func (m *Model) hintBar() string {
	if m.width <= 0 {
		return ""
	}
	if s := m.activeSearch(); s.active() {
		return m.searchHintBar(s)
	}
	left := []string{
		statusKey.Render("r") + statusDim.Render(" rebuild"),
		statusKey.Render("o") + statusDim.Render(" open"),
		statusKey.Render("c") + statusDim.Render(" clear"),
		statusKey.Render("d") + statusDim.Render(" wipe deps"),
	}
	if len(m.dockerTabs) > 0 {
		left = append(left, statusKey.Render("Tab")+statusDim.Render(" tabs"))
	}
	left = append(left, statusKey.Render("q")+statusDim.Render(" quit"))

	// `? help` is right-aligned on the bar's far edge; users tend to
	// look there for "where is everything?" affordances.
	right := statusKey.Render("?") + statusDim.Render(" help")

	// Explicitly bg-styled separators so the bar reads as a single
	// unbroken block of colour; same reasoning as barPad.
	sep := hintPad(2)
	leftContent := hintPad(1) + strings.Join(left, sep)
	leftW := lipgloss.Width(leftContent)
	rightW := lipgloss.Width(right)

	// Reserve one trailing column so the help label isn't flush against
	// the very last cell — matches the right padding on the status bar.
	const rightTrailing = 1
	gap := m.width - leftW - rightW - rightTrailing
	if gap < 1 {
		// Out of room for both — keep the left group and pad to width.
		return leftContent + hintPad(m.width-leftW)
	}
	return leftContent + hintPad(gap) + right + hintPad(rightTrailing)
}

func (m *Model) wipeView() string {
	switch m.wipe.stage {
	case wipeSelecting:
		var lines []string
		lines = append(lines, modalTitle.Render("Wipe which compose stack?"))
		for i, dc := range m.wipe.composes {
			services := strings.Join(dc.Services, ", ")
			if services == "" {
				services = "all services"
			}
			lines = append(lines, fmt.Sprintf("  %s) %s  %s",
				statusKey.Render(fmt.Sprintf("%d", i+1)),
				dc.Name,
				statusDim.Render(services),
			))
		}
		lines = append(lines, "")
		lines = append(lines, statusDim.Render("any other key to cancel"))
		return modalStyle.Render(strings.Join(lines, "\n"))

	case wipeConfirming:
		dc := m.wipe.composes[m.wipe.pickedIx]
		services := strings.Join(dc.Services, ", ")
		if services == "" {
			services = "all services"
		}
		body := strings.Join([]string{
			modalTitle.Render("Wipe " + dc.Name + "?"),
			statusDim.Render("compose file: ") + dc.File,
			statusDim.Render("services:     ") + services,
			"",
			modalDanger.Render("This runs `docker compose down -v` and removes volumes."),
			modalWarn.Render("All data in those volumes will be lost."),
			"",
			statusKey.Render("y") + statusDim.Render(" confirm   ") +
				statusKey.Render("any other key") + statusDim.Render(" cancel"),
		}, "\n")
		return modalStyle.Render(body)

	case wipeRunning:
		return modalStyle.Render(modalTitle.Render(m.wipe.status))
	}
	return ""
}

// overlay places the modal in the centre of the base view by replacing
// lines underneath it. Bubbletea has no native compositor, so this is the
// pragmatic approach for a single modal at a time.
func overlay(base, modal string, width, height int) string {
	if modal == "" {
		return base
	}
	baseLines := strings.Split(base, "\n")
	modalLines := strings.Split(modal, "\n")

	// Pad base to the full screen height so positioning math is stable.
	for len(baseLines) < height {
		baseLines = append(baseLines, "")
	}

	mh := len(modalLines)
	mw := lipgloss.Width(modal)
	startY := (height - mh) / 2
	if startY < 0 {
		startY = 0
	}
	startX := (width - mw) / 2
	if startX < 0 {
		startX = 0
	}

	for i, line := range modalLines {
		row := startY + i
		if row >= len(baseLines) {
			break
		}
		baseLines[row] = padOrTruncate(baseLines[row], startX) + line +
			padOrTruncate("", width-startX-lipgloss.Width(line))
	}
	return strings.Join(baseLines, "\n")
}

// padOrTruncate pads s with spaces to width w, or truncates if longer.
// Width is measured visually (lipgloss.Width) so ANSI escapes don't count.
func padOrTruncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	have := lipgloss.Width(s)
	if have == w {
		return s
	}
	if have < w {
		return s + strings.Repeat(" ", w-have)
	}
	// Truncation of styled strings is lossy; for the overlay context the
	// pre-modal slice is empty padding so this branch is rarely taken.
	return lipgloss.NewStyle().MaxWidth(w).Render(s)
}
