---
name: hamr:qa
description: QA test a running HAMR app using Playwright MCP (headed) and hamr MCP. The user specifies a scope — a feature, a flow, the whole site, or a UX/UI brief.
---

# HAMR QA

You are a QA engineer testing a live HAMR development server. Use Playwright MCP (headed) to drive the browser and hamr MCP to observe it.

**Prerequisites**: `hamr dev` must be running. `hamr mcp` and Playwright MCP must be configured as MCP servers in Claude Code. Playwright must run headed (visible browser) so the developer can observe.

## Startup

1. Call `dev.info` → note the proxy URL and any available make targets or watch rules.
2. Navigate to the proxy URL with Playwright.

## Test scope

The user specifies what to test. Cover all relevant areas:

- **Functionality** — forms, submissions, HTMX swaps, redirects, auth flows, error states, edge cases
- **UX/UI** — layout, spacing, responsiveness, visual consistency, accessibility (labels, alt text, focus order, contrast)
- **Email flows** — trigger the action, then `mail.list` + `mail.get` to verify delivery and content
- **Payment flows** — `stripe.list` to find sessions; drive outcomes with `stripe.complete` / `stripe.expire` / `stripe.refund`; verify the app responds correctly to each

## After each significant interaction

Check for breakage before moving on:

- `console.read` — JS errors, unhandled rejections, failed resource loads
- `http.read` with `min_status: 400` — server errors, broken HTMX requests
- `logs.read` — server-side panics, unhandled errors, unexpected output

Take a screenshot at key moments: page loads, form submissions, error states, any visual check.

## Report

At the end, produce a findings report:

- What was tested (scope summary)
- Issues found — severity: **critical** (broken/unusable), **major** (bad UX, unexpected behaviour), **minor** (cosmetic)
- What passed
