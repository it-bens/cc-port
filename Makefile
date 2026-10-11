BINARY      := cc-port
PKG         := ./cmd/cc-port
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -s -w -X main.version=$(VERSION)
GOLANGCI    ?= golangci-lint
GORELEASER  ?= goreleaser
CODEX       ?= codex
PRE_COMMIT_BIN ?= pre-commit
DEADCODE_VERSION := v0.52.0
# The version is part of the install path, so a bump installs a fresh binary instead of reusing a stale one.
DEADCODE_DIR := $(CURDIR)/.bin/deadcode-$(DEADCODE_VERSION)
DEADCODE_BIN := $(DEADCODE_DIR)/deadcode
# git resolves the hook path through core.hooksPath and linked worktrees; it is empty outside a git checkout.
HOOK_FILE   := $(shell git rev-parse --git-path hooks/pre-push 2>/dev/null)
# The developer-facing targets depend on the installed hook file, so the install runs once per checkout; CI (CI is non-empty on GitHub Actions) and a running pre-commit hook (pre-commit sets PRE_COMMIT) skip it.
# Outside a git checkout the hook cannot be installed, so a target that needs it stops at hooks-unavailable.
HOOKS       := $(if $(CI)$(PRE_COMMIT),,$(if $(HOOK_FILE),$(HOOK_FILE),hooks-unavailable))

.DEFAULT_GOAL := help
.PHONY: help hooks hooks-unavailable build install test test-race test-integration test-large test-all \
        vet deadcode lint pre-push fmt tidy release-check snapshot snapshot-sign ci clean \
        s3-up s3-down s3-reset s3-wait videos

define install-hook
@command -v -- '$(PRE_COMMIT_BIN)' >/dev/null 2>&1 || { \
  echo "make hooks: '$(PRE_COMMIT_BIN)' not found; install it with 'pipx install pre-commit' or 'brew install pre-commit'" >&2; exit 1; }
$(PRE_COMMIT_BIN) install
endef

# deadcode exits 0 even with findings, so each pass fails on any output. Each
# pass runs once per GOOS, because the platform-specific files of each GOOS
# are invisible to the other. The logo pass reaches banner_logo.go, which the
# default pass excludes; the seed-home pass covers the demo seeder, a separate
# main package.
define deadcode-check
@out="$$(GOOS=$(1) '$(DEADCODE_BIN)' $(2))" || exit 1; \
  [ -z "$$out" ] || { printf 'deadcode (GOOS=$(1)):\n%s\n' "$$out" >&2; exit 1; }
endef

define deadcode-os
$(call deadcode-check,$(1),./cmd/cc-port)
$(call deadcode-check,$(1),-tags logo ./cmd/cc-port)
$(call deadcode-check,$(1),-filter '/docs/videos/fixtures' ./docs/videos/fixtures/cmd/seed-home)
endef

define deadcode-passes
$(call deadcode-os,darwin)
$(call deadcode-os,linux)
endef

define lint-passes
$(GOLANGCI) run ./...
$(GOLANGCI) run --build-tags=integration,large,logo ./...
$(deadcode-passes)
endef

help: ## Show available targets
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z0-9_-]+:.*##/ {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

hooks: ## Install the pre-push git hook (runs make pre-push) via pre-commit
	$(install-hook)

ifneq ($(HOOK_FILE),)
$(HOOK_FILE):
	$(install-hook)
endif

hooks-unavailable:
	$(error make: not a git checkout, so the pre-push hook cannot be installed; run make inside a git checkout, or set CI or PRE_COMMIT to skip the hook)

build: $(HOOKS) ## Build the cc-port binary into the repo root
	go build -ldflags '$(LDFLAGS)' -o $(BINARY) $(PKG)

install: $(HOOKS) ## Install cc-port via `go install`
	go install -ldflags '$(LDFLAGS)' $(PKG)

test: $(HOOKS) ## Run unit tests (fast iteration)
	go test ./...

test-race: $(HOOKS) ## Run unit tests with -race and coverage
	go test -race -coverprofile=coverage.txt ./...

test-integration: $(HOOKS) ## Run unit + integration tests
	go test -tags integration -race ./...

test-large: $(HOOKS) ## Run archive tests at production scale
	go test -tags large ./internal/importer/...

test-all: $(HOOKS) ## Run unit, integration, and large-archive tests
	go test -tags 'integration large' -race ./...

vet: $(HOOKS) ## Run go vet
	go vet ./...

$(DEADCODE_BIN):
	GOBIN='$(DEADCODE_DIR)' go install golang.org/x/tools/cmd/deadcode@$(DEADCODE_VERSION)

deadcode: $(HOOKS) $(DEADCODE_BIN) ## Report unreachable functions from the cc-port and seed-home entry points on darwin and linux
	$(deadcode-passes)

