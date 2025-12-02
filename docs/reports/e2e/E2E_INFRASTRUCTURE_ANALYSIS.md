# E2E Test Infrastructure Analysis

## Executive Summary

**Date**: 2025-11-20
**Analysis Scope**: Comparison of working test infrastructure vs E2E test setup
**Key Finding**: E2E tests follow similar patterns to working Postman tests but have minor infrastructure mismatches

---

## 1. Working Test Infrastructure Patterns

### 1.1 Integration Tests (test.yml workflow) ✅

**Infrastructure File**: `docker-compose.ci.yml`

**Characteristics**:
- Services run on **standard ports** (5432, 6379, 5001, 3310)
- Tests execute **from host**, connecting to `localhost`
- Uses **GitHub Actions services** pattern
- Explicit environment variables in workflow
- Simple, predictable service discovery

**Service Configuration**:
```yaml
Services:
  - postgres-ci: 5432:5432
  - redis-ci: 6379:6379
  - ipfs-ci: 5001:5001
  - clamav-ci: 3310:3310
Network: ci-network (bridge)
```

**Test Execution Pattern**:
```bash
# Services start
docker compose -f docker-compose.ci.yml up -d

# Tests run from host
DATABASE_URL=postgres://test_user:test_password@localhost:5432/athena_test
REDIS_URL=redis://localhost:6379/0
make test-integration-ci

# Cleanup
docker compose -f docker-compose.ci.yml down -v
```

**Success Factors**:
1. Host-to-container networking (no container-to-container dependencies)
2. Standard ports (no conflicts)
3. Simple healthchecks
4. Explicit environment variables
5. tmpfs for transient data (no volume persistence issues)

---

### 1.2 Postman E2E Tests (Makefile: postman-e2e) ✅

**Infrastructure File**: `docker-compose.test.yml`

**Characteristics**:
- Services run on **non-standard ports** (5433, 6380, 15001, 18080)
- Application runs **inside Docker** (app-test container)
- Newman runs **inside Docker network**
- Uses **service-to-service** communication
- All tests containerized

**Service Configuration**:
```yaml
Services:
  - postgres-test: 5433:5432
  - redis-test: 6380:6379
  - ipfs-test: 15001:5001
  - clamav-test: 3310:3310
  - app-test: 18080:8080 (Athena API)
  - newman: (test runner)
Network: test-network (bridge)
```

**Test Execution Pattern**:
```bash
# Unique project name for isolation
COMPOSE_PROJECT_NAME=athena-test docker compose -f docker-compose.test.yml up -d

# Newman runs inside network, talks to app-test:8080
docker compose -f docker-compose.test.yml run --rm newman

# Cleanup
COMPOSE_PROJECT_NAME=athena-test docker compose -f docker-compose.test.yml down -v
```

**Success Factors**:
1. Full isolation via COMPOSE_PROJECT_NAME
2. Container-to-container communication (no host involvement)
3. Non-standard ports prevent conflicts
4. Health checks ensure service readiness
5. tmpfs for ephemeral data
6. Pre-flight cleanup prevents port conflicts

---

## 2. E2E Test Infrastructure

### 2.1 Current Setup (tests/e2e/docker-compose.yml)

**Infrastructure File**: `tests/e2e/docker-compose.yml`

**Characteristics**:
- Services run on **non-standard ports** (5433, 6380, 9000/9001, 3311, 8080)
- Application runs **inside Docker** (athena-api-e2e)
- Tests run **from host**, connecting to `localhost:8080`
- **Hybrid pattern**: Some services containerized, tests on host

**Service Configuration**:
```yaml
Services:
  - postgres-e2e: 5433:5432
  - redis-e2e: 6380:6379
  - minio-e2e: 9000:9000, 9001:9001
  - clamav-e2e: 3311:3310
  - athena-api-e2e: 8080:8080
Network: athena-e2e-network
Volumes: Named volumes (persistent)
```

**Test Execution Pattern**:
```bash
# Start environment
cd tests/e2e
COMPOSE_PROJECT_NAME=athena-e2e-${RUN_ID} docker compose up -d

# Wait for API
timeout 180 bash -c 'until curl -sf http://localhost:8080/health; do sleep 5; done'

# Run tests from host
cd tests/e2e
go test -v -timeout 30m ./scenarios/...

# Cleanup
COMPOSE_PROJECT_NAME=athena-e2e-${RUN_ID} docker compose down -v
```

---

## 3. Key Differences Analysis

