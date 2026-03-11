# CLAUDE.md

## Workflow

- Always discuss problems and proposed solutions before implementing. Get explicit agreement before writing code or exiting plan mode.
- Never add new libraries or dependencies without asking first.
- On every change we mist ensure that human docs/guides and llm.txt/llm-full.txt are up to date

## Build & Test

Always use the Makefile targets — never run `go build`, `go test`, or `go vet` directly against individual packages.

- `make ai` — build, lint, test, vet, and confirm
