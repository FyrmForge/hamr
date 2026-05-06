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
	dockerSinks map[string]*DockerSink
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
	sink := NewSink()
	// The run-overlay's `make <target>` output flows through the same
	// hamr Sink as the runner's slog handler, so a single tab carries
	// every line tagged for the user (slog lines from hamr itself,
	// `[make:<target>]`-prefixed lines from on-demand make runs).
	model.SetMakeOutput(sink)

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
		dockerSinks: make(map[string]*DockerSink),
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
	)
}

// RegisterDockerStacks publishes the ordered list of compose-entry
// names to the model (so Tab cycles through them and the status bar
// labels each tab) and returns the per-entry io.Writer map the runner
// should pass to WithDockerLogSinks. Sinks are created lazily and
// cached, so a config reload that re-registers the same name reuses
// the same DockerSink (its already-buffered lines stay buffered until
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

// onActions runs on the runner goroutine; it subscribes to error-state
// changes so the status bar updates without polling.
func (r *Runtime) onActions(a *devserver.DevActions) {
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

// Wait runs until ctx is done, then quits the program. Useful when the
// runner exits first (config reload exhausted, fatal error) and the TUI
// needs to be torn down too.
func (r *Runtime) Wait(ctx context.Context) {
	<-ctx.Done()
	r.Quit()
}
