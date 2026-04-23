BINARY      := cc-port
PKG         := ./cmd/cc-port
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -s -w -X main.version=$(VERSION)
GOLANGCI    ?= golangci-lint
GORELEASER  ?= goreleaser

.DEFAULT_GOAL := help
.PHONY: help build install test test-race test-integration test-large test-all \
        vet lint fmt tidy snapshot ci clean

help: ## Show available targets
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build the cc-port binary into the repo root
	go build -ldflags '$(LDFLAGS)' -o $(BINARY) $(PKG)

install: ## Install cc-port via `go install`
	go install -ldflags '$(LDFLAGS)' $(PKG)

test: ## Run unit tests (fast iteration)
	go test ./...

test-race: ## Run unit tests with -race and coverage
	go test -race -coverprofile=coverage.txt ./...

test-integration: ## Run unit + integration tests
	go test -tags integration -race ./...

test-large: ## Run importer cap-rejection tests
	go test -tags large ./internal/importer/...

test-all: ## Run unit, integration, and large-archive tests
	go test -tags 'integration large' -race ./...

vet: ## Run go vet
	go vet ./...

lint: ## Run golangci-lint
	$(GOLANGCI) run ./...

fmt: ## Apply gofmt + goimports via golangci-lint fmt
	$(GOLANGCI) fmt

tidy: ## Tidy go.mod and go.sum
	go mod tidy

snapshot: ## Build a snapshot release with goreleaser
	$(GORELEASER) build --snapshot --clean

ci: vet test-race test-integration lint ## Run the same checks CI runs

clean: ## Remove the binary and build artifacts
	rm -f $(BINARY) coverage.txt
	rm -rf dist
