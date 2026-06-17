# Respond — HTTP Response Helpers

`hamr/pkg/respond` provides HTTP response helpers for HTMX-first applications. It renders
templ components or JSON payloads via Echo.

## Quick Start

```go
import "github.com/FyrmForge/hamr/pkg/respond"
```

## Design

HAMR is HTMX-first. Use `respond.HTML` for templ components and `respond.JSON` for
dedicated API endpoints. Error handling is done via the `ErrorPages` middleware
(see [Middleware](middleware.md)), not in the respond package.

## HTML Responses

Render a templ component:

```go
func (h *Handler) Home(c echo.Context) error {
    return respond.HTML(c, http.StatusOK, templates.HomePage())
}
```

The component is rendered into a buffer before any status or body is written, so a
render error is returned cleanly (reaching Echo's error handler) instead of leaving
a committed `200` with a truncated body.

## JSON Responses

```go
func (h *Handler) GetUser(c echo.Context) error {
    user, err := h.repo.GetUser(ctx, id)
    if err != nil {
        return echo.NewHTTPError(http.StatusNotFound, "User not found")
    }
    return respond.JSON(c, http.StatusOK, user)
}
```

## Redirects

```go
return respond.Redirect(c, "/dashboard")
```

HTMX-aware: sets `HX-Redirect` header for HTMX requests, falls back to HTTP 303.

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
func Redirect(c echo.Context, url string) error

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
