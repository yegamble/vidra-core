# Test Infrastructure

Comprehensive guide to running, writing, and debugging tests for Athena.

## Test Categories

### Unit Tests (`*_test.go`, `*_unit_test.go`)

- **Location**: Alongside source files
- **Require infrastructure**: No (except `internal/repository/` tests)
- **Run with**: `make test-unit` or `go test -short ./...`
- **Speed**: Fast (<30s for full suite)

**Note**: Test file naming is inconsistent - both `*_test.go` and `*_unit_test.go` exist. The Makefile `test-unit` target excludes the repository package (requires DB) rather than relying on naming.

### Integration Tests (`*_integration_test.go`, `tests/integration/`)

- **Location**: `tests/integration/` directory
- **Require infrastructure**: PostgreSQL, Redis, IPFS, ClamAV (Docker)
- **Run with**: `make test-integration` or `go test -tags=integration ./tests/integration`
- **Speed**: Moderate (~2-5 minutes)

### E2E Tests (`tests/e2e/`)

- **Location**: `tests/e2e/` directory
- **Require infrastructure**: Full stack (app + all services)
- **Run with**: `go test ./tests/e2e/...` (skipped with `-short`)
- **Speed**: Slow (~5-10 minutes)

## Infrastructure Requirements

### Local Development

```bash
# Start required services
docker compose up -d postgres redis ipfs

# Verify connectivity
pg_isready -h localhost -p 5432 -U athena_user
redis-cli -h localhost -p 6379 ping
curl http://localhost:5001/api/v0/version
```

### Test Stack (Isolated Ports)

```bash
# Start test services
docker compose --profile test up -d postgres-test redis-test ipfs-test clamav-test

# Ports: 5433 (postgres), 6380 (redis), 15001 (ipfs), 3310 (clamav)
```

### CI Environment

- Uses Docker Compose with `ci` profile
- Services run on standard ports (5432, 6379, 5001, 3310)
- Configured in `.github/workflows/`

## Running Tests

### Common Commands

```bash
# Fast: Unit tests only (exclude repository, skip integration)
make test-unit
# Or: go test -short ./...

# All tests (with coverage)
make test
# Or: go test -coverprofile=coverage.out ./...

# Integration tests with Docker
make test-integration
# Or: docker compose --profile test up -d && go test -tags=integration ./tests/integration

# E2E tests (requires full stack)
go test ./tests/e2e/...

# Race detector (validated clean in Sprint 19)
make test-race
# Or: CGO_ENABLED=1 go test -race -short ./...

# Specific package
go test -v ./internal/usecase/video

# Specific test
go test -run TestCreateVideo ./internal/httpapi
```

### Local Fast Paths

```bash
# Skip long-running tests
go test -short ./...

# Run specific test
go test -run TestName ./package

# Verbose output (debugging)
go test -v ./package

# Clean test cache (if stale)
go clean -testcache
```

## Test Patterns

### Table-Driven Tests

```go
func TestValidateEmail(t *testing.T) {
    tests := []struct {
        name    string
        email   string
        wantErr bool
    }{
        {"valid", "user@example.com", false},
        {"missing @", "userexample.com", true},
        {"empty", "", true},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := ValidateEmail(tt.email)
            if (err != nil) != tt.wantErr {
                t.Errorf("got error %v, wantErr %v", err, tt.wantErr)
            }
        })
    }
}
```

### Repository Tests (sqlmock)

```go
func TestGetVideo(t *testing.T) {
    db, mock, err := sqlmock.New()
    require.NoError(t, err)
    defer db.Close()

    repo := repository.NewVideoRepository(db)

    rows := sqlmock.NewRows([]string{"id", "title"}).
        AddRow("123", "Test Video")
    mock.ExpectQuery("SELECT (.+) FROM videos").WillReturnRows(rows)

    video, err := repo.GetByID(context.Background(), "123")
    require.NoError(t, err)
    assert.Equal(t, "Test Video", video.Title)
}
```

### HTTP Handler Tests (httptest)

```go
func TestCreateVideo(t *testing.T) {
    handler := NewVideoHandler(mockService)

    body := `{"title":"Test"}`
    req := httptest.NewRequest("POST", "/videos", strings.NewReader(body))
    rec := httptest.NewRecorder()

    handler.CreateVideo(rec, req)

    assert.Equal(t, http.StatusCreated, rec.Code)
}
```

### Test Helpers (testutil)

```go
import "athena/internal/testutil"

func TestWithDB(t *testing.T) {
    if testing.Short() {
        t.Skip("Skipping DB test in short mode")
    }

    testDB := testutil.SetupTestDB(t)
    defer testDB.Close()

    // Use testDB.DB for repository tests
}
```

## Coverage

### Check Coverage

```bash
# Overall coverage (with threshold check)
make coverage-check
# Threshold: 50% (see Makefile COVERAGE_THRESHOLD)

# Per-package coverage
make coverage-per-package
# Thresholds: scripts/coverage-thresholds.txt

# HTML report
make coverage-report
# Opens coverage.html in browser
```

### Coverage Thresholds

See `scripts/coverage-thresholds.txt` for per-package targets. Core packages target 80-90% coverage.

Current status (as of Sprint 19):

- **Total coverage**: 62.3% average across 72 packages
- **Test files**: 313
- **Test functions**: 3,752

## CI Configuration

### GitHub Actions Workflows

| Workflow | Runs | Services |
|----------|------|----------|
| `unit-tests.yml` | `make test-unit` | None (short mode) |
| `integration-tests.yml` | `make test-integration-ci` | Docker (`ci` profile) |
| `lint.yml` | `make lint` | None |
| `security-tests.yml` | gosec, SARIF upload | None |

### CI Test Command

```bash
# Integration tests in CI (with services)
make test-integration-ci
# Runs: go test -short -parallel=8 ./...
```

### Reproducing CI Locally

```bash
# Install act (GitHub Actions runner)
# See: https://github.com/nektos/act

# Run unit tests
act -j unit --secret-file .secrets

# Run linting
act -j lint --secret-file .secrets
```

## Race Detector

**Validated clean in Sprint 19** - zero data races detected.

```bash
# Run with race detection
make test-race
# Or: CGO_ENABLED=1 go test -race -short ./...

# Requires CGO and gcc installed
```

## Troubleshooting

### Port Conflicts

```bash
# Check for conflicting services
make test-ports-check

# Clean up test containers
make test-cleanup
```

### Database Connection Errors

```bash
# Ensure services are running
docker compose ps

# Check database is ready
pg_isready -h localhost -p 5432 -U athena_user

# For test database
pg_isready -h localhost -p 5433 -U test_user
```

### Stale Test Cache

```bash
# Clear cache and re-run
go clean -testcache
go test ./package
```

### Tests Pass Locally, Fail in CI

- Check environment variables (CI uses `TEST_DATABASE_URL`, etc.)
- Verify service startup order (healthchecks in docker-compose.yml)
- Check for timing dependencies (add retries or longer timeouts)
