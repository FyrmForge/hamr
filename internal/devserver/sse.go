package devserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
)

// SSEEvent is a server-sent event.
type SSEEvent struct {
	Type string // event type (e.g., "reload", "css")
	Data string // event data
}

// Event types broadcast on the broker. The broker is the dev server's one
// event bus: every consumer (browser dev panel, TUI, MCP) subscribes to it
// rather than being wired its own callback. See docs/adr/004-dev-event-bus.md.
//
// Always broadcast with one of these constants — a free string here is a typo
// no compiler catches, and the mismatch only shows up as a consumer that
// silently never updates.
const (
	EvBuilding    = "building"     // Data: rule name
	EvBuildOK     = "build_ok"     // Data: rule name
	EvBuildError  = "build_error"  // Data: JSON {rule, output}
	EvOutput      = "output"       // Data: JSON {rule, text, color}
	EvReload      = "reload"       // Data: reload mode
	EvShutdown    = "shutdown"     // Data: ""
	EvDarkFilter  = "dark_filter"  // Data: "on" / "off"
	EvRestarting  = "restarting"   // Data: ""
	EvMakeStart   = "make_start"   // Data: target
	EvMakeDone    = "make_done"    // Data: "<target> <exit code>"
	EvComposeUp   = "compose_up"   // Data: compose entry name
	EvComposeOK   = "compose_ok"   // Data: compose entry name
	EvTunnelStart = "tunnel_start" // Data: "starting" / "stopping"
	EvTunnelUp    = "tunnel_up"    // Data: public URL
	EvTunnelDown  = "tunnel_down"  // Data: "" (off, or a start that failed)
	EvStripeMode  = "stripe_mode"  // Data: "mock" / "listen" / "switching"
)

// sseRule is a watch rule serialized for the config SSE event.
type sseRule struct {
	Name    string   `json:"name"`
	Watch   []string `json:"watch,omitempty"`
	Cmd     string   `json:"cmd,omitempty"`
	Run     string   `json:"run,omitempty"`
	Reload  string   `json:"reload,omitempty"`
	Depends []string `json:"depends,omitempty"`
}

// sseDaemon is a daemon serialized for the config SSE event.
type sseDaemon struct {
	Name string `json:"name"`
	Cmd  string `json:"cmd"`
}

// sseDockerCompose is a docker compose entry serialized for the config SSE event.
type sseDockerCompose struct {
	Name     string   `json:"name"`
	File     string   `json:"file"`
	Services []string `json:"services,omitempty"`
}

// sseConfig is the payload for the "config" SSE event.
type sseConfig struct {
	Rules         []sseRule          `json:"rules"`
	Daemons       []sseDaemon        `json:"daemons"`
	DockerCompose []sseDockerCompose `json:"docker_compose,omitempty"`
	// MailMock indicates the /__hamr/mail inbox UI is mounted. The dev panel
	// uses this to decide whether to render the mail-inbox shortcut.
	MailMock bool `json:"mail_mock,omitempty"`
	// SMSMock indicates the /__hamr/sms inbox UI is mounted. The dev panel
	// uses this to decide whether to render the SMS-inbox shortcut.
	SMSMock bool `json:"sms_mock,omitempty"`
	// StripeMock indicates the /__hamr/stripe dashboard UI is mounted. The
	// dev panel uses this to render the Stripe-mock shortcut.
	StripeMock bool `json:"stripe_mock,omitempty"`
	// ConsoleCapture indicates the /__hamr/console WS endpoint is mounted.
	// The injected reload script reads this on the SSE config event and
	// only patches window.console + opens the upstream WS when true. When
	// false (capture disabled), the script skips all of it — zero overhead.
	ConsoleCapture bool `json:"console_capture,omitempty"`
}

// SSEBroker manages SSE client connections and broadcasts events.
type SSEBroker struct {
	mu         sync.RWMutex
	clients    map[uint64]chan SSEEvent
	nextID     atomic.Uint64
	configJSON string // pre-serialized config payload
	// tunnelConfigJSON is configJSON for tunnel visitors: names and mock
	// flags only (no commands, globs or compose files), console capture off.
	tunnelConfigJSON string
	// darkFilter is the live state of the dark comfort filter over the
	// proxied site. It lives here rather than in the pre-serialized config
	// so a tab connecting after a runtime toggle gets the current state, not
	// the [dev].dark_filter seed. Flipped by DevActions.handleDark.
	darkFilter atomic.Bool
}

