# HAMR Live: Livewire-Style Component Runtime Plan

> Original proposal as submitted for review. See `analysis.md` for critique and recommendations.

## Objective

Implement an optional Livewire-style component system for HAMR using Go, templ, and htmx.

The system should let application developers write mostly plain Go structs and templ components while HAMR handles:

- component registration
- signed state snapshots
- hydration/dehydration
- action dispatch
- templ re-rendering
- htmx request/response wiring
- safe DOM replacement defaults

This must remain optional. Standard HAMR apps using explicit handlers, routes, templ views, and raw htmx should continue to work unchanged.

---

## Non-Goals

Do not attempt to fully clone Livewire in the first version.

Avoid implementing these initially:

- nested live component coordination
- file uploads
- computed properties
- deep lifecycle magic
- full validation framework
- model/database binding
- automatic DOM morphing beyond htmx swap defaults
- custom `live:click` templ syntax preprocessing
- replacing normal HAMR handlers or raw htmx

The MVP should be small, explicit, and stable.

---

## Product Positioning

HAMR core remains:

```text
Go + Echo + templ + htmx, explicit server-rendered apps
```

HAMR Live adds:

```text
optional stateful server-side components for richer UI
```

Use plain HAMR/htmx by default. Use HAMR Live only when stateful UI becomes repetitive.

---

## Desired Developer Experience

A developer should write something close to this:

```go
type Counter struct {
    Count int `live:"state"`
}

func (c *Counter) Increment() {
    c.Count++
}

func (c *Counter) Decrement() {
    c.Count--
}
```

And in templ:

```templ
templ Counter(c *Counter) {
    <div { live.Root(c)... }>
        <button { live.Click(c, "Decrement")... }>-</button>
        <span>{ fmt.Sprint(c.Count) }</span>
        <button { live.Click(c, "Increment")... }>+</button>
    </div>
}
```

The framework should generate or provide the glue for:

```text
render component
→ embed signed snapshot
→ htmx posts action
→ server hydrates component
→ action method runs
→ templ re-renders
→ htmx swaps updated HTML
```

---

## Recommended Package Layout

Add a new package:

```text
pkg/live/
  component.go
  registry.go
  snapshot.go
  action.go
  helpers.go
  errors.go
  middleware.go
```

Generated app-level code can live under:

```text
internal/live/
  counter.go

internal/live/generated/
  live_gen.go
```

Existing HAMR app structure should remain unchanged.

---

## Core Runtime Design

### 1. Component Contract

Avoid forcing users to manually implement many interfaces.

Internally, the runtime can use interfaces like:

```go
type Component interface {
    LiveName() string
    LiveID() string
    SetLiveID(id string)
    LiveRender(ctx context.Context) templ.Component
}

type ActionComponent interface {
    LiveCall(action string, r *http.Request) error
}
```

But users should not normally hand-write these.

Prefer generated adapters.

---

### 2. User-Facing Base Type

Provide an embeddable base for IDs and metadata:

```go
type Base struct {
    ComponentID string `json:"_id"`
}

func (b *Base) LiveID() string {
    return b.ComponentID
}

func (b *Base) SetLiveID(id string) {
    b.ComponentID = id
}
```

However, do not require the user to understand the internals.

---

### 3. Registry

Implement a component registry:

```go
type Registry struct {
    items map[string]func() Component
}

func NewRegistry() *Registry
func (r *Registry) Register(name string, factory func() Component)
func (r *Registry) New(name string) (Component, error)
```

The generated file should register components automatically.

Example generated code:

```go
func init() {
    live.Register("Counter", func() live.Component {
        return &Counter{}
    })
}
```

---

### 4. Signed Snapshots

Each live component needs a snapshot embedded in the HTML.

Snapshot shape:

```go
type Snapshot struct {
    Name    string          `json:"name"`
    ID      string          `json:"id"`
    Version int             `json:"version"`
    State   json.RawMessage `json:"state"`
}
```

Encode flow:

```text
component state JSON
→ base64url encode
→ HMAC-SHA256 sign
→ token = payload.signature
```

