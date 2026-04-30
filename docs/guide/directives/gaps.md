# Gaps & Drifts

Where the codebase or docs don't match the directives. Tracked so we don't forget.

---

## HTML push WS events

`NewHTMLEvent` / `NewOuterHTMLEvent` push rendered HTML over the WS. Not the intended pattern (see [ws-trigger-refresh.md](ws-trigger-refresh.md)). Still documented in [../11-real-time.md](../11-real-time.md).

**Direction:** likely remove. Don't add new usages.

## Client-side WS → HTMX trigger wiring

`NewTriggerEvent` exists server-side, but the client JS that turns a WS event into an htmx trigger hasn't been located in the repo.

**Direction:** confirm whether app authors write this themselves or hamr ships it.
