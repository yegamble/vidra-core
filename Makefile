.PHONY: help deps lint test test-unit test-integration test-integration-ci build docker docker-up docker-down clean dev install-tools test-ci postman-newman postman-e2e run logs run-with-encoding
.PHONY: migrate-dev migrate-test migrate-custom migrate-dev-docker migrate-test-docker

# Use docker compose v2 if available; override with DOCKER_COMPOSE="docker-compose" if needed
DOCKER_COMPOSE ?= docker compose

# Default target
help: ## Display this help message
	@echo "Available targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

deps: ## Download Go dependencies
	go mod download
	go mod tidy

lint: ## Run golangci-lint
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed. Installing..."; \
		    brew install golangci-lint; \
		golangci-lint run ./...; \
	fi

fmt: ## Format Go files
	@gofmt -s -w $(shell git ls-files "*.go")

fmt-check: ## Verify Go files are formatted
	@# Use go fmt which respects modules instead of gofmt directly
	@unformatted=$$(go fmt ./...); \
	if [ -n "$$unformatted" ]; then \
		echo "The following files need formatting:"; \
		echo "$$unformatted"; \
		exit 1; \
	else \
		echo "All Go files are properly formatted."; \
	fi

test: ## Run unit tests
	go test -v -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

test-unit: ## Run unit tests (exclude DB-backed repository pkg)
	@set -e; \
	PKGS=$$(go list ./... | grep -v "/internal/repository$$"); \
	echo "Running unit tests in: $$PKGS"; \
	go test -v -race -parallel=8 $$PKGS

test-ci: ## Run tests for CI environment
	go test -v -race -coverprofile=coverage.out ./...

.PHONY: generate-openapi
generate-openapi: ## Regenerate OpenAPI types and server interfaces
	@scripts/gen-openapi.sh

test-integration: ## Run only integration tests (loads .env.test if present)
	@bash -lc 'set -a; [ -f .env.test ] && source .env.test || true; set +a; go test -v -race -run Integration ./...'

test-integration-ci: ## Run repository + httpapi Integration tests (CI services env)
	@echo "Running repository integration tests..."
	go test -v -race -parallel=8 ./internal/repository
	@echo "Running httpapi integration tests..."
	go test -v -race -parallel=8 ./internal/httpapi -run Integration

test-local: ## Run tests with local Docker services
	COMPOSE_PROJECT_NAME=athena-test $(DOCKER_COMPOSE) -f docker-compose.test.yml up -d
	@echo "Waiting for services to be ready..."
	@sleep 10
	DATABASE_URL="postgres://test_user:test_password@localhost:5433/athena_test?sslmode=disable" \
	REDIS_URL="redis://localhost:6380/0" \
	JWT_SECRET="test-jwt-secret" \
	IPFS_API="http://localhost:15001" \
	go test -v -race -coverprofile=coverage.out ./...
	COMPOSE_PROJECT_NAME=athena-test $(DOCKER_COMPOSE) -f docker-compose.test.yml down -v

test-integration-local: ## Run only integration tests with local Docker services
	COMPOSE_PROJECT_NAME=athena-test $(DOCKER_COMPOSE) -f docker-compose.test.yml up -d
	@echo "Waiting for services to be ready..."
	@sleep 10
	DATABASE_URL="postgres://test_user:test_password@localhost:5433/athena_test?sslmode=disable" \
	REDIS_URL="redis://localhost:6380/0" \
	JWT_SECRET="test-jwt-secret" \
	IPFS_API="http://localhost:15001" \
	go test -v -race -run Integration ./...
	COMPOSE_PROJECT_NAME=athena-test $(DOCKER_COMPOSE) -f docker-compose.test.yml down -v

