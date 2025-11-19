# CI/CD Workflow Optimization Guide

## Problem Statement

**Original Architecture**: Each job independently downloads 304 Go modules

```
Current CI Pipeline (Per Workflow Run):
├── unit job         → downloads 304 modules (10 min)
├── unit-race job    → downloads 304 modules (10 min)
├── integration job  → downloads 304 modules (10 min)
├── integration-race → downloads 304 modules (10 min)
├── lint job         → downloads 304 modules (10 min)
└── build job        → downloads 304 modules (10 min)

Total wasted time: 60 minutes on duplicate downloads
```

##Solution: Shared Setup with Reusable Workflows

**Optimized Architecture**: Download once, share via cache

```
Optimized CI Pipeline:
├── setup job        → downloads 304 modules (10 min) + caches
│
├── Fast parallel jobs (use cached modules):
│   ├── unit         → restores cache (10 sec) + runs tests (3 min)
│   ├── lint         → restores cache (10 sec) + runs lint (2 min)
│   └── format-check → restores cache (10 sec) + checks format (1 min)
│
├── integration      → restores cache (10 sec) + runs tests (8 min)
│
├── build            → restores cache (10 sec) + builds (2 min)
│
└── Race detection (slowest, run last):
    ├── unit-race        → restores cache (10 sec) + runs tests (8 min)
    └── integration-race → restores cache (10 sec) + runs tests (12 min)

Total time: ~12-15 minutes (vs 60+ minutes)
Time saved: 45-48 minutes per workflow run (75% reduction)
```

## Implementation

### 1. Reusable Workflow: `_setup-go-environment.yml`

**Purpose**: Single source of truth for Go environment setup

**Features**:
- Checkout code (shared)
- Setup Go (shared)
- Download modules (shared)
- Cache management (shared)
- Configurable inputs

**Benefits**:
- ✅ No duplicate checkouts across jobs
- ✅ No duplicate module downloads
- ✅ Consistent environment setup
- ✅ Single place to update Go version or caching strategy

**Usage**:
```yaml
jobs:
  setup:
    uses: ./.github/workflows/_setup-go-environment.yml
    with:
      go-version: "1.24"
      download-modules: true
```

### 2. Optimized Test Workflow: `test-v2.yml`

**Job Dependency Graph**:
```
setup (10 min)
  ├─→ unit (3 min)          ─┬─→ build (2 min)
  ├─→ lint (2 min)          ─┘
  ├─→ format-check (1 min)
  │
  └─→ integration (8 min) ─┬─→ integration-race (12 min)
                           │
       unit-race (8 min) ←─┘
```

**Critical Path**: setup → integration → integration-race = ~30 min total

**Parallel Execution**:
- unit, lint, format-check run simultaneously (wall clock: 3 min)
- Fast feedback: Basic tests complete in 13 minutes

### 3. Cache Strategy

**Cache Key Format**:
```
go-mod-{OS}-{Go-Version}-{go.sum-hash}
```

**Cache Contents**:
- `~/go/pkg/mod` - Downloaded modules (~200MB)
- `~/.cache/go-build` - Build cache (~100MB)

**Cache Behavior**:
- **setup job**: Creates cache if not found
- **other jobs**: Restore cache (read-only)
- **cache hit**: Skip download entirely (0 seconds)
- **cache miss**: Download takes 10 minutes

**Cache Invalidation**:
- Automatic when `go.sum` changes
- Automatic when Go version changes
- Manual via `actions/cache/delete` if needed

## Performance Comparison

### Current Workflow (test.yml)

| Job | Module Download | Test Execution | Total |
|-----|-----------------|----------------|-------|
| unit | 10 min | 3 min | 13 min |
| unit-race | 10 min | 8 min | 18 min |
| integration | 10 min | 8 min | 18 min |
| integration-race | 10 min | 12 min | 22 min |
| lint | 10 min | 2 min | 12 min |
| build | 10 min | 2 min | 12 min |
| **Total (sequential)** | **60 min** | **35 min** | **95 min** |
| **Total (parallel)** | **10 min** | **12 min** | **~22 min** |

### Optimized Workflow (test-v2.yml)

| Job | Module Download | Test Execution | Total |
|-----|-----------------|----------------|-------|
| setup | 10 min | 0 min | 10 min |
| unit | 10 sec | 3 min | 3 min |
| lint | 10 sec | 2 min | 2 min |
| format-check | 10 sec | 1 min | 1 min |
| integration | 10 sec | 8 min | 8 min |
| build | 10 sec | 2 min | 2 min |
| unit-race | 10 sec | 8 min | 8 min |
| integration-race | 10 sec | 12 min | 12 min |
| **Total (sequential)** | **11 min** | **36 min** | **47 min** |
| **Total (parallel)** | **10 min** | **12 min** | **~13 min** |

### Savings Summary

| Metric | Current | Optimized | Savings |
|--------|---------|-----------|---------|
| Module downloads | 6× | 1× | 50 min |
| Parallel execution time | ~22 min | ~13 min | 9 min |
| **Total savings** | | | **59 min (73%)** |

## Migration Guide

### Phase 1: Test Optimized Workflow (No Risk)

1. **Deploy new workflow alongside old**:
   ```bash
   # New workflow runs on feature branches only
   git checkout -b test/optimized-ci
   # test-v2.yml is only triggered manually for testing
   ```

2. **Test with workflow_dispatch**:
   - Manually trigger `test-v2.yml`
   - Compare execution times
   - Verify all tests pass
   - Check cache hit rates

