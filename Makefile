SHELL := /bin/bash

.PHONY: help deps lint test test-unit test-integration test-integration-ci build docker docker-up docker-down clean dev install-tools test-ci postman-newman postman-e2e run logs run-with-encoding act-test
.PHONY: migrate-dev migrate-test migrate-custom migrate-dev-docker migrate-test-docker migrate-up db-ensure-dev-user
.PHONY: validate-all validate-quick
.PHONY: coverage-check coverage-report coverage-per-package
.PHONY: update-readme-metrics check-readme-metrics

.PHONY: install-hooks
install-hooks: ## Configure Git to use repo .githooks
	@git config core.hooksPath .githooks
	@echo "Git hooks path set to .githooks"

# Use docker compose v2 if available; override with DOCKER_COMPOSE="docker-compose" if needed
DOCKER_COMPOSE ?= docker compose
TEST_PROFILE_SERVICES ?= postgres-test redis-test ipfs-test clamav-test app-test

# Offline toolchain toggle:
# - Set GO_OFFLINE=1 to force Go to use the locally installed toolchain
#   (equivalent to GOTOOLCHAIN=local). Use this when your environment blocks
#   network access for automatic toolchain downloads.
# - Note: Your locally installed Go must satisfy go.mod (>= 1.23.4).
ifeq ($(GO_OFFLINE),1)
GO_ENV := GOTOOLCHAIN=local
else
GO_ENV :=
endif

# Default target
help: ## Display this help message
	@echo "Available targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

validate-all: ## Run all validation checks (formatting, linting, tests, build)
	@echo "Running comprehensive validation suite..."
	@./scripts/validate-all.sh

validate-quick: fmt-check lint ## Quick validation (formatting + linting only)
	@echo "Quick validation complete!"

update-readme-metrics: ## Recompute and refresh README metric tables
	@./scripts/update-readme-metrics.sh

check-readme-metrics: ## Verify README metric tables are up to date
	@./scripts/update-readme-metrics.sh --check


deps: ## Download Go dependencies
	go mod download
	go mod tidy

lint: ## Run golangci-lint (auto-fixes incl. import sorting)
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run --fix ./...; \
	else \
		echo "golangci-lint not installed. Installing..."; \
		brew install golangci-lint; \
		golangci-lint run --fix ./...; \
	fi

fmt: ## Format Go files (incl. import sorting)
	@# Sort and group imports, then run gofmt simplify
	@if command -v goimports >/dev/null 2>&1; then \
		goimports -w $(shell git ls-files "*.go"); \
	else \
		echo "goimports not installed. Installing (requires internet)..."; \
		$(GO_ENV) go install golang.org/x/tools/cmd/goimports@latest || echo "Install goimports manually to enable import sorting"; \
		command -v goimports >/dev/null 2>&1 && goimports -w $(shell git ls-files "*.go") || true; \
	fi
	@gofmt -s -w $(shell git ls-files "*.go")

fmt-check: ## Verify Go files are formatted and imports sorted
	@set -e; \
	unformatted=$$(go fmt ./...); \
	unsorted=""; \
	if command -v goimports >/dev/null 2>&1; then \
		unsorted=$$(goimports -l $(shell git ls-files "*.go")); \
	else \
		echo "Note: goimports not found; skipping import sort check. Run 'make install-tools' to install."; \
	fi; \
	if [ -n "$$unformatted" ] || [ -n "$$unsorted" ]; then \
		[ -n "$$unformatted" ] && { echo "The following files need gofmt:"; echo "$$unformatted"; }; \
		[ -n "$$unsorted" ] && { echo "The following files need import sorting (goimports):"; echo "$$unsorted"; }; \
		echo "Run: make fmt"; \
		exit 1; \
	else \
		echo "All Go files are formatted and imports are sorted."; \
	fi

test: ## Run unit tests (without race detection for speed)
	$(GO_ENV) go test -v -coverprofile=coverage.out ./...
	$(GO_ENV) go tool cover -html=coverage.out -o coverage.html

# Coverage threshold (percentage). Adjust upward as coverage improves.
COVERAGE_THRESHOLD ?= 50