// NewSSEBroker creates a new SSE broker. The provided watch rules, daemons, and
// docker compose entries are serialized once and sent to each client on connect
// as a "config" event. The mock flags decide which mock-shortcut buttons
// the dev panel renders. consoleCaptureEnabled toggles the browser-console
// transport client-side: true tells the injected reload script to patch
// console + open /__hamr/console; false tells it to do nothing. darkFilter
// seeds the dark comfort filter state (see SSEBroker.darkFilter).
func NewSSEBroker(rules []WatchRule, daemons []Daemon, dockerCompose []DockerCompose, mailMockEnabled, smsMockEnabled, stripeMockEnabled, consoleCaptureEnabled, darkFilter bool) *SSEBroker {
	cfg := sseConfig{
		Rules:          make([]sseRule, len(rules)),
		Daemons:        make([]sseDaemon, len(daemons)),
		MailMock:       mailMockEnabled,
		SMSMock:        smsMockEnabled,
		StripeMock:     stripeMockEnabled,
		ConsoleCapture: consoleCaptureEnabled,
	}
	for i, r := range rules {
		cfg.Rules[i] = sseRule{
			Name:    r.Name,
			Watch:   []string(r.Watch),
			Cmd:     r.Cmd,
			Run:     r.Run,
			Reload:  string(r.Reload),
			Depends: []string(r.Depends),
		}
	}
	for i, d := range daemons {
		cfg.Daemons[i] = sseDaemon{Name: d.Name, Cmd: d.Cmd}
	}
	if len(dockerCompose) > 0 {
		cfg.DockerCompose = make([]sseDockerCompose, len(dockerCompose))
		for i, dc := range dockerCompose {
			cfg.DockerCompose[i] = sseDockerCompose{
				Name:     dc.Name,
				File:     dc.File,
				Services: dc.Services,
			}
		}
	}
	data, _ := json.Marshal(cfg)

	pub := cfg
	pub.ConsoleCapture = false // /__hamr/console is blocked through the tunnel
	pub.Rules = make([]sseRule, len(cfg.Rules))
	for i, r := range cfg.Rules {
		pub.Rules[i] = sseRule{Name: r.Name, Reload: r.Reload}
	}
	pub.Daemons = make([]sseDaemon, len(cfg.Daemons))
	for i, d := range cfg.Daemons {
		pub.Daemons[i] = sseDaemon{Name: d.Name}
	}
	if cfg.DockerCompose != nil {
		pub.DockerCompose = make([]sseDockerCompose, len(cfg.DockerCompose))
		for i, dc := range cfg.DockerCompose {
			pub.DockerCompose[i] = sseDockerCompose{Name: dc.Name}
		}
	}
	pubData, _ := json.Marshal(pub)

	b := &SSEBroker{
		clients:          make(map[uint64]chan SSEEvent),
		configJSON:       string(data),
		tunnelConfigJSON: string(pubData),
	}
	b.darkFilter.Store(darkFilter)
	return b
}

// Handler returns an http.HandlerFunc that serves SSE connections.
func (b *SSEBroker) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		ch, unsubscribe := b.Subscribe()
		defer unsubscribe()

		tunneled := isTunnelRequest(r)
		configJSON := b.configJSON
		if tunneled {
			configJSON = b.tunnelConfigJSON
		}

		// Send initial connected event, followed by config.
		_, _ = fmt.Fprintf(w, "event: connected\ndata: ok\n\n")
		_, _ = fmt.Fprintf(w, "event: config\ndata: %s\n\n", configJSON)
		// Only sent when on: the client defaults to off, so the common case
		// costs no frame.
		if b.darkFilter.Load() {
			_, _ = fmt.Fprintf(w, "event: dark_filter\ndata: on\n\n")
		}
		flusher.Flush()

		for {
			select {
			case <-r.Context().Done():
				return
			case evt, ok := <-ch:
				if !ok {
					return
				}
				if tunneled {
					if evt, ok = redactForTunnel(evt); !ok {
						continue
					}
				}
				_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", evt.Type, evt.Data)
				flusher.Flush()
			}
		}
	}
}

// onOff renders a bool as the "on"/"off" wire form used by the dark_filter
// event.
func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

// Subscribe registers a consumer and returns its event channel plus a cancel
// func that removes and closes it. The channel is buffered (16) and events are
// dropped for a slow consumer, exactly as for HTTP clients — a stalled TUI must
// not wedge a build.
//
// In-process consumers (the TUI runtime) use this directly; Handler uses it for
// each browser connection. Cancel is idempotent and must be called, or the
// consumer's slot leaks for the life of the broker.
//
// Closing under the write lock is safe: Broadcast sends while holding the read
// lock, so no send can be in flight when the close happens.
func (b *SSEBroker) Subscribe() (<-chan SSEEvent, func()) {
	id := b.nextID.Add(1)
	ch := make(chan SSEEvent, 16)

	b.mu.Lock()
	b.clients[id] = ch
	b.mu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.clients, id)
			close(ch)
			b.mu.Unlock()
		})
	}
}

// Broadcast sends an event to all connected clients. Non-blocking: if a
// client's buffer is full, the event is dropped for that client.
func (b *SSEBroker) Broadcast(evt SSEEvent) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, ch := range b.clients {
		select {
		case ch <- evt:
		default:
			// Client buffer full, drop event.
		}
	}
}

// ClientCount returns the number of connected SSE clients.
func (b *SSEBroker) ClientCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.clients)
}