lint: $(HOOKS) $(DEADCODE_BIN) ## Run golangci-lint, plus a pass over the logo-tagged banner and deadcode
	$(lint-passes)

pre-push: $(HOOKS) $(DEADCODE_BIN) ## Lint the commits being pushed; refuses while uncommitted changes would be linted instead
	@unstaged="$$(git diff --ignore-submodules --name-only)" || exit 1; \
	  staged="$$(git diff --ignore-submodules --cached --name-only)" || exit 1; \
	  untracked="$$(git ls-files --others --exclude-standard -- '*.go' go.mod go.sum .golangci.yml)" || exit 1; \
	  dirty="$$(printf '%s\n%s\n%s\n' "$$unstaged" "$$staged" "$$untracked" | sed '/^$$/d' | sort -u)"; \
	  [ -z "$$dirty" ] || { \
	    echo "make pre-push: the working tree has changes, so lint would check them instead of the commits being pushed:" >&2; \
	    printf '%s\n' "$$dirty" | sed 's/^/  /' >&2; \
	    echo "  Commit or stash these files, then push again." >&2; exit 1; }
	$(lint-passes)

fmt: $(HOOKS) ## Apply gofmt + goimports via golangci-lint fmt
	$(GOLANGCI) fmt

tidy: $(HOOKS) ## Tidy go.mod and go.sum
	go mod tidy

release-check: $(HOOKS) ## Validate .goreleaser.yml schema
	$(GORELEASER) check

snapshot: $(HOOKS) ## Run the full release pipeline locally (no publish, no brew, no signing)
	$(GORELEASER) release --snapshot --clean --skip=publish,homebrew,sign

snapshot-sign: $(HOOKS) ## Same as snapshot but exercises cosign sign-blob (opens browser for OIDC)
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

videos: build ## Re-render all VHS demo tapes (GIF + MP4); override with CODEX=/path/to/codex
	@codex_bin="$$(command -v -- '$(CODEX)' 2>/dev/null)"; \
	  [ -n "$$codex_bin" ] || { echo "make videos: codex binary '$(CODEX)' not found; set CODEX=/path/to/codex-0.156.1" >&2; exit 1; }; \
	  ver="$$("$$codex_bin" --version)"; \
	  [ "$$ver" = "codex-cli 0.156.1" ] || { echo "make videos: codex-cli 0.156.1 required; '$(CODEX)' reports '$$ver'" >&2; exit 1; }
# Codex's liveness witness scans the whole process table, so any running Codex
# (a stray codex mcp-server counts) makes the paired tape's `import --apply`
# refuse, and vhs records that refusal as the demo while still exiting 0. The
# name set mirrors codexProcessNames in internal/tool/codex/witness.go, so the
# guard and the witness cannot disagree. Each case pattern opens with '(':
# an unbalanced ')' inside a command substitution aborts the shell's parse.
	@live="$$(ps -Ao pid=,comm= | while read -r pid comm; do \
	    case "$${comm##*/}" in (codex|codex-tui|codex-app-server) printf '%s ' "$$pid";; esac; \
	  done)"; \
	  [ -z "$$live" ] || { \
	    echo "make videos: live Codex process(es) detected (pid: $$live)" >&2; \
	    echo "  cc-port refuses a mutating apply while one runs, so the render would record the refusal." >&2; \
	    echo "  Stop them (a codex mcp-server counts) and re-run." >&2; exit 1; }
	go build -o seed-home ./docs/videos/fixtures/cmd/seed-home
# s3-reset runs in the recipe body, after every guard, so a refused render
# leaves the local Garage container untouched instead of reset and running.
	$(MAKE) s3-reset
# A relative CODEX must be absolutized before it is symlinked: a symlink target
# resolves against the link's own dir (the mktemp bindir), so a relative target
# would dangle and PATH would silently fall through to another codex. Removing
# the bindir and stopping S3 run from a trap so a failed or interrupted render
# still tears both down.
	@codex_bin="$$(command -v -- '$(CODEX)')"; \
	  case "$$codex_bin" in /*) ;; *) codex_bin="$$(cd "$$(dirname "$$codex_bin")" && pwd)/$$(basename "$$codex_bin")";; esac; \
	  bindir="$$(mktemp -d)"; \
	  trap 'rc=$$?; rm -rf "$$bindir"; $(MAKE) s3-down; exit $$rc' EXIT; \
	  trap 'exit 130' INT TERM; \
	  ln -s "$$codex_bin" "$$bindir/codex"; \
	  status=0; \
	  for tape in demo-move demo-export-import demo-push-pull; do \
	    PATH="$$PWD:$$bindir:$$PATH" vhs "docs/videos/$$tape.tape" || { status=1; break; }; \
	  done; \
	  exit "$$status"
