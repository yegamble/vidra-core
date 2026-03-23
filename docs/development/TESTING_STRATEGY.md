# Testing Strategy

This document outlines the comprehensive testing strategy for the Athena video platform, including test types, coverage requirements, and best practices.

## Overview

**Current Test Metrics**:

- **Total Test Files**: 191
- **Code Coverage Baseline**: 23.8% (latest full-package report)
- **Near-Term Coverage Target**: >=60%
- **Stretch Coverage Target**: >=80%
- **Security Tests**: 50+
- **Integration Tests**: Comprehensive across all major features
- **E2E Tests**: Critical user workflows
- **Usecase Unit Tests**: analytics (42 subtests), redundancy (53 subtests) — mock-based, no DB required

**Testing Philosophy**:

1. **Test Pyramid**: Many unit tests, fewer integration tests, minimal E2E tests
2. **Fast Feedback**: Unit tests run in < 5s, integration tests in < 30s
3. **Isolation**: Tests don't depend on external services (use mocks/fakes)
4. **Deterministic**: Tests produce same result every time
5. **Meaningful**: Tests verify behavior, not implementation

---

## Test Types

### 1. Unit Tests

**Purpose**: Test individual functions/methods in isolation.

**Location**: `*_test.go` files alongside source code

**Characteristics**:

- No external dependencies (database, Redis, IPFS)
- Use mocks/stubs for dependencies
- Fast execution (< 1s per test)
- High coverage target (> 90%)

**Example**:

```go
// internal/usecase/video_service_test.go
func TestVideoService_Create(t *testing.T) {
    // Arrange
    mockRepo := &MockVideoRepository{}
    mockStorage := &MockStorageService{}
    service := NewVideoService(mockRepo, mockStorage)

    // Act
    video, err := service.Create(ctx, createReq)

    // Assert
    assert.NoError(t, err)
    assert.NotNil(t, video)
    mockRepo.AssertExpectations(t)
}
```

**Best Practices**:

- Test happy path and error cases
- Use table-driven tests for multiple scenarios
- Mock only direct dependencies
- Verify behavior, not implementation
- Use `testify/assert` and `testify/mock`

### 2. Integration Tests

**Purpose**: Test interactions between components.

**Location**: `*_integration_test.go` or `integration_test/` directory

**Characteristics**:

- Use real database (dockerized PostgreSQL)
- Use real Redis (dockerized)
- May use real IPFS (optional)
- Slower execution (< 30s per test)
- Coverage target (> 80%)

**Setup**:

```go
// integration_test/setup.go
func SetupTestDB(t *testing.T) *sqlx.DB {
    db := dockertest.NewPostgreSQL(t)
    RunMigrations(t, db)
    return db
}

func TeardownTestDB(t *testing.T, db *sqlx.DB) {
    db.Close()
}
```

**Example**:

```go
// internal/repository/video_repository_integration_test.go
func TestVideoRepository_Create_Integration(t *testing.T) {
    if testing.Short() {
        t.Skip("Skipping integration test")
    }

    db := SetupTestDB(t)
    defer TeardownTestDB(t, db)

    repo := NewVideoRepository(db)
    video, err := repo.Create(ctx, videoModel)

    assert.NoError(t, err)
    assert.NotEqual(t, uuid.Nil, video.ID)
}
```

**Best Practices**:

- Use `testing.Short()` flag to skip in CI
- Clean up test data after each test
- Use transactions for isolation
- Test actual SQL queries
- Verify database constraints

### 3. End-to-End (E2E) Tests

**Purpose**: Test complete user workflows.

**Location**: `e2e_test/` directory

**Characteristics**:

- Full stack testing (HTTP → DB → IPFS)
- Real HTTP requests to API
- Verify entire flow
- Slowest execution (< 2 minutes per test)
- Coverage target (critical paths only)

**Example**:

```go
// e2e_test/video_upload_test.go
func TestVideoUploadFlow(t *testing.T) {
    // 1. Authenticate
    token := authenticate(t, testUser)

    // 2. Initiate upload
    session := initiateUpload(t, token, uploadReq)

    // 3. Upload chunks
    for i, chunk := range chunks {
        uploadChunk(t, token, session.ID, i, chunk)
    }

    // 4. Finalize upload
    video := finalizeUpload(t, token, session.ID)

    // 5. Wait for processing
    waitForProcessing(t, video.ID, 2*time.Minute)

    // 6. Verify playback
    verifyHLSPlayback(t, video.ID)
}
```

**Best Practices**:

- Test critical user journeys
- Use realistic test data
- Handle async operations (polling/webhooks)
- Clean up resources after test
- Run in dedicated test environment

### 4. Security Tests

**Purpose**: Verify security controls and prevent vulnerabilities.

**Location**: `security_test/` or alongside feature tests

