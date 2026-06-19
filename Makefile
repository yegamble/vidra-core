# Vidra core developer commands. Run `make help` for the list.

.DEFAULT_GOAL := help
SHELL := /bin/bash

DATABASE_URL ?= postgres://vidra:vidra@localhost:5432/vidra?sslmode=disable

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
build: ## Build the api binary into ./bin
	go build -o bin/api ./cmd/api

.PHONY: run
run: ## Run the api server locally (needs Postgres + Redis)
	go run ./cmd/api

.PHONY: sqlc
sqlc: ## Generate typed query code (requires sqlc installed)
	sqlc generate

.PHONY: openapi-lint
openapi-lint: ## Lint the OpenAPI contract (requires npx; uses Redocly CLI)
	npx --yes @redocly/cli@latest lint api/openapi.yaml

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
