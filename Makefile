BINARY      := cc-port
PKG         := ./cmd/cc-port
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -s -w -X main.version=$(VERSION)
GOLANGCI    ?= golangci-lint
GORELEASER  ?= goreleaser
# Codex binary the `videos` render invokes. Override when the system codex is
# not the pinned release: make videos CODEX=/path/to/downloaded/codex
CODEX       ?= codex

.DEFAULT_GOAL := help
.PHONY: help build install test test-race test-integration test-large test-all \
        vet lint fmt tidy release-check snapshot snapshot-sign ci clean \
        s3-up s3-down s3-reset s3-wait videos

help: ## Show available targets
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z0-9_-]+:.*##/ {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

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

test-large: ## Run archive tests at production scale
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

release-check: ## Validate .goreleaser.yml schema
	$(GORELEASER) check

snapshot: ## Run the full release pipeline locally (no publish, no brew, no signing)
	$(GORELEASER) release --snapshot --clean --skip=publish,homebrew,sign

snapshot-sign: ## Same as snapshot but exercises cosign sign-blob (opens browser for OIDC)
	$(GORELEASER) release --snapshot --clean --skip=publish,homebrew

ci: vet test-race test-large test-integration lint release-check snapshot ## Run the same checks CI runs

clean: ## Remove the binary and build artifacts
	rm -f $(BINARY) coverage.txt
	rm -rf dist

s3-up: ## Start the dev S3 backend (Garage) and wait until it accepts requests
	docker compose -f dev/s3/docker-compose.yml up -d
	$(MAKE) s3-wait

s3-wait: ## Block until the dev S3 endpoint at http://localhost:9000 responds
	@echo "Waiting for Garage S3 API on http://localhost:9000 ..."
	@for i in $$(seq 1 60); do \
		if curl -sS -o /dev/null -w '%{http_code}' http://localhost:9000/ | grep -qE '^[2345][0-9][0-9]$$'; then \
			echo "Garage is up."; \
			exit 0; \
		fi; \
		sleep 1; \
	done; \
	echo "Garage did not become ready in 60s." >&2; \
	exit 1

s3-down: ## Stop the dev S3 backend (preserves data volumes)
	docker compose -f dev/s3/docker-compose.yml down

s3-reset: ## Destroy and recreate the dev S3 backend (drops all data)
	docker compose -f dev/s3/docker-compose.yml down -v
	$(MAKE) s3-up

videos: build s3-reset ## Re-render all VHS demo tapes (GIF + MP4); override with CODEX=/path/to/codex
	@codex_bin="$$(command -v -- '$(CODEX)' 2>/dev/null)"; \
	  [ -n "$$codex_bin" ] || { echo "make videos: codex binary '$(CODEX)' not found; set CODEX=/path/to/codex-0.145.0" >&2; exit 1; }; \
	  ver="$$("$$codex_bin" --version)"; \
	  [ "$$ver" = "codex-cli 0.145.0" ] || { echo "make videos: codex-cli 0.145.0 required; '$(CODEX)' reports '$$ver'" >&2; exit 1; }
	go build -o seed-home ./docs/videos/fixtures/cmd/seed-home
	@codex_bin="$$(command -v -- '$(CODEX)')"; \
	  bindir="$$(mktemp -d)"; ln -s "$$codex_bin" "$$bindir/codex"; \
	  status=0; \
	  for tape in demo-move demo-export-import demo-push-pull; do \
	    PATH="$$PWD:$$bindir:$$PATH" vhs "docs/videos/$$tape.tape" || { status=1; break; }; \
	  done; \
	  rm -rf "$$bindir"; \
	  [ "$$status" -eq 0 ]
	@$(MAKE) s3-down
