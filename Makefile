# Vidra core developer commands. Run `make help` for the list.

.DEFAULT_GOAL := help
SHELL := /bin/bash

DATABASE_URL ?= postgres://vidra:vidra@localhost:5432/vidra?sslmode=disable

# Build metadata injected into internal/version via -ldflags. Falls back to
# safe defaults outside a git checkout.
VERSION    ?= 0.1.0
COMMIT     := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
VERSION_PKG := github.com/vidra/vidra-core/internal/version
LDFLAGS := -X $(VERSION_PKG).Version=$(VERSION) -X $(VERSION_PKG).Commit=$(COMMIT) -X $(VERSION_PKG).Date=$(BUILD_DATE)

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

.PHONY: tidy
tidy: ## Sync go.mod/go.sum
	go mod tidy

.PHONY: fmt
fmt: ## Format Go code
	go fmt ./...

.PHONY: fmt-check
fmt-check: ## Fail if any Go file is not gofmt-clean (CI-safe, non-mutating)
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then \
		echo "Not gofmt-clean:"; echo "$$out"; exit 1; fi

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: test
test: ## Run unit tests
	go test ./...

.PHONY: test-race
test-race: ## Run tests with the race detector
	go test -race ./...

.PHONY: cover
cover: ## Run tests with coverage summary
	go test -cover ./...

.PHONY: build
build: ## Build the api binary into ./bin (injects version metadata)
	go build -ldflags "$(LDFLAGS)" -o bin/api ./cmd/api

.PHONY: run
run: ## Run the api server locally (needs Postgres + Redis)
	go run ./cmd/api

.PHONY: sqlc
sqlc: ## Generate typed query code (requires sqlc installed)
	sqlc generate

.PHONY: openapi-lint
openapi-lint: ## Lint the OpenAPI contract (requires npx; uses Redocly CLI)
	npx --yes @redocly/cli@1 lint api/openapi.yaml   # pinned 1.x; keep in lock-step with openapi.yml

.PHONY: openapi-verify
openapi-verify: ## Verify routes match api/openapi.yaml (documentation drift guard)
	go test ./internal/httpapi/ -run TestOpenAPIContract

.PHONY: docs-check
docs-check: openapi-verify ## Run the documentation stop guard (route<->spec drift)
	@echo "docs-check: OpenAPI contract is in sync with the router."
	@echo "Reminder: confirm README.md and .ralph/specs/ reflect this change too."

.PHONY: migrate-up
migrate-up: ## Apply migrations against DATABASE_URL (requires migrate CLI)
	migrate -path migrations -database "$(DATABASE_URL)" up

.PHONY: migrate-down
migrate-down: ## Roll back one migration
	migrate -path migrations -database "$(DATABASE_URL)" down 1

.PHONY: up
up: ## Start the local Docker stack (postgres, redis, migrate, api)
	docker compose --profile core up --build

.PHONY: down
down: ## Stop the local Docker stack
	docker compose --profile core down

.PHONY: check
check: fmt vet test ## Run the standard local gate (fmt, vet, test)

# ci is the single source of truth for the gate. CI (backend-ci.yml) runs THIS
# exact target, so "passes locally" == "passes in GitHub". Keep CI and local in
# lock-step by adding any new required check here, never only in the workflow.
# Assumes Postgres/Redis are reachable (run `make up` locally; CI provides them).
.PHONY: ci
ci: fmt-check vet openapi-verify test-race ## Canonical CI gate (run locally to mirror GitHub exactly)
	@echo "ci: gate passed (fmt-check, vet, openapi-verify, test-race)."
