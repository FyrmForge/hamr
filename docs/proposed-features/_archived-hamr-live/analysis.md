# HAMR Live — Analysis & Critique

Review of the proposal in `plan.md`. Includes internal codebase findings, an external critique from Codex CLI, and a recommended path forward.

Status: **discussion** — no implementation work has been authorised.

---

## Bottom line

The niche is real but the plan as written would land as a graft, not native HAMR. The signed-snapshot model fights htmx's grain, the "MVP non-goals" list reads like a roadmap in denial, and the proposed templ ergonomics break HAMR's current convention of stateless templates + state-on-the-handler. The leanest valuable version is much smaller than the proposal — or arguably should not ship as a stateful runtime at all.

---

## Product POV

**Niche exists** — small, interaction-dense, state-too-transient-for-DB/session UI: wizards, inline editors, transient filter builders, tabsets, accordion panels with derived content.

**But the boundary is fuzzy.** "Use when stateful UI becomes repetitive" (plan §Product Positioning) is a vibe, not a rule. Devs will pick wrong: reach for Live on CRUD forms that should just post to a handler; stick with raw htmx on wizards until pain piles up. If the rule cannot fit in one sentence, the runtime becomes ornamental.

**Live competes with idiomatic htmx, not just supplements it.** `live.Click` duplicates "button posts → fragment back". `live.Model` duplicates `hx-trigger="input changed delay:300ms"`. `live.Submit` duplicates form posts. htmx assumes the server reconstructs truth from stable IDs + DB/session; Live ships full state on every interaction. Different model wearing the same clothes.

**Open product question (must answer before any code):** what is the one-sentence rule that tells a HAMR developer "use Live, not raw htmx"? If we cannot draft it, we should not ship the runtime.

---

## Architectural fit with HAMR

Verified against the codebase.

1. **Templ is stateless today.** Real HAMR scaffolds (`internal/cli/generator/templates/new/internal/web/handler/auth/login/login.templ.tmpl`, `.../components/form/fields.templ.tmpl`) define templ as pure functions: `templ loginPage(c, f LoginForm, errors map[string]string)`. Handlers own state, templates render. The plan inverts this — `live.Root(c)` makes the component own its state. Paradigm shift, not extension.
2. **No attribute-spread anywhere.** `grep -r "templ.Attributes"` → zero hits. `{ helper()... }` is a new pattern this proposal would introduce. Not impossible, but it is not "fits the grain".
3. **House style for IDs is semantic** (`#login-form`, `#error-email`, OOB by `id="error-{field}"`). Plan defaults to UUID-style `#live-counter-abc123`. Diverges.
4. **`init()` registration is not idiomatic.** HAMR uses explicit DI via `NewHandler(deps)` and central wiring in `internal/web/server.go`. Auto-registering components via `init()` sidesteps that on purpose.
5. **`pkg/validate.Form` already covers per-field + full-form validation with OOB rendering.** `live.Model`/`live.Submit` would re-plumb the same problem with a parallel system.
6. **Hot reload restarts the binary** (`internal/devserver/`: `.templ` and `.go` changes both → `ReloadFull`). Snapshots-in-DOM survive this; any future "server-side store" would need durability (Redis/DB) or accept dev-time state loss.
7. **Reserved framework prefix is `/__hamr/`** (double underscore — already used by `/__hamr/mail`, `/__hamr/mail/ingest`). Original plan used `/_hamr/`; corrected in `plan.md`.
8. **No precedent for "scan user code, emit registry init() file"** in HAMR's existing codegen. Existing generators are domain-specific (i18n, static asset fingerprinting). Building a Go AST scanner for user component packages is new infrastructure.
9. **`pkg/templint` does not lint htmx attributes** — generated `hx-*` would pass. No risk there.

---

## Runtime design holes

- **Snapshot bloat.** 50 components on a page = 50 hidden HMAC tokens in the DOM. "Keep snapshots small" is an aspiration; nothing enforces it. The plan already admits "IDs over full objects, optional server-side state later" — that is quietly conceding the model does not scale.
- **Replay protection deferred.** No nonce, no timestamp. A captured snapshot is replayable until external state moves. "Authorization in action methods" is technically true but operationally naive once the snapshot becomes the trust boundary.
- **Private protocol = worse debugging.** Raw htmx failures are HTTP req/res. Live failures are decode/hydrate/dispatch/render. New mental model to learn before you can read a bug.
- **Codegen is a tax before the API has stabilised.** Rename a method → silent broken dispatch unless regen runs. Reflection + startup validator is strictly better for v1.

---

