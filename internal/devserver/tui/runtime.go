package tui

import (
	"context"
	"io"

	"github.com/FyrmForge/hamr/internal/devserver"
	tea "github.com/charmbracelet/bubbletea"
)

// Runtime bundles the bubbletea program with the adapters the dev runner
// needs (hotkey source, log sink). Keep references on the same value so
// the dev command can wire them via WithHotkeys / WithLogWriter /
// WithProcessOutput / WithActionsHook in one place.
type Runtime struct {
	model       *Model
	program     *tea.Program
	sink        *Sink
	hotkeys     *HotkeySource
	dockerSinks map[string]*Sink
	// unsubscribe drops the previous run's broker subscription. onActions
	// fires once per Run(), so a restart would otherwise leak the old
	// forwarding goroutine and its broker slot on every R press.
	unsubscribe func()
}

// NewRuntime builds the TUI runtime. Call Wire on a Runner before its Run
// to feed all the right adapters in. Call Start to run the program (it
// blocks). The recommended flow is in dev.go:
//
//	rt := tui.NewRuntime()
//	go func() { runErr <- runDevLoop(ctx, rt, configPath, ...) }()
//	rt.Start()  // blocks until the model returns tea.Quit
//	<-runErr
func NewRuntime() *Runtime {
	hotkeys := NewHotkeySource()
	model := NewModel(hotkeys)
	// `make <target>` output reaches this sink the same way every rule's
	// output does — through the dev server's ProcessManager, which the
	// runner points at the sink via WithProcessOutput. The TUI never spawns
	// make itself; see docs/adr/004-dev-event-bus.md.
	sink := NewSink()

	prog := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	sink.Bind(prog)

	return &Runtime{
		model:       model,
		program:     prog,
		sink:        sink,
		hotkeys:     hotkeys,
		dockerSinks: make(map[string]*Sink),
	}
}

// Wire applies the runtime's adapters to a Runner via the standard option
// constructors. Centralising the wiring here keeps dev.go from having to
// know which sink goes where.
func (r *Runtime) Wire(opts []devserver.Option) []devserver.Option {
	return append(opts,
		devserver.WithHotkeys(r.hotkeys),
		devserver.WithLogWriter(r.sink),
		devserver.WithProcessOutput(r.sink, r.sink),
		devserver.WithActionsHook(r.onActions),
		devserver.WithProxyURLHook(r.SetProxyURL),
		devserver.WithMCPStatusHook(r.SetMCPStatus),
		devserver.WithMCPLogHook(r.AppendMCPLog),
	)
}

// RegisterDockerStacks publishes the ordered list of compose-entry
// names to the model (so Tab cycles through them and the status bar
// labels each tab) and returns the per-entry io.Writer map the runner
// should pass to WithDockerLogSinks. Sinks are created lazily and
// cached, so a config reload that re-registers the same name reuses
// the same docker sink (its already-buffered lines stay buffered until
// the new follower process emits more).
func (r *Runtime) RegisterDockerStacks(names []string) map[string]io.Writer {
	out := make(map[string]io.Writer, len(names))
	for _, name := range names {
		s, ok := r.dockerSinks[name]
		if !ok {
			s = NewDockerSink(name)
			s.Bind(r.program)
			r.dockerSinks[name] = s
		}
		out[name] = s
	}
	// Tell the model the current tab order so the active-tab indicator
	// and viewport switching stay aligned with what's actually being
	// followed. A copy so caller mutation can't corrupt the message.
	cp := append([]string(nil), names...)
	r.program.Send(dockerStacksMsg{names: cp})
	return out
}

