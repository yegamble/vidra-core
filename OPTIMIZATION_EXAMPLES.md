# Test Workflow Optimization Examples

This document contains ready-to-use code snippets for optimizing test workflows based on the analysis in `TEST_WORKFLOW_ANALYSIS.md`.

---

## 1. Test Result Caching

### Basic Test Result Cache

```yaml
- name: Restore test result cache
  id: test-cache
  uses: actions/cache@v4
  with:
    path: |
      ~/.cache/go-test-results
      coverage-*.out
      .test-cache/
    key: test-results-${{ runner.os }}-${{ hashFiles('**/*.go', 'go.sum') }}
    restore-keys: |
      test-results-${{ runner.os }}-

- name: Run tests (cache-aware)
  run: |
    if [ "${{ steps.test-cache.outputs.cache-hit }}" = "true" ]; then
      echo "✅ Test cache hit - skipping unchanged tests"
      # Only test changed packages
      changed_pkgs=$(git diff --name-only HEAD~1 | grep '\.go$' | xargs -I{} dirname {} | sort -u | xargs go list || echo "")
      if [ -n "$changed_pkgs" ]; then
        go test -v $changed_pkgs
      fi
    else
      echo "❌ Cache miss - running all tests"
      go test -v ./...
    fi
```

### Advanced: Per-Package Test Caching

```yaml
- name: Generate test cache keys per package
  id: cache-keys
  run: |
    packages=$(go list ./...)
    for pkg in $packages; do
      # Hash package files
      pkg_path=$(echo $pkg | sed 's|athena/||')
      pkg_hash=$(find $pkg_path -name '*.go' -exec sha256sum {} \; | sha256sum | cut -d' ' -f1)
      echo "cache-key-${pkg//\//-}=test-$pkg-$pkg_hash" >> $GITHUB_OUTPUT
    done

- name: Run tests with per-package caching
  run: |
    # Custom test runner that caches results per package
    ./scripts/cached-test-runner.sh
```

---

## 2. Conditional Execution

### Path-Based Conditional Execution

```yaml
jobs:
  check-changes:
    runs-on: ubuntu-latest
    outputs:
      should_test_go: ${{ steps.filter.outputs.go }}
      should_test_api: ${{ steps.filter.outputs.api }}
      should_test_db: ${{ steps.filter.outputs.db }}

    steps:
      - uses: actions/checkout@v4

      - uses: dorny/paths-filter@v3
        id: filter
        with:
          filters: |
            go:
              - '**/*.go'
              - 'go.mod'
              - 'go.sum'
            api:
              - 'internal/httpapi/**'
              - 'internal/handlers/**'
              - 'postman/**'
            db:
              - 'migrations/**'
              - 'internal/repository/**'

  unit-tests:
    needs: check-changes
    if: needs.check-changes.outputs.should_test_go == 'true'
    runs-on: ubuntu-latest
    steps:
      - name: Run unit tests
        run: go test ./...

  api-tests:
    needs: check-changes
    if: needs.check-changes.outputs.should_test_api == 'true'
    runs-on: ubuntu-latest
    steps:
      - name: Run API tests
        run: newman run postman/*.json
```

### Smart Package Selection

```yaml
- name: Determine packages to test
  id: packages
  run: |
    # Get changed files
    changed_files=$(git diff --name-only HEAD~1 HEAD | grep '\.go$' || echo "")

    if [ -z "$changed_files" ]; then
      # No Go files changed, skip tests or run minimal smoke tests
      echo "packages=" >> $GITHUB_OUTPUT
      echo "skip_tests=true" >> $GITHUB_OUTPUT
    else
      # Get affected packages including their dependencies
      affected_packages=$(echo "$changed_files" | xargs -I{} dirname {} | sort -u | xargs go list)

      # Also test packages that depend on changed packages
      dependents=$(go list -f '{{.ImportPath}}' ./... | xargs -I{} sh -c 'go list -f "{{if .Imports}}{{.}}{{end}}" {} | grep -F "$affected_packages" && echo {}' || echo "")

      all_packages=$(echo -e "$affected_packages\n$dependents" | sort -u | tr '\n' ' ')

      echo "packages=$all_packages" >> $GITHUB_OUTPUT
      echo "skip_tests=false" >> $GITHUB_OUTPUT
    fi

- name: Run tests on affected packages
  if: steps.packages.outputs.skip_tests != 'true'
  run: |
    packages="${{ steps.packages.outputs.packages }}"
    if [ -n "$packages" ]; then
      go test -v $packages
    fi
```

---

## 3. Docker Layer Caching

### Setup Docker Buildx with Caching

