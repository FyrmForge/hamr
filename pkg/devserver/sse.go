package devserver

import (
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

// SSEBroker manages SSE client connections and broadcasts events.
type SSEBroker struct {
	mu      sync.RWMutex
	clients map[uint64]chan SSEEvent
	nextID  atomic.Uint64
}

// NewSSEBroker creates a new SSE broker.
func NewSSEBroker() *SSEBroker {
	return &SSEBroker{
		clients: make(map[uint64]chan SSEEvent),
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

		// Send initial connected event.
		fmt.Fprintf(w, "event: connected\ndata: ok\n\n")
		flusher.Flush()

		for {
			select {
			case <-r.Context().Done():
				return
			case evt, ok := <-ch:
				if !ok {
					return
				}
				fmt.Fprintf(w, "event: %s\ndata: %s\n\n", evt.Type, evt.Data)
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
