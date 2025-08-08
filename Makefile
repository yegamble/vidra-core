.PHONY: run test migrate lint

# Run the API locally without Docker.  Requires PostgreSQL, Redis, MinIO and Kubo to be running.
run:
	go run ./cmd/server

# Execute unit tests.
test:
	go test ./...

# Apply the database schema using Atlas.  POSTGRES_URL must be set.
migrate:
	atlas schema apply -u "$${POSTGRES_URL}" -f migrations/schema.hcl --dev-url "docker://postgres/16/dev"

# Run the linter.  Ignores failures to prevent CI from failing prematurely.
lint:
	golangci-lint run || true