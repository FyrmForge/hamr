# WebSocket — Session & Room-Based Real-Time Hub

`hamr/pkg/websocket` provides a session-based WebSocket hub with room routing, optional
subject (user) targeting, and typed event helpers with HTMX integration.

## Quick Start

```go
import "github.com/FyrmForge/hamr/pkg/websocket"
```

## Design

The hub uses a pure mutex design with no background event-loop goroutine. Connections
are indexed by session ID (1:1), optionally by subject ID (1:many), and by rooms
(many:many). Works without auth — session IDs default to random UUIDs.

## Creating a Hub

```go
hub := websocket.NewHub(
    websocket.WithLogger(logger),
)

// Mount the WebSocket endpoint
e.GET("/ws", hub.Handler())
```

### With auth integration

```go
hub := websocket.NewHub(
    websocket.WithSessionIDFunc(func(r *http.Request) string {
        cookie, _ := r.Cookie("session_token")
        return cookie.Value
    }),
    websocket.WithSubjectIDFunc(func(r *http.Request) string {
        return r.Header.Get("X-Subject-ID")
    }),
)
```

### Message handling

By default, the hub operates in server-push-only mode. To handle inbound messages:

```go
hub := websocket.NewHub(
    websocket.WithOnMessage(func(client *websocket.Client, msg []byte) {
        log.Printf("message from %s: %s", client.SessionID, msg)
    }),
)
```

## Sending Messages

### To a specific session

```go
hub.SendToSession(sessionID, []byte(`{"type":"notification","html":"<div>Hello</div>"}`))
```

### To a subject (all their sessions)

When auth has mapped session to subject via `WithSubjectIDFunc`:

```go
hub.SendToSubject(userID, data)
```

### To a room

```go
hub.SendToRoom("chat:general", data)
hub.SendToRoomExcept("chat:general", data, senderSessionID)
```

### Broadcast

```go
hub.Broadcast(data)
```

## Rooms

```go
hub.JoinRoom(client, "chat:general")
hub.LeaveRoom(client, "chat:general")
```

## Runtime Subject Association

Associate a session with a subject after the WebSocket connection is established (e.g.
after authentication completes post-connection):

```go
hub.AssociateSubject(sessionID, userID)
```

## Events

Structured event types for common patterns.

### Data-only event

Client handles via a registered JavaScript callback:

```go
event := websocket.NewEvent("notification", payload)
hub.SendToSession(id, event.JSON())
```

### HTML swap event

Swap HTML into a target element (works with client-side HTMX integration):

```go
event := websocket.NewHTMLEvent("update", "#user-count", "<span>42</span>")
hub.Broadcast(event.JSON())

// outerHTML replacement
event := websocket.NewOuterHTMLEvent("replace", "#banner", newBannerHTML)
```

### HTMX trigger event

Trigger an htmx event on a target element:

```go
event := websocket.NewTriggerEvent("refresh", "#notifications", "load")
hub.SendToSubject(userID, event.JSON())
```

## Emitter

A typed wrapper over Hub that accepts `*Event` values instead of raw bytes:

```go
emit := websocket.NewEmitter(hub)

emit.ToSession(sessionID, websocket.NewHTMLEvent("update", "#count", html))
emit.ToSubject(userID, websocket.NewEvent("notification", data))
emit.ToRoom("chat:general", websocket.NewHTMLEvent("message", "#messages", msgHTML))
emit.ToRoomExcept("chat:general", event, senderSessionID)
emit.Broadcast(websocket.NewTriggerEvent("refresh", "body", "reload"))
```

## Client Metadata

Each client carries arbitrary metadata:

```go
hub.WithOnMessage(func(client *websocket.Client, msg []byte) {
    client.Meta["last_seen"] = time.Now()
    rooms := client.Rooms // map[string]bool
})
```

## Hub Stats

```go
stats := hub.Stats()
fmt.Printf("clients=%d subjects=%d rooms=%d\n",
    stats.Clients, stats.Subjects, stats.Rooms)
```

## Cleanup

```go
hub.Close() // cancels context, waits for all pumps, clears maps
```

Safe to call multiple times.

## API Reference

```go
// Hub
func NewHub(opts ...HubOption) *Hub
func (h *Hub) Handler() echo.HandlerFunc
func (h *Hub) Close()
func (h *Hub) Stats() HubStats

// Hub options
func WithSessionIDFunc(fn func(r *http.Request) string) HubOption
func WithSubjectIDFunc(fn func(r *http.Request) string) HubOption
func WithOnMessage(fn func(*Client, []byte)) HubOption
func WithAcceptOptions(opts *ws.AcceptOptions) HubOption
func WithLogger(l *slog.Logger) HubOption

// Messaging
func (h *Hub) SendToSession(sessionID string, msg []byte)
func (h *Hub) SendToSubject(subjectID string, msg []byte)
func (h *Hub) SendToRoom(room string, msg []byte)
func (h *Hub) SendToRoomExcept(room string, msg []byte, exceptSessionID string)
func (h *Hub) Broadcast(msg []byte)

// Rooms
func (h *Hub) JoinRoom(c *Client, room string)
func (h *Hub) LeaveRoom(c *Client, room string)
func (h *Hub) AssociateSubject(sessionID, subjectID string)

// Events
type EventType string
type Event struct { ... }
func NewEvent(eventType EventType, payload any) *Event
func NewHTMLEvent(eventType EventType, target, html string) *Event
func NewOuterHTMLEvent(eventType EventType, target, html string) *Event
func NewTriggerEvent(eventType EventType, target, trigger string) *Event
func (e *Event) JSON() []byte

// Emitter
func NewEmitter(hub *Hub) *Emitter
func (e *Emitter) ToSession(sessionID string, event *Event)
func (e *Emitter) ToSubject(subjectID string, event *Event)
func (e *Emitter) ToRoom(room string, event *Event)
func (e *Emitter) ToRoomExcept(room string, event *Event, exceptSessionID string)
func (e *Emitter) Broadcast(event *Event)

// Client
type Client struct {
    SessionID string
    SubjectID string
    Rooms     map[string]bool
    Meta      map[string]any
}
```
