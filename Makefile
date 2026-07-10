# Makefile for pmx-cli
# Default target: help (categorized, colored)

MODULE  := github.com/fivetwenty-io/pmx-cli
BINARY  := ./dist/pmx
SCRIPTS := ./scripts

# Version + date for man-page headers. Derived from git so `make man` is
# reproducible and matches goreleaser ({{ .Version }} / {{ .CommitDate }}).
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
MAN_DATE ?= $(shell git log -1 --format=%cs 2>/dev/null || echo 1970-01-01)

# --- Install locations (FHS / GNU coding standards) ---------------------------
# Override any: `make install PREFIX=$$HOME/.local`, `make install DESTDIR=/tmp/stage`.
PREFIX      ?= /usr/local
DESTDIR     ?=
BINDIR      ?= $(PREFIX)/bin
DATAROOT    ?= $(PREFIX)/share
MANDIR      ?= $(DATAROOT)/man
BASHCOMPDIR ?= $(DATAROOT)/bash-completion/completions
ZSHCOMPDIR  ?= $(DATAROOT)/zsh/site-functions
FISHCOMPDIR ?= $(DATAROOT)/fish/vendor_completions.d
# install(1) portable across BSD (macOS) and GNU. Never use -D (BSD lacks it).
INSTALL         ?= install
INSTALL_PROGRAM ?= $(INSTALL) -m 0755
INSTALL_DATA    ?= $(INSTALL) -m 0644
PERSONAS := pve pbs pdm
# Man pages are gzipped on install (distro convention; -n strips name/mtime -> reproducible).
GZIP ?= gzip -9n

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
		printf "\n$(CYAN)pmx-cli$(RESET) — Proxmox command-line interface\n"; \
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
build: ## Build ./dist/pmx binary (+ pve/pbs/pdm persona symlinks) with version ldflags
	$(SCRIPTS)/build
	@ln -sf pmx ./dist/pve
	@ln -sf pmx ./dist/pbs
	@ln -sf pmx ./dist/pdm
	@echo "build: linked ./dist/pve -> pmx"
	@echo "build: linked ./dist/pbs -> pmx"
	@echo "build: linked ./dist/pdm -> pmx"

.PHONY: generate
generate: ## Regenerate generated sources (cluster options schema from apidoc.json)
	@echo "generate: running go generate ./..."
	@go generate ./...

.PHONY: man
man: ## Generate roff man pages into dist/man/ (man1 + man5, all personas)
	@go run ./cmd/docgen -out ./dist/man -version "$(VERSION)" -date "$(MAN_DATE)"

.PHONY: check-docs
check-docs: ## Smoke-test man page generation (CI gate; not part of `check`)
	@tmp=$$(mktemp -d) && go run ./cmd/docgen -out "$$tmp/man" -version dev -date 1970-01-01 >/dev/null && \
		rm -rf "$$tmp" && echo "check-docs: man generation ok"

