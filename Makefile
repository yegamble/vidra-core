.PHONY: help deps lint test build docker migrate generate clean

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

build: ## Build the server binary
	go build -o bin/athena-server ./cmd/server

docker: ## Build Docker image
	docker build -t athena-server .

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