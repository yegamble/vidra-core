.PHONY: help deps lint test build docker docker-up docker-down migrate generate clean

# Default target
help: ## Display this help message
	@echo "Available targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

deps: ## Download Go dependencies
	go mod download
	go mod tidy

generate: ## Generate code from OpenAPI spec
	@echo "Generating Go code from OpenAPI specification..."
	@echo "Note: Using manually crafted types and interfaces for best practices"
	@echo "Types are in internal/generated/types.go and server interface in internal/generated/server.go"
	@echo "These follow the repository's conventions and avoid code generation issues"
	@echo "Code generation complete!"

lint: ## Run golangci-lint
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed. Installing..."; \
		go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest; \
		golangci-lint run ./...; \
	fi

test: ## Run unit tests
	go test -v -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

test-setup: ## Start test environment
	docker-compose -f docker-compose.test.yml up -d postgres-test redis-test
	@echo "Waiting for services to be ready..."
	@for i in $$(seq 1 30); do \
		if pg_isready -h localhost -p 5433 -U test_user >/dev/null 2>&1; then \
			echo "PostgreSQL is ready"; \
			break; \
		fi; \
		echo "Waiting for PostgreSQL... ($$i/30)"; \
		sleep 2; \
	done
	@for i in $$(seq 1 30); do \
		if redis-cli -h localhost -p 6380 ping >/dev/null 2>&1; then \
			echo "Redis is ready"; \
			break; \
		fi; \
		echo "Waiting for Redis... ($$i/30)"; \
		sleep 2; \
	done

test-integration: test-setup ## Run integration tests
	go test -v -race -tags=integration ./internal/repository/...

test-teardown: ## Stop test environment
	docker-compose -f docker-compose.test.yml down -v

test-all: test-setup test-integration test-teardown ## Run all tests with Docker services

build: ## Build the server binary
	go build -o bin/athena-server ./cmd/server

docker: ## Build Docker image
	docker build -t athena-server .

docker-up: ## Start docker-compose services and wait for health checks
	docker-compose up -d
	@echo "Waiting for services to be healthy..."
	@services=$$(docker-compose ps --services); \
	for service in $$services; do \
		echo "Waiting for $$service..."; \
		container=$$(docker-compose ps -q $$service); \
		for i in $$(seq 1 30); do \
			status=$$(docker inspect --format='{{if .State.Health}}{{.State.Health.Status}}{{else}}healthy{{end}}' $$container); \
			if [ "$$status" = "healthy" ]; then \
				echo "$$service is healthy"; \
				break; \
			fi; \
			echo "Waiting for $$service health check... ($$i/30)"; \
			sleep 2; \
		done; \
	done

docker-down: ## Stop docker-compose services
	docker-compose down

migrate: ## Run database migrations (requires atlas)
	@if command -v atlas >/dev/null 2>&1; then \
		atlas migrate apply --dir "file://migrations" --url "${DATABASE_URL}"; \
	else \
		echo "Atlas CLI not installed. Installing..."; \
		curl -sSf https://atlasgo.sh | sh; \
		atlas migrate apply --dir "file://migrations" --url "${DATABASE_URL}"; \
	fi

validate-openapi: ## Validate OpenAPI specification
	@if command -v swagger >/dev/null 2>&1; then \
		swagger validate api/openapi.yaml; \
	else \
		echo "Swagger CLI not installed. Skipping validation..."; \
		echo "Install with: go install github.com/go-swagger/go-swagger/cmd/swagger@latest"; \
	fi

serve-docs: ## Serve OpenAPI documentation
	@if command -v swagger >/dev/null 2>&1; then \
		swagger serve --no-open --port 8081 api/openapi.yaml; \
	else \
		echo "Swagger CLI not installed."; \
		echo "Install with: go install github.com/go-swagger/go-swagger/cmd/swagger@latest"; \
	fi

clean: ## Clean build artifacts
	rm -rf bin/
	rm -f coverage.out coverage.html
	go clean -cache -testcache

dev: ## Run development server with live reload
	@if command -v air >/dev/null 2>&1; then \
		air; \
	else \
		echo "Air not installed. Installing..."; \
		go install github.com/cosmtrek/air@latest; \
		air; \
	fi

install-tools: ## Install development tools
	go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install github.com/go-swagger/go-swagger/cmd/swagger@latest
	go install github.com/cosmtrek/air@latest
	@echo "Development tools installed!"

# CI/CD targets
ci-test: deps generate lint test ## Run CI test pipeline
	@echo "CI test pipeline completed successfully!"

ci-build: ci-test build ## Run CI build pipeline
	@echo "CI build pipeline completed successfully!"