| Aspect | Integration Tests | Postman E2E | E2E Tests |
|--------|------------------|-------------|-----------|
| **Test Location** | Host | Container (Newman) | Host |
| **App Container** | No (tests mock) | Yes | Yes |
| **Port Strategy** | Standard (5432) | Non-standard (5433) | Non-standard (5433) |
| **Network Pattern** | Host→Container | Container→Container | Host→Container |
| **Working Directory** | Root `/home/user/athena` | Root `/home/user/athena` | Subdirectory `tests/e2e/` |
| **Compose File** | `docker-compose.ci.yml` | `docker-compose.test.yml` | `tests/e2e/docker-compose.yml` |
| **Volume Strategy** | tmpfs (ephemeral) | tmpfs (ephemeral) | Named volumes (persistent) |
| **Project Name** | None | `athena-test` | `athena-e2e-${RUN_ID}` |
| **Cleanup** | Down -v | Down -v | Down -v |

---

## 4. Identified Infrastructure Issues

### 4.1 Working Directory Mismatch

**Issue**: E2E tests expect to run from `tests/e2e/` subdirectory

**Evidence**:
```go
// tests/e2e/scenarios/video_workflow_test.go:203
testVideoPath = "../../postman/test-files/videos/test-video.mp4"
```

**Impact**:
- Relative paths may fail if test runner is in wrong directory
- Video file lookup fails
- Docker Compose context may be incorrect

**Recommendation**:
- Use absolute paths or E2E_TEST_VIDEO_PATH environment variable
- Ensure CI workflow sets correct working directory

---

### 4.2 Port Conflict Risk

**Issue**: E2E tests use port 8080 (same as dev environment)

**Evidence**:
```yaml
# tests/e2e/docker-compose.yml
athena-api-e2e:
  ports:
    - "8080:8080"  # ⚠️ Conflicts with dev server
```

**Working Pattern (Postman)**:
```yaml
# docker-compose.test.yml
app-test:
  ports:
    - "18080:8080"  # ✅ No conflict
```

**Recommendation**:
- Change E2E API port to `18080:8080` or similar
- Update E2E_BASE_URL default to `http://localhost:18080`
- Align with Postman test pattern

---

### 4.3 Volume Persistence vs Ephemeral Storage

**Issue**: E2E tests use named volumes, may retain state between runs

**Evidence**:
```yaml
# tests/e2e/docker-compose.yml
volumes:
  postgres-e2e-data:
  minio-e2e-data:
  clamav-e2e-data:
```

**Working Pattern (Integration & Postman)**:
```yaml
# docker-compose.test.yml
postgres-test:
  tmpfs:
    - /var/lib/postgresql/data  # ✅ Ephemeral, clean state
```

**Impact**:
- Tests may not be idempotent
- Previous test data can interfere
- Database migrations may fail due to existing schema

**Recommendation**:
- Use tmpfs for Postgres and Redis (like other tests)
- Keep named volume only for ClamAV signatures (optimization)
- Ensures clean state per test run

---

### 4.4 Docker Compose File Location

**Issue**: E2E docker-compose.yml is in subdirectory `tests/e2e/`

**Working Patterns**:
- Integration: `docker-compose.ci.yml` (root)
- Postman: `docker-compose.test.yml` (root)
- E2E: `tests/e2e/docker-compose.yml` (subdirectory)

**Impact**:
- Different build context (affects Dockerfile path)
- Relative volume mounts may break
- Harder to maintain consistency

**Recommendation**:
- Consider moving to root as `docker-compose.e2e.yml`
- Or ensure build context points to repo root: `context: ../..`

---

### 4.5 Test Video Path Resolution

**Issue**: Test video path uses relative path from test file location

**Current Implementation**:
```go
// tests/e2e/scenarios/video_workflow_test.go
testVideoPath := os.Getenv("E2E_TEST_VIDEO_PATH")
if testVideoPath == "" {
    testVideoPath = "../../postman/test-files/videos/test-video.mp4"
}
```

**Risk**:
- Path resolution depends on where `go test` is executed
- Breaks if run from root vs `tests/e2e/` directory

**Working Pattern (Postman)**:
- Tests run from repo root
- Paths are relative to root: `./postman/test-files/videos/test-video.mp4`

**Recommendation**:
```go
// Option 1: Use absolute path via environment variable
testVideoPath := os.Getenv("E2E_TEST_VIDEO_PATH")
if testVideoPath == "" {
    // Use os.Getwd() and construct absolute path
    cwd, _ := os.Getwd()
    testVideoPath = filepath.Join(cwd, "postman/test-files/videos/test-video.mp4")
}

// Option 2: Always require environment variable in CI
if testVideoPath == "" {
    t.Skip("E2E_TEST_VIDEO_PATH not set")
}
```

---

## 5. GitHub Actions Workflow Comparison

### 5.1 Integration Test Workflow (test.yml) ✅