**Characteristics**:

- Test authentication/authorization
- Verify input validation
- Test virus scanning
- Check rate limiting
- Test SSRF prevention

**Example**:

```go
// internal/security/virus_scanner_test.go
func TestVirusScanner_EICAR_Detection(t *testing.T) {
    scanner := setupScanner(t)
    eicarFile := loadEICARTestFile(t)

    result, err := scanner.ScanFile(ctx, eicarFile)

    assert.NoError(t, err)
    assert.Equal(t, StatusInfected, result.Status)
    assert.Contains(t, result.Virus, "EICAR")
}

func TestVirusScanner_RetryLogic_CVE_ATHENA_2025_001(t *testing.T) {
    // Test P1 vulnerability fix
    scanner := setupScannerWithUnreachableClamAV(t)

    result, err := scanner.ScanFile(ctx, cleanFile)

    // Should error, not fall through to fallback
    assert.Error(t, err)
    assert.Nil(t, result)
}
```

**Critical Security Tests**:

- ✅ CVE-ATHENA-2025-001 (Virus scanner retry bypass)
- ✅ SQL injection prevention
- ✅ CSRF token validation
- ✅ JWT signature verification
- ✅ Rate limiting enforcement
- ✅ IPFS CID validation
- ✅ File type validation
- ✅ E2EE message encryption

### 5. Performance Tests

**Purpose**: Verify performance and identify bottlenecks.

**Location**: `*_bench_test.go` or `perf_test/` directory

**Characteristics**:

- Benchmark critical code paths
- Load testing (concurrent users)
- Stress testing (resource limits)
- Latency measurements
- Throughput measurements

**Benchmark Example**:

```go
// internal/repository/video_repository_bench_test.go
func BenchmarkVideoRepository_List(b *testing.B) {
    db := setupBenchDB(b)
    repo := NewVideoRepository(db)

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        _, err := repo.List(ctx, ListOptions{Limit: 50})
        if err != nil {
            b.Fatal(err)
        }
    }
}
```

**Load Test Example**:

```bash
# k6 load test
k6 run --vus 100 --duration 5m scripts/load_test.js
```

---

## Baseline Coverage Snapshot

The latest full-package baseline (`docs/development/TEST_BASELINE_REPORT.md`, generated 2025-11-16) reports:

- **Overall coverage**: 23.8%
- **Repository layer**: 9.6%
- **Handler hotspots**:
  - `internal/httpapi/handlers/channel`: 7.3%
  - `internal/httpapi/handlers/federation`: 14.8%
  - `internal/httpapi/handlers/moderation`: 0.0%
  - `internal/httpapi/handlers/social`: 0.0%
- **Usecase hotspots**:
  - `internal/usecase/import`: 0.0%
  - `internal/usecase/encoding`: 27.3%

Highest-coverage packages in the same baseline:

- `internal/middleware`: 95.4%
- `internal/config`: 91.9%
- `internal/scheduler`: 90.6%
- `internal/worker`: 86.9%
- `internal/activitypub`: 82.4%

### Critical Gaps Identified

1. **Concurrency Tests**
   - [ ] Race condition testing for upload chunks
   - [ ] Concurrent transcode job handling
   - [ ] Simultaneous IPFS pin operations

2. **Error Recovery Tests**
   - [x] Stale/orphaned encoding job recovery on server restart
   - [x] Long-running encode job not incorrectly reset (heartbeat safety)
   - [ ] Database connection loss recovery
   - [ ] Redis failover handling
   - [ ] IPFS gateway failure recovery

3. **Edge Cases**
   - [ ] Very large file uploads (> 10GB)
   - [ ] Extremely long video titles/descriptions
   - [ ] Malformed IPFS CIDs
   - [ ] Corrupted video files

---

## Test Execution

### Local Development

**Run All Tests**:

```bash
make test
# Or manually:
go test ./... -race -coverprofile=coverage.out
```

**Run Specific Test**:

```bash
go test ./internal/usecase -run TestVideoService_Create
```

**Run Encoding Resilience Tests**:

```bash
# Unit tests (mock-based, no DB required)
go test ./internal/usecase/encoding/... -v -run "Recovery|ResetStale"

# Integration tests (requires test DB)
go test ./internal/repository/... -v -run "ResetStaleJobs"
```

**Run with Coverage**:

```bash
go test ./... -coverprofile=coverage.out
go tool cover -html=coverage.out -o coverage.html
```

**Run Only Unit Tests** (skip integration):

```bash
go test -short ./...
```

**Run Only Integration Tests**:

```bash
go test ./... -run Integration
```

**Run Benchmarks**:

```bash
go test -bench=. -benchmem ./...
```

### CI/CD Pipeline

**GitHub Actions Workflow**:

```yaml
name: Test
on: [push, pull_request]

jobs:
  test:
    runs-on: ubuntu-latest
    services:
      postgres:
        image: postgres:15
        env:
          POSTGRES_PASSWORD: postgres
        options: >-
          --health-cmd pg_isready
          --health-interval 10s
          --health-timeout 5s
          --health-retries 5

      redis:
        image: redis:7
        options: >-
          --health-cmd "redis-cli ping"
          --health-interval 10s
          --health-timeout 5s
          --health-retries 5

    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v4
        with:
          go-version: '1.21'

      - name: Run tests
        run: go test -race -coverprofile=coverage.out ./...

      - name: Upload coverage
        uses: codecov/codecov-action@v3
        with:
          files: ./coverage.out
```

### Pre-commit Hooks

**Install hooks**:

```bash
make install-hooks
```

**Hook runs**:

- `gofmt` - Format code
- `golangci-lint` - Lint code
- `go test -short` - Run unit tests
- `go vet` - Vet code

---

## Test Data Management

### Test Fixtures

**Location**: `testdata/` directory

**Structure**:

```
testdata/
├── videos/
│   ├── sample_720p.mp4
│   ├── sample_1080p.mp4
│   └── corrupted.mp4
├── virus_scanner/
│   ├── eicar.txt
│   └── clean_file.txt
├── fixtures/
│   ├── users.json
│   ├── videos.json
│   └── channels.json
└── schemas/
    └── openapi.yaml
```

**Loading Fixtures**:

```go
func LoadUserFixtures(t *testing.T, db *sqlx.DB) []domain.User {
    data, err := os.ReadFile("testdata/fixtures/users.json")
    require.NoError(t, err)

    var users []domain.User
    require.NoError(t, json.Unmarshal(data, &users))

    for _, user := range users {
        _, err := db.Exec("INSERT INTO users (...) VALUES (...)", user)
        require.NoError(t, err)
    }

    return users
}
```

### Test Database Seeding

**Seed Script**:

```sql
-- testdata/seed.sql
INSERT INTO users (id, username, email) VALUES
  ('550e8400-e29b-41d4-a716-446655440000', 'testuser', 'test@example.com'),
  ('550e8400-e29b-41d4-a716-446655440001', 'admin', 'admin@example.com');

INSERT INTO videos (id, user_id, title, privacy) VALUES
  ('660e8400-e29b-41d4-a716-446655440000', '550e8400-e29b-41d4-a716-446655440000', 'Test Video', 'public');
```

**Apply Seed**:

```bash
psql -U athena -d athena_test -f testdata/seed.sql
```

---

## Test Best Practices

### 1. Test Naming

**Convention**: `Test<Function>_<Scenario>_<ExpectedResult>`

**Examples**:

- ✅ `TestVideoService_Create_ValidInput_ReturnsVideo`
- ✅ `TestVideoRepository_List_WithPagination_ReturnsLimitedResults`
- ✅ `TestVirusScanner_EICAR_DetectsVirus`
- ❌ `TestCreate`
- ❌ `TestVideo`

### 2. Table-Driven Tests

**Pattern**:

```go
func TestVideoService_Create(t *testing.T) {
    tests := []struct {
        name    string
        input   CreateVideoRequest
        wantErr bool
        errMsg  string
    }{
        {
            name: "valid input",
            input: CreateVideoRequest{Title: "Test", Privacy: "public"},
            wantErr: false,
        },
        {
            name: "empty title",
            input: CreateVideoRequest{Title: "", Privacy: "public"},
            wantErr: true,
            errMsg: "title is required",
        },
        {
            name: "invalid privacy",
            input: CreateVideoRequest{Title: "Test", Privacy: "invalid"},
            wantErr: true,
            errMsg: "invalid privacy setting",
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            service := setupService(t)
            video, err := service.Create(ctx, tt.input)

            if tt.wantErr {
                assert.Error(t, err)
                assert.Contains(t, err.Error(), tt.errMsg)
            } else {
                assert.NoError(t, err)
                assert.NotNil(t, video)
            }
        })
    }
}
```

### 3. Test Isolation

**Use t.Cleanup()**:

```go
func TestWithDatabase(t *testing.T) {
    db := setupTestDB(t)
    t.Cleanup(func() {
        db.Close()
    })

    // Test code...
}
```

**Use subtests**:

```go
func TestVideoWorkflow(t *testing.T) {
    db := setupTestDB(t)
    defer db.Close()

    t.Run("create video", func(t *testing.T) {
        // Test create
    })

    t.Run("update video", func(t *testing.T) {
        // Test update
    })
}
```

### 4. Mocking

**Use interfaces**:

```go
// Define interface in production code
type VideoRepository interface {
    Create(ctx context.Context, video *domain.Video) error
    Get(ctx context.Context, id uuid.UUID) (*domain.Video, error)
}

// Generate mock
//go:generate mockery --name=VideoRepository
```

