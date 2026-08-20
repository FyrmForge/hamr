# Marked for Deletion

Features/code queued for removal. Add an entry, delete the code when ready, remove the entry.

## `hamr ai capture` (browser screenshot capture)

- **Code:** `internal/browsercapture/` (capture.go + capture_test.go), `internal/cli/cmd/capture.go`
- **Why:** Overlaps with Playwright MCP for the main use case (agent screenshots a URL). Unique bits — tiled capture, on-disk capture bundle, no-MCP usage — haven't proven worth the ~940 lines + go-rod dependency.
- **Before deleting:** drop go-rod from go.mod, grep repo for `browsercapture` / `ai capture` references (docs, guide, llm.txt).
