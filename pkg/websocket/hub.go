package websocket

import (
	"context"
	"log/slog"
	"net/http"
	"sync"

	ws "github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// HubOption configures a Hub.
type HubOption func(*Hub)

// WithSessionIDFunc sets the function used to derive a session ID from the
// HTTP request. The default generates a random UUID.
func WithSessionIDFunc(fn func(r *http.Request) string) HubOption {
	return func(h *Hub) { h.sessionIDFunc = fn }
}

// WithSubjectIDFunc sets the function used to derive a subject (user) ID from
// the HTTP request. When nil (the default), no subject tracking occurs.
func WithSubjectIDFunc(fn func(r *http.Request) string) HubOption {
	return func(h *Hub) { h.subjectIDFunc = fn }
}

// WithOnMessage registers a callback invoked for every inbound client message.
// When nil (the default), the hub operates in server-push-only mode.
func WithOnMessage(fn func(*Client, []byte)) HubOption {
	return func(h *Hub) { h.onMessage = fn }
}

// WithAcceptOptions sets the websocket.AcceptOptions used when upgrading.
func WithAcceptOptions(opts *ws.AcceptOptions) HubOption {
	return func(h *Hub) { h.acceptOpts = opts }
}

// WithLogger sets the logger for the hub. Defaults to slog.Default().
func WithLogger(l *slog.Logger) HubOption {
	return func(h *Hub) { h.logger = l }
}

// Hub manages WebSocket clients with session, subject, and room routing.
// It uses a pure mutex design with no background event-loop goroutine.
type Hub struct {
	mu       sync.RWMutex
	clients  map[string]*Client          // sessionID → client (1:1)
	subjects map[string]map[*Client]bool // subjectID → clients (1:many)
	rooms    map[string]map[*Client]bool // room → clients (many:many)

	sessionIDFunc func(r *http.Request) string
	subjectIDFunc func(r *http.Request) string
	onMessage     func(*Client, []byte)
	acceptOpts    *ws.AcceptOptions
	logger        *slog.Logger

	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	closeOnce sync.Once
}

// NewHub creates a Hub with the given options.
func NewHub(opts ...HubOption) *Hub {
	ctx, cancel := context.WithCancel(context.Background())
	h := &Hub{
		clients:  make(map[string]*Client),
		subjects: make(map[string]map[*Client]bool),
		rooms:    make(map[string]map[*Client]bool),
		ctx:      ctx,
		cancel:   cancel,
	}
	for _, o := range opts {
		o(h)
	}
	if h.sessionIDFunc == nil {
		h.sessionIDFunc = func(_ *http.Request) string { return uuid.NewString() }
	}
	if h.logger == nil {
		h.logger = slog.Default()
	}
	return h
}

// Handler returns an Echo handler that upgrades HTTP connections to WebSocket.
func (h *Hub) Handler() echo.HandlerFunc {
	return func(c echo.Context) error {
		conn, err := ws.Accept(c.Response(), c.Request(), h.acceptOpts)
		if err != nil {
			return err
		}

		sessionID := h.sessionIDFunc(c.Request())
		var subjectID string
		if h.subjectIDFunc != nil {
			subjectID = h.subjectIDFunc(c.Request())
		}

		client := &Client{
			SessionID: sessionID,
			SubjectID: subjectID,
			Rooms:     make(map[string]bool),
			Meta:      make(map[string]any),
			hub:       h,
			conn:      conn,
			send:      make(chan []byte, sendBufferSize),
		}

		h.register(client)

		h.wg.Add(1)
		go func() {
			defer h.wg.Done()
			client.writePump(h.ctx)
		}()

		client.readPump(h.ctx, h.onMessage)
		return nil
	}
}

// Close cancels the hub context, waits for all pumps to exit, and clears maps.
// Safe to call multiple times.
func (h *Hub) Close() {
	h.closeOnce.Do(func() {
		h.cancel()
		h.wg.Wait()

		h.mu.Lock()
		clear(h.clients)
		clear(h.subjects)
		clear(h.rooms)
		h.mu.Unlock()
	})
}

// --- Sending ---

// SendToSession sends a message to the client with the given session ID.
func (h *Hub) SendToSession(sessionID string, msg []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if c, ok := h.clients[sessionID]; ok {
		h.safeSend(c, msg)
	}
}

// SendToSubject sends a message to all clients associated with a subject ID.
func (h *Hub) SendToSubject(subjectID string, msg []byte) {
	if subjectID == "" {
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	for c := range h.subjects[subjectID] {
		h.safeSend(c, msg)
	}
}

// SendToRoom sends a message to all clients in a room.
func (h *Hub) SendToRoom(room string, msg []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for c := range h.rooms[room] {
		h.safeSend(c, msg)
	}
}

// SendToRoomExcept sends a message to all clients in a room except one session.
func (h *Hub) SendToRoomExcept(room string, msg []byte, exceptSessionID string) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for c := range h.rooms[room] {
		if c.SessionID == exceptSessionID {
			continue
		}
		h.safeSend(c, msg)
	}
}

// Broadcast sends a message to every connected client.
func (h *Hub) Broadcast(msg []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, c := range h.clients {
		h.safeSend(c, msg)
	}
}

// --- Room management ---

// JoinRoom adds a client to a room.
func (h *Hub) JoinRoom(c *Client, room string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.rooms[room] == nil {
		h.rooms[room] = make(map[*Client]bool)
	}
	h.rooms[room][c] = true
	c.Rooms[room] = true
}

// LeaveRoom removes a client from a room.
func (h *Hub) LeaveRoom(c *Client, room string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if members, ok := h.rooms[room]; ok {
		delete(members, c)
		if len(members) == 0 {
			delete(h.rooms, room)
		}
	}
	delete(c.Rooms, room)
}

// --- Runtime subject association ---

// AssociateSubject associates a session with a subject ID at runtime (e.g.
// after authentication completes post-connection).
func (h *Hub) AssociateSubject(sessionID, subjectID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	c, ok := h.clients[sessionID]
	if !ok {
		return
	}

	// Remove from old subject group if present.
	if c.SubjectID != "" {
		if set, exists := h.subjects[c.SubjectID]; exists {
			delete(set, c)
			if len(set) == 0 {
				delete(h.subjects, c.SubjectID)
			}
		}
	}

	c.SubjectID = subjectID
	if subjectID != "" {
		if h.subjects[subjectID] == nil {
			h.subjects[subjectID] = make(map[*Client]bool)
		}
		h.subjects[subjectID][c] = true
	}
}

// --- Observability ---

// HubStats contains hub connection counts.
type HubStats struct {
	Clients  int
	Subjects int
	Rooms    int
}

// Stats returns current hub connection counts.
func (h *Hub) Stats() HubStats {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return HubStats{
		Clients:  len(h.clients),
		Subjects: len(h.subjects),
		Rooms:    len(h.rooms),
	}
}

// --- internal helpers ---

func (h *Hub) register(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.clients[c.SessionID] = c
	if c.SubjectID != "" {
		if h.subjects[c.SubjectID] == nil {
			h.subjects[c.SubjectID] = make(map[*Client]bool)
		}
		h.subjects[c.SubjectID][c] = true
	}
}

func (h *Hub) unregister(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.clients[c.SessionID]; !ok {
		return
	}

	delete(h.clients, c.SessionID)

	if c.SubjectID != "" {
		if set, ok := h.subjects[c.SubjectID]; ok {
			delete(set, c)
			if len(set) == 0 {
				delete(h.subjects, c.SubjectID)
			}
		}
	}

	for room := range c.Rooms {
		if members, ok := h.rooms[room]; ok {
			delete(members, c)
			if len(members) == 0 {
				delete(h.rooms, room)
			}
		}
	}

	close(c.send)
}

// safeSend performs a non-blocking send to the client's send channel.
// Drops the message and logs a warning if the buffer is full.
func (h *Hub) safeSend(c *Client, msg []byte) {
	select {
	case c.send <- msg:
	default:
		h.logger.Warn("websocket: send buffer full, dropping message",
			"session_id", c.SessionID,
		)
	}
}
