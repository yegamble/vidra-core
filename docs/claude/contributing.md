# Claude Contributing Guide - Vidra Core Backend

## Overview

This guide helps Claude AI assistants contribute effectively to the Vidra Core codebase while maintaining project standards and conventions.

## Before You Start

1. **Read CLAUDE.md** - Contains system-specific requirements and constraints
2. **Check existing code** - Always examine neighboring files for patterns
3. **Verify dependencies** - Never assume libraries are available; check go.mod
4. **Respect boundaries** - Stay within assigned package responsibilities

## Directory Boundaries

### DO modify

- `/internal/*` - Core application code
- `/cmd/server` - Main application entry
- `/migrations` - Database migrations (forward-only)
- `/docs` - Documentation updates

### DO NOT modify without explicit request

- `/vendor` - Dependency vendoring
- `/.github` - CI/CD workflows
- `/scripts` - Build and deployment scripts
- `go.mod/go.sum` - Only via `go get` command

## Code Standards

### Required Checks

Run these commands before considering any task complete:

```bash
# Format code
go fmt ./...

# Run linter
golangci-lint run ./...

# Run tests
go test -short -race ./...

# Check for vulnerabilities
go vet ./...
```

### Style Guidelines

1. **No Comments** - Code should be self-documenting unless explicitly requested
2. **Error Wrapping** - Always wrap errors with context: `fmt.Errorf("operation failed: %w", err)`
3. **Context Usage** - All network/DB operations must accept context
4. **Naming** - Follow Go conventions (exported = PascalCase, private = camelCase)

## Testing Requirements

### Unit Tests

- Place in same package with `_test.go` suffix
- Use table-driven tests for multiple cases
- Mock external dependencies
- Aim for >80% coverage of business logic

### Integration Tests

- Use `//go:build integration` build tag
- Require Docker test environment
- Clean up test data after completion
- Test actual database/Redis/IPFS interactions

Example test structure:

```go
//go:build integration

func TestVideoUpload_Integration(t *testing.T) {
    // Setup
    ctx := context.Background()
    db := setupTestDB(t)
    defer cleanupTestDB(t, db)

    // Test
    // ...

    // Assert
    require.NoError(t, err)
    assert.Equal(t, expected, actual)
}
```

## Database Migrations

### Using Goose

```bash
# Create new migration
make migrate-create NAME=add_column

# Apply all pending migrations
make migrate-up

# Rollback last migration
make migrate-down

# Check migration status
make migrate-status
```

### Migration Rules

- Forward-only (no rollbacks)
- No data loss without explicit approval
- Test with sample data first
- Include migration in PR description

## API Changes

### OpenAPI Updates

1. Modify OpenAPI spec if changing API contracts
2. Regenerate types: `make generate-api`
3. Update tests to match new contracts
4. Document breaking changes clearly

### Endpoint Patterns

- RESTful naming: `/api/v1/resource/{id}/action`
- Consistent error responses (problem details)
- Support `Idempotency-Key` for mutations
- Include pagination for list endpoints

## Federation Code

### ATProto Handling

- Validate signatures on incoming requests
- Use exponential backoff for retries
- Respect rate limits from remote instances
- Log federation events for debugging

### Bluesky Integration

- Check `BLUESKY_ENABLED` flag
- Handle firehose disconnections gracefully
- Queue failed operations for retry
- Monitor ingestion metrics

## Security Practices

### NEVER

- Log sensitive data (passwords, tokens, keys)
- Commit secrets to repository
- Trust user input without validation
- Execute user-provided code/commands
- Bypass authentication/authorization checks

### ALWAYS

- Validate and sanitize input
- Use parameterized queries (SQLX)
- Check file types before processing
- Limit request sizes
- Implement timeouts for external calls

## Common Tasks

### Adding a New Endpoint

1. Define route in `/internal/httpapi/routes.go`
2. Create handler in appropriate file
3. Add validation for request body
4. Implement business logic in usecase layer
5. Add repository methods if needed
6. Write unit and integration tests
7. Update OpenAPI specification
8. Add to Postman collection if applicable

### Modifying Database Schema

1. Create migration with Goose (`make migrate-create NAME=...`)
2. Update domain models
3. Update repository interfaces
4. Modify repository implementations
5. Update affected usecases
6. Test with integration tests
7. Document in PR

### Adding Background Job

1. Define job type in `/internal/worker`
2. Implement job processor
3. Add to worker pool
4. Set up Redis queue if needed
5. Add monitoring/metrics
6. Handle failures with retry logic
7. Test with timeout scenarios

## Debugging Commands

```bash
# Check test database
DATABASE_URL="postgres://test_user:test_password@localhost:5433/vidra_test?sslmode=disable" \
  go test -v -short -race ./internal/repository

# Run specific test
go test -run TestVideoUpload ./internal/httpapi

# Check Redis
redis-cli -h localhost -p 6379 ping

# Verify IPFS
curl http://localhost:5001/api/v0/version

# Check running services
docker compose ps

# View logs
docker compose logs -f server
```

## Performance Guidelines

- Pool database connections
- Cache frequently accessed data
- Use pagination for large results
- Implement request coalescing
- Add circuit breakers for external services
- Monitor memory usage in workers
- Profile CPU hotspots regularly

## When to Ask for Help

Request user guidance when:

- Making breaking API changes
- Modifying CI/CD pipelines
- Adding new dependencies
- Changing authentication flow
- Altering database indexes
- Implementing new federation features

## Final Checklist

Before completing any task:

- [ ] Code compiles without warnings
- [ ] All tests pass
- [ ] Linter reports no issues
- [ ] No secrets in code
- [ ] Migrations tested
- [ ] Documentation updated
- [ ] Performance impact considered