Decode flow:

```text
split token
→ verify HMAC
→ decode JSON
→ hydrate component
```

Important security rules:

- Signing prevents tampering.
- Signing does not hide state.
- Do not put secrets in snapshot state.
- Authorization must still happen in action methods/services.
- Consider encryption later only if required.

---

### 5. State Field Selection

Only fields tagged with `live:"state"` should be serialized.

Example:

```go
type SearchBox struct {
    Query string `live:"state"`

    // Not serialized.
    Results []Result
}
```

For MVP, allow exported fields with `live:"state"`.

Avoid serializing the entire struct by default.

---

### 6. Root Helper

The root helper should output:

```html
id="live-counter-abc123"
data-live-root="Counter"
```

plus a hidden input:

```html
<input type="hidden" name="__live" value="signed_snapshot">
```

Preferred rendered shape:

```html
<div id="live-counter-abc123" data-live-root="Counter">
  <input type="hidden" name="__live" value="signed_snapshot">
  ...
</div>
```

Do not use `hx-include="closest [data-live-root]"` as the default.

---

### 7. Action Helper

User code:

```templ
<button { live.Click(c, "Increment")... }>+</button>
```

Generated attributes should be explicit:

```html
hx-post="/__hamr/live/action"
hx-vals='{"action":"Increment"}'
hx-include="#live-counter-abc123 input[name='__live']"
hx-target="#live-counter-abc123"
hx-swap="outerHTML"
```

Default rule:

```text
button action → include only __live snapshot
```

Do not include the entire component root by default.

---

### 8. Form Submit Helper

User code:

```templ
<form { live.Submit(c, "Save")... }>
    <input name="title" value={ c.Title }>
    <button>Save</button>
</form>
```

Generated attributes:

```html
hx-post="/__hamr/live/action"
hx-vals='{"action":"Save"}'
hx-include="#component-id input[name='__live'], this"
hx-target="#component-id"
hx-swap="outerHTML"
```

Default rule:

```text
form submit → include snapshot + form fields
```

---

### 9. Model Binding Helper

User code:

```templ
<input value={ c.Query } { live.Model(c, "Query")... }>
```

Generated attributes:

```html
name="Query"
hx-post="/__hamr/live/model"
hx-trigger="input changed delay:300ms"
hx-include="#component-id input[name='__live'], this"
hx-target="#component-id"
hx-swap="outerHTML"
```

Default rule:

```text
model update → include snapshot + one field
```

For MVP, model updates can use reflection against `live:"state"` fields only.

---

## HTTP Endpoints

Add runtime routes:

```text
POST /__hamr/live/action
POST /__hamr/live/model
```

These should be registered by a helper:

```go
live.RegisterRoutes(e, live.Options{
    Registry: registry,
    Secret: appSecret,
})
```

HAMR app scaffolding should wire this automatically when live components are enabled.

---

## Action Request Flow

For `POST /__hamr/live/action`:

```text
1. Read __live snapshot token.
2. Verify HMAC.
3. Decode snapshot.
4. Create component using registry.
5. Hydrate component state from snapshot.
6. Read action name.
7. Dispatch action.
8. Encode new snapshot.
9. Render component root with new snapshot.
10. Return HTML fragment.
```

Pseudo-code:

```go
func HandleAction(registry *Registry, secret []byte) echo.HandlerFunc {
    return func(c echo.Context) error {
        token := c.FormValue("__live")
        action := c.FormValue("action")

        snapshot, err := DecodeSnapshot(secret, token)
        if err != nil {
            return echo.NewHTTPError(http.StatusBadRequest, "invalid live snapshot")
        }

        component, err := registry.New(snapshot.Name)
        if err != nil {
            return echo.NewHTTPError(http.StatusBadRequest, err.Error())
        }

        if err := Hydrate(snapshot, component); err != nil {
            return echo.NewHTTPError(http.StatusBadRequest, "could not hydrate component")
        }

        actionComponent, ok := component.(ActionComponent)
        if !ok {
            return echo.NewHTTPError(http.StatusBadRequest, "component does not support actions")
        }

        if err := actionComponent.LiveCall(action, c.Request()); err != nil {
            return echo.NewHTTPError(http.StatusBadRequest, err.Error())
        }

        return RenderLiveComponent(c, component)
    }
}
```