coverage-check: ## Run tests and fail if coverage drops below threshold
	@echo "Running tests with coverage..."
	@$(GO_ENV) go test -coverprofile=coverage.out ./... > /dev/null 2>&1 || true
	@COVERAGE=$$($(GO_ENV) go tool cover -func=coverage.out | grep '^total:' | awk '{print $$NF}' | tr -d '%'); \
	echo "Total coverage: $${COVERAGE}% (threshold: $(COVERAGE_THRESHOLD)%)"; \
	if [ $$(echo "$${COVERAGE} < $(COVERAGE_THRESHOLD)" | bc -l) -eq 1 ]; then \
		echo "FAIL: Coverage $${COVERAGE}% is below threshold $(COVERAGE_THRESHOLD)%"; \
		exit 1; \
	else \
		echo "PASS: Coverage $${COVERAGE}% meets threshold $(COVERAGE_THRESHOLD)%"; \
	fi

coverage-per-package: ## Check per-package coverage thresholds
	@if [ ! -f coverage.out ]; then \
		echo "coverage.out not found. Run 'make test-unit' with COVERAGE_OUT=coverage.out first."; \
		exit 1; \
	fi
	@./scripts/check-per-package-coverage.sh coverage.out

coverage-report: ## Generate and open HTML coverage report
	@$(GO_ENV) go test -coverprofile=coverage.out ./...
	@$(GO_ENV) go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report written to coverage.html"
	@open coverage.html 2>/dev/null || xdg-open coverage.html 2>/dev/null || echo "Open coverage.html in your browser"

test-race: ## Run unit tests with race detection (requires CGO_ENABLED=1 and gcc)
	CGO_ENABLED=1 $(GO_ENV) go test -v -race -coverprofile=coverage.out ./...
	$(GO_ENV) go tool cover -html=coverage.out -o coverage.html

test-unit: ## Run unit tests (exclude DB-backed repository pkg and integration tests)
	@set -e; \
	PKGS=$$(go list ./... | grep -v "/internal/repository$$" | grep -v '^athena/tests/integration$$'); \
	echo "Running unit tests in: $$PKGS"; \
	if [ -n "$${COVERAGE_OUT:-}" ]; then \
		echo "Writing coverage profile to $${COVERAGE_OUT}"; \
		$(GO_ENV) go test -v -parallel=8 -short -covermode=atomic -coverprofile="$${COVERAGE_OUT}" $$PKGS; \
	else \
		$(GO_ENV) go test -v -parallel=8 -short $$PKGS; \
	fi

test-unit-race: ## Run unit tests with race detection (requires CGO_ENABLED=1 and gcc)
	@set -e; \
	PKGS=$$(go list ./... | grep -v "/internal/repository$$" | grep -v '^athena/tests/integration$$'); \
	echo "Running unit tests with race detection in: $$PKGS"; \
	CGO_ENABLED=1 $(GO_ENV) go test -v -race -parallel=8 -short $$PKGS

test-ci: ## Run tests for CI environment
	$(GO_ENV) go test -v -coverprofile=coverage.out ./...

test-ci-race: ## Run tests for CI environment with race detection (requires CGO_ENABLED=1 and gcc)
	CGO_ENABLED=1 $(GO_ENV) go test -v -race -coverprofile=coverage.out ./...

.PHONY: act-test
act-test: ## Run primary GitHub Actions jobs locally with act (requires .secrets)
	@if [ ! -f .secrets ]; then \
		echo "Missing .secrets file. Create one (see CONTRIBUTING.md) before running act."; \
		exit 1; \
	fi
	act -j unit --secret-file .secrets
	act -j lint --secret-file .secrets

.PHONY: generate-openapi
generate-openapi: ## Regenerate OpenAPI types and server interfaces
	@scripts/gen-openapi.sh

.PHONY: verify-openapi
verify-openapi: ## Verify generated OpenAPI types match the spec (fails on drift)
	@scripts/verify-openapi.sh

test-integration: ## Run only integration tests (loads .env.test if present)
	@bash -lc 'set -a; [ -f .env.test ] && source .env.test || true; set +a; $(GO_ENV) go test -v -tags=integration ./tests/integration'

test-integration-race: ## Run integration tests with race detection (requires CGO_ENABLED=1 and gcc)
	@bash -lc 'set -a; [ -f .env.test ] && source .env.test || true; set +a; CGO_ENABLED=1 $(GO_ENV) go test -v -race -tags=integration ./tests/integration'

test-integration-ci: ## Run repository + httpapi Integration tests (CI services env)
	@echo "Running integration tests with short flag to skip load/stress tests..."
	@$(GO_ENV) go test -v -short -parallel=8 ./...

test-integration-ci-race: ## Run integration tests in CI with race detection (requires CGO_ENABLED=1 and gcc)
	@echo "Running integration tests with short flag and race detection..."
	@CGO_ENABLED=1 $(GO_ENV) go test -v -short -race -parallel=8 ./...