```yaml
steps:
  - uses: actions/checkout@v4

  - name: Start services
    run: docker compose -f docker-compose.ci.yml up -d

  - name: Wait for services
    run: |
      timeout 60 bash -c 'until docker exec $(docker compose -f docker-compose.ci.yml ps -q postgres-ci) pg_isready -U test_user -d athena_test; do sleep 2; done'

  - name: Run tests
    env:
      DATABASE_URL: postgres://test_user:test_password@localhost:5432/athena_test
    run: make test-integration-ci

  - name: Cleanup
    if: always()
    run: docker compose -f docker-compose.ci.yml down -v
```

**Success Pattern**:
1. Clear service startup
2. Explicit readiness checks
3. Environment variables in workflow (not in compose)
4. Tests run from repo root
5. Guaranteed cleanup

---

### 5.2 E2E Test Workflow (e2e-tests.yml)

```yaml
steps:
  - name: Checkout
    uses: actions/checkout@v4

  - name: Generate fixtures
    run: |
      cd tests/e2e  # ⚠️ Changes directory
      make fixtures

  - name: Cleanup previous environment
    run: |
      cd tests/e2e  # ⚠️ Changes directory
      docker compose down -v 2>/dev/null || true

  - name: Start test environment
    run: |
      cd tests/e2e  # ⚠️ Changes directory
      COMPOSE_PROJECT_NAME=athena-e2e-${{ github.run_id }} docker compose up -d

  - name: Wait for API
    run: |
      timeout 180 bash -c 'until curl -sf http://localhost:8080/health; do sleep 5; done'

  - name: Run tests
    env:
      E2E_BASE_URL: http://localhost:8080
    run: |
      cd tests/e2e  # ⚠️ Changes directory
      go test -v -timeout 30m -count=1 ./scenarios/...

  - name: Cleanup
    if: always()
    run: |
      cd tests/e2e
      COMPOSE_PROJECT_NAME=athena-e2e-${{ github.run_id }} docker compose down -v
```

**Potential Issues**:
1. Multiple `cd tests/e2e` commands (state management)
2. Working directory inconsistency
3. Relative paths in test code may break
4. Different from other workflows (integration, Postman)

---

## 6. Recommendations

### 6.1 Immediate Fixes (High Priority)

#### Fix #1: Standardize Working Directory

**Problem**: Tests change directory multiple times, causing path resolution issues

**Solution**: Run all E2E operations from repo root

```yaml
# Before
- name: Start test environment
  run: |
    cd tests/e2e
    docker compose up -d

# After
- name: Start test environment
  run: |
    docker compose -f tests/e2e/docker-compose.yml up -d
  working-directory: .  # Explicit: stay in repo root
```

---

#### Fix #2: Use Non-Conflicting Ports

**Problem**: Port 8080 conflicts with dev environment

**Solution**: Change E2E API port to match Postman pattern

```yaml
# tests/e2e/docker-compose.yml
athena-api-e2e:
  ports:
    - "18080:8080"  # Match postman-e2e pattern
```

```yaml
# .github/workflows/e2e-tests.yml
- name: Wait for API
  run: |
    timeout 180 bash -c 'until curl -sf http://localhost:18080/health; do sleep 5; done'

- name: Run tests
  env:
    E2E_BASE_URL: http://localhost:18080
  run: go test -v -timeout 30m ./tests/e2e/scenarios/...
```

---

#### Fix #3: Use Ephemeral Storage (tmpfs)

**Problem**: Named volumes persist state between runs

**Solution**: Use tmpfs like other tests

```yaml
# tests/e2e/docker-compose.yml
postgres-e2e:
  image: postgres:15-alpine
  tmpfs:
    - /var/lib/postgresql/data  # ✅ Clean state every run
  # Remove: volumes: postgres-e2e-data

redis-e2e:
  image: redis:7-alpine
  tmpfs:
    - /data  # ✅ Ephemeral cache
```

---

#### Fix #4: Set Test Video Path Environment Variable

**Problem**: Relative path resolution breaks

**Solution**: Set absolute path in workflow

```yaml
# .github/workflows/e2e-tests.yml
- name: Run E2E tests
  env:
    E2E_BASE_URL: http://localhost:18080
    E2E_TEST_VIDEO_PATH: ${{ github.workspace }}/postman/test-files/videos/test-video.mp4
  run: go test -v -timeout 30m ./tests/e2e/scenarios/...
```

---

#### Fix #5: Align Docker Compose Build Context

**Problem**: Build context may be wrong when compose file is in subdirectory

**Solution**: Explicitly set build context to repo root

```yaml
# tests/e2e/docker-compose.yml
athena-api-e2e:
  build:
    context: ../..  # ✅ Repo root
    dockerfile: Dockerfile
```

---

### 6.2 Pattern Alignment (Medium Priority)

#### Align with Postman E2E Pattern

**Recommendation**: Follow `postman-e2e` Makefile target pattern

**Current E2E Pattern**:
```
Host → localhost:8080 → Container (athena-api-e2e)
```

**Consider**:
```
Host → Go tests → API calls → Container
```

