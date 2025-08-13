.PHONY: help deps lint test test-integration build docker docker-up docker-down migrate clean dev install-tools test-ci postman-newman postman-e2e

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

migrate-up: ## Run database migrations
	@if [ -z "${DATABASE_URL}" ]; then \
		echo "DATABASE_URL is not set. Using default."; \
		export DATABASE_URL="postgres://athena_user:athena_password@localhost:5432/athena?sslmode=disable"; \
	fi; \
	psql "${DATABASE_URL}" -f init-shared-db.sql

migrate-test: ## Run test database migrations
	psql "postgres://test_user:test_password@localhost:5433/athena_test?sslmode=disable" -f init-test-db.sql

validate-openapi: ## Validate OpenAPI specification
	@if command -v swagger >/dev/null 2>&1; then \
		swagger validate api/openapi.yaml; \
	else \
		echo "Swagger CLI not installed."; \
		echo "Install with: npm install -g @apidevtools/swagger-cli"; \
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
	@BASE_URL?=http://localhost:8080; \
	docker run --rm -t \
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
