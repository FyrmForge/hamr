# Respond — Content-Negotiated HTTP Responses

`hamr/pkg/respond` provides content-negotiated HTTP response helpers. It detects htmx
requests, negotiates between HTML and JSON responses, and renders templ components or
JSON payloads via Echo.

## Quick Start

```go
import "github.com/FyrmForge/hamr/pkg/respond"
```

## Design

The same handler can serve both HTMX (HTML) and JSON API clients. The `Negotiate`
function inspects `HX-Request` and `Accept` headers to pick the right format
automatically.

## HTML Responses

Render a templ component:

```go
func (h *Handler) Home(c echo.Context) error {
    return respond.HTML(c, http.StatusOK, templates.HomePage())
}
```

## JSON Responses

```go
func (h *Handler) GetUser(c echo.Context) error {
    user, err := h.repo.GetUser(ctx, id)
    if err != nil {
        return respond.Error(c, http.StatusNotFound, "User not found")
    }
    return respond.JSON(c, http.StatusOK, user)
}
```

## Content Negotiation

Serve both HTML and JSON from a single handler:

```go
func (h *Handler) GetUser(c echo.Context) error {
    user, err := h.repo.GetUser(ctx, id)
    if err != nil {
        return respond.Error(c, http.StatusNotFound, "User not found")
    }
    return respond.Negotiate(c, http.StatusOK, user, templates.UserPage(user))
}
```

Priority: `HX-Request` header (HTML) > `Accept` header > default (JSON).

## Error Responses

```go
// Simple error — negotiates format automatically
respond.Error(c, http.StatusForbidden, "Access denied")

// With an HTML error component
respond.Error(c, http.StatusNotFound, "Not found", templates.NotFoundPage())
```

JSON output: `{"error": "Access denied", "code": 403}`

HTML output: renders the provided templ component, or returns a plain text response if
none is given.

## Validation Errors

```go
errors := map[string]string{
    "email": "Invalid email address",
    "name":  "This field is required",
}
return respond.ValidationError(c, errors)
```

JSON output: `{"error": "validation failed", "fields": {"email": "Invalid email address", "name": "This field is required"}}`

HTML output: renders the provided templ component for OOB swap validation display.

Always returns 422 (Unprocessable Entity).

## Pagination

### Parse pagination params

```go
page, size := respond.ParsePagination(c, 20) // default page size 20
```

Reads `page` and `size` query params. Defaults page to 1, clamps size to [1, 100].

### Build pagination metadata

```go
pg := respond.NewPage(page, size, totalCount)
```

`Page` contains: `Number`, `Size`, `Total`, `TotalPages`, `HasNext`, `HasPrev`.

### Paged responses

```go
return respond.JSON(c, http.StatusOK, respond.PagedResponse[User]{
    Data: users,
    Page: respond.NewPage(page, size, total),
})
```

## API Reference

```go
// Responses
func HTML(c echo.Context, status int, component templ.Component) error
func JSON(c echo.Context, status int, data any) error
func Negotiate(c echo.Context, status int, jsonData any, component templ.Component) error
func Error(c echo.Context, status int, msg string, component ...templ.Component) error
func ValidationError(c echo.Context, fields map[string]string, component ...templ.Component) error

// Pagination
type Page struct {
    Number     int
    Size       int
    Total      int
    TotalPages int
    HasNext    bool
    HasPrev    bool
}
type PagedResponse[T any] struct {
    Data []T
    Page Page
}
func ParsePagination(c echo.Context, defaultSize int) (page, size int)
func NewPage(page, size, total int) Page
```
