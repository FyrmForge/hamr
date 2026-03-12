# CLAUDE.md

## Workflow

- Always discuss problems and proposed solutions before implementing. Get explicit agreement before writing code or exiting plan mode.
- Never add new libraries or dependencies without asking first.
- On every change we mist ensure that human docs/guides and llm.txt/llm-full.txt are up to date

## Post-Change Checklist (mandatory)

After any rename, move, or delete:
1. `grep -r` the entire repo for the old name/references before declaring done
2. This includes: docs/, llmsdocs/, docs/adr/, docs/todos.md, all templates, test files — everything
3. Do NOT wait for the user to ask — this is part of completing the task, not a separate step

General rule: scrutinise every decision and change. Review your own work as if you're a hostile PR reviewer trying to find problems — naming inconsistencies, stale references, missed edge cases, doc drift, test gaps. Do this BEFORE saying you're done.

## Build & Test

Always use the Makefile targets — never run `go build`, `go test`, or `go vet` directly against individual packages.

- `make ai` — build, lint, test, vet, and confirm
