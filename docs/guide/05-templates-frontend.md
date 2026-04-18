# Templates & Frontend

HAMR uses Templ for compile-time type-safe HTML and HTMX for server-driven interactivity without client-side JavaScript. Alpine.js is available as an opt-in addition for lightweight client-side UI state. This guide covers how these pieces fit together.

**Package references:** [HTMX](pkg/htmx.md), [Respond](pkg/respond.md)

---

## Templ Components

[Templ](https://templ.guide/) gives you compile-time type safety, autocompletion, and prevents runtime template errors. Each handler domain has a `templates/` folder:

```
internal/web/handler/home/
├── handler.go
└── templates/
    ├── home.templ
    └── components.templ
```

### Rendering

Use `respond.HTML` to render a templ component:

```go
func (h *Handler) Home(c echo.Context) error {
    return respond.HTML(c, http.StatusOK, templates.HomePage(user))
}
```

### Layout Pattern

Define a base layout that wraps page content:

```go
// templates/layout.templ
templ Layout(title string) {
    <!DOCTYPE html>
    <html>
    <head>
        <title>{ title }</title>
        <link rel="stylesheet" href={ StaticURL("css/app.css") }/>
        <script src={ StaticURL("js/htmx.min.js") } defer></script>
        // Alpine is only vendored/included when the project opted in.
        <script src={ StaticURL("js/alpine.min.js") } defer></script>
    </head>
    <body>
        { children... } // renders whatever is passed inside the @Layout block
    </body>
    </html>
}

// templates/home.templ
templ HomePage(user *models.User) {
    @Layout("Home") {
        <h1>Welcome, { user.Name }</h1>
    }
}
```

---

## HTMX

HAMR is HTMX-first — you get SPA-like interactivity without writing client-side JavaScript. The server sends HTML fragments and HTMX swaps them into the DOM. The `htmx` package provides request detection and response header helpers.

### Partial vs Full Rendering

```go
func (h *Handler) UserList(c echo.Context) error {
    users, _ := h.repo.List(c.Request().Context())

    if htmx.IsHTMX(c.Request()) {
        // HTMX request — render just the partial
        return respond.HTML(c, http.StatusOK, templates.UserTable(users))
    }
    // Full page request — render with layout
    return respond.HTML(c, http.StatusOK, templates.UserListPage(users))
}
```

### Redirects

Client-side redirect without full page reload:

```go
htmx.Redirect(c.Response(), "/dashboard")
```

### Triggering Events

Fire custom events on the client:

```go
htmx.Trigger(c.Response(), "userCreated", "showToast")
htmx.TriggerAfterSettle(c.Response(), "formReset")
```

### Swap Control

Override the swap strategy or target:

```go
htmx.Reswap(c.Response(), "outerHTML")
htmx.Retarget(c.Response(), "#error-container")
```

### URL Manipulation

Update the browser URL bar:

```go
htmx.PushURL(c.Response(), "/users/42")
htmx.ReplaceURL(c.Response(), "/users/42")
```

---

## Alpine.js (optional)

Alpine.js is opt-in. Enable it at scaffold time via `hamr new --alpine` (or answer **yes** to the Alpine prompt in the wizard). To add Alpine to an existing project, run `hamr vendor alpine` and add `<script src={ StaticURL("js/alpine.min.js") } defer></script>` to your layout's `<head>`. Use it for dropdowns, modals, tabs, and other local UI state:

```html
<div x-data="{ open: false }">
    <button @click="open = !open">Toggle</button>
    <div x-show="open" x-transition>
        Dropdown content
    </div>
</div>
```

Alpine works alongside HTMX — use HTMX for server communication, Alpine for client-side UI state.

---

## StaticURL Helper

All generated projects get a `STATIC_BASE_URL` env var (default: `/static`). Use the `StaticURL` helper in templates instead of hardcoded paths:

```go
<link rel="stylesheet" href={ StaticURL("css/app.css") }/>
<script src={ StaticURL("js/htmx.min.js") } defer></script>
```

In development, this resolves to `/static/css/app.css`. In production, override `STATIC_BASE_URL` to point at your S3 bucket or CDN:

```bash
STATIC_BASE_URL=https://cdn.example.com/static
```

---

## Vendored JS Dependencies

HAMR vendors frontend JS dependencies into `static/js/`. `htmx` and `idiomorph` are always vendored; `alpine` is vendored only when the project opts in. Manage them with:

```bash
hamr vendor                     # vendor all deps at locked versions
hamr vendor htmx                # vendor only htmx
hamr vendor alpine@3.14.9       # pin a specific version
hamr vendor --update            # re-vendor all at latest
hamr vendor --verify            # check checksums
```

Checksums are recorded in `hamr.vendor.json`.

---

## Next Steps

- [Forms & Validation](06-forms-validation.md) — Form handling with HTMX field validation
- [Static Assets](09-static-assets.md) — CSS, JS vendoring, CDN patterns