.PHONY: test-setup
test-setup: ## Setup test environment with DNS and port checks
	@./scripts/test-setup.sh

test-local: test-setup ## Run tests with local Docker services
	@echo "Pre-flight cleanup for test-local..."
	-COMPOSE_PROJECT_NAME=athena-test $(DOCKER_COMPOSE) --profile test down -v 2>/dev/null || true
	@echo "Starting test services..."
	COMPOSE_PROJECT_NAME=athena-test $(DOCKER_COMPOSE) --profile test up -d $(TEST_PROFILE_SERVICES)
	@echo "Waiting for Postgres on 5433..."
	@bash -lc 'for i in $$(seq 1 60); do pg_isready -h 127.0.0.1 -p 5433 -d athena_test -U test_user >/dev/null 2>&1 && exit 0; sleep 1; done; echo "Postgres not ready"; exit 1'
	@echo "Waiting for Redis on 6380..."
	@bash -lc 'for i in $$(seq 1 60); do redis-cli -u redis://127.0.0.1:6380 ping >/dev/null 2>&1 && exit 0; sleep 1; done; echo "Redis not ready"; exit 1'
	DATABASE_URL="postgres://test_user:test_password@localhost:5433/athena_test?sslmode=disable" \
	TEST_DATABASE_URL="postgres://test_user:test_password@localhost:5433/athena_test?sslmode=disable" \
	REDIS_URL="redis://localhost:6380/0" \
	JWT_SECRET="test-jwt-secret" \
	IPFS_API="http://localhost:15001" \
	$(GO_ENV) go test -v -coverprofile=coverage.out ./...
	@echo "Cleaning up test services..."
	COMPOSE_PROJECT_NAME=athena-test $(DOCKER_COMPOSE) --profile test down -v

test-local-race: test-setup ## Run tests with local Docker services with race detection (requires CGO_ENABLED=1 and gcc)
	@echo "Pre-flight cleanup for test-local..."
	-COMPOSE_PROJECT_NAME=athena-test $(DOCKER_COMPOSE) --profile test down -v 2>/dev/null || true
	@echo "Starting test services..."
	COMPOSE_PROJECT_NAME=athena-test $(DOCKER_COMPOSE) --profile test up -d $(TEST_PROFILE_SERVICES)
	@echo "Waiting for Postgres on 5433..."
	@bash -lc 'for i in $$(seq 1 60); do pg_isready -h 127.0.0.1 -p 5433 -d athena_test -U test_user >/dev/null 2>&1 && exit 0; sleep 1; done; echo "Postgres not ready"; exit 1'
	@echo "Waiting for Redis on 6380..."
	@bash -lc 'for i in $$(seq 1 60); do redis-cli -u redis://127.0.0.1:6380 ping >/dev/null 2>&1 && exit 0; sleep 1; done; echo "Redis not ready"; exit 1'
	DATABASE_URL="postgres://test_user:test_password@localhost:5433/athena_test?sslmode=disable" \
	TEST_DATABASE_URL="postgres://test_user:test_password@localhost:5433/athena_test?sslmode=disable" \
	REDIS_URL="redis://localhost:6380/0" \
	JWT_SECRET="test-jwt-secret" \
	IPFS_API="http://localhost:15001" \
	CGO_ENABLED=1 $(GO_ENV) go test -v -race -coverprofile=coverage.out ./...
	@echo "Cleaning up test services..."
	COMPOSE_PROJECT_NAME=athena-test $(DOCKER_COMPOSE) --profile test down -v

test-integration-local: ## Run only integration tests with local Docker services
	COMPOSE_PROJECT_NAME=athena-test $(DOCKER_COMPOSE) --profile test up -d $(TEST_PROFILE_SERVICES)
	@echo "Waiting for Postgres on 5433..."
	@bash -lc 'for i in $$(seq 1 60); do pg_isready -h 127.0.0.1 -p 5433 -d athena_test -U test_user >/dev/null 2>&1 && exit 0; sleep 1; done; echo "Postgres not ready"; exit 1'
	@echo "Waiting for Redis on 6380..."
	@bash -lc 'for i in $$(seq 1 60); do redis-cli -u redis://127.0.0.1:6380 ping >/dev/null 2>&1 && exit 0; sleep 1; done; echo "Redis not ready"; exit 1'
	DATABASE_URL="postgres://test_user:test_password@localhost:5433/athena_test?sslmode=disable" \
	TEST_DATABASE_URL="postgres://test_user:test_password@localhost:5433/athena_test?sslmode=disable" \
	REDIS_URL="redis://localhost:6380/0" \
	JWT_SECRET="test-jwt-secret" \
	IPFS_API="http://localhost:15001" \
	$(GO_ENV) go test -v -tags=integration ./tests/integration
	COMPOSE_PROJECT_NAME=athena-test $(DOCKER_COMPOSE) --profile test down -v