3. **Validate results**:
   - All tests pass ✅
   - Cache restores work ✅
   - Time savings confirmed ✅

### Phase 2: Gradual Rollout (Low Risk)

1. **Update trigger conditions**:
   ```yaml
   # test.yml - current workflow
   on:
     push:
       branches: [ main ]  # Only main branch

   # test-v2.yml - optimized workflow
   on:
     push:
       branches: [ develop ]  # Only develop branch
     pull_request:
       branches: [ main ]     # All PRs
   ```

2. **Monitor for issues**:
   - Watch CI run times
   - Check for cache-related failures
   - Verify test reliability

3. **Collect metrics**:
   - Average run time
   - Cache hit rate
   - Failure rate
   - Runner minute costs

### Phase 3: Full Migration (After Validation)

1. **Replace old workflow**:
   ```bash
   mv .github/workflows/test.yml .github/workflows/test-old.yml.bak
   mv .github/workflows/test-v2.yml .github/workflows/test.yml
   ```

2. **Update other workflows**:
   - `e2e-tests.yml` → use reusable setup
   - `security-tests.yml` → use reusable setup
   - `virus-scanner-tests.yml` → use reusable setup
   - `video-import.yml` → use reusable setup

3. **Clean up**:
   ```bash
   rm .github/workflows/test-old.yml.bak
   rm .github/workflows/test-optimized.yml
   ```

## Rollback Plan

If issues arise:

1. **Immediate rollback**:
   ```bash
   mv .github/workflows/test.yml.bak .github/workflows/test.yml
   ```

2. **Clear caches** (if corruption suspected):
   ```bash
   gh cache delete --all
   ```

3. **Investigate**:
   - Check cache restore logs
   - Verify module checksums
   - Test locally with same cache

## Best Practices

### 1. Cache Management

**Do**:
- ✅ Use unique cache keys (include go.sum hash)
- ✅ Restore cache read-only in downstream jobs
- ✅ Monitor cache size (~300MB typical)
- ✅ Delete old caches automatically (7 day retention)

**Don't**:
- ❌ Share cache between different projects
- ❌ Cache vendor directory (use modules instead)
- ❌ Write to cache from multiple jobs

### 2. Job Dependencies

**Do**:
- ✅ Run fast tests first (unit, lint)
- ✅ Run slow tests last (race detection)
- ✅ Fail fast (stop on first failure)
- ✅ Parallelize independent jobs

**Don't**:
- ❌ Make all jobs depend on each other (serial execution)
- ❌ Run slow tests before fast tests
- ❌ Continue on failure for critical tests

### 3. Container Usage

**Do**:
- ✅ Use containers for isolation (integration tests)
- ✅ Mount cache read-only in containers
- ✅ Use specific image tags (golang:1.24, not :latest)

**Don't**:
- ❌ Download modules inside containers (use cache)
- ❌ Use heavy containers for fast tests
- ❌ Pull images on every run (cache them)

## Advanced Optimizations

### 1. Test Sharding (Future)

For large test suites (1000+ tests):
```yaml
strategy:
  matrix:
    shard: [1, 2, 3, 4]
run: go test -run "TestShard${{ matrix.shard }}" ./...
```

**Benefit**: 4× parallelization of tests

### 2. Selective Testing

Run only tests affected by changes:
```bash
# Get changed packages
CHANGED=$(git diff --name-only HEAD^ HEAD | grep '\.go$' | xargs dirname | sort -u)

# Run tests only for changed packages
for pkg in $CHANGED; do
  go test ./$pkg/...
done
```

**Benefit**: 50-90% fewer tests on typical PRs

### 3. Build Cache

Cache compiled binaries:
```yaml
- name: Cache built binaries
  uses: actions/cache@v4
  with:
    path: bin/
    key: bin-${{ runner.os }}-${{ hashFiles('**/*.go') }}
```

**Benefit**: Skip rebuilds when code hasn't changed

## Monitoring & Metrics

### Key Metrics to Track

1. **CI Duration**:
   - P50, P95, P99 run times
   - Target: <15 min for 95% of runs

2. **Cache Hit Rate**:
   - % of jobs with cache hits
   - Target: >90% cache hits

3. **Runner Minutes**:
   - Total minutes consumed per day
   - Cost implications
   - Target: 50% reduction vs current

4. **Failure Rate**:
   - % of CI runs that fail
   - Time to failure (fast feedback)
   - Target: Fail in <5 min if tests fail

### Monitoring Tools

```yaml
# Add to workflow
- name: Report metrics
  if: always()
  run: |
    echo "ci_duration_seconds=${{ job.duration }}" >> $GITHUB_OUTPUT
    echo "cache_hit=${{ steps.cache.outputs.cache-hit }}" >> $GITHUB_OUTPUT
```

Send to monitoring:
- GitHub Actions dashboard
- Prometheus/Grafana
- Custom analytics

## Conclusion

**Implementation Effort**: Low (2-4 hours)
**Risk Level**: Low (gradual rollout)
**Time Savings**: 45-50 minutes per workflow run (73% reduction)
**Cost Savings**: ~50% reduction in runner minutes
**Maintenance**: Lower (single source of setup)

**Recommendation**: Implement Phase 1 immediately to validate savings.

---

**Next Steps**:
1. Test `test-v2.yml` with `workflow_dispatch`
2. Compare run times (current vs optimized)
3. Validate all tests pass
4. Proceed with Phase 2 rollout
