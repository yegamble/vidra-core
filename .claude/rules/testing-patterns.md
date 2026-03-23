# Testing Patterns

Project-specific testing conventions and best practices.

## Test Infrastructure

**testutil package:** Provides test helpers to reduce boilerplate.

### Database Setup

```go
import "vidra-core/internal/testutil"

func TestMyFunction(t *testing.T) {
    if testing.Short() {
        t.Skip("Skipping database tests in short mode")
    }

    testDB := testutil.SetupTestDB(t)
    repo := repository.NewMyRepository(testDB.DB)
    // ... test implementation
}
```

**Why `testing.Short()`:** Integration tests requiring DB/Redis are skipped in CI when infrastructure unavailable. Always guard DB tests with this check.

### Test Data Generators

**Images:**

```go
pngData := testutil.CreateTestPNG()
jpegData := testutil.CreateTestJPEG()
webpData := testutil.CreateTestWebP()
```

**Video:** See `testutil/video_helpers.go` for video generation utilities.

### User Creation Helper

```go
func createTestUser(t *testing.T, userRepo *repository.UserRepository, ctx context.Context, username, email string) *domain.User {
    user := &domain.User{
        ID:       uuid.New(),
        Username: username,
        Email:    email,
        // ... other required fields
    }
    err := userRepo.Create(ctx, user)
    require.NoError(t, err)
    return user
}
```

## Table-Driven Tests

**Standard pattern for testing multiple cases:**

```go
func TestValidateInput(t *testing.T) {
    tests := []struct {
        name    string
        input   string
        wantErr bool
    }{
        {
            name:    "valid input",
            input:   "valid",
            wantErr: false,
        },
        {
            name:    "empty input",
            input:   "",
            wantErr: true,
        },
        {
            name:    "invalid format",
            input:   "!@#$",
            wantErr: true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := ValidateInput(tt.input)
            if (err != nil) != tt.wantErr {
                t.Errorf("ValidateInput() error = %v, wantErr %v", err, tt.wantErr)
            }
        })
    }
}
```

**Benefits:**

- Clear separation of test cases
- Easy to add new cases
- Descriptive test names in output

## Test Organization

**File naming:**

- `*_test.go` - Unit tests
- `*_integration_test.go` - Integration tests (require infrastructure)
- `*_fuzz_test.go` - Fuzz tests

**Package naming:**

- Test files in same package: `package mypackage`
- Black-box tests: `package mypackage_test` (tests exported API only)

## Assertions

**Use testify for clearer assertions:**

```go
import (
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

// require stops test on failure
require.NoError(t, err)
require.NotNil(t, response)

// assert continues test after failure
assert.Equal(t, expected, actual)
assert.Contains(t, slice, element)
assert.True(t, condition)
```

## Running Tests

```bash
# All tests (skips integration if no DB)
make test

# Unit tests only (fast)
make test-unit

# With race detector
go test -race ./...

# Specific package
go test ./internal/usecase/...

# Verbose (debugging only)
go test -v ./...

# Short mode (skip integration)
go test -short ./...
```

## Coverage

**Per-package thresholds enforced in CI:**

```bash
# Check coverage meets threshold
make coverage-check

# Per-package coverage report
make coverage-per-package
```

## Integration Test Pattern

```go
func TestMyIntegration(t *testing.T) {
    if testing.Short() {
        t.Skip("Skipping integration test in short mode")
    }

    // Setup
    testDB := testutil.SetupTestDB(t)
    defer testDB.Close()

    // Create dependencies
    repo := repository.NewRepo(testDB.DB)
    service := usecase.NewService(repo)

    // Test
    ctx := context.Background()
    result, err := service.DoSomething(ctx, input)

    // Assertions
    require.NoError(t, err)
    assert.Equal(t, expected, result)

    // Verify in database
    stored, err := repo.Get(ctx, result.ID)
    require.NoError(t, err)
    assert.Equal(t, expected, stored)
}
```

## Quick Reference

| Need | Helper |
|------|--------|
| Test DB | `testutil.SetupTestDB(t)` |
| Test images | `testutil.CreateTestPNG/JPEG/WebP()` |
| Skip integration | `if testing.Short() { t.Skip(...) }` |
| Assert equal | `assert.Equal(t, expected, actual)` |
| Require no error | `require.NoError(t, err)` |
| Table tests | `tests := []struct{name, input, want}{}` |
