# CLAUDE.md

## Build & Test

Always use the Makefile targets — never run `go build`, `go test`, or `go vet` directly against individual packages.

- `make build` — build the project
- `make test` — run all tests
- `make vet` — vet all packages
- `make lint` — run golangci-lint