## Dev POV — HAMR-app developer

Trivial counter: shorter with Live. Anything non-trivial: not obviously shorter once you count struct + tags + generated adapter + helpers + hidden input + understanding the action vs model split. Conceptual surface is bigger than `handler + templ + hx-*`, not smaller. Failure modes are also new (snapshot decode, hydrate, dispatch, render) on top of the existing HTTP-request failure space.

## Dev POV — HAMR maintainer

Shipping Live commits us to: (a) snapshot wire format + signing migrations forever, (b) a code generator that scans user source, (c) a parallel runtime to the existing handler/route system, (d) docs explaining when *not* to use the thing we just built. The non-goals list (no nesting, no uploads, no validation, no morphing, no polling) is the exact backlog Livewire-style users will demand.

---

## Recommended options

Three options, ordered by aggression:

### Option A — Don't ship Live. Ship `pkg/htmx/attrs` instead.

A handful of attribute helpers (`htmx.Post(url, opts...)`, `htmx.Validate(field)`) plus scaffold conventions for fragment handlers. Covers most of the "raw htmx is too manual" pain without inventing a runtime. Aligns with HAMR's stateless-template grain.

### Option B — Ship a 50-line "stateful fragment" primitive.

Opaque component ID; state lives in a short-lived server store keyed by session + component-id (Redis or in-memory with TTL). Sign only the opaque ID. No JSON-in-DOM. No codegen. No `Model` helper. Just `live.Root(c)` + `live.Click(c, "Action")`. Keeps the trust boundary on the server, scales linearly with components-on-page (token size constant).

### Option C — Ship the proposal but cut hard.

- drop `live.Model` for v1
- drop codegen entirely (reflection + startup validator instead)
- drop `live.Submit` for v1
- force `live:"state"` to scalars only
- use semantic IDs not UUIDs
- fix the route prefix to `/__hamr/live/...` (done in `plan.md`)

---

## External critique (Codex CLI)

Independently arrived at Option A/B framing. Verbatim excerpts:

> "Use HAMR Live only when stateful UI becomes repetitive" is not a product boundary; it is a vibe. The actual buyer inside a HAMR app is not "anyone doing richer UI." It is the developer building small, interaction-dense, component-local workflows where the state is awkward to re-derive from URL, DB, or session on every request.

> Your model sends a self-contained signed snapshot on every interaction, which is materially different [from idiomatic htmx]. That is not automatically wrong, but it is a regression for many idiomatic htmx cases. A stateless handler with `hx-target` is simpler, more inspectable, cheaper on the wire, and easier to authorize.

> Signed snapshots are the weakest part. "Keep snapshots small" is an aspiration, not a design. JSON + base64url + HMAC on every request is fine for a counter; it becomes ugly fast with 50 components on a page. The plan admits "IDs over full objects" and "optional server-side state later"; that is basically admitting the chosen model does not scale.

> Security is also softer than the document implies. The plan explicitly defers nonce/timestamp replay protection to "later". So no, it is not defending against replay.

> This is not "mostly plain." The developer must understand a base type, state tags, root helper, click helper, submit helper, model helper, hidden snapshot input, generated adapters, dispatch semantics, and the difference between action and model endpoints. That is a bigger conceptual surface than "write a handler, render a templ fragment, add `hx-*`."

> The codegen story is classic Go framework tax. "Rename method, forget generator, ship unknown action" is exactly the sort of failure that makes maintainers hate the feature. For MVP, reflection with a startup validator is probably better.

> The non-goals list reads like a roadmap in denial. The moment people use this for forms, they will ask for validation state, nested coordination, lazy loading, polling, and uploads. The MVP as written is the camel's nose.

> The smallest valuable version here is not "Livewire for Go." It is "stateful fragment helpers for the 10% of cases where raw htmx is too manual." If you cannot keep it that small, do not ship it.

---

## Open questions to resolve before any implementation

1. **One-sentence rule** for "use Live, not raw htmx". If we cannot draft it, do not ship.
2. **State location.** In-DOM signed snapshot vs server-side keyed store. Choice drives the entire wire format and security model.
3. **Codegen vs reflection** for v1. Recommendation: reflection + startup validator.
4. **Scope of v1 helpers.** Recommendation: `live.Root` + `live.Click` only. Defer `Submit` and `Model`.
5. **Component IDs.** UUID vs semantic. Recommendation: semantic by default, UUID only if multiple instances of the same component on a page.
6. **Validation overlap.** How does Live coexist with `pkg/validate.Form`? Either reuse it or explicitly leave validation to the handler/service layer.
