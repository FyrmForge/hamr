# Stripe listen mode for `hamr dev`

Status: implemented (unreleased). Deviations: `STRIPE_KEY` is injected in listen mode only (in mock mode an injected copy would go stale over `.env`, and the mock ignores keys), mock webhook delivery also refuses in listen mode, and `env.example` gets no `_CONNECT` vars (the scaffold doesn't read them).

## Problem

The hamr Stripe mock covers the common flows, but some work has to run against
a real Stripe sandbox (hosted Checkout, real onboarding, anything the mock
doesn't model). Today that means turning the mock off, running
`stripe listen` by hand, copying its `whsec_` into `.env`, getting the forward
URLs and thin event names right, and remembering to switch it all back.

## Shape

Same pattern as the public tunnel (`internal/devserver/tunnel.go`): hamr owns a
child process, reads what it needs from its output, injects env into the app
and restarts it. A config value picks the starting mode, a hotkey flips it live.

- **mock**: today's behaviour. The mock serves the API and sends webhooks.
- **listen**: hamr runs `stripe listen` against a real sandbox and forwards to
  the app's webhook routes. The app calls api.stripe.com.

## Config

`enabled` is removed (breaking). `mode` replaces it.

```toml
[dev.stripe]
mode = "mock"                  # "off" | "mock" | "listen". Default "off".
webhook_url = "http://localhost:8080/api/webhooks/stripe"
thin_webhook_url = "http://localhost:8080/api/webhooks/stripe/v2"
connect_webhook_url = ""       # optional; connected-account events. Empty = webhook_url
thin_connect_webhook_url = ""  # optional; connected-account thin events. Empty = thin_webhook_url
thin_events = [                # thin events stripe listen forwards (it has no default)
  "v2.core.account[requirements].updated",
  "v2.core.account[configuration.recipient].capability_status_updated",
]
webhook_secret = "whsec_dev_local"        # mock mode only
persist = true                            # mock only
persist_path = ".hamr/stripe/state.json"  # mock only
```

- `thin_events` defaults to the two account events above when unset.
- All four URLs get the existing app-port rewrite (`rewriteWebhookURLForAppPort`).
- A `[dev.stripe]` table that still contains `enabled` is a load error:
  `dev.stripe.enabled was replaced by mode: use mode = "mock" (or "off")`.
- `.pref.hamr.toml` can override `mode` per developer (existing merge).

## Env injected into spawned processes

Injected in both modes (mode `off` injects nothing), last-wins over `.env`, same
path as the port-walk rewrites and `HAMR_STRIPE_MOCK_URL`
(`buildHamrInjectedEnv`, `SetInjectedEnv`). Names are fixed.

| Var | mock | listen |
|---|---|---|
| `STRIPE_MOCK` | `true` | `false` |
| `STRIPE_KEY` | from `.env` | from `.env` |
| `STRIPE_WEBHOOK_SECRET` | `webhook_secret` | secret from `stripe listen` |
| `STRIPE_WEBHOOK_SECRET_V2` | same | same |
| `STRIPE_WEBHOOK_SECRET_CONNECT` | same | same |
| `STRIPE_WEBHOOK_SECRET_V2_CONNECT` | same | same |
| `HAMR_STRIPE_MOCK_URL` | proxy origin (as today) | proxy origin (as today) |

Locally one secret signs everything, so all four secret vars hold the same
value. Deployed environments register separate endpoints with separate secrets;
that is the app's concern.

## Listen mode

Start:

1. Read `STRIPE_KEY` from `.env`. Refuse `sk_live_` / `rk_live_`. No format
   guessing beyond that.
2. Start `stripe listen` with `--forward-to`, `--forward-thin-to`,
   `--forward-connect-to`, `--forward-thin-connect-to` (connect URLs fall back
   to the plain ones), `--thin-events <thin_events>`, `--skip-update`.
3. Scan its output for the ready line, which carries the signing secret
   (`whsec_…`), the same way `urlScanner` reads the tunnel URL. Bounded wait.
   A fake or revoked key fails here with Stripe's own error. No separate
   `--print-secret` run.
4. Inject env, restart running run-rules and daemons (same as `tunnel.apply`).

Failure (CLI not on PATH, bad key, no secret within the timeout): log why.
From a hotkey flip, stay in the mode that was running. At boot with
`mode = "listen"`, fall back to mock with a warning; `S` retries.

- The key is passed as `STRIPE_API_KEY` in the child's environment, not
  `--api-key`, so it doesn't show in `ps` (`stripe --help` confirms the CLI
  reads it).
- Output goes to the TUI/log under a `stripe` prefix, like `tunnel`.
- If the process exits on its own: log it, fall back to mock, re-inject,
  restart the app. Never leave the app on listen env with no listener.

Key change: hamr has no `.env` watcher today (`configwatch.go` only watches
`hamr.toml`), so listen mode adds one: an fsnotify watch on `.env`, debounced.
When `STRIPE_KEY` differs from the running one, redo steps 1–4 with the new
key. Unchanged key = nothing happens. Mode `off` / `mock` never starts the watch.

## Runtime env layers

The tunnel currently keeps its own `baseEnv` and overwrites the whole injected
list on toggle; a second toggle doing the same would wipe the other's vars.
Replace that with one composer on the runner:

    injected = base (port-walk rewrites + hamr vars) + stripe layer + tunnel layer

Each layer is a `[]string` owned by its feature; setting a layer recomputes the
list, calls `SetInjectedEnv`, and restarts running run-rules and daemons
(the existing `tunnel.apply` restart loop, moved to the composer). Last-wins
order: base, stripe, tunnel. The tunnel's `baseEnv` field goes away.

## Mock routing change (both modes agree)

The mock sends events that carry a connected `account` to
`connect_webhook_url` (and thin ones to `thin_connect_webhook_url`) when set,
otherwise to the plain URLs as today.

## Mock surfaces in listen mode

Stay mounted: dashboards, control page, API routes and state keep working
against mock data, and switching back keeps state. The MCP `stripe.*` tools
return an error `stripe is in listen mode` instead of acting on the mock.

## Hotkey, status, MCP

- `S` flips mock ⇄ listen. Hidden in the hint bar and a no-op when `mode = "off"`.
- Status bar: `stripe: mock` / `stripe: listen` / `stripe: switching…`, via
  event-bus events like `tunnel_start` / `tunnel_up`.
- A failed switch leaves the current mode in place and logs why.
- New MCP write tool (area `stripe`) that flips or sets the mode, like
  `dev.restart`. `dev.info` reports the current mode instead of `enabled`.

## Out of scope

- `hamr mock-serve` stays mock-only.
- Env var renaming.
- Starting listen mode for `sk_live_` keys.

## Touch list

- `internal/devserver/config.go`: `StripeConfig` (`Mode`, connect URLs,
  `ThinEvents`), validation, `enabled` rejection, every `Stripe.Enabled` use.
- `internal/devserver/devserver.go`: mock construction gate, injection, runner wiring.
- New `internal/devserver/envlayers.go` (+ test): the runtime env composer; `tunnel.go` switches to it.
- New `internal/devserver/stripelisten.go` (+ test): process, secret scan, env, key-change `.env` watch, boot fallback.
- `internal/devserver/stripemock_webhook.go`: connect URL routing.
- `internal/devserver/stripemock_mcp.go`, `mcpgateway*.go`, `mcpconfig.go`, `internal/cli/cmd/mcp_tools.go`: listen-mode errors, new tool, `dev.info`.
- `internal/devserver/tui/model.go` + hint bar + status bar: `S`.
- `internal/cli/generator/templates/new/root/hamr.toml.tmpl`, `env.example.tmpl`: `mode = "mock"`, connect secret vars.
- Scaffold `main.go.tmpl`: no change (already switches on `STRIPE_MOCK`).
- Docs: `docs/guide/hamr-toml.md`, `docs/guide/pkg/stripemock.md`, `docs/guide/02-dev-workflow.md` (hotkeys), `docs/changelog.md` (Breaking + Features), `llmsdocs/llms.txt`, `llmsdocs/llms-full.txt`.
- Grep for `dev.stripe.enabled` / `Stripe.Enabled` / `enabled = true` under `[dev.stripe]` everywhere after the rename.
