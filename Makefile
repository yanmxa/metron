GO      ?= go
BIN     ?= bin/metron
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo devel)

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) | \
	  awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build the binary into bin/
	$(GO) build -ldflags "-X main.version=$(VERSION)" -o $(BIN) ./cmd/metron

.PHONY: test
test: ## Run the tests with the race detector
	$(GO) test -race ./...

.PHONY: lint
lint: ## Run golangci-lint
	golangci-lint run

.PHONY: fmt
fmt: ## Format the source
	gofmt -w .

.PHONY: dogfood
dogfood: build ## Measure this repository with the binary just built
	$(BIN) --all --axes complexity --fail-on headline

.PHONY: check
check: ## Everything CI runs
	@gofmt -l . | grep . && { echo "not gofmt'd"; exit 1; } || true
	$(GO) vet ./...
	$(GO) test -race ./...
	$(MAKE) dogfood

.PHONY: clean
clean: ## Remove build output and caches
	rm -rf bin/ .metron/

.PHONY: skill
skill: ## Regenerate the in-repo agent skill from agent/metron.md
	@./install.sh --skill-only --agent claude

.PHONY: skill-check
skill-check: ## Fail if the generated skill has drifted from agent/metron.md
	@cp .claude/skills/metron/SKILL.md /tmp/metron-skill-before
	@./install.sh --skill-only --agent claude >/dev/null
	@diff -q /tmp/metron-skill-before .claude/skills/metron/SKILL.md >/dev/null || { \
	  echo "the checked-in skill is out of date; run: make skill"; exit 1; }
	@echo "skill is in sync with agent/metron.md"
