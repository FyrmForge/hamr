# Logging — Context-Aware Structured Logging

`hamr/pkg/logging` wraps `log/slog` with environment-aware handler selection and
context propagation. JSON in production, coloured tint output in development.

## Quick Start

```go
import "github.com/FyrmForge/hamr/pkg/logging"
```

## Creating a Logger

```go
logger := logging.New(true)  // production: JSON to stdout
logger := logging.New(false) // development: coloured to stderr
```

Production mode uses `slog.NewJSONHandler` at Info level. Development mode uses
`tint.NewHandler` at Debug level with `time.Kitchen` timestamps.

## Context Propagation

Thread loggers through `context.Context` so request-scoped attributes propagate
automatically across function boundaries.

### Store a logger in context

```go
ctx = logging.WithLogger(ctx, logger)
```

### Retrieve from context

```go
l := logging.FromContext(ctx)
l.Info("processing request")
```

Returns `slog.Default()` if no logger is stored — always safe to call.

### Add attributes

```go
ctx = logging.With(ctx, "user_id", userID, "request_id", reqID)
```

Subsequent calls to `FromContext(ctx)` return a logger with those attributes attached.

## Integration with Middleware

The `RequestID` middleware automatically stores a logger with `request_id` and
`client_ip` in the request context. Handlers and services that use
`logging.FromContext(ctx)` get those attributes for free.

```go
func (h *Handler) GetUser(c echo.Context) error {
    ctx := c.Request().Context()
    log := logging.FromContext(ctx)
    log.Info("fetching user", "id", id)  // includes request_id, client_ip
    // ...
}
```

## API Reference

```go
func New(production bool) *slog.Logger
func FromContext(ctx context.Context) *slog.Logger
func WithLogger(ctx context.Context, l *slog.Logger) context.Context
func With(ctx context.Context, args ...any) context.Context
```