migrate-dev: ## Apply migrations to development database (uses .env)
	@echo "Loading development environment from .env..."
	@set -a; [ -f .env ] && source .env; set +a; \
	if [ -z "$$DATABASE_URL" ]; then \
		echo "DATABASE_URL not found in .env file. Please check your .env configuration."; \
		exit 2; \
	fi; \
	echo "Applying migrations to development database: $$DATABASE_URL"; \
	set -e; \
	shopt -s nullglob; \
	for f in migrations/*.sql; do \
		echo "Applying $$f"; \
		psql "$$DATABASE_URL" -v ON_ERROR_STOP=1 -f "$$f"; \
	done; \
	echo "Development migrations applied successfully."

migrate-test: ## Apply migrations to test database (uses .env.test)
	@echo "Loading test environment from .env.test..."
	@set -a; [ -f .env.test ] && source .env.test; set +a; \
	if [ -z "$$DATABASE_URL" ]; then \
		echo "DATABASE_URL not found in .env.test file. Please check your .env.test configuration."; \
		exit 2; \
	fi; \
	echo "Applying migrations to test database: $$DATABASE_URL"; \
	set -e; \
	shopt -s nullglob; \
	for f in migrations/*.sql; do \
		echo "Applying $$f"; \
		psql "$$DATABASE_URL" -v ON_ERROR_STOP=1 -f "$$f"; \
	done; \
	echo "Test migrations applied successfully."

migrate-custom: ## Apply migrations to custom DATABASE_URL (set via environment)
	@if [ -z "${DATABASE_URL}" ]; then \
		echo "DATABASE_URL is not set. Export it to run migrations."; \
		echo "Example: DATABASE_URL=\"postgres://user:pass@host:port/db\" make migrate-custom"; \
		exit 2; \
	fi; \
	echo "Applying migrations to custom database: ${DATABASE_URL}"; \
	set -e; \
	shopt -s nullglob; \
	for f in migrations/*.sql; do \
		echo "Applying $$f"; \
		psql "${DATABASE_URL}" -v ON_ERROR_STOP=1 -f "$$f"; \
	done; \
	echo "Custom migrations applied successfully."

# docker-up moved to avoid duplicates - see line 101
# docker-down moved to avoid duplicates - see line 107  
# dev moved to avoid duplicates - see line 169

run: ## Run server locally (requires local Postgres/Redis/IPFS env)
	go run ./cmd/server

logs: ## Tail app logs
	docker compose logs -f app
	TEST_DATABASE_URL="postgres://test_user:test_password@localhost:5433/athena_test?sslmode=disable" \
	DATABASE_URL="postgres://test_user:test_password@localhost:5433/athena_test?sslmode=disable" \
	REDIS_URL="redis://localhost:6380/0" \
	JWT_SECRET="test-jwt-secret" \
	IPFS_API="http://localhost:5001" \
	sh -lc 'go test -v -race ./internal/repository && go test -v -race ./internal/httpapi -run Integration'
	docker-compose -f docker-compose.test.yml down -v

build: ## Build the server binary
	go build -o bin/athena-server ./cmd/server

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
	@set -e; \
	shopt -s nullglob; \
	for f in migrations/*.sql; do \
		echo "Applying $$f via Docker"; \
		$(DOCKER_COMPOSE) exec -T postgres psql -U athena_user -d athena -f /dev/stdin < "$$f"; \
	done; \
	echo "Development Docker migrations applied successfully."

migrate-test-docker: ## Apply test migrations using Docker test Postgres container
	@echo "Applying test migrations inside docker service 'postgres-test'..."
	@COMPOSE_PROJECT_NAME=athena-test $(DOCKER_COMPOSE) -f docker-compose.test.yml up -d postgres-test >/dev/null
	@echo "Waiting for postgres-test to be healthy..." && sleep 3
	@set -e; \
	shopt -s nullglob; \
	for f in migrations/*.sql; do \
		echo "Applying $$f via Docker test container"; \
		COMPOSE_PROJECT_NAME=athena-test $(DOCKER_COMPOSE) -f docker-compose.test.yml exec -T postgres-test psql -U test_user -d athena_test -f /dev/stdin < "$$f"; \
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

serve-docs: ## Serve OpenAPI documentation
	@echo "Opening API documentation at http://localhost:8081"
	@python3 -m http.server 8081 --directory . &
	@open http://localhost:8081/api/openapi.yaml || xdg-open http://localhost:8081/api/openapi.yaml

clean: ## Clean build artifacts
	rm -rf bin/
	rm -f coverage.out coverage.html
	go clean -cache -testcache

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
postman-e2e: ## Start test services + app and run Newman end-to-end
	@echo "Starting test stack (DB, Redis, App, IPFS)..."
	COMPOSE_PROJECT_NAME=athena-test $(DOCKER_COMPOSE) -f docker-compose.test.yml up -d postgres-test redis-test ipfs-test app-test
	@echo "Waiting for app-test to be healthy..."
	@for i in $$(seq 1 40); do \
	  status=$$(docker inspect --format='{{json .State.Health.Status}}' $$(COMPOSE_PROJECT_NAME=athena-test $(DOCKER_COMPOSE) -f docker-compose.test.yml ps -q app-test) 2>/dev/null | tr -d '"'); \
	  if [ "$$status" = "healthy" ]; then echo "app-test is healthy"; break; fi; \
	  sleep 2; \
	done
	@echo "Running Newman inside compose network against http://app-test:8080 ..."
	COMPOSE_PROJECT_NAME=athena-test $(DOCKER_COMPOSE) -f docker-compose.test.yml run --rm newman || { \
	  echo "Newman tests failed"; \
	  COMPOSE_PROJECT_NAME=athena-test $(DOCKER_COMPOSE) -f docker-compose.test.yml down -v; \
	  exit 1; \
	}
	@echo "Shutting down test stack..."
	COMPOSE_PROJECT_NAME=athena-test $(DOCKER_COMPOSE) -f docker-compose.test.yml down -v


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