---

## Code Generation

Use code generation to avoid making users implement framework interfaces manually.

Add command:

```bash
hamr live:generate
```

Potential future command:

```bash
hamr make:live Counter
```

Generated adapter responsibilities:

- register component
- expose component name
- connect struct to templ renderer
- dispatch allowed methods
- optionally validate action signatures
- avoid reflection for action dispatch

Example generated adapter:

```go
// Code generated by hamr live:generate. DO NOT EDIT.

func init() {
    live.Register("Counter", func() live.Component {
        return &Counter{}
    })
}

func (c *Counter) LiveName() string {
    return "Counter"
}

func (c *Counter) LiveRender(ctx context.Context) templ.Component {
    return components.Counter(c)
}

func (c *Counter) LiveCall(action string, r *http.Request) error {
    switch action {
    case "Increment":
        c.Increment()
        return nil
    case "Decrement":
        c.Decrement()
        return nil
    default:
        return live.ErrUnknownAction
    }
}
```

Initial implementation can use reflection if faster, but the target design should be codegen.

---

## Lifecycle Hooks

Support optional hooks later.

MVP may include only `Mount`.

Possible hooks:

```go
type Mountable interface {
    Mount(r *http.Request) error
}

type Hydratable interface {
    Hydrate(r *http.Request) error
}

type Dehydratable interface {
    Dehydrate(r *http.Request) error
}
```

Flow:

```text
initial render:
Mount → render → snapshot

action request:
decode → hydrate → action → dehydrate → render → snapshot
```

Do not overbuild lifecycle behavior in MVP.

---

## Error Handling

Dev mode should return clear HTML error fragments.

Production should return safe generic errors.

Useful error types:

```go
var ErrInvalidSnapshot = errors.New("invalid live snapshot")
var ErrUnknownComponent = errors.New("unknown live component")
var ErrUnknownAction = errors.New("unknown live action")
var ErrInvalidModelField = errors.New("invalid live model field")
```

Dev-mode errors should include:

- component name
- action name
- field name
- snapshot decode failure reason
- missing generated adapter hints

---

## Security Requirements

Implement these from day one:

1. HMAC-sign snapshots.
2. Use constant-time signature comparison.
3. Include snapshot version.
4. Do not serialize untagged fields.
5. Do not include sensitive data in snapshots.
6. Validate action names against generated allowlist.
7. Maintain CSRF protection.
8. Recheck authorization in action methods/services.
9. Consider timestamp or nonce later if replay becomes a concern.

---

## htmx Integration Rules

HAMR Live should emit normal htmx attributes.

Do not replace htmx.

Do not make raw htmx second-class.

Allow escape hatches:

```templ
<button { live.Click(c, "Save")... } hx-swap="morph">
    Save
</button>
```

or raw htmx:

```templ
<button hx-post="/todos/1/toggle" hx-target="#todo-1">
    Toggle
</button>
```

Core rule:

```text
live helpers generate htmx;
developers can still use htmx directly.
```

---

## MVP Build Order

### Phase 1: Manual Runtime

Implement:

1. `pkg/live.Component`
2. `pkg/live.Registry`
3. signed snapshot encoding/decoding
4. hydration for `live:"state"` fields
5. `live.Root(c)`
6. `live.Click(c, action)`
7. `POST /__hamr/live/action`
8. counter demo component

Acceptance criteria:

- Counter renders.
- Clicking increment posts via htmx.
- Server hydrates state.
- `Increment()` runs.
- Updated HTML replaces component.
- Snapshot changes after each render.
- Tampered snapshot is rejected.

---

### Phase 2: Form and Model Helpers

Implement:

1. `live.Submit(c, action)`
2. `live.Model(c, field)`
3. `POST /__hamr/live/model`
4. basic string/int/bool field conversion
5. form demo component

