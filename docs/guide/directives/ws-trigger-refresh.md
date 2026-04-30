# WebSocket-Triggered HTMX Refresh

**Rule:** Server pushes a WS *trigger* event at a div. The div re-fetches itself with `hx-get`.

**Why:** One renderer per partial — the route the div hits. No drift between push payloads and handlers.

## Do

Server:

```go
emit.ToSubject(userID, websocket.NewTriggerEvent("notifications:changed", "#notifications", "refresh"))
```

Client:

```html
<div id="notifications" hx-get="/notifications" hx-trigger="refresh">...</div>
```

## Don't

- Push rendered HTML over the WS (`NewHTMLEvent` / `NewOuterHTMLEvent`) — see [gaps.md](gaps.md).
- Poll when a WS trigger works.

## See also

- `pkg/websocket/event.go` — `NewTriggerEvent`
- [../11-real-time.md](../11-real-time.md)
