# Makefile for pve-cli
# Default target: help (categorized, colored)

MODULE  := github.com/fivetwenty-io/pve-cli
BINARY  := ./dist/pve
SCRIPTS := ./scripts

# ANSI color helpers — used in help awk block
GREEN  := \033[0;32m
YELLOW := \033[0;33m
CYAN   := \033[0;36m
RESET  := \033[0m

.DEFAULT_GOAL := help

##@ Help

.PHONY: help
help: ## Show this help message
	@awk 'BEGIN { \
		FS = ":.*##"; \
		printf "\n$(CYAN)pve-cli$(RESET) — Proxmox VE command-line interface\n"; \
		printf "\nUsage:\n  make $(GREEN)<target>$(RESET)\n"; \
	} \
	/^##@/ { \
		printf "\n$(YELLOW)%s$(RESET)\n", substr($$0, 5); \
	} \
	/^[a-zA-Z_0-9-]+:.*?##/ { \
		printf "  $(GREEN)%-22s$(RESET) %s\n", $$1, $$2; \
	}' $(MAKEFILE_LIST)
	@printf "\n"

##@ Build

.PHONY: build
build: ## Build ./dist/pve binary with version ldflags
	$(SCRIPTS)/build

.PHONY: generate
generate: ## Regenerate generated sources (cluster options schema from apidoc.json)
	@echo "generate: running go generate ./..."
	@go generate ./...

.PHONY: install
install: build ## Install pve binary to $GOPATH/bin (or ~/go/bin)
	@DEST="$${GOPATH:-$$HOME/go}/bin/pve"; \
	cp $(BINARY) "$$DEST"; \
	echo "install: copied $(BINARY) -> $$DEST"

.PHONY: clean
clean: ## Remove ./dist/ build artifacts
	@rm -rf ./dist
	@echo "clean: removed ./dist/"

##@ Quality

.PHONY: fmt
fmt: ## Format Go sources (gofmt + goimports if available)
	$(SCRIPTS)/fmt

.PHONY: vet
vet: ## Run go vet on all packages
	@echo "vet: running go vet ./..."
	@go vet ./...

.PHONY: lint
lint: ## Run golangci-lint (falls back to go vet if not installed)
	$(SCRIPTS)/lint

.PHONY: staticcheck
staticcheck: ## Run staticcheck static analysis
	@if command -v staticcheck >/dev/null 2>&1; then \
		echo "staticcheck: running staticcheck ./..."; \
		staticcheck ./...; \
	else \
		echo "staticcheck: not found — install: go install honnef.co/go/tools/cmd/staticcheck@latest"; \
		exit 1; \
	fi

.PHONY: check
check: fmt vet lint test ## Run fmt + vet + lint + unit tests (full quality gate)

##@ Test

.PHONY: test
test: ## Run unit tests
	$(SCRIPTS)/test unit

.PHONY: test-unit
test-unit: ## Run unit tests (explicit)
	$(SCRIPTS)/test unit

.PHONY: test-integration
test-integration: ## Run integration tests (requires config/.env.test or PVE_TEST_*)
	$(SCRIPTS)/test integration

.PHONY: test-e2e
test-e2e: ## Run end-to-end happy-path sweep of all command trees (CONTEXT=lab; PBS_CONTEXT=<pbs ctx> opts into the pbs tree)
	$(SCRIPTS)/e2e $(if $(CONTEXT),--context $(CONTEXT),) $(if $(PBS_CONTEXT),--pbs-context $(PBS_CONTEXT),) $(TREES)

.PHONY: test-e2e-mutate
test-e2e-mutate: ## Run the e2e sweep plus the destructive qemu/lxc verb matrix (CONTEXT=lab; PBS_CONTEXT=<pbs ctx> opts into the pbs tree)
	$(SCRIPTS)/e2e --mutate $(if $(CONTEXT),--context $(CONTEXT),) $(if $(PBS_CONTEXT),--pbs-context $(PBS_CONTEXT),) $(TREES)

.PHONY: test-lifecycle
test-lifecycle: ## Run destructive VM+CT lifecycle on an isolated SDN/pool (CONTEXT=lab)
	$(SCRIPTS)/lifecycle $(if $(CONTEXT),--context $(CONTEXT),) $(LIFECYCLE_ARGS)

.PHONY: test-race
test-race: ## Run unit tests with race detector
	$(SCRIPTS)/test unit --race

.PHONY: coverage
coverage: ## Generate test coverage report (HTML + console summary)
	@echo "coverage: running go test -coverprofile=coverage.out ./..."
	@go test -coverprofile=coverage.out ./...
	@go tool cover -func=coverage.out
	@go tool cover -html=coverage.out -o coverage.html
	@echo "coverage: HTML report written to coverage.html"

##@ Release

.PHONY: release-check
release-check: ## Validate .goreleaser.yaml
	goreleaser check

.PHONY: release-snapshot
release-snapshot: ## Build a local cross-platform release into dist/ (no publish)
	goreleaser release --snapshot --clean

.PHONY: release-publish
release-publish: ## Publish the GitHub release for the current tag (requires a v* tag + GITHUB_TOKEN)
	goreleaser release --clean

.PHONY: release
release: ## (legacy) Cross-compile via scripts/release; prefer release-snapshot
	$(SCRIPTS)/release

.PHONY: tag
tag: ## Create and push a git tag (VERSION required, e.g. make tag VERSION=v1.2.3)
	@if [ -z "$(VERSION)" ]; then \
		echo "usage: make tag VERSION=v1.2.3"; \
		exit 1; \
	fi
	@echo "tag: creating $(VERSION)"
	git tag -a "$(VERSION)" -m "Release $(VERSION)"
	@echo "tag: push with: git push origin $(VERSION)"

##@ Package

.PHONY: package
package: ## Produce release tarballs from dist/ binaries
	$(SCRIPTS)/package

.PHONY: package-deb
package-deb: ## Print Debian packaging instructions
	$(SCRIPTS)/package --deb

##@ Dev

.PHONY: run
run: build ## Build and run pve (pass ARGS="..." for arguments)
	@$(BINARY) $(ARGS)

.PHONY: completions
completions: build ## Generate shell completions into dist/completions/
	@mkdir -p ./dist/completions
	@$(BINARY) completion bash  > ./dist/completions/pve.bash
	@$(BINARY) completion zsh   > ./dist/completions/_pve
	@$(BINARY) completion fish  > ./dist/completions/pve.fish
	@$(BINARY) completion powershell > ./dist/completions/pve.ps1
	@echo "completions: written to ./dist/completions/"

.PHONY: deps
deps: ## Tidy Go module dependencies
	@echo "deps: running go mod tidy"
	@go mod tidy
	@echo "deps: ok"