```yaml
- name: Set up Docker Buildx
  uses: docker/setup-buildx-action@v3

- name: Build and cache Docker image
  uses: docker/build-push-action@v5
  with:
    context: .
    file: ./Dockerfile
    push: false
    load: true
    tags: athena:test
    cache-from: type=gha
    cache-to: type=gha,mode=max
    build-args: |
      GO_VERSION=${{ env.GO_VERSION }}

- name: Run tests in container
  run: |
    docker run --rm athena:test go test ./...
```

### Multi-Stage Build with Layer Caching

```dockerfile
# syntax=docker/dockerfile:1.4

FROM golang:1.24 AS builder
WORKDIR /app

# Cache dependencies separately
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Cache build artifacts
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -o /app/bin/athena ./cmd/server

FROM alpine:latest
COPY --from=builder /app/bin/athena /usr/local/bin/
CMD ["athena"]
```

```yaml
- name: Build with Docker cache
  run: |
    DOCKER_BUILDKIT=1 docker build \
      --cache-from type=registry,ref=ghcr.io/${{ github.repository }}/cache:latest \
      --cache-to type=registry,ref=ghcr.io/${{ github.repository }}/cache:latest,mode=max \
      -t athena:test .
```

---

## 4. Test Parallelization & Sharding

### Matrix-Based Test Sharding

```yaml
jobs:
  test:
    strategy:
      matrix:
        shard: [1, 2, 3, 4]
    steps:
      - name: Run tests (sharded)
        env:
          TOTAL_SHARDS: 4
          SHARD_INDEX: ${{ matrix.shard }}
        run: |
          # Get all tests
          all_tests=$(go test -list=. ./... | grep '^Test')
          total_tests=$(echo "$all_tests" | wc -l)

          # Calculate shard size
          tests_per_shard=$(( (total_tests + TOTAL_SHARDS - 1) / TOTAL_SHARDS ))

          # Get tests for this shard
          start=$(( (SHARD_INDEX - 1) * tests_per_shard + 1 ))
          end=$(( SHARD_INDEX * tests_per_shard ))

          shard_tests=$(echo "$all_tests" | sed -n "${start},${end}p" | tr '\n' '|' | sed 's/|$//')

          # Run only this shard's tests
          if [ -n "$shard_tests" ]; then
            go test -v -run "^($shard_tests)$" ./...
          fi
```

### Package-Based Parallelization

```yaml
jobs:
  generate-matrix:
    runs-on: ubuntu-latest
    outputs:
      packages: ${{ steps.packages.outputs.matrix }}
    steps:
      - uses: actions/checkout@v4
      - id: packages
        run: |
          # Get all packages as JSON array
          packages=$(go list ./... | jq -R -s -c 'split("\n")[:-1]')
          echo "matrix=$packages" >> $GITHUB_OUTPUT

  test:
    needs: generate-matrix
    strategy:
      matrix:
        package: ${{ fromJson(needs.generate-matrix.outputs.packages) }}
    steps:
      - name: Test ${{ matrix.package }}
        run: go test -v ${{ matrix.package }}
```

---

## 5. Enhanced Retry Logic

### Retry with Exponential Backoff

```yaml
- name: Run tests with retry
  uses: ./.github/actions/retry-command
  with:
    command: |
      go test -v ./...
    max_attempts: 3
    initial_delay: 2
    description: "Go tests"

# Or inline:
- name: Run tests with inline retry
  run: |
    max_attempts=3
    attempt=1
    delay=2

    while [ $attempt -le $max_attempts ]; do
      if go test -v ./...; then
        echo "Tests passed on attempt $attempt"
        exit 0
      else
        if [ $attempt -lt $max_attempts ]; then
          echo "Tests failed on attempt $attempt, retrying in ${delay}s..."
          sleep $delay
          delay=$((delay * 2))  # Exponential backoff
          attempt=$((attempt + 1))
        else
          echo "Tests failed after $max_attempts attempts"
          exit 1
        fi
      fi
    done
```

### Selective Test Retry (Only Flaky Tests)

```yaml
- name: Run tests with flaky test retry
  run: |
    # First run: identify failures
    go test -v -json ./... > test-results.json || true

    # Parse failures
    failed_tests=$(jq -r 'select(.Action=="fail") | .Test' test-results.json | sort -u)

    if [ -n "$failed_tests" ]; then
      echo "Retrying failed tests: $failed_tests"

      # Retry only failed tests
      for test in $failed_tests; do
        echo "Retrying: $test"
        if go test -v -run "^${test}$" ./...; then
          echo "✅ $test passed on retry (likely flaky)"
        else
          echo "❌ $test failed on retry (genuine failure)"
          exit 1
        fi
      done
    fi
```