test-integration-local-race: ## Run integration tests with local Docker services with race detection (requires CGO_ENABLED=1 and gcc)
	COMPOSE_PROJECT_NAME=athena-test $(DOCKER_COMPOSE) --profile test up -d $(TEST_PROFILE_SERVICES)
	@echo "Waiting for Postgres on 5433..."
	@bash -lc 'for i in $$(seq 1 60); do pg_isready -h 127.0.0.1 -p 5433 -d athena_test -U test_user >/dev/null 2>&1 && exit 0; sleep 1; done; echo "Postgres not ready"; exit 1'
	@echo "Waiting for Redis on 6380..."
	@bash -lc 'for i in $$(seq 1 60); do redis-cli -u redis://127.0.0.1:6380 ping >/dev/null 2>&1 && exit 0; sleep 1; done; echo "Redis not ready"; exit 1'
	DATABASE_URL="postgres://test_user:test_password@localhost:5433/athena_test?sslmode=disable" \
	TEST_DATABASE_URL="postgres://test_user:test_password@localhost:5433/athena_test?sslmode=disable" \
	REDIS_URL="redis://localhost:6380/0" \
	JWT_SECRET="test-jwt-secret" \
	IPFS_API="http://localhost:15001" \
	CGO_ENABLED=1 $(GO_ENV) go test -v -race -tags=integration ./tests/integration
	COMPOSE_PROJECT_NAME=athena-test $(DOCKER_COMPOSE) --profile test down -v


# Helper: ensure dev DB role/db exists inside docker postgres
db-ensure-dev-user:
	@echo "Ensuring dev DB role/database exist in docker postgres..."
	@if ! $(DOCKER_COMPOSE) ps postgres >/dev/null 2>&1; then \
		echo "Postgres container not found. Starting it..."; \
		$(DOCKER_COMPOSE) up -d postgres >/dev/null; \
		sleep 2; \
	fi
	@# Parse DB credentials from .env DATABASE_URL
	@set -e; \
	DB_URL=""; \
	if [ -f .env ]; then \
		DB_URL=$$(grep -E '^[[:space:]]*(export[[:space:]]+)?DATABASE_URL[[:space:]]*=' .env | head -n1 | sed -E 's/^[[:space:]]*(export[[:space:]]+)?DATABASE_URL[[:space:]]*=[[:space:]]*//' | sed -E 's/[[:space:]]*#.*$$//'); \
	fi; \
	DB_USER=$$(echo "$$DB_URL" | sed -E 's#^postgres://([^:@/]+)(:.*)?@.*#\1#'); \
	DB_PASS=$$(echo "$$DB_URL" | sed -E 's#^postgres://[^:]+:([^@]+)@.*#\1#'); \
	DB_NAME=$$(echo "$$DB_URL" | sed -E 's#.*/([^/?]+)(\?.*)?$$#\1#'); \
	if [ -z "$$DB_USER" ] || [ -z "$$DB_PASS" ] || [ -z "$$DB_NAME" ]; then \
		echo "Falling back to default docker credentials (athena_user/athena_password/athena)"; \
		DB_USER=athena_user; DB_PASS=athena_password; DB_NAME=athena; \
	fi; \
	echo "Using DB user '$$DB_USER' and database '$$DB_NAME' from .env"; \
	COMPOSE_INTERACTIVE_NO_CLI=1 $(DOCKER_COMPOSE) exec -T postgres psql -U athena_user -d athena -c "SELECT 1" 2>&1 >/dev/null || echo "Note: Database connection check"; \
	echo "Role/database ensured in docker postgres."