// onActions runs on the runner goroutine once per Run(). It subscribes the
// model to the dev server's event bus — the same stream the browser dev panel
// reads — and hands it the RunMake entry point so the `m` hotkey dispatches
// through the dev server instead of spawning its own make.
//
// Error state stays on its own callback: it is a snapshot the model replaces
// wholesale, not an event. Migrating it (and the proxy/MCP hooks) onto the bus
// is a follow-up.
func (r *Runtime) onActions(a *devserver.DevActions) {
	// A restart builds a fresh DevActions with a fresh broker; drop the old
	// subscription before taking the new one.
	if r.unsubscribe != nil {
		r.unsubscribe()
	}
	// Output is excluded: process log lines reach the viewport through the
	// process manager's sinks (WithLogWriter / SetOutputSinks), not the bus.
	// Leaving them on this subscription let a restart's log burst overflow the
	// 16-slot buffer and drop the build_ok that clears the status bar, so a
	// finished build sat there spinning.
	events, cancel := a.Broker().Subscribe(devserver.EvOutput)
	r.unsubscribe = cancel
	go func() {
		// Ends when cancel closes the channel, i.e. on the next restart or
		// when the runtime shuts down.
		for evt := range events {
			r.program.Send(brokerEventMsg{evt: evt})
		}
	}()

	r.program.Send(actionsReadyMsg{runMake: a.RunMake})

	es := a.ErrorState()
	push := func() {
		r.program.Send(errorChangedMsg{rules: es.RuleNames()})
	}
	es.OnChange(push)
	push() // initial snapshot
}

// Start runs the bubbletea program. Blocks until the model returns
// tea.Quit (q / Ctrl+C inside the model, or an external Quit call).
func (r *Runtime) Start() error {
	_, err := r.program.Run()
	return err
}

// Quit asks the program to exit. Safe to call from any goroutine; if the
// program has already exited this is a no-op.
func (r *Runtime) Quit() {
	r.program.Quit()
}

// HotkeyActions exposes the underlying hotkey channel so the dev runner
// can react to q / Ctrl+C while parked outside Run() — e.g. waiting for
// a config fix, where bubbletea owns the keyboard but the runner-side
// loop has nothing else to select on.
func (r *Runtime) HotkeyActions() <-chan devserver.HotkeyAction {
	return r.hotkeys.Actions()
}

// Log writes a single line to the TUI viewport. Intended for the dev
// command's own status messages (config errors, "config changed,
// retrying...") that don't flow through the runner's slog handler.
func (r *Runtime) Log(line string) {
	r.program.Send(LogLineMsg(line))
}

// SetVersion sets the version label shown on the right of the status
// bar. Safe from any goroutine.
func (r *Runtime) SetVersion(label string) {
	r.program.Send(versionLabelMsg{label: label})
}

// SetVersionStatus updates the version indicator state and message.
func (r *Runtime) SetVersionStatus(status devserver.VersionStatus, msg string) {
	r.program.Send(versionStatusMsg{status: status, msg: msg})
}

// SetVersionUpdateIfOK promotes the indicator to VersionUpdate only when
// the current status is VersionOK. Returning a bool would require
// synchronous access to the model state; the message is always sent and
// the model applies the guard. The caller logs the "update available"
// line unconditionally.
func (r *Runtime) SetVersionUpdateIfOK(msg string) {
	r.program.Send(versionUpdateIfOKMsg{msg: msg})
}

// SetProxyURL publishes the actual reachable proxy URL to the model so
// it can render in the status bar. Wired via WithProxyURLHook so the
// runner calls it once the listener has bound. Safe from any goroutine.
func (r *Runtime) SetProxyURL(url string) {
	r.program.Send(proxyURLMsg{url: url})
}

// SetMCPStatus publishes the MCP gateway's state to the model's status-bar
// indicator. Wired via WithMCPStatusHook; fires at startup and on each
// M-toggle. Safe from any goroutine.
func (r *Runtime) SetMCPStatus(enabled bool, tools int) {
	r.program.Send(mcpStatusMsg{enabled: enabled, tools: tools})
}

// AppendMCPLog pushes one MCP request line to the model's dedicated MCP tab.
// Wired via WithMCPLogHook; fires on each gateway request. Safe from any
// goroutine.
func (r *Runtime) AppendMCPLog(line string) {
	r.program.Send(mcpLogMsg{line: line})
}

// Wait runs until ctx is done, then quits the program. Useful when the
// runner exits first (config reload exhausted, fatal error) and the TUI
// needs to be torn down too.
func (r *Runtime) Wait(ctx context.Context) {
	<-ctx.Done()
	r.Quit()
}
