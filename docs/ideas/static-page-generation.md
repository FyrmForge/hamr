# Static Page/Resource Generation

## Problem

Many pages in a typical web app are fully static or rarely change (marketing pages, docs, about pages, terms of service, etc.). Serving these through the full handler pipeline on every request wastes CPU and adds latency when the output is always identical.

## Proposal

A build-time system that renders eligible pages/resources into static files, which are then served directly without going through the handler chain at runtime.

### Core System

- A `hamr generate` (or `make static`) build step that:
  1. Scans registered routes for ones marked as static-eligible
  2. Renders each to its output (HTML, JSON, etc.)
  3. Writes the output to a `static/generated/` directory (or configurable path)
  4. At runtime, `StaticPage` routes serve these paths directly from disk, falling back to the handler otherwise

- Route-level opt-in via a marker, e.g.:
  ```go
  app.GET("/about", aboutHandler, hamr.Static())
  // or
  app.StaticPage("/about", aboutHandler)
  ```

- The generated output is treated as a build artifact — committed or gitignored depending on the project's preference.

### Regeneration

- Full regeneration on every build by default
- Optional: content-hash based cache to skip unchanged pages
- Optional: a `hamr regenerate` command for on-demand rebuild without a full build cycle

### What Counts as "Static"

A page is static-eligible when its output depends only on:
- Compile-time constants
- Embedded files / templates
- Configuration that doesn't change between requests

A page is **not** static-eligible when it depends on:
- Request context (user session, cookies, headers)
- Database queries
- Time-sensitive data
- Query parameters or path parameters

---

## Stretch Goal: Automatic Static Detection

Automatically detect whether a handler produces static output, removing the need for manual annotation.

### Approach Ideas

1. **Signature analysis**: If a handler's dependencies (via DI or function params) include only the template engine and static config — no DB, no request context, no session — flag it as a static candidate.

2. **Dry-run diffing**: During a test/build phase, invoke the handler multiple times with varying synthetic request contexts. If the output is byte-identical across invocations, it's static.

3. **Taint tracking (advanced)**: Track whether any request-scoped value flows into the response. If not, the page is static. This is the most precise but hardest to implement.

4. **Hybrid**: Use signature analysis as a fast first pass, then confirm with dry-run diffing. Surface results as suggestions rather than automatic conversion — let the developer confirm.

### Output

- A report during build: `[static-detect] /about -> STATIC (no request-scoped deps)`
- Optional: auto-apply the static marker if confidence is high enough
- A flag to promote suggestions: `hamr generate --auto-detect`

---

## Open Questions

- Should generated files live inside the project or in a temp build directory?
- How does this interact with HTMX partial responses — can partials be static too?
- Cache invalidation strategy for pages that are *mostly* static but change on deploy?
- Should this integrate with CDN/reverse-proxy cache headers as an alternative to file generation?