**Use mock in tests**:

```go
func TestVideoService_Create(t *testing.T) {
    mockRepo := new(MockVideoRepository)
    mockRepo.On("Create", mock.Anything, mock.Anything).Return(nil)

    service := NewVideoService(mockRepo)
    err := service.Create(ctx, video)

    assert.NoError(t, err)
    mockRepo.AssertExpectations(t)
}
```

### 5. Error Testing

**Test all error paths**:

```go
func TestVideoRepository_Create_DatabaseError(t *testing.T) {
    db := setupFailingDB(t)
    repo := NewVideoRepository(db)

    _, err := repo.Create(ctx, video)

    assert.Error(t, err)
    assert.Contains(t, err.Error(), "database")
}
```

### 6. Concurrency Testing

**Use -race flag**:

```bash
go test -race ./...
```

**Test concurrent access**:

```go
func TestVideoService_ConcurrentCreate(t *testing.T) {
    service := setupService(t)
    var wg sync.WaitGroup

    for i := 0; i < 10; i++ {
        wg.Add(1)
        go func(i int) {
            defer wg.Done()
            _, err := service.Create(ctx, CreateVideoRequest{
                Title: fmt.Sprintf("Video %d", i),
            })
            assert.NoError(t, err)
        }(i)
    }

    wg.Wait()
}
```

---

## Test Reporting

### Coverage Reports

**Generate HTML report**:

```bash
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out -o coverage.html
```

**Check coverage threshold**:

```bash
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out | grep total | awk '{print $3}' | sed 's/%//' | awk '{if ($1 < 60) exit 1}'
```

### Test Reports in CI

**Use gotestsum** for better output:

```bash
gotestsum --junitfile report.xml --format testname -- -coverprofile=coverage.out ./...
```

---

## Recent Feature Updates

### Encoding Progress Tracking (February 2026)

**Feature**: Real-time FFmpeg encoding progress tracking with authorization-based access control.

**Implementation**:

- Added `Progress` field (0-100) to `EncodingJob` domain model
- Enhanced encoding service to parse FFmpeg stderr output in real-time
- Created new API endpoints with role-based authorization
- Added Postman E2E test collection for comprehensive testing

**New Endpoints**:

- `GET /api/v1/encoding/jobs/{jobID}` - Get individual job details
- `GET /api/v1/videos/{id}/encoding-jobs` - Get all jobs for a video

**Test Coverage**:

- **Unit Tests**:
  - Progress parser utility (`internal/usecase/encoding/progress_test.go`)
  - Authorization scenarios (owner, admin, moderator, unauthorized)
  - Error handling (job not found, video not found)
- **Integration Tests**:
  - API handler tests with mocked repositories
  - Response structure validation
  - Overall progress calculation for multiple jobs
- **E2E Tests** (Postman):
  - Complete authorization matrix testing
  - Progress monitoring workflow
  - Active job filtering
  - Error response validation

**Authorization Matrix**:
| Role | Can Access Own Videos | Can Access Any Video |
|------|---------------------|-------------------|
| Owner | ✅ | ❌ |
| Admin | ✅ | ✅ |
| Moderator | ✅ | ✅ |
| Other User | ❌ | ❌ |

**Run Progress Tracking Tests**:

```bash
# Unit tests for progress parsing
go test ./internal/usecase/encoding -v -run "Progress"

# API handler tests with authorization
go test ./internal/httpapi/handlers/video -v -run "EncodingJob"

# Run Postman E2E tests
newman run postman/athena-encoding-jobs.postman_collection.json \
  -e postman/athena.local.postman_environment.json
```

---

## Future Improvements

### Planned Enhancements

1. **Increase Coverage** (Target: >=60% near-term, >=80% stretch)
   - [ ] ATProto integration tests
   - [ ] Plugin system edge cases
   - [ ] Video redundancy scenarios

2. **Performance Testing**
   - [ ] Load tests for API endpoints
   - [ ] Stress tests for transcode workers
   - [ ] IPFS gateway throughput tests

3. **Chaos Engineering**
   - [ ] Database failure injection
   - [ ] Network partition simulation
   - [ ] Resource exhaustion tests

4. **Contract Testing**
   - [ ] ActivityPub protocol compliance
   - [ ] OpenAPI schema validation
   - [ ] Federation interoperability

---

## Related Documentation

- [Test Execution Guide](TEST_EXECUTION_GUIDE.md)
- [Test Baseline Report](TEST_BASELINE_REPORT.md)
- [Virus Scanner Tests](VIRUS_SCANNER_TEST_REPORT.md)
- [Claude Hooks](CLAUDE_HOOKS.md) - Automated testing workflow
- [Code Quality Review](CODE_QUALITY_REVIEW.md)
