# Typed Routes with Built-in OpenAPI

Date: 2026-07-27

Status: idea / not started

## The idea

Let a handler declare its request and response *types*, and have hamr do three
things from those types in one registration call:

1. register the route
2. bind + validate the request, marshal the response
3. reflect the OpenAPI spec

The types are the single source of truth, so the published spec can never drift
from what the handler actually accepts and returns.

## Prior art

Two Go libraries already do exactly this — worth reading before building anything:

- **fuego** (`go-fuego/fuego`)
- **huma** (`danielgtaylor/huma`)

Both are **runtime reflection + generics** (not codegen, not interfaces). The
handler itself is typed:

```go
// fuego
fuego.Post(s, "/envs", func(c fuego.ContextWithBody[EnvIn]) (EnvOut, error) { ... })

// huma
huma.Register(api, op, func(ctx, *EnvIn) (*EnvOut, error) { ... })
```

The framework reads the generic type params via reflection at registration and
assembles the spec live at startup. No `.go` files generated; no interface to
implement.

## Working reference in the wild

stackr already runs a **half-measure** of this on top of `swaggest/openapi-go`.
See `internal/api/v1/v1.go` — the `op[Req, Resp]()` helper:

```go
op[EnvIn, EnvOut](a, g, POST, "/projects/:id/envs", scope, "Create environment", a.createEnv)
```

One call registers the echo route AND reflects the spec entry from `EnvIn`/`EnvOut`
struct tags (`json:`, `path:`, `query:`, `required:`). Spec served live at
`/api/openapi.json`; a `make openapi` target dumps it to a file for codegen.

**Why it's only half:** the handler is still a plain `echo.HandlerFunc`, so each
one still hand-writes `c.Bind(&in)` and `c.JSON(out)`. The full pattern (fuego/huma)
types the handler signature itself and deletes that boilerplate everywhere.

## The real design decision

Not "copy stackr's `op()`" — it's **should hamr adopt the typed-handler signature
as a first-class way to declare routes.** That's framework-defining, not a helper.

Keep mechanism and policy separate so hamr stays app-agnostic:

- **hamr owns (mechanism):** typed route → bind/marshal → spec; status inference
  by method; error-response shapes; operationId derivation; security-scheme wiring.
- **the app owns (policy):** auth model, capability scopes, URL prefixes, error
  body shape. Expose these as per-op middleware + metadata hooks, don't bake them in.

If hamr swallows the policy bits it becomes stackr-shaped and no other app wants it.

## Open questions

- Adopt fuego/huma directly, or build a thin hamr-native layer over `swaggest`?
- How does the typed handler coexist with hamr's existing `echo.HandlerFunc` routes
  (opt-in per route, or a new registration surface)?
- Where do per-op auth hooks attach cleanly?