migrate-dev: ## Apply migrations to development database (uses .env) [idempotent]
	@echo "Loading DATABASE_URL from .env..."
	@if [ ! -f .env ]; then \
		echo ".env file not found. Please create it (e.g., cp .env.example .env)."; \
		exit 2; \
	fi; \
	DB_URL=$$(grep -E '^[[:space:]]*(export[[:space:]]+)?DATABASE_URL[[:space:]]*=' .env | head -n1 | sed -E 's/^[[:space:]]*(export[[:space:]]+)?DATABASE_URL[[:space:]]*=[[:space:]]*//' | sed -E 's/[[:space:]]*#.*$$//'); \
	if [ -z "$$DB_URL" ]; then \
		echo "DATABASE_URL not found in .env file. Please check your .env configuration."; \
		exit 2; \
	fi; \
	echo "Testing database connection..."; \
	CONN_ERR=$$(psql "$$DB_URL" -c '\q' 2>&1 >/dev/null || true); \
	if echo "$$CONN_ERR" | grep -qi "role .* does not exist"; then \
		echo "Detected missing DB role for connection URL: $$CONN_ERR"; \
		echo "Attempting to ensure role/database via docker (service 'postgres')..."; \
		$(MAKE) db-ensure-dev-user || true; \
	fi; \
	if ! psql "$$DB_URL" -c '\q' >/dev/null 2>&1; then \
		echo "Unable to connect to database using DATABASE_URL. If you use Docker, run 'make docker-up' or 'make docker-reset' then retry, or update .env to valid local credentials."; \
		exit 2; \
	fi; \
	echo "Applying migrations (idempotent) to development database: $$DB_URL"; \
	DATABASE_URL="$$DB_URL" bash ./scripts/migrate_idempotent.sh

migrate-test: ## Apply migrations to test database (uses .env.test) [idempotent]
	@echo "Loading test environment from .env.test..."
	@set -a; [ -f .env.test ] && source .env.test; set +a; \
	if [ -z "$$DATABASE_URL" ]; then \
		echo "DATABASE_URL not found in .env.test file. Please check your .env.test configuration."; \
		exit 2; \
	fi; \
	echo "Applying migrations (idempotent) to test database: $$DATABASE_URL"; \
	DATABASE_URL="$$DATABASE_URL" bash ./scripts/migrate_idempotent.sh

migrate-custom: ## Apply migrations to custom DATABASE_URL (set via environment) [idempotent]
	@if [ -z "${DATABASE_URL}" ]; then \
		echo "DATABASE_URL is not set. Export it to run migrations."; \
		echo "Example: DATABASE_URL=\"postgres://user:pass@host:port/db\" make migrate-custom"; \
		exit 2; \
	fi; \
	echo "Applying migrations (idempotent) to custom database: ${DATABASE_URL}"; \
	DATABASE_URL="${DATABASE_URL}" bash ./scripts/migrate_idempotent.sh

# docker-up moved to avoid duplicates - see line 101
# docker-down moved to avoid duplicates - see line 107
# dev moved to avoid duplicates - see line 169

run: ## Run server locally (requires local Postgres/Redis/IPFS env)
	go run ./cmd/server

logs: ## Tail app logs
	docker compose logs -f app

build: ## Build the server binary
	go build -o bin/athena-server ./cmd/server

build-cli: ## Build the CLI tool
	go build -o bin/athena-cli ./cmd/cli

docker: ## Build Docker image
	docker build -t athena-server:latest .

docker-up: ## Start docker-compose services
	$(DOCKER_COMPOSE) up -d
	@echo "Waiting for services to be healthy..."
	@sleep 15
	@echo "Services are up! Application available at http://localhost:8080"

docker-down: ## Stop docker-compose services
	$(DOCKER_COMPOSE) down

docker-logs: ## View docker-compose logs
	$(DOCKER_COMPOSE) logs -f

docker-reset: ## Reset docker environment (remove volumes)
	$(DOCKER_COMPOSE) down -v
	$(DOCKER_COMPOSE) up -d

