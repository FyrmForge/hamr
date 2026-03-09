# Real-Time

Push live updates to the browser without polling — notifications, chat messages, dashboard counters. This guide covers the WebSocket hub, rooms, subject targeting, and HTMX integration.

**Package references:** [WebSocket](pkg/websocket.md)

---

## Hub Setup

Create a hub and mount the WebSocket endpoint:

```go
import "github.com/FyrmForge/hamr/pkg/websocket"

hub := websocket.NewHub(
    websocket.WithLogger(logger),
)

e.GET("/ws", hub.Handler())
defer hub.Close()
```

The hub handles all routing under a lock when you call send methods, rather than running a separate goroutine with a channel-based event loop. Connections are indexed by session ID (1:1), optionally by subject ID (1:many), and by rooms (many:many).

### With Auth Integration

Map WebSocket connections to authenticated users:

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

Without auth, session IDs default to random UUIDs.

---

## Sending Messages

### To a Specific Session

```go
hub.SendToSession(sessionID, []byte(`{"type":"notification"}`))
```

### To a Subject (All Their Sessions)

```go
hub.SendToSubject(userID, data)
```

### To a Room

```go
hub.SendToRoom("chat:general", data)
hub.SendToRoomExcept("chat:general", data, senderSessionID)
```

### Broadcast

```go
hub.Broadcast(data)
```

---

## Rooms

```go
hub.JoinRoom(client, "chat:general")
hub.LeaveRoom(client, "chat:general")
```

---

## Typed Events

Structured event types for common patterns:

### Data Event

```go
event := websocket.NewEvent("notification", payload)
hub.SendToSession(id, event.JSON())
```

### HTML Swap Event

Swap HTML into a target element (HTMX integration):

```go
event := websocket.NewHTMLEvent("update", "#user-count", "<span>42</span>")
hub.Broadcast(event.JSON())

// outerHTML replacement
event := websocket.NewOuterHTMLEvent("replace", "#banner", newBannerHTML)
```

### HTMX Trigger Event

Trigger an htmx event on a target element:

```go
event := websocket.NewTriggerEvent("refresh", "#notifications", "load")
hub.SendToSubject(userID, event.JSON())
```

---

## Emitter

For type safety, the Emitter wraps the Hub so you pass `*Event` values instead of raw `[]byte` — no risk of sending malformed JSON:

```go
emit := websocket.NewEmitter(hub)

emit.ToSession(sessionID, websocket.NewHTMLEvent("update", "#count", html))
emit.ToSubject(userID, websocket.NewEvent("notification", data))
emit.ToRoom("chat:general", websocket.NewHTMLEvent("message", "#messages", msgHTML))
emit.Broadcast(websocket.NewTriggerEvent("refresh", "body", "reload"))
```

---

## Handling Inbound Messages

By default, the hub operates in server-push-only mode. To handle client messages:

```go
hub := websocket.NewHub(
    websocket.WithOnMessage(func(client *websocket.Client, msg []byte) {
        log.Printf("message from %s: %s", client.SessionID, msg)
    }),
)
```

---

## Runtime Subject Association

Associate a session with a subject after the WebSocket connection is established:

```go
hub.AssociateSubject(sessionID, userID)
```

---

## Hub Stats

```go
stats := hub.Stats()
fmt.Printf("clients=%d subjects=%d rooms=%d\n",
    stats.Clients, stats.Subjects, stats.Rooms)
```

---

## Next Steps

- [Testing](12-testing.md) — E2E browser testing
- [Background Jobs](10-background-jobs.md) — Triggering WebSocket pushes from scheduled tasks
