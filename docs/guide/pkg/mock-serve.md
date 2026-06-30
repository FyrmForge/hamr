# Deploying Mocks (`hamr mock serve`)

`hamr mock serve` runs the mail and Stripe mocks as a standalone process — no
proxy, TUI, build, or file-watching. It is designed for a dedicated container
in a Docker Compose dev environment.

Use it instead of `hamr dev` when your dev stack runs in containers and you
want the mocks on a long-lived service that other containers talk to directly.

## Quick Start

**1. Build an image with `hamr` on PATH.**

A minimal Dockerfile:

```dockerfile
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY . .
RUN go build -o /usr/local/bin/hamr ./cmd/hamr

FROM alpine:3.21
COPY --from=build /usr/local/bin/hamr /usr/local/bin/hamr
ENTRYPOINT ["hamr"]
```

**2. Add a `mocks` service to `docker-compose.yml`.**

```yaml
services:
  mocks:
    build:
      context: .
      dockerfile: Dockerfile.hamr
    command: ["mock", "serve"]
    environment:
      HAMR_MOCKS: "mail,stripe"
      HAMR_MOCK_PORT: "4500"
      HAMR_MOCK_UI_PORT: "4501"
      HAMR_STRIPE_BASE_URL: "http://localhost:4501"
      HAMR_STRIPE_WEBHOOK_URL: "http://app:8080/api/webhooks/stripe"
      HAMR_STRIPE_WEBHOOK_SECRET: "whsec_dev_abc123"
    ports:
      - "4500:4500"
      - "4501:4501"

  app:
    # ... your app service
    environment:
      EMAIL_MOCK: "true"
      HAMR_DEV_URL: "http://mocks:4500"
      STRIPE_MOCK: "true"
      HAMR_STRIPE_MOCK_URL: "http://mocks:4500"
      STRIPE_WEBHOOK_SECRET: "whsec_dev_abc123"
```

**3. Open the dashboards.**

- Mail inbox: `http://localhost:4501/__hamr/mail`
- Stripe dashboard: `http://localhost:4501/__hamr/stripe`

## Two Ports

`HAMR_MOCK_PORT` (default `4500`) is the surface your app talks to — Stripe
`/v1/*` API and the mail ingest sink. `HAMR_MOCK_UI_PORT` is the human
dashboards. Splitting them lets you expose the two surfaces differently: publish
the app-facing port internally between containers, and publish the UI port only
to localhost (or not at all on shared hosts).

When `HAMR_MOCK_UI_PORT` is unset, both surfaces share `HAMR_MOCK_PORT`.

## App Wiring

**Mail** — `emailmock.New` reads `HAMR_DEV_URL` as its ingest base URL. Set it
to the mock's app-facing address as seen from the app container:

```go
sender = emailmock.New(os.Getenv("HAMR_DEV_URL")) // "http://mocks:4500"
```

**Stripe** — point `stripe-go`'s backend at `HAMR_STRIPE_MOCK_URL`:

```go
if os.Getenv("STRIPE_MOCK") == "true" {
    stripe.SetBackend(stripe.APIBackend, stripe.GetBackendWithConfig(
        stripe.APIBackend,
        &stripe.BackendConfig{URL: stripe.String(os.Getenv("HAMR_STRIPE_MOCK_URL"))},
    ))
}
```

`HAMR_STRIPE_BASE_URL` is the browser-reachable origin of the mock UI —
what ends up in `session.URL` so the user's browser can reach the checkout
page. Set it to the published host address of `HAMR_MOCK_UI_PORT`, not the
container-internal address.

## Security

The mock surfaces are unauthenticated. The UI serves every captured email
(which may contain password-reset tokens and magic-login links) over plain
GET, and the Stripe surface fires correctly-signed webhooks at your app on
request. Inside a container network this is fine — control exposure via which
ports you publish. On a shared host, set `HAMR_MOCK_BIND=127.0.0.1` to
bind both listeners to loopback only.

## Environment Variables

Full reference: [CLI reference — hamr mock serve](../cli.md#hamr-mock-serve).

## See Also

- [emailmock](emailmock.md) — mail mock used by `hamr dev` and `hamr mock serve`
- [stripemock](stripemock.md) — Stripe mock used by `hamr dev` and `hamr mock serve`
- [CLI reference](../cli.md#hamr-mock-serve) — full env var table
