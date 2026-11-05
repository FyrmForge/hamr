# WebSockets

Push live updates to the browser without polling — notifications, chat messages, dashboard counters. `pkg/websocket` provides a hub that routes events to a connection, a user or a room, and htmx applies them in the page.

**Package references:** [WebSocket](pkg/websocket.md)

> [!WARNING]
> **Single instance only.** The hub keeps connections in memory. With two or more replicas, a send only reaches clients connected to the same instance. A Postgres-backed hub for cross-instance routing is not implemented yet.

## Hub setup

Projects scaffolded with `--websocket` create the hub in `cmd/site/main.go`, pass it to the router as `deps.Hub`, and mount it in `internal/web/server.go`:

```go title="cmd/site/main.go"
import "github.com/FyrmForge/hamr/pkg/websocket"

hub := websocket.NewHub(websocket.WithLogger(log))
defer hub.Close()
```

```go title="internal/web/server.go"
e.GET("/ws", deps.Hub.Handler())
```

Each connection gets a random session ID (a UUID). Connections are indexed by session ID (1:1), optionally by subject ID (1:many), and by rooms (many:many). Routing happens under a lock when you call a send method; there is no background event loop.

The upgrade uses `github.com/coder/websocket`, which rejects cross-origin connections by default. To allow other origins, pass `websocket.WithAcceptOptions(&ws.AcceptOptions{OriginPatterns: ...})`.

## Linking connections to logged-in users

To send to a user (`SendToSubject`), the hub needs the subject ID when the connection opens. The subject ID func receives an `*http.Request`, but `auth.Load()` stores the subject in the Echo context. Copy it across with a small middleware, and mount `/ws` on the group that runs `auth.Load()` — this replaces the scaffold's `e.GET("/ws", ...)` on the root router:

```go title="internal/web/server.go"
type wsSubjectKey struct{}

// On the site group, after site.Use(auth.Load())
site.GET("/ws", deps.Hub.Handler(), auth.RequireAuth(),
    func(next echo.HandlerFunc) echo.HandlerFunc {
        return func(c echo.Context) error {
            r := c.Request()
            id := hamrmw.GetSubjectID(c)
            c.SetRequest(r.WithContext(context.WithValue(r.Context(), wsSubjectKey{}, id)))
            return next(c)
        }
    },
)
```

```go title="cmd/site/main.go"
hub := websocket.NewHub(
    websocket.WithLogger(log),
    websocket.WithSubjectIDFunc(func(r *http.Request) string {
        id, _ := r.Context().Value(wsSubjectKey{}).(string)
        return id
    }),
)
```

Don't use these as identity sources:

- **The `X-Subject-ID` request header.** The browser sends it, so any client can claim to be any user.
- **The session cookie as the session ID.** It puts the session token into hub keys and logs. The hub also allows one connection per session ID, so two tabs sharing a cookie evict each other, and auto-reconnect turns that into a loop. Keep the default random ID.

To link a subject after the connection is open (for example from an inbound message), call `hub.AssociateSubject(sessionID, userID)`.

## Sending events

Use the `Emitter` so you send typed `*Event` values instead of raw bytes:

```go
emit := websocket.NewEmitter(hub)

emit.ToSession(sessionID, event)            // one connection
emit.ToSubject(userID, event)               // every connection of one user
emit.ToRoom("chat:general", event)          // everyone in a room
emit.ToRoomExcept("chat:general", event, senderSessionID)
emit.Broadcast(event)                       // every connection
```

The `Hub` has the same methods taking `[]byte` (`SendToSession`, `SendToSubject`, `SendToRoom`, `SendToRoomExcept`, `Broadcast`), and they report delivery: `SendToSession` returns `false` if the session isn't connected or its buffer is full; the others return how many messages were dropped. Each client buffers 256 messages by default (`websocket.WithSendBufferSize`).

### Event types

`EventType` is a string; define your own constants.

```go
// Data only: the client handles the payload itself
websocket.NewEvent("notification", payload)

// Swap HTML into an element (innerHTML / outerHTML)
websocket.NewHTMLEvent("count", "#user-count", "<span>42</span>")
websocket.NewOuterHTMLEvent("banner", "#banner", bannerHTML)

// Fire an htmx event on an element, e.g. to re-run its hx-get
websocket.NewTriggerEvent("refresh", "#notifications", "refresh")
```

All of them serialise to the same JSON shape: `type`, `payload`, `target`, `swap`, `html`, `trigger`.

## Applying events in the browser

The scaffold ships `frontend/static/js/ws.js`, which provides `HamrWS`: a WebSocket with automatic reconnect and backoff. The layout doesn't load it, and it doesn't apply events on its own. Add `<script src={ StaticURL("js/ws.js") }></script>` to the layout before `main.js` (which runs without `defer`), then a handler to your page script (for example `main.js`):

```js title="frontend/static/js/main.js"
const ws = new HamrWS("/ws");

ws.onmessage = function (e) {
  const ev = JSON.parse(e.data);
  const el = ev.target && document.querySelector(ev.target);

  if (el && ev.html !== undefined) {
    htmx.swap(el, ev.html, { swapStyle: ev.swap || "innerHTML" });
  } else if (el && ev.trigger) {
    htmx.trigger(el, ev.trigger);
  } else {
    // data-only event: handle ev.type / ev.payload yourself
  }
};
```

`htmx.swap` processes the new HTML, so `hx-*` attributes inside it work. A trigger event pairs with an element that listens for it:

```html
<div id="notifications" hx-get="/notifications" hx-trigger="load, refresh"></div>
```

The HTML you send is swapped in as-is. Render it with templ (which escapes user content), never by concatenating strings.

## Handling inbound messages

By default the hub is push-only. To receive client messages:

```go
hub := websocket.NewHub(
    websocket.WithOnMessage(func(client *websocket.Client, msg []byte) {
        log.Info("ws message", "session", client.SessionID, "bytes", len(msg))
    }),
)
```

Inside the callback you have the `*Client`, which is what room membership needs:

```go
hub.JoinRoom(client, "chat:general")
hub.LeaveRoom(client, "chat:general")
```

`client.Meta` is a free-form map for per-connection state.

## Hub stats

```go
stats := hub.Stats()
log.Info("ws", "clients", stats.Clients, "subjects", stats.Subjects, "rooms", stats.Rooms)
```
