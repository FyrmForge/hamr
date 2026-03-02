# Client — Inter-Service HTTP Client

`hamr/pkg/client` provides a type-safe HTTP client for inter-service communication. It
automatically propagates `X-Request-ID` and `X-Subject-ID` headers from the incoming
request context, ensuring traceability and auth propagation across service boundaries.

## Quick Start

```go
import "github.com/FyrmForge/hamr/pkg/client"
```

## Creating a Client

```go
billing := client.New(
    client.WithBaseURL("http://billing:8082"),
    client.WithTimeout(5*time.Second),
)
```

Default timeout is 30 seconds. You can also supply a custom `*http.Client`:

```go
billing := client.New(
    client.WithBaseURL("http://billing:8082"),
    client.WithHTTPClient(customHTTPClient),
)
```

## Generic HTTP Methods

All methods JSON-encode the request body and JSON-decode the response into the type
parameter:

```go
// GET
invoice, err := client.Get[dto.Invoice](ctx, billing, "/invoices/123")

// POST
created, err := client.Post[dto.Invoice](ctx, billing, "/invoices", newInvoice)

// PUT
updated, err := client.Put[dto.Invoice](ctx, billing, "/invoices/123", changes)

// DELETE
result, err := client.Delete[dto.Result](ctx, billing, "/invoices/123")
```

Non-2xx responses return a `*ResponseError` with the status code and body.

## Header Propagation

The client automatically propagates `X-Request-ID` and `X-Subject-ID` from the
context. This means downstream services see the same request trace and authenticated
subject.

### From Echo handlers

Use `EchoContext` to extract headers from the incoming Echo request:

```go
func (h *Handler) GetInvoice(c echo.Context) error {
    svcCtx := client.EchoContext(c)
    invoice, err := client.Get[dto.Invoice](svcCtx, h.billing, "/invoices/"+id)
    // ...
}
```

`EchoContext` reads `X-Request-ID` (or the `request_id` context key) and
`X-Subject-ID` (via `middleware.GetSubjectID`) and attaches them to the returned
context.

### Manual context setup

For non-Echo code (background jobs, CLI tools):

```go
ctx := context.Background()
ctx = client.WithRequestID(ctx, "job-abc-123")
ctx = client.WithSubjectID(ctx, "system")
result, err := client.Get[dto.Status](ctx, billing, "/health")
```

## Raw Requests

For non-JSON endpoints or custom handling, use `Do` directly:

```go
resp, err := billing.Do(ctx, http.MethodGet, "/health", nil)
if err != nil {
    return err
}
defer resp.Body.Close()
```

## Error Handling

Non-2xx responses from the generic methods return a `*ResponseError`:

```go
invoice, err := client.Get[dto.Invoice](ctx, billing, "/invoices/999")
if err != nil {
    var respErr *client.ResponseError
    if errors.As(err, &respErr) {
        fmt.Printf("status: %d, body: %s\n", respErr.StatusCode, respErr.Body)
    }
}
```

## API Reference

```go
// Client
type Client struct { ... }
type Option func(*Client)
func New(opts ...Option) *Client
func WithBaseURL(url string) Option
func WithTimeout(d time.Duration) Option
func WithHTTPClient(hc *http.Client) Option
func (c *Client) Do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error)

// Generic methods
func Get[T any](ctx context.Context, c *Client, path string) (T, error)
func Post[T any](ctx context.Context, c *Client, path string, body any) (T, error)
func Put[T any](ctx context.Context, c *Client, path string, body any) (T, error)
func Delete[T any](ctx context.Context, c *Client, path string) (T, error)

// Context propagation
func WithRequestID(ctx context.Context, id string) context.Context
func WithSubjectID(ctx context.Context, id string) context.Context
func EchoContext(c echo.Context) context.Context

// Error type
type ResponseError struct {
    StatusCode int
    Status     string
    Body       []byte
}
```
