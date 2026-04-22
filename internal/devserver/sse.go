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
}

// SSEBroker manages SSE client connections and broadcasts events.
type SSEBroker struct {
	mu         sync.RWMutex
	clients    map[uint64]chan SSEEvent
	nextID     atomic.Uint64
	configJSON string // pre-serialized config payload
}

// NewSSEBroker creates a new SSE broker. The provided watch rules, daemons, and
// docker compose entries are serialized once and sent to each client on connect
// as a "config" event. mailMockEnabled flags the dev panel to render the mail
// inbox shortcut.
func NewSSEBroker(rules []WatchRule, daemons []Daemon, dockerCompose []DockerCompose, mailMockEnabled bool) *SSEBroker {
	cfg := sseConfig{
		Rules:    make([]sseRule, len(rules)),
		Daemons:  make([]sseDaemon, len(daemons)),
		MailMock: mailMockEnabled,
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
	return &SSEBroker{
		clients:    make(map[uint64]chan SSEEvent),
		configJSON: string(data),
	}
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

		id := b.nextID.Add(1)
		ch := make(chan SSEEvent, 16)

		b.mu.Lock()
		b.clients[id] = ch
		b.mu.Unlock()

		defer func() {
			b.mu.Lock()
			delete(b.clients, id)
			b.mu.Unlock()
		}()

		// Send initial connected event, followed by config.
		_, _ = fmt.Fprintf(w, "event: connected\ndata: ok\n\n")
		_, _ = fmt.Fprintf(w, "event: config\ndata: %s\n\n", b.configJSON)
		flusher.Flush()

		for {
			select {
			case <-r.Context().Done():
				return
			case evt, ok := <-ch:
				if !ok {
					return
				}
				_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", evt.Type, evt.Data)
				flusher.Flush()
			}
		}
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
