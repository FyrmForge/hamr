---
name: hamr:qa-loop
description: Iterative test → fix → rebuild → retest loop for a HAMR app. Loops until a user-defined goal is met, or an iteration cap is hit.
---

# HAMR QA Loop

You are a QA engineer and developer running an iterative fix loop on a live HAMR development server. Each iteration: test, identify blockers, fix code, rebuild, retest.

**Prerequisites**: `hamr dev` must be running. `hamr mcp` and Playwright MCP must be configured as MCP servers in Claude Code. Playwright must run headed.

## Setup

Before starting, confirm with the user:

1. **Goal** — a concrete, testable criterion the loop runs until met (e.g. "the checkout flow completes without JS errors and sends a confirmation email").
2. **Iteration cap** — maximum number of test→fix cycles. If not given, default to 5. This is a safety net, not a target.

Then call `dev.info` → note the proxy URL and available make targets.

## Each iteration

1. **Test** — follow the `/hamr:qa` workflow scoped to what is relevant to the goal. Use Playwright (headed) + hamr MCP tools (`console.read`, `http.read`, `logs.read`, `mail.*`, `stripe.*`) as needed.
2. **Evaluate** — does the current state fully satisfy the goal? If yes, stop and report success.
3. **Fix** — address the highest-impact issues blocking the goal. Read `references/practices.md` in the skill directory for HAMR coding conventions before editing. Follow HAMR rules: `respond.HTML`, `validate.NewForm`, PRG redirects, no `html/template`, etc.
4. **Rebuild** — call `make.run` with target `build`. Poll `logs.read` until the build marker appears. If the build fails, fix the error and retry before moving to the next test iteration.
5. Increment the iteration count. If the cap is reached, stop.

## Stopping conditions

- **Goal met** → report success, iterations used, and a summary of what changed
- **Cap reached** → report what is still failing, why, and what would be needed to fix it
- **Repeated build failure** → report the build error and stop; do not keep iterating on a broken build

## Output

Each iteration: one short line — what failed, what was fixed.

Final report: goal met or not · iterations used · outstanding issues.
