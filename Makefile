VERSION ?= dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
LDFLAGS  = -X github.com/FyrmForge/hamr/internal/cli/cmd.version=$(VERSION) -X github.com/FyrmForge/hamr/internal/cli/cmd.commit=$(COMMIT)

.PHONY: build lint test test-integration-db vet

build:
	go build -ldflags '$(LDFLAGS)' -o bin/hamr ./cmd/hamr

lint:
	golangci-lint run ./...

test:
	go test ./...

test-integration-db:
	go test -mod=mod -tags=integration -count=1 ./pkg/db -run TestConnectContext_ReconnectsAfterBackendTermination

vet:
	go vet ./...

install: 
	go install -ldflags '$(LDFLAGS)' ./cmd/hamr 