---

## 6. Service Startup Optimization

### Shared Service Stack

```yaml
services:
  postgres:
    image: postgres:16
    env:
      POSTGRES_USER: test_user
      POSTGRES_PASSWORD: test_pass
      POSTGRES_DB: test_db
    options: >-
      --health-cmd pg_isready
      --health-interval 10s
    ports:
      - 5432:5432

  redis:
    image: redis:7-alpine
    options: >-
      --health-cmd "redis-cli ping"
      --health-interval 10s
    ports:
      - 6379:6379

jobs:
  test-suite:
    strategy:
      matrix:
        test: [unit, integration, api]
    services:
      postgres: ${{ fromJSON('{"image": "postgres:16", ...}') }}
      redis: ${{ fromJSON('{"image": "redis:7-alpine", ...}') }}

    steps:
      - name: Run ${{ matrix.test }} tests
        run: |
          case "${{ matrix.test }}" in
            unit) go test ./internal/... ;;
            integration) go test ./tests/integration/... ;;
            api) newman run postman/*.json ;;
          esac
```

### Optimized Service Health Checks

```yaml
- name: Wait for services (optimized)
  run: |
    # Parallel health checks
    (
      timeout 60 bash -c 'until pg_isready -h localhost; do sleep 1; done' &&
      echo "✅ Postgres ready"
    ) &
    (
      timeout 60 bash -c 'until redis-cli ping; do sleep 1; done' &&
      echo "✅ Redis ready"
    ) &
    (
      timeout 120 bash -c 'until curl -sf http://localhost:5001/api/v0/version; do sleep 2; done' &&
      echo "✅ IPFS ready"
    ) &

    # Wait for all background jobs
    wait

    echo "All services ready"
```

---

## 7. Coverage Reporting

### Merged Coverage Report

```yaml
jobs:
  unit-tests:
    steps:
      - name: Run unit tests
        run: go test -coverprofile=coverage-unit.out ./...
      - uses: actions/upload-artifact@v4
        with:
          name: coverage-unit
          path: coverage-unit.out

  integration-tests:
    steps:
      - name: Run integration tests
        run: go test -coverprofile=coverage-integration.out ./tests/integration/...
      - uses: actions/upload-artifact@v4
        with:
          name: coverage-integration
          path: coverage-integration.out

  coverage-report:
    needs: [unit-tests, integration-tests]
    steps:
      - uses: actions/download-artifact@v4

      - name: Merge coverage
        run: |
          go install github.com/wadey/gocovmerge@latest
          gocovmerge coverage-*/*.out > coverage-merged.out

      - name: Check coverage threshold
        run: |
          total=$(go tool cover -func=coverage-merged.out | grep total | awk '{print $3}' | sed 's/%//')
          if (( $(echo "$total < 70" | bc -l) )); then
            echo "::error::Coverage $total% below 70% threshold"
            exit 1
          fi
          echo "✅ Coverage: $total%"
```

### Coverage with Codecov

```yaml
- name: Upload to Codecov
  uses: codecov/codecov-action@v4
  with:
    files: ./coverage-merged.out
    flags: all-tests
    name: full-coverage
    fail_ci_if_error: true
    verbose: true
```

---

## 8. Failure Detection & Reporting

### Comprehensive Failure Report

```yaml
- name: Generate failure report
  if: failure()
  run: |
    cat > failure-report.md <<'EOF'
    # Test Failure Report

    **Workflow:** ${{ github.workflow }}
    **Run:** #${{ github.run_number }}
    **Commit:** ${{ github.sha }}
    **Author:** ${{ github.actor }}

    ## Failed Jobs

    EOF

    # List failed jobs (requires GitHub CLI)
    gh run view ${{ github.run_id }} --json jobs --jq '.jobs[] | select(.conclusion=="failure") | "- " + .name' >> failure-report.md

    echo "" >> failure-report.md
    echo "## Debug Commands" >> failure-report.md
    echo '```bash' >> failure-report.md
    echo "gh run view ${{ github.run_id }} --log-failed" >> failure-report.md
    echo '```' >> failure-report.md

- name: Post failure report to PR
  if: failure() && github.event_name == 'pull_request'
  uses: actions/github-script@v7
  with:
    script: |
      const fs = require('fs');
      const report = fs.readFileSync('failure-report.md', 'utf8');

      await github.rest.issues.createComment({
        issue_number: context.issue.number,
        owner: context.repo.owner,
        repo: context.repo.repo,
        body: report
      });
