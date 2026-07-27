# Bug: every scaffolded project 500s on its first 404

Status: **fixed** — see the Unreleased section of `docs/changelog.md`. Kept for
the diagnosis; the checklist below records what landed.

Severity: **high**. Hits every generated project with `auth`/flash enabled, on
the most ordinary request there is — a 404. No unusual configuration needed.
The user-visible symptom is a 500 where a styled 404 was intended, and a panic
in the log on every miss (including `/favicon.ico`, which browsers request
unprompted).

## The contradiction

Two templates that ship together disagree about whether `Layout`'s
`echo.Context` may be nil.

`internal/cli/generator/templates/new/internal/web/components/error.templ.tmpl:16`
(and `:23`) passes nil, correctly — `middleware.ErrorPages` renders from a
`func(code int, message string) templ.Component`, so there is no request
context to hand down:

```templ
templ errorPage(code int, message string) {
	@Layout(nil, http.StatusText(code)) {
```

`internal/cli/generator/templates/new/internal/web/components/layout.templ.tmpl:38`
dereferences it unconditionally:

```templ
if flash := middleware.GetFlash(c); flash != nil {
```

`GetFlash` → `ctx.Get(c, ctx.FlashKey)` → `c.Get(key.name)` on a nil interface
→ panic. Echo's `Recover` catches it and returns 500, so the failure surfaces
as a wrong status code rather than a crash.

## Proof

Run in any scaffolded project (this isolates the framework call — no template
rendering involved):

```go
func TestGetFlashNilContext(t *testing.T) {
	defer func() { t.Logf("panic: %v", recover()) }()
	_ = middleware.GetFlash(nil)
}
```

```
middleware.GetFlash(nil) PANICS: runtime error: invalid memory address or nil pointer dereference
```

End to end, from a scaffolded app's log on `GET /favicon.ico`:

```
ERR panic recovered error="runtime error: invalid memory address or nil pointer dereference"
  hamr/pkg/ctx.Get[...]                     pkg/ctx/ctx.go:31
  hamr/pkg/middleware.GetSubject(...)       pkg/middleware/subject.go:21
  <project>/internal/web/components.Layout.func1 …
  <project>/internal/web/components.ErrorPage.errorPage.func1
  hamr/pkg/respond.HTML({...}, 0x194 /* 404 */, {...})
  hamr/pkg/middleware.ErrorPages.func4.1    pkg/middleware/errors.go:68
```

(That trace is from a project whose `Layout` also calls a nav component using
`middleware.GetSubject`, which panics a few lines before `GetFlash` would.
Either accessor reaches the same nil dereference; the `GetFlash` path is the
one present in the stock scaffold.)

## Fix

One line in `pkg/ctx/ctx.go`. `Get` already guards a nil *value*; it just
doesn't guard a nil *context*:

```go
func Get[T any](c echo.Context, key Key[T]) (T, bool) {
	if c == nil {
		var zero T
		return zero, false
	}
	val := c.Get(key.name)
	...
}
```

This fixes `GetFlash`, `GetSubject`, `GetSubjectID` and every future accessor
built on `ctx.Get` at once, and makes the `@Layout(nil, …)` idiom the scaffold
already ships actually valid. `MustGet` should keep panicking — that is its
contract — but on nil it should panic with a clear message rather than a bare
nil dereference.

### Why not fix it in the templates

Guarding in `layout.templ.tmpl` (`if c != nil { … }`) fixes the shipped
scaffold and nothing else. Every project that later adds a context-reading
component to `Layout` — a nav, a locale switcher, a cart badge — reintroduces
the same panic, and gets a stack trace pointing into hamr rather than at their
own line. The nil-context case is a framework-level invariant; it belongs in
the framework.

Templates are still worth a second look independently: passing `nil` is easy to
misread as an oversight. A short comment at `error.templ.tmpl:16` saying *"nil
is deliberate — error pages render without a request; `ctx.Get` tolerates it"*
would stop the next person from `Layout(c)`-ing it and wondering why.

## Checklist

- [x] `ctx.Get` returns the zero value for a nil context (`GetAs` too — same
      dereference, same fix)
- [x] `ctx.MustGet` / `MustGetAs` panic with `ctx: nil echo.Context for key <k>`
- [x] Test: `GetFlash`/`GetSubject`/`GetSubjectID` with a nil context
- [x] Test: `ErrorPages` end to end — a page calling the accessors with a nil
      context returns 404, not a recovered panic and a 500. Verified by
      reverting the guard: the test reproduces the reported trace exactly.
- [x] Comment in `error.templ.tmpl` explaining the deliberate nil

## Unrelated to

`compose-override-passthrough.md`, found in the same session. Different
subsystem (scaffold templates vs. dev-server compose handling), no shared
cause. Listed only so the two are not conflated during triage.