Acceptance criteria:

- Form submit includes snapshot + form fields.
- Model update includes snapshot + one input.
- Only `live:"state"` fields can be updated.
- Invalid field updates are rejected.

---

### Phase 3: Code Generation

Implement:

1. scanner for live components
2. generated registry file
3. generated action dispatch
4. dev command integration

Acceptance criteria:

- User does not manually implement `LiveName`, `LiveRender`, or `LiveCall`.
- Missing or stale generated code gives a clear error.
- `hamr dev` runs generation automatically or warns clearly.

---

### Phase 4: Developer Experience

Add:

1. `hamr make:live Counter`
2. starter Go component
3. starter templ component
4. docs
5. examples

Acceptance criteria:

- New live component can be created with one command.
- Generated code compiles.
- Example app demonstrates counter, search box, and form.

---

## Example Demo Components

Build these examples:

### Counter

Tests action dispatch and snapshot updates.

### SearchBox

Tests model binding.

```go
type SearchBox struct {
    Query string `live:"state"`
}
```

### MultiStepForm

Tests component-local state and form submit.

```go
type SignupWizard struct {
    Step  int    `live:"state"`
    Email string `live:"state"`
}
```

---

## Testing Plan

Unit tests:

- snapshot encode/decode
- tampered snapshot rejection
- expired/invalid version behavior
- state field extraction
- hydration
- model field conversion
- registry lookup
- action dispatch

Integration tests:

- POST action returns updated HTML
- POST model update returns updated HTML
- raw htmx still works
- CSRF behavior remains valid
- malformed requests fail safely

E2E tests:

- counter increments without full reload
- search input updates component
- form submit preserves validation errors
- focus behavior is acceptable

---

## Important Design Decisions

### Use explicit component IDs

Prefer:

```html
hx-target="#live-counter-123"
hx-include="#live-counter-123 input[name='__live']"
```

Avoid global default:

```html
hx-target="closest [data-live-root]"
hx-include="closest [data-live-root]"
```

`closest` can be a fallback, not the default.

---

### Use codegen over heavy reflection

Reflection is acceptable for early MVP internals.

Long-term, prefer codegen because it gives:

- clearer errors
- better performance
- compile-time safety
- less runtime magic

---

### Keep snapshots small

Only serialize tagged state.

Do not serialize derived data, database models, secrets, service objects, or large result sets.

---

### Keep HAMR core unchanged

Do not force applications into the live component model.

Normal HAMR should remain:

```text
route → handler → templ → htmx
```

HAMR Live should be opt-in:

```text
component → snapshot → action → render → htmx swap
```

---

## Risks

### Risk: too much framework magic

Mitigation:

- keep helpers explicit
- generated code should be inspectable
- allow raw htmx
- avoid custom templ syntax initially

### Risk: snapshot security mistakes

Mitigation:

- HMAC from day one
- tagged fields only
- documentation warning against secrets
- authorization still handled server-side

### Risk: bad debugging experience

Mitigation:

- dev-mode error fragments
- clear generated-code errors
- include component/action names in logs

### Risk: large HTML/state payloads

Mitigation:

- tagged state only
- docs encouraging IDs over full objects
- optional server-side session-backed state later

### Risk: trying to match Livewire feature-for-feature

Mitigation:

- keep MVP narrow
- build only what HAMR apps need
- treat raw htmx as first-class

---

## Final Recommendation

Implement HAMR Live as an optional package and generated-code workflow.

Do not make it the default application architecture.

The first version should prove this loop only:

```text
Go struct with tagged state
→ templ root helper embeds signed snapshot
→ htmx posts action
→ runtime hydrates component
→ generated adapter calls method
→ templ re-renders component
→ htmx swaps exact component root
```

Once this loop is reliable, add model binding, form helpers, and scaffolding.

---

> Note: Original draft used `/_hamr/...` as the runtime route prefix. HAMR's existing convention is `/__hamr/...` (double underscore, e.g. `/__hamr/mail`). This file has been corrected to use the existing convention. See `analysis.md` §"Architectural fit" item 7.
