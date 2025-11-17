#!/bin/bash
set -e

echo "Starting local test environment..."

# Start test services
echo "Starting Docker services..."
docker-compose -f docker-compose.test.yml up -d postgres-test redis-test

# Wait for services to be ready
echo "Waiting for PostgreSQL..."
for i in {1..30}; do
    if pg_isready -h localhost -p 5433 -U test_user >/dev/null 2>&1; then
        echo "PostgreSQL is ready"
        break
    fi
    echo "Waiting for PostgreSQL... ($i/30)"
    sleep 2
done

echo "Waiting for Redis..."
for i in {1..30}; do
    if redis-cli -h localhost -p 6380 ping >/dev/null 2>&1; then
        echo "Redis is ready"
        break
    fi
    echo "Waiting for Redis... ($i/30)"
    sleep 2
done

echo "Running tests..."

# Run unit tests
echo "Running unit tests..."
go test -v -race -coverprofile=coverage.out ./...

# Run integration tests
echo "Running integration tests..."
go test -v -race -tags=integration ./internal/repository/...

# Generate coverage report
echo "Generating coverage report..."
go tool cover -html=coverage.out -o coverage.html
go tool cover -func=coverage.out

echo "Tests completed successfully!"
echo "Coverage report available at: coverage.html"

# Cleanup
echo "Cleaning up..."
docker-compose -f docker-compose.test.yml down -v

echo "Done!"
