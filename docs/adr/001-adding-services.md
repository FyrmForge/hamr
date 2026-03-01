# ADR-001: Adding Services to a HAMR Project

- **Status**: Accepted
- **Date**: 2026-02-28
- **Authors**: JamesTiberiusKirk

## Context

A HAMR project always starts as a monolith via `hamr new`. Over time, a project may need
additional services — a separate API with its own DB, a background worker, etc. HAMR should
make this easy without being prescriptive about project organization decisions.

## Decisions

- **Always start monolith** — `hamr new` never asks about architecture
- **Add services later** — `hamr add service <name>` scaffolds a new service
- **HAMR provides tools, not opinions on project layout** — where shared types live, whether to restructure the monolith, shared DB vs separate DB — all project decisions
- **Inter-service communication**: HTTP only, via `pkg/client/`
- **Auth propagation**: Gateway forwards subject ID via trusted header
- **Event bus**: Interface only for now (noop impl), NATS + PG LISTEN/NOTIFY post-MVP

## What HAMR provides

### Framework packages (`pkg/`)

#### `pkg/client/client.go` — Service HTTP client

HTTP client wrapper for inter-service calls:
- Auto-propagates `X-Request-ID` from incoming request context
- Auto-propagates `X-Subject-ID` (authenticated subject)
- JSON encode/decode helpers
- Configurable base URL, timeouts, retries via functional options

```go
billing := client.New(
    client.WithBaseURL(cfg.BillingServiceURL),
    client.WithTimeout(5 * time.Second),
)

// Context carries request ID + subject ID automatically
ctx := client.EchoContext(c)
invoice, err := client.Get[dto.Invoice](ctx, billing, "/invoices/"+id)
```

#### `pkg/middleware/trusted.go` — Trusted internal auth

For services behind the gateway/main service:
- `TrustedSubject()` middleware — reads `X-Subject-ID` header, sets in context
- Same `GetSubjectID(c)` API as session-based auth — handlers don't care how auth was resolved
- Only for internal network, never exposed publicly

#### `pkg/bus/bus.go` — Event bus interface

Contracts only, implementations post-MVP:
- `Publisher` interface: `Publish(ctx, subject string, data any) error`
- `Subscriber` interface: `Subscribe(subject string, handler func(ctx, []byte)) error`
- `NewNoopPublisher()` — for testing and when bus isn't needed
- Future: NATS implementation, PG LISTEN/NOTIFY implementation

### CLI command: `hamr add service <name>`

Scaffolds a new service into an existing HAMR project:
- Creates `cmd/<name>/main.go` — config, server start, graceful shutdown
- Creates `cmd/<name>/Dockerfile`
- Creates `internal/<name>/config/config.go`
- Creates `internal/<name>/handler/health.go` — health check endpoint
- Adds service to `docker/docker-compose.yaml`
- Adds Makefile targets: `run-<name>`, `build-<name>`

That's it. Doesn't touch existing code. Doesn't restructure anything. Doesn't decide where
shared types go or whether the DB is shared.

## Auth flow when services call each other

```
Browser -> Main service (has session cookie)
  1. Main service middleware validates session via SessionManager
  2. Main service handler calls billing service via pkg/client:
     GET http://billing:8082/invoices/456
     Headers:
       X-Request-ID: abc-123       (propagated)
       X-Subject-ID: user-789      (from session)
  3. Billing service TrustedSubject middleware reads X-Subject-ID
  4. Billing handler calls GetSubjectID(c) -- same API as main service
```

## What HAMR does NOT decide

These are project-level decisions, not framework decisions:
- Where shared types/DTOs live (`internal/shared/`? top-level `types/`? duplicated?)
- Whether to restructure the monolith's `internal/` when adding services
- Shared DB vs separate DB per service
- Communication direction (which service calls which)
- Whether to use the event bus or direct HTTP calls

## Changes to ADR-000

### New packages in repo structure

```
pkg/
├── client/
│   ├── client.go              # Service HTTP client with header propagation
│   └── echo.go                # Echo context bridge for header propagation
├── bus/
│   ├── bus.go                 # Publisher/Subscriber interfaces
│   └── noop.go                # No-op implementation
├── middleware/
│   ├── ... (existing)
│   ├── subject.go             # GetSubjectID/GetSubject shared helpers
│   └── trusted.go             # Trusted internal subject extraction from header
```

### Template Data Model addition

```go
// Used by `hamr add service`, not `hamr new`
type ServiceConfig struct {
    Name       string   // "billing"
    Module     string   // inherited from project's go.mod
    GoVersion  string   // inherited from project's go.mod
}
```

## Verification

- `hamr new testproject` still works as before (monolith, no changes)
- `cd testproject && hamr add service billing` scaffolds correctly
- New service compiles: `go build ./cmd/billing`
- New service starts and health check responds
- `pkg/client` propagates X-Request-ID and X-Subject-ID headers
- `TrustedSubject` middleware correctly sets subject in context
- docker-compose includes the new service
- Makefile has `run-billing` and `build-billing` targets
