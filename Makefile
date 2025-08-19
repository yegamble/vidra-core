.PHONY: help deps lint test test-integration build docker docker-up docker-down migrate clean dev install-tools test-ci postman-newman postman-e2e run logs migrate-up migrate-up-docker migrate-test migrate-test-docker run-encoder

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
	@diffs=$(shell gofmt -s -l $(shell git ls-files "*.go")); \
	if [ -n "$$diffs" ]; then \
		echo "The following files are not formatted:"; \
		echo "$$diffs"; \
		exit 1; \
	else \
		echo "All Go files are properly formatted."; \
	fi

test: ## Run unit tests
	go test -v -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

test-ci: ## Run tests for CI environment
	go test -v -race -coverprofile=coverage.out ./...

test-integration: ## Run only integration tests (loads .env.test if present)
	@bash -lc 'set -a; [ -f .env.test ] && source .env.test || true; set +a; go test -v -race -run Integration ./...'

test-local: ## Run tests with local Docker services
	docker-compose -f docker-compose.test.yml up -d
	@echo "Waiting for services to be ready..."
	@sleep 10
	DATABASE_URL="postgres://test_user:test_password@localhost:5433/athena_test?sslmode=disable" \
	REDIS_URL="redis://localhost:6380/0" \
	JWT_SECRET="test-jwt-secret" \
	IPFS_API="http://localhost:5001" \
	go test -v -race -coverprofile=coverage.out ./...
	docker-compose -f docker-compose.test.yml down -v

test-integration-local: ## Run only integration tests with local Docker services
	docker-compose -f docker-compose.test.yml up -d
	@echo "Waiting for services to be ready..."
	@sleep 10
	DATABASE_URL="postgres://test_user:test_password@localhost:5433/athena_test?sslmode=disable" \
	REDIS_URL="redis://localhost:6380/0" \
	JWT_SECRET="test-jwt-secret" \
	IPFS_API="http://localhost:5001" \
	go test -v -race -run Integration ./...
	docker-compose -f docker-compose.test.yml down -v

migrate-migrations: ## Apply all SQL migrations in migrations/ to DATABASE_URL
	@if [ -z "${DATABASE_URL}" ]; then \
		echo "DATABASE_URL is not set. Export it to run migrations."; \
		exit 2; \
	fi; \
	set -e; \
	shopt -s nullglob; \
	for f in migrations/*.sql; do \
		echo "Applying $$f"; \
		psql "${DATABASE_URL}" -v ON_ERROR_STOP=1 -f "$$f"; \
	done; \
	echo "Migrations applied successfully."

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
	docker-compose up -d
	@echo "Waiting for services to be healthy..."
	@sleep 15
	@echo "Services are up! Application available at http://localhost:8080"

docker-down: ## Stop docker-compose services
	docker-compose down

docker-logs: ## View docker-compose logs
	docker-compose logs -f

docker-reset: ## Reset docker environment (remove volumes)
	docker-compose down -v
	docker-compose up -d

migrate-up: ## Run database migrations against local DB (psql)
	@if [ -z "${DATABASE_URL}" ]; then \
		echo "DATABASE_URL is not set. Using default."; \
		export DATABASE_URL="postgres://athena_user:athena_password@127.0.0.1:5432/athena?sslmode=disable"; \
	fi; \
	psql "${DATABASE_URL}" -f init-shared-db.sql || { \
		echo "\npsql migration failed. If you are using Docker, try:\n  make migrate-up-docker\n"; \
		exit 2; \
	}

migrate-up-docker: ## Run database migrations inside the postgres container
	@echo "Applying migrations inside docker service 'postgres'..."
	@$(DOCKER_COMPOSE) ps postgres >/dev/null 2>&1 || { echo "Postgres container not found. Run 'make docker-up' first."; exit 1; }
	-@$(DOCKER_COMPOSE) cp init-shared-db.sql postgres:/tmp/init.sql
	@$(DOCKER_COMPOSE) exec -T postgres psql -U athena_user -d athena -f /tmp/init.sql

migrate-test: ## Run test database migrations against local test DB (psql)
	psql "postgres://test_user:test_password@127.0.0.1:5433/athena_test?sslmode=disable" -f init-test-db.sql || { \
		echo "\npsql test migration failed. If you are using Docker, try:\n  make migrate-test-docker\n"; \
		exit 2; \
	}

migrate-test-docker: ## Run test DB migrations inside the postgres-test container
	@echo "Applying test migrations inside docker service 'postgres-test'..."
	@$(DOCKER_COMPOSE) -f docker-compose.test.yml up -d postgres-test >/dev/null
	@echo "Waiting for postgres-test to be healthy..." && sleep 3
	-@$(DOCKER_COMPOSE) -f docker-compose.test.yml cp init-test-db.sql postgres-test:/tmp/init.sql
	@$(DOCKER_COMPOSE) -f docker-compose.test.yml exec -T postgres-test psql -U test_user -d athena_test -f /tmp/init.sql

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
	@echo "Starting test stack (DB, Redis, App)..."
	docker-compose -f docker-compose.test.yml up -d --build
	@echo "Waiting for app-test to be healthy..."
	@for i in $$(seq 1 40); do \
	  status=$$(docker inspect --format='{{json .State.Health.Status}}' $$(docker-compose -f docker-compose.test.yml ps -q app-test) 2>/dev/null | tr -d '"'); \
	  if [ "$$status" = "healthy" ]; then echo "app-test is healthy"; break; fi; \
	  sleep 2; \
	done
	@echo "Running Newman inside compose network against http://app-test:8080 ..."
	docker-compose -f docker-compose.test.yml run --rm newman || { \
	  echo "Newman tests failed"; \
	  docker-compose -f docker-compose.test.yml down -v; \
	  exit 1; \
	}
	@echo "Shutting down test stack..."
	docker-compose -f docker-compose.test.yml down -v

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

ENCODER_WORKERS ?= 2
METRICS_ADDR ?= :9090
UPLOADS_DIR ?= ./uploads
FFMPEG_PATH ?= ffmpeg

run-encoder: ## Run encoding worker with metrics
	@echo "Starting encoder (workers=$(ENCODER_WORKERS), metrics=$(METRICS_ADDR))..."
	@ENCODER_WORKERS=$(ENCODER_WORKERS) METRICS_ADDR=$(METRICS_ADDR) UPLOADS_DIR=$(UPLOADS_DIR) FFMPEG_PATH=$(FFMPEG_PATH) \
		go run ./cmd/encoder