**OR (Postman Pattern)**:
```
Container (test runner) → Container (API) [same network]
```

**Tradeoffs**:
- Host-based: Easier debugging, simpler setup
- Container-based: Full isolation, matches production

---

### 6.3 Infrastructure Consolidation (Low Priority)

#### Consider: Unified Test Compose File

**Current State**:
- `docker-compose.ci.yml` - Integration tests
- `docker-compose.test.yml` - Postman E2E
- `tests/e2e/docker-compose.yml` - Go E2E tests

**Recommendation**:
- Move `tests/e2e/docker-compose.yml` to root as `docker-compose.e2e.yml`
- Ensures consistent patterns across all test types
- Easier maintenance and documentation

---

## 7. Implementation Priority

### Phase 1: Critical Fixes (Week 1)
1. ✅ Fix working directory issues
2. ✅ Change port from 8080 to 18080
3. ✅ Set E2E_TEST_VIDEO_PATH environment variable
4. ✅ Fix build context in docker-compose.yml

### Phase 2: Stability Improvements (Week 2)
1. ✅ Convert named volumes to tmpfs
2. ✅ Add pre-flight cleanup like postman-e2e
3. ✅ Improve health check reliability
4. ✅ Add better error logging

### Phase 3: Pattern Alignment (Week 3)
1. Consider moving docker-compose.e2e.yml to root
2. Align test execution pattern with Postman or Integration tests
3. Document chosen pattern in README

---

## 8. Success Metrics

### How to Verify Fixes

1. **Idempotency**: Run E2E tests 3 times back-to-back, all pass
2. **Isolation**: Run E2E tests while dev server is running on 8080
3. **Path Resolution**: Tests find video file regardless of working directory
4. **Clean State**: Database starts empty every run
5. **CI Success**: GitHub Actions workflow passes consistently

---

## 9. Reference Commands

### Integration Tests (Working ✅)
```bash
# Start services
docker compose -f docker-compose.ci.yml up -d

# Run tests
DATABASE_URL=postgres://test_user:test_password@localhost:5432/athena_test \
REDIS_URL=redis://localhost:6379/0 \
make test-integration-ci

# Cleanup
docker compose -f docker-compose.ci.yml down -v
```

### Postman E2E Tests (Working ✅)
```bash
make postman-e2e
# This handles:
# - Pre-flight cleanup
# - Building fresh image
# - Starting all services
# - Running Newman tests
# - Cleanup
```

### E2E Tests (Current)
```bash
# Generate fixtures
cd tests/e2e && make fixtures

# Start environment
cd tests/e2e
COMPOSE_PROJECT_NAME=athena-e2e docker compose up -d

# Run tests
E2E_BASE_URL=http://localhost:8080 go test -v ./scenarios/...

# Cleanup
cd tests/e2e
COMPOSE_PROJECT_NAME=athena-e2e docker compose down -v
```

### E2E Tests (Recommended)
```bash
# From repo root
E2E_TEST_VIDEO_PATH=$(pwd)/postman/test-files/videos/test-video.mp4 \
E2E_BASE_URL=http://localhost:18080 \
go test -v -timeout 30m ./tests/e2e/scenarios/...
```

---

## 10. Appendix: File Structure Comparison

### Working Test Files
```
/home/user/athena/
├── docker-compose.ci.yml          # Integration tests ✅
├── docker-compose.test.yml        # Postman E2E ✅
├── Makefile                       # Has working postman-e2e target
├── postman/
│   ├── athena-auth.postman_collection.json
│   └── test-files/
│       └── videos/
│           └── test-video.mp4     # Test fixture (19KB)
└── .github/workflows/
    ├── test.yml                   # Integration CI ✅
    └── virus-scanner-tests.yml    # Working ✅
```

### E2E Test Files
```
/home/user/athena/tests/e2e/
├── docker-compose.yml             # E2E environment
├── Makefile                       # E2E-specific commands
├── helpers.go                     # Test utilities
├── workflows_test.go              # Placeholder tests
├── scenarios/
│   └── video_workflow_test.go     # Actual E2E tests
├── fixtures/
│   ├── README.md
│   └── data/
│       └── users.json
└── config/
    └── e2e_config.yaml
```

---

## Conclusion

The E2E test infrastructure follows similar patterns to working tests but has several key differences that may cause issues:

1. **Working directory inconsistency** (tests/e2e/ vs root)
2. **Port conflicts** (8080 vs 18080)
3. **Volume persistence** (named volumes vs tmpfs)
4. **Path resolution** (relative vs absolute)

The **Postman E2E tests** provide the best working reference for full-stack E2E testing. Recommendations align E2E tests with proven patterns while maintaining Go test runner flexibility.

**Next Steps**:
1. Apply Phase 1 critical fixes
2. Verify tests pass consistently
3. Document final pattern
4. Update CI workflow accordingly
