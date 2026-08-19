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

.PHONY: test-integration
test-integration: ## Run integration tests (-tags=integration); needs DATABASE_URL, REDIS_URL, ffmpeg — each test self-skips if its dependency is absent
	go test -tags=integration -race ./...

.PHONY: test-ipfs-integration
test-ipfs-integration: ## Run real-Kubo tests; public proof additionally needs IPFS_TEST_PUBLIC_GATEWAY_URL + IPFS_TEST_VIDEO_PATH (self-skips unless required)
	go test -count=1 -v -tags=ipfs_integration -race ./internal/ipfs/...

.PHONY: test-ipfs-private-integration
test-ipfs-private-integration: ## Run PRIVATE-swarm IPFS integration tests (-tags=ipfs_private_integration) against a swarm.key'd kubo pair; needs IPFS_PRIVATE_TEST_API_A/_B (+ _OUTSIDE, _KUBO_BIN, _CLUSTER_API) — self-skips if unset. -v so the CI log shows each proof's RUN/PASS (this target is NOT part of `make ci`, so the canonical gate stays quiet).
	go test -count=1 -v -tags=ipfs_private_integration -race ./internal/ipfs/...

.PHONY: bench
bench: ## Run the hot-path benchmarks (NOT in the gate); set DATABASE_URL for the store feed/search benches
	go test -tags=integration -run='^$$' -bench=. -benchmem ./...

.PHONY: build
build: ## Build the api binary into ./bin (injects version metadata)
	go build -ldflags "$(LDFLAGS)" -o bin/api ./cmd/api

.PHONY: run
run: ## Run the api server locally (needs Postgres + Redis)
	go run ./cmd/api

# Pinned sqlc release. sqlc-verify enforces this version so "current" means the
# same thing on every machine and in CI; keep it in lock-step with the version
# headers in internal/store/sqlcgen/*.go and the install step in backend-ci.yml.
SQLC_VERSION := v1.31.1

.PHONY: sqlc
sqlc: ## Generate typed query code (requires sqlc installed)
	sqlc generate

.PHONY: sqlc-verify
sqlc-verify: ## Fail if internal/store/sqlcgen is stale vs queries/migrations (non-mutating sqlc diff)
	@if command -v sqlc >/dev/null 2>&1 && [ "$$(sqlc version)" = "$(SQLC_VERSION)" ]; then \
		sqlc diff || { echo "sqlc-verify: generated code is STALE — run 'make sqlc' and commit internal/store/sqlcgen."; exit 1; }; \
	else \
		echo "sqlc-verify: pinned sqlc $(SQLC_VERSION) not on PATH; falling back to 'go run' (slower)"; \
		go run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION) diff || { echo "sqlc-verify: generated code is STALE — run 'make sqlc' and commit internal/store/sqlcgen."; exit 1; }; \
	fi

.PHONY: openapi-lint
openapi-lint: ## Lint the OpenAPI contract (requires npx; uses Redocly CLI)
	npx --yes @redocly/cli@1 lint api/openapi.yaml   # pinned 1.x; keep in lock-step with openapi.yml

.PHONY: openapi-verify
openapi-verify: ## Verify routes match api/openapi.yaml (documentation drift guard)
	go test ./internal/httpapi/ -run TestOpenAPIContract

.PHONY: postman
postman: ## Regenerate the curated Postman collection + environment from api/openapi.yaml (docs/postman/; requires node)
	node docs/postman/generate.mjs

.PHONY: docs-check
docs-check: openapi-verify ## Run the documentation stop guard (route<->spec drift)
	@echo "docs-check: OpenAPI contract is in sync with the router."
	@echo "Reminder: confirm README.md and .ralph/specs/ reflect this change too."

.PHONY: migrate-up
migrate-up: ## Apply migrations against DATABASE_URL (embedded in the api binary; no CLI needed)
	DATABASE_URL="$(DATABASE_URL)" go run ./cmd/api migrate up

.PHONY: migrate-version
migrate-version: ## Print the schema_migrations version + dirty flag (non-zero exit when dirty)
	DATABASE_URL="$(DATABASE_URL)" go run ./cmd/api migrate version

.PHONY: migrate-down
migrate-down: ## Roll back one migration (rollback is CLI-only: the binary ships `up`/`version`)
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
ci: fmt-check vet openapi-verify sqlc-verify test-race ## Canonical CI gate (run locally to mirror GitHub exactly)
	@echo "ci: gate passed (fmt-check, vet, openapi-verify, sqlc-verify, test-race)."