migrate-dev-docker: ## Apply development migrations using Docker Postgres container
	@echo "Applying development migrations inside docker service 'postgres'..."
	@$(DOCKER_COMPOSE) ps postgres >/dev/null 2>&1 || { echo "Postgres container not found. Run 'make docker-up' first."; exit 1; }
	@$(MAKE) db-ensure-dev-user
	@set -e; \
	shopt -s nullglob; \
	for f in migrations/*.sql; do \
		echo "Applying $$f via Docker"; \
		$(DOCKER_COMPOSE) exec -T postgres psql -U athena_user -d athena -f /dev/stdin < "$$f"; \
	done; \
	echo "Development Docker migrations applied successfully."

migrate-test-docker: ## Apply test migrations using Docker test Postgres container
	@echo "Applying test migrations inside docker service 'postgres-test'..."
	@COMPOSE_PROJECT_NAME=athena-test $(DOCKER_COMPOSE) --profile test up -d postgres-test >/dev/null
	@echo "Waiting for postgres-test to be healthy..." && sleep 3
	@set -e; \
	shopt -s nullglob; \
	for f in migrations/*.sql; do \
		echo "Applying $$f via Docker test container"; \
		COMPOSE_PROJECT_NAME=athena-test $(DOCKER_COMPOSE) --profile test exec -T postgres-test psql -U test_user -d athena_test -f /dev/stdin < "$$f"; \
	done; \
	echo "Test Docker migrations applied successfully."

validate-openapi: ## Validate OpenAPI specification
	@if command -v swagger-cli >/dev/null 2>&1; then \
		swagger-cli validate api/openapi.yaml; \
	else \
		if command -v swagger >/dev/null 2>&1; then \
			swagger validate api/openapi.yaml; \
		else \
			echo "Swagger CLI not installed."; \
			echo "Install with: npm install -g @apidevtools/swagger-cli"; \
			exit 1; \
		fi; \
	fi

generate-docs: ## Generate API documentation from OpenAPI spec
	@echo "Generating API documentation..."
	@if [ -f "openapi.yaml" ] || [ -f "api/openapi.yaml" ]; then \
		if command -v redocly >/dev/null 2>&1; then \
			redocly build-docs openapi.yaml -o docs/api/index.html 2>/dev/null || \
			redocly build-docs api/openapi.yaml -o docs/api/index.html 2>/dev/null || \
			echo "Failed to generate documentation"; \
		else \
			echo "Redocly CLI not installed."; \
			echo "Install with: npm install -g @redocly/cli"; \
		fi; \
	else \
		echo "No OpenAPI specification found"; \
		mkdir -p docs/api; \
		echo "<html><body><h1>API Documentation</h1><p>OpenAPI spec not found</p></body></html>" > docs/api/index.html; \
	fi

serve-docs: generate-docs ## Serve OpenAPI documentation
	@echo "Serving API documentation at http://localhost:8081"
	@cd docs/api && python3 -m http.server 8081 &
	@sleep 1
	@open http://localhost:8081 || xdg-open http://localhost:8081 || echo "Open http://localhost:8081 in your browser"

clean: ## Clean build artifacts
	rm -rf bin/
	rm -f coverage.out coverage.html
	go clean -cache -testcache

.PHONY: test-cleanup
test-cleanup: ## Clean up ALL test containers and ports
	@echo "Performing comprehensive test cleanup..."
	@echo "Stopping all test-related containers..."
	-docker stop $$(docker ps -aq --filter "name=athena-test") 2>/dev/null || true
	-docker stop $$(docker ps -aq --filter "name=athena_test") 2>/dev/null || true
	@echo "Removing test containers..."
	-docker rm -f $$(docker ps -aq --filter "name=athena-test") 2>/dev/null || true
	-docker rm -f $$(docker ps -aq --filter "name=athena_test") 2>/dev/null || true
	@echo "Cleaning up docker-compose projects..."
	-COMPOSE_PROJECT_NAME=athena-test $(DOCKER_COMPOSE) --profile test down --remove-orphans --volumes 2>/dev/null || true
	@echo "Removing test networks..."
	-docker network rm athena-test_test-network 2>/dev/null || true
	-docker network rm athena_test_default 2>/dev/null || true
	@echo "Pruning unused docker resources..."
	-docker network prune -f 2>/dev/null || true
	@echo "Test cleanup complete!"

.PHONY: test-ports-check
test-ports-check: ## Check if test ports are available
	@echo "Checking test port availability..."
	@PORT_CONFLICTS=""; \
	if lsof -Pi :5433 -sTCP:LISTEN -t >/dev/null 2>&1; then \
		echo "✗ Port 5433 (Postgres test) is in use"; \
		PORT_CONFLICTS="yes"; \
	else \
		echo "✓ Port 5433 (Postgres test) is available"; \
	fi; \
	if lsof -Pi :6380 -sTCP:LISTEN -t >/dev/null 2>&1; then \
		echo "✗ Port 6380 (Redis test) is in use"; \
		PORT_CONFLICTS="yes"; \
	else \
		echo "✓ Port 6380 (Redis test) is available"; \
	fi; \
	if lsof -Pi :15001 -sTCP:LISTEN -t >/dev/null 2>&1; then \
		echo "✗ Port 15001 (IPFS test) is in use"; \
		PORT_CONFLICTS="yes"; \
	else \
		echo "✓ Port 15001 (IPFS test) is available"; \
	fi; \
	if lsof -Pi :18080 -sTCP:LISTEN -t >/dev/null 2>&1; then \
		echo "✗ Port 18080 (App test) is in use"; \
		PORT_CONFLICTS="yes"; \
	else \
		echo "✓ Port 18080 (App test) is available"; \
	fi; \
	if [ -n "$$PORT_CONFLICTS" ]; then \
		echo ""; \
		echo "Some test ports are in use. Run 'make test-cleanup' to free them."; \
		exit 1; \
	fi

dev: ## Run development server with live reload
	@if [ ! -f .env ]; then \
		echo "Creating .env from .env.example..."; \
		cp .env.example .env; \
	fi
	@if command -v air >/dev/null 2>&1; then \
		air; \
	else \
		echo "Air not installed. Running without hot reload..."; \
		go run ./cmd/server; \
	fi

install-tools: ## Install development tools
	@echo "Installing development tools..."
	go install github.com/air-verse/air@latest
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install golang.org/x/tools/cmd/goimports@latest
	@echo "Installing Node.js tools..."
	npm install -g @apidevtools/swagger-cli @redocly/cli
	@echo "Development tools installation completed!"

# Run Postman collection against a running server
postman-newman: ## Run Postman auth tests via Newman (server must be running)
	@echo "Running Postman collection with Newman..."
	@BASE_URL=http://localhost:8080; \
	docker run --rm --network host \
	  -v "$(PWD)/postman:/etc/newman" \
	  postman/newman:alpine \
	  run /etc/newman/athena-auth.postman_collection.json \
	  --env-var baseUrl=$$BASE_URL \
	  --reporters cli,junit \
	  --reporter-junit-export /etc/newman/newman-results.xml

# Spin up test stack, app, then run Newman end-to-end
load-test: ## Run k6 load test (requires app running at localhost:8080)
	@echo "Running k6 load test..."
	@if ! docker ps | grep -q "athena"; then \
		echo "Warning: Athena app container not detected. Ensure app is running on localhost:8080"; \
	fi
	docker run --rm -i --network host \
		-v $$(pwd)/tests/loadtest:/src \
		-e BASE_URL=http://localhost:8080 \
		grafana/k6 run /src/k6-video-platform.js

postman-e2e: ## Start test services + app and run Newman end-to-end
	@echo "========================================="
	@echo "Postman E2E Tests - Starting..."
	@echo "========================================="

	@echo "[1/8] Pre-flight cleanup - removing any existing test containers..."
	-docker stop $$(docker ps -aq --filter "name=athena-test") 2>/dev/null || true
	-docker rm -f $$(docker ps -aq --filter "name=athena-test") 2>/dev/null || true
	-docker rm -f athena-test-redis athena-test-postgres athena-test-api 2>/dev/null || true
	-COMPOSE_PROJECT_NAME=athena-test $(DOCKER_COMPOSE) --profile test down --remove-orphans --volumes 2>/dev/null || true
	-docker network rm athena-test_test-network 2>/dev/null || true

	@echo "[2/8] Checking port availability..."
	@if lsof -Pi :6380 -sTCP:LISTEN -t >/dev/null 2>&1; then \
		echo "  WARNING: Port 6380 is in use. Attempting to free it..."; \
		PID=$$(lsof -Pi :6380 -sTCP:LISTEN -t); \
		echo "  Process $$PID is using port 6380"; \
		docker ps --filter "publish=6380" --format "table {{.Names}}\t{{.Ports}}" | grep 6380 || true; \
		docker stop $$(docker ps --filter "publish=6380" -q) 2>/dev/null || true; \
	fi
	@if lsof -Pi :5433 -sTCP:LISTEN -t >/dev/null 2>&1; then \
		echo "  WARNING: Port 5433 is in use. Attempting to free it..."; \
		docker stop $$(docker ps --filter "publish=5433" -q) 2>/dev/null || true; \
	fi

	@echo "[3/8] Removing old Docker image..."
	docker rmi athena:latest 2>/dev/null || true

	@echo "[4/8] Building fresh Docker image..."
	docker build -t athena:latest . --no-cache

	@echo "[5/8] Starting test stack (DB, Redis, App, IPFS, ClamAV)..."
	COMPOSE_PROJECT_NAME=athena-test $(DOCKER_COMPOSE) --profile test up -d --build postgres-test redis-test ipfs-test clamav-test app-test

	@echo "[6/8] Waiting for services to be healthy..."
	@echo "  Checking postgres-test..."
	@for i in $$(seq 1 30); do \
		if docker exec $$(COMPOSE_PROJECT_NAME=athena-test $(DOCKER_COMPOSE) --profile test ps -q postgres-test) pg_isready -U test_user -d athena_test >/dev/null 2>&1; then \
			echo "  ✓ postgres-test is ready"; break; \
		fi; \
		echo -n "."; sleep 1; \
	done

	@echo "  Checking redis-test..."
	@for i in $$(seq 1 30); do \
		if docker exec $$(COMPOSE_PROJECT_NAME=athena-test $(DOCKER_COMPOSE) --profile test ps -q redis-test) redis-cli ping >/dev/null 2>&1; then \
			echo "  ✓ redis-test is ready"; break; \
		fi; \
		echo -n "."; sleep 1; \
	done

	@echo "  Checking app-test health..."
	@for i in $$(seq 1 40); do \
		status=$$(docker inspect --format='{{json .State.Health.Status}}' $$(COMPOSE_PROJECT_NAME=athena-test $(DOCKER_COMPOSE) --profile test ps -q app-test) 2>/dev/null | tr -d '"'); \
		if [ "$$status" = "healthy" ]; then \
			echo "  ✓ app-test is healthy"; break; \
		fi; \
		echo -n "."; sleep 2; \
	done

	@echo "[7/8] Running Newman tests against http://app-test:8080..."
	COMPOSE_PROJECT_NAME=athena-test $(DOCKER_COMPOSE) --profile test run --rm newman || { \
		echo "=========================================" ; \
		echo "Newman tests FAILED" ; \
		echo "Preserving logs for debugging..." ; \
		COMPOSE_PROJECT_NAME=athena-test $(DOCKER_COMPOSE) --profile test logs app-test | tail -100 > postman-e2e-failure.log ; \
		echo "Logs saved to postman-e2e-failure.log" ; \
		echo "=========================================" ; \
		COMPOSE_PROJECT_NAME=athena-test $(DOCKER_COMPOSE) --profile test down -v ; \
		exit 1; \
	}

	@echo "[8/8] Cleaning up test environment..."
	COMPOSE_PROJECT_NAME=athena-test $(DOCKER_COMPOSE) --profile test down --remove-orphans --volumes

	@echo "========================================="
	@echo "Postman E2E Tests - PASSED ✓"
	@echo "========================================="


setup: ## Initial project setup
	@echo "Setting up Athena project..."
	@if [ ! -f .env ]; then \
		cp .env.example .env; \
		echo "Created .env file from .env.example"; \
	fi
	@make deps
	@make install-tools
	@echo "Starting Docker services..."
	@make docker-up
	@echo "Running database migrations..."
	@sleep 5
	@make migrate-up
	@echo "Setup complete! Run 'make dev' to start the development server."

ci-test: deps lint test-ci ## Run CI test pipeline
	@echo "CI test pipeline completed successfully!"

ci-build: ci-test build ## Run CI build pipeline
	@echo "CI build pipeline completed successfully!"

run-with-encoding: ## Run server with encoding workers enabled
	@echo "Starting server with encoding workers..."
	@ENABLE_ENCODING=true ENCODING_WORKERS=2 METRICS_ADDR=:9090 go run ./cmd/server

# Common alias used in README and scripts
migrate: migrate-dev ## Alias: apply migrations to development database

# ============================================================================
# Goose Migration Management
# ============================================================================

.PHONY: migrate-up
migrate-up:  ## Apply all pending migrations using Goose
	@echo "Applying pending migrations with Goose..."
	@goose -dir migrations postgres "$${DATABASE_URL}" up

.PHONY: migrate-down
migrate-down:  ## Rollback the last migration using Goose
	@echo "Rolling back last migration..."
	@goose -dir migrations postgres "$${DATABASE_URL}" down

.PHONY: migrate-status
migrate-status:  ## Show migration status using Goose
	@echo "Migration status:"
	@goose -dir migrations postgres "$${DATABASE_URL}" status

.PHONY: migrate-version
migrate-version:  ## Show current migration version
	@goose -dir migrations postgres "$${DATABASE_URL}" version

.PHONY: migrate-validate
migrate-validate:  ## Validate migration files using Goose
	@echo "Validating migration files..."
	@goose -dir migrations validate

.PHONY: migrate-create
migrate-create:  ## Create new migration file (usage: make migrate-create NAME=add_feature)
	@if [ -z "$(NAME)" ]; then \
		echo "Error: NAME is required."; \
		echo "Usage: make migrate-create NAME=add_feature"; \
		exit 1; \
	fi
	@goose -dir migrations create $(NAME) sql
	@echo "New migration created. Edit the file in migrations/ directory."

.PHONY: migrate-reset
migrate-reset:  ## Reset database to version 0 and re-run all migrations
	@echo "WARNING: This will reset the database!"
	@echo "Press Ctrl+C to cancel, or wait 5 seconds to continue..."
	@sleep 5
	@goose -dir migrations postgres "$${DATABASE_URL}" reset
	@goose -dir migrations postgres "$${DATABASE_URL}" up