.PHONY: install
install: build man completions ## Install pmx + personas, man pages, completions under $(DESTDIR)$(PREFIX) (default /usr/local; may need sudo)
	$(INSTALL) -d "$(DESTDIR)$(BINDIR)"
	$(INSTALL_PROGRAM) $(BINARY) "$(DESTDIR)$(BINDIR)/pmx"
	@for p in $(PERSONAS); do ln -sf pmx "$(DESTDIR)$(BINDIR)/$$p"; done
	@echo "install: pmx + personas -> $(DESTDIR)$(BINDIR)"
	$(INSTALL) -d "$(DESTDIR)$(MANDIR)/man1" "$(DESTDIR)$(MANDIR)/man5"
	@for m in ./dist/man/man1/*.1; do \
		[ -e "$$m" ] || continue; \
		$(GZIP) -c "$$m" > "$(DESTDIR)$(MANDIR)/man1/$$(basename $$m).gz"; \
	done
	@for m in ./dist/man/man5/*.5; do \
		[ -e "$$m" ] || continue; \
		$(GZIP) -c "$$m" > "$(DESTDIR)$(MANDIR)/man5/$$(basename $$m).gz"; \
	done
	@echo "install: man pages -> $(DESTDIR)$(MANDIR)"
	$(INSTALL) -d "$(DESTDIR)$(BASHCOMPDIR)" "$(DESTDIR)$(ZSHCOMPDIR)" "$(DESTDIR)$(FISHCOMPDIR)"
	$(INSTALL_DATA) ./dist/completions/pmx.bash "$(DESTDIR)$(BASHCOMPDIR)/pmx"
	$(INSTALL_DATA) ./dist/completions/_pmx     "$(DESTDIR)$(ZSHCOMPDIR)/_pmx"
	$(INSTALL_DATA) ./dist/completions/pmx.fish "$(DESTDIR)$(FISHCOMPDIR)/pmx.fish"
	@echo "install: completions (bash/zsh/fish) -> $(DESTDIR)$(DATAROOT)"
	@echo "install: done (prefix $(DESTDIR)$(PREFIX)). Permission denied? re-run: sudo make install"

.PHONY: uninstall
uninstall: ## Remove everything `make install` placed under $(DESTDIR)$(PREFIX)
	rm -f "$(DESTDIR)$(BINDIR)/pmx"
	@for p in $(PERSONAS); do rm -f "$(DESTDIR)$(BINDIR)/$$p"; done
	rm -f "$(DESTDIR)$(MANDIR)"/man1/pmx.1.gz "$(DESTDIR)$(MANDIR)"/man1/pmx-*.1.gz
	@for p in $(PERSONAS); do \
		rm -f "$(DESTDIR)$(MANDIR)/man1/$$p.1.gz" "$(DESTDIR)$(MANDIR)"/man1/$$p-*.1.gz; \
	done
	rm -f "$(DESTDIR)$(MANDIR)/man5/pmx-config.5.gz"
	rm -f "$(DESTDIR)$(BASHCOMPDIR)/pmx" "$(DESTDIR)$(ZSHCOMPDIR)/_pmx" "$(DESTDIR)$(FISHCOMPDIR)/pmx.fish"
	@echo "uninstall: removed pmx artifacts from $(DESTDIR)$(PREFIX)"

.PHONY: install-user
install-user: build ## Install pmx + personas to $$GOPATH/bin (or ~/go/bin) — no sudo, no man pages
	@BIN="$${GOPATH:-$$HOME/go}/bin"; \
	mkdir -p "$$BIN"; \
	cp $(BINARY) "$$BIN/pmx"; \
	for p in $(PERSONAS); do ln -sf pmx "$$BIN/$$p"; done; \
	echo "install-user: pmx + personas -> $$BIN"

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
test-integration: ## Run integration tests (requires config/.env.test or PMX_TEST_*)
	$(SCRIPTS)/test integration

.PHONY: test-e2e
test-e2e: ## Run end-to-end happy-path sweep of all command trees (CONTEXT=lab; PBS_CONTEXT=<pbs ctx> opts into the pbs tree; PDM_CONTEXT=<pdm ctx> opts into the pdm tree)
	$(SCRIPTS)/e2e $(if $(CONTEXT),--context $(CONTEXT),) $(if $(PBS_CONTEXT),--pbs-context $(PBS_CONTEXT),) $(if $(PDM_CONTEXT),--pdm-context $(PDM_CONTEXT),) $(TREES)

.PHONY: test-e2e-mutate
test-e2e-mutate: ## Run the e2e sweep plus the destructive qemu/lxc verb matrix (CONTEXT=lab; PBS_CONTEXT=<pbs ctx> opts into the pbs tree; PDM_CONTEXT=<pdm ctx> opts into the pdm tree)
	$(SCRIPTS)/e2e --mutate $(if $(CONTEXT),--context $(CONTEXT),) $(if $(PBS_CONTEXT),--pbs-context $(PBS_CONTEXT),) $(if $(PDM_CONTEXT),--pdm-context $(PDM_CONTEXT),) $(TREES)

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

.PHONY: packages
packages: ## Build .deb + .rpm packages (and archives) into dist/ via goreleaser snapshot
	goreleaser release --snapshot --clean --skip=publish,announce
	@ls -1 dist/*.deb dist/*.rpm

.PHONY: release-publish
release-publish: ## Publish the GitHub release for the current tag (requires a v* tag + GITHUB_TOKEN)
	goreleaser release --clean

.PHONY: release
release: ## (legacy) Cross-compile via scripts/release; prefer release-snapshot
	$(SCRIPTS)/release

.PHONY: tag
tag: ## Create and push a git tag (VERSION required, e.g. make tag VERSION=v1.2.3)
	@if [ "$(filter command%,$(origin VERSION))" = "" ]; then \
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
run: build ## Build and run pmx (pass ARGS="..." for arguments)
	@$(BINARY) $(ARGS)

.PHONY: completions
completions: build ## Generate shell completions into dist/completions/
	@mkdir -p ./dist/completions
	@$(BINARY) completion bash  > ./dist/completions/pmx.bash
	@$(BINARY) completion zsh   > ./dist/completions/_pmx
	@$(BINARY) completion fish  > ./dist/completions/pmx.fish
	@$(BINARY) completion powershell > ./dist/completions/pmx.ps1
	@echo "completions: written to ./dist/completions/"

.PHONY: deps
deps: ## Tidy Go module dependencies
	@echo "deps: running go mod tidy"
	@go mod tidy
	@echo "deps: ok"
