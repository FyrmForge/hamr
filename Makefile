VERSION ?= dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
LDFLAGS  = -X github.com/FyrmForge/hamr/internal/cli/cmd.version=$(VERSION) -X github.com/FyrmForge/hamr/internal/cli/cmd.commit=$(COMMIT)

.PHONY: build lint test test-integration-db test-integration-scaffold vet

build:
	go build -ldflags '$(LDFLAGS)' -o bin/hamr ./cmd/hamr

lint:
	golangci-lint run ./...
	go run golang.org/x/tools/gopls/internal/analysis/modernize/cmd/modernize@latest ./...

modernise-fix:
	go run golang.org/x/tools/gopls/internal/analysis/modernize/cmd/modernize@latest -fix ./...

test:
	go test ./... -v -race

test-integration-db:
	go test -mod=mod -tags=integration -count=1 ./pkg/db -run TestConnectContext_ReconnectsAfterBackendTermination

test-integration-scaffold:
	go test -mod=mod -tags=integration -count=1 -timeout=20m -v ./test/integration/...

vet:
	go vet ./...

install: 
	go install -ldflags '$(LDFLAGS)' ./cmd/hamr 

aiquestion:
	@echo "Is this of the highest code quality and usability? Are user and ai docs updated? Is docs/changelog.md updated for user-facing changes?"
	
# Mirrors the CI job. test-integration-scaffold is in here because it is the
# only check that scaffolds a project and builds it — template/path changes
# pass every unit test and still break `hamr new`. Needs docker, npm, and templ.
ai: build lint test test-integration-scaffold vet aiquestion

## fmt: Format all Go source files
fmt:
	go fmt ./...