```

### Automatic Issue Creation for Persistent Failures

```yaml
- name: Create issue for persistent failure
  if: failure() && github.event_name == 'schedule'
  uses: actions/github-script@v7
  with:
    script: |
      const title = `🚨 Scheduled Test Failure: ${context.workflow}`;

      // Check if issue already exists
      const issues = await github.rest.issues.listForRepo({
        owner: context.repo.owner,
        repo: context.repo.repo,
        state: 'open',
        labels: 'ci-failure'
      });

      const existing = issues.data.find(i => i.title === title);

      const body = `Scheduled test run failed.

**Workflow:** ${context.workflow}
**Run:** [#${context.runNumber}](${context.payload.repository.html_url}/actions/runs/${context.runId})
**Time:** ${new Date().toISOString()}

This indicates a persistent issue that needs attention.
      `;

      if (existing) {
        await github.rest.issues.createComment({
          owner: context.repo.owner,
          repo: context.repo.repo,
          issue_number: existing.number,
          body: body
        });
      } else {
        await github.rest.issues.create({
          owner: context.repo.owner,
          repo: context.repo.repo,
          title: title,
          body: body,
          labels: ['ci-failure', 'bug', 'P1']
        });
      }
```

---

## 9. Best Practices Summary

### Complete Optimized Test Job Template

```yaml
jobs:
  optimized-test:
    name: Optimized Test Example
    runs-on: self-hosted
    timeout-minutes: 15

    # Conditional execution
    if: |
      github.event_name != 'pull_request' ||
      contains(github.event.pull_request.labels.*.name, 'run-tests') ||
      (needs.check-changes.outputs.should_test == 'true')

    strategy:
      fail-fast: false
      matrix:
        # Parallelization
        shard: [1, 2, 3, 4]

    env:
      HOME: /root
      GOPATH: /root/go
      GOCACHE: /root/.cache/go-build

    steps:
      - uses: actions/checkout@v4

      # Module cache
      - name: Restore Go modules
        uses: actions/cache@v4
        with:
          path: |
            ~/go/pkg/mod
            ~/.cache/go-build
          key: go-mod-${{ hashFiles('go.sum') }}

      # Test result cache
      - name: Restore test cache
        id: test-cache
        uses: actions/cache@v4
        with:
          path: .test-cache/
          key: test-${{ hashFiles('**/*.go') }}

      - uses: actions/setup-go@v5
        with:
          go-version: '1.24'
          cache: false

      # Smart test execution
      - name: Run tests (cache-aware, sharded)
        env:
          SHARD: ${{ matrix.shard }}
          TOTAL_SHARDS: 4
        run: |
          if [ "${{ steps.test-cache.outputs.cache-hit }}" = "true" ]; then
            # Only test changed packages
            ./scripts/test-changed-packages.sh
          else
            # Full test run with sharding
            ./scripts/test-with-sharding.sh $SHARD $TOTAL_SHARDS
          fi

      # Retry on failure
      - name: Retry failed tests
        if: failure()
        run: |
          ./scripts/retry-failed-tests.sh

      # Coverage
      - name: Upload coverage
        uses: actions/upload-artifact@v4
        with:
          name: coverage-shard-${{ matrix.shard }}
          path: coverage-*.out

      # Enhanced reporting
      - name: Generate test report
        if: always()
        run: |
          ./scripts/generate-test-report.sh > test-report.md

      - name: Comment on PR
        if: always() && github.event_name == 'pull_request'
        uses: actions/github-script@v7
        with:
          script: |
            const fs = require('fs');
            const report = fs.readFileSync('test-report.md', 'utf8');
            await github.rest.issues.createComment({
              issue_number: context.issue.number,
              owner: context.repo.owner,
              repo: context.repo.repo,
              body: report
            });
```

---

## 10. Quick Reference: Before/After

### Before: Slow, Sequential, No Caching

```yaml
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
      - run: go test ./...  # ~20min, no caching
```

### After: Fast, Parallel, Cached

```yaml
jobs:
  check-changes:
    outputs:
      should_test: ${{ steps.filter.outputs.code }}
    steps:
      - uses: dorny/paths-filter@v3

  test:
    needs: check-changes
    if: needs.check-changes.outputs.should_test == 'true'
    strategy:
      matrix:
        shard: [1, 2, 3, 4]
    steps:
      - uses: actions/cache@v4  # Cache results
      - run: ./test-shard.sh ${{ matrix.shard }}  # ~5min with caching + sharding
```

**Result:** 4x speedup (20min → 5min)

---

**Generated:** 2025-11-22
**Purpose:** Practical implementation guide for test workflow optimizations
