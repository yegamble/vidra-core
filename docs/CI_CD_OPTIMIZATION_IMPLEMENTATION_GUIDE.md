# CI/CD Optimization Implementation Guide

This guide provides step-by-step instructions for implementing the CI/CD optimizations detailed in the [Optimization Report](./CI_CD_OPTIMIZATION_REPORT.md).

---

## Quick Start: 30-Minute Quick Wins

These changes provide immediate 30-40% improvement with minimal effort.

### Step 1: Enable Optimized Test Workflow (5 minutes)

```bash
# Backup current test workflow
cp .github/workflows/test.yml .github/workflows/test-backup.yml

# Replace with optimized version
cp .github/workflows/test-optimized.yml .github/workflows/test.yml

# Commit and test
git add .github/workflows/test.yml
git commit -m "optimize: Enable parallel test execution in CI"
git push
```

**Expected Result:** Test suite runs 6-10 minutes faster

### Step 2: Enable Optimized Security Tests (5 minutes)

```bash
# Backup current security workflow
cp .github/workflows/security-tests.yml .github/workflows/security-tests-backup.yml

# Replace with optimized version
cp .github/workflows/security-tests-optimized.yml .github/workflows/security-tests.yml

# Commit and test
git add .github/workflows/security-tests.yml
git commit -m "optimize: Use matrix strategy for security tests"
git push
```

**Expected Result:** Security tests run 10-12 minutes faster

### Step 3: Verify Optimizations (20 minutes)

1. **Monitor the next PR build:**
   - Go to Actions tab in GitHub
   - Watch the new parallel execution
   - Compare timing with previous runs

2. **Check for issues:**
   - Verify all tests still pass
   - Check for resource contention (if you see failures)
   - Monitor runner CPU/memory usage

3. **Adjust if needed:**
   - If runner capacity is exceeded, reduce parallelism:

     ```yaml
     strategy:
       max-parallel: 4  # Reduce from unlimited
     ```

---

## Phase 1: Quick Wins (2 hours)

### 1.1 Remove Docker Installation Steps

**Why:** Self-hosted runners already have Docker installed. This check wastes 5-10 seconds per job.

**Files to modify:**

- `.github/workflows/test.yml`
- `.github/workflows/e2e-tests.yml`
- `.github/workflows/virus-scanner-tests.yml`
- `.github/workflows/video-import.yml`
- `.github/workflows/goose-migrate.yml`

**Change:**

```yaml
# REMOVE this entire step:
- name: Install Docker
  run: |
    if ! command -v docker &> /dev/null; then
      echo "Installing Docker..."
      curl -fsSL https://get.docker.com -o get-docker.sh
      sudo sh get-docker.sh
      sudo usermod -aG docker $USER
      rm get-docker.sh
      echo "Docker installed successfully"
    else
      echo "Docker already installed"
    fi
```

**Time Saved:** 2-3 minutes per workflow run

---

### 1.2 Optimize Go Module Caching

**Why:** Manual `go mod download` with retry logic is redundant when using `setup-go@v5` with caching.

**Change in all workflow files:**

```yaml
# BEFORE:
- name: Set up Go
  uses: actions/setup-go@v5
  with:
    go-version: ${{ env.GO_VERSION }}

- name: Install dependencies
  run: |
    # 30+ lines of retry logic
    max_attempts=5
    for i in $(seq 1 $max_attempts); do
      if timeout 600 go mod download; then
        # ...
      fi
    done

# AFTER:
- name: Set up Go
  uses: actions/setup-go@v5
  with:
    go-version: ${{ env.GO_VERSION }}
    cache: true
    cache-dependency-path: go.sum

# That's it! No manual download needed.
```

**Time Saved:** 1-2 minutes per job (4-8 minutes total per run)

---

### 1.3 Add Path Filters

**Why:** Skip CI runs for documentation-only changes.

**Update `test.yml`:**

```yaml
on:
  push:
    branches: [ main, develop ]
    paths-ignore:
      - '**/*.md'
      - 'docs/**'
      - '.github/workflows/blue-green-deploy.yml'
  pull_request:
    branches: [ main ]
    paths-ignore:
      - '**/*.md'
      - 'docs/**'
```

**Time Saved:** 100% on documentation-only PRs (30-40% of all PRs)

---

### 1.4 Remove Redundant apt-get Updates

**Why:** Self-hosted runners likely have common packages pre-installed.

**Change:**

```yaml
# BEFORE:
- name: Install system dependencies
  run: |
    sudo apt-get update
    sudo apt-get install -y make

# AFTER:
- name: Install system dependencies
  run: sudo apt-get install -y make
```

**Even Better:** Pre-install common packages on self-hosted runners.

**Time Saved:** 10-15 seconds per job (1-2 minutes total)

---

## Phase 2: Parallelization (4 hours)

### 2.1 Parallelize Main Test Suite

**Current dependency graph:**

```
changes → unit → integration → build
         unit → lint
```

**Optimized graph:**

```
┌─ unit ────────┐
├─ integration ─┼─→ build
├─ lint ────────┤
└─ format-check ┘
```

**Implementation:** See `.github/workflows/test-optimized.yml`

**Key changes:**

1. Remove `needs: [unit, changes]` from `integration` job
2. Remove `needs: changes` from multiple jobs
3. Add final gate job to verify all tests passed

**Time Saved:** 6-10 minutes

---

### 2.2 Convert Security Tests to Matrix

**Current:** 6 sequential jobs
**Optimized:** 1 matrix job with 6 parallel instances

**Implementation:** See `.github/workflows/security-tests-optimized.yml`

**Key changes:**

```yaml
jobs:
  security-tests:
    strategy:
      fail-fast: false
      matrix:
        category:
          - ssrf-protection
          - url-validation
          - activitypub-security
          - dependency-scanning
          - static-analysis
          - penetration-testing
    steps:
      - name: Run tests for ${{ matrix.category }}
        if: matrix.category == 'ssrf-protection'
        run: # category-specific tests
```

**Time Saved:** 10-12 minutes

---

### 2.3 Parallelize Virus Scanner Tests

**Current bottleneck:** ClamAV startup (2-5 min) happens 4 times

**Solution:** Share ClamAV service across jobs using job dependencies

```yaml
jobs:
  start-clamav:
    services:
      clamav:
        image: clamav/clamav:latest
        # ... config

  unit-tests:
    needs: start-clamav
    # Uses same clamav instance

  integration-tests:
    needs: start-clamav
    # Uses same clamav instance
```

**Alternative:** Use matrix strategy with single ClamAV startup

**Time Saved:** 8-12 minutes

---

## Phase 3: Composite Actions (6 hours)

### 3.1 Using Composite Actions

The optimized workflows already use three composite actions:

1. **`.github/actions/setup-go-cached/`** - Optimized Go setup
2. **`.github/actions/setup-postgres-test/`** - PostgreSQL test environment
3. **`.github/actions/install-security-tools/`** - Security tool installation

**Example usage:**

```yaml
steps:
  # Instead of 10+ lines of setup-go configuration:
  - name: Setup Go
    uses: ./.github/actions/setup-go-cached
    with:
      go-version: '1.24'

  # Instead of 20+ lines of security tool installation:
  - name: Install Security Tools
    uses: ./.github/actions/install-security-tools
    with:
      tools: 'govulncheck,gosec'

  # Instead of 15+ lines of PostgreSQL setup:
  - name: Setup PostgreSQL
    id: postgres
    uses: ./.github/actions/setup-postgres-test

  - name: Run migrations
    env:
      DATABASE_URL: ${{ steps.postgres.outputs.database-url }}
    run: make migrate-custom
```

**Time Saved:** Indirect (maintainability improvement, faster future changes)

---

### 3.2 Create Additional Composite Actions

Create these if you want further consolidation:

#### 3.2.1 Node.js Tools Setup

**File:** `.github/actions/setup-node-tools/action.yml`

```yaml
name: 'Setup Node.js Tools'
description: 'Installs Node.js and common npm tools with caching'

inputs:
  tools:
    description: 'npm packages to install'
    required: false
    default: '@apidevtools/swagger-cli @redocly/cli newman'

runs:
  using: 'composite'
  steps:
    - name: Set up Node.js
      uses: actions/setup-node@v4
      with:
        node-version: '20'
        cache: 'npm'

    - name: Cache global npm packages
      uses: actions/cache@v4
      with:
        path: ~/.npm
        key: npm-global-${{ runner.os }}-${{ inputs.tools }}

    - name: Install tools
      shell: bash
      run: npm install -g ${{ inputs.tools }}
```

**Usage:**

```yaml
- uses: ./.github/actions/setup-node-tools
  with:
    tools: 'newman newman-reporter-htmlextra'
```

---

#### 3.2.2 Retry Command

**File:** `.github/actions/retry-command/action.yml`

```yaml
name: 'Retry Command'
description: 'Executes a command with exponential backoff retry logic'

inputs:
  command:
    description: 'Command to execute'
    required: true
  max-attempts:
    description: 'Maximum retry attempts'
    required: false
    default: '5'
  timeout:
    description: 'Timeout in seconds'
    required: false
    default: '600'

runs:
  using: 'composite'
  steps:
    - name: Execute with retry
      shell: bash
      run: |
        max_attempts=${{ inputs.max-attempts }}
        timeout_seconds=${{ inputs.timeout }}

        for i in $(seq 1 $max_attempts); do
          if timeout $timeout_seconds ${{ inputs.command }}; then
            echo "Command succeeded on attempt $i"
            exit 0
          else
            if [ $i -lt $max_attempts ]; then
              delay=$((2 ** (i - 1) * 2))
              echo "Attempt $i failed, retrying in ${delay}s..."
              sleep $delay
            else
              echo "All $max_attempts attempts failed"
              exit 1
            fi
          fi
        done
```

**Usage:**

```yaml
- name: Install dependencies
  uses: ./.github/actions/retry-command
  with:
    command: 'go mod download'
    max-attempts: 5
```

---

## Phase 4: Advanced Optimizations (8 hours)

### 4.1 Self-Hosted Runner Optimization

#### 4.1.1 Pre-install Common Dependencies

Create a runner setup script:

**File:** `scripts/setup-runner.sh`

```bash
#!/bin/bash
set -e

echo "Setting up self-hosted runner optimizations..."

# Install common packages
sudo apt-get update
sudo apt-get install -y \
  make \
  postgresql-client \
  ffmpeg \
  jq

# Install Go
wget https://go.dev/dl/go1.24.6.linux-amd64.tar.gz
sudo tar -C /usr/local -xzf go1.24.6.linux-amd64.tar.gz
rm go1.24.6.linux-amd64.tar.gz

# Install security tools
/usr/local/go/bin/go install golang.org/x/vuln/cmd/govulncheck@latest
/usr/local/go/bin/go install github.com/securego/gosec/v2/cmd/gosec@latest
/usr/local/go/bin/go install honnef.co/go/tools/cmd/staticcheck@latest

# Install Node.js
curl -fsSL https://deb.nodesource.com/setup_20.x | sudo -E bash -
sudo apt-get install -y nodejs

# Install global npm packages
npm install -g \
  @apidevtools/swagger-cli \
  @redocly/cli \
  newman \
  newman-reporter-htmlextra

# Set up Go module cache directory
mkdir -p /opt/go-cache/modcache
mkdir -p /opt/go-cache/buildcache
sudo chown -R runner:runner /opt/go-cache

echo "Runner setup complete!"
```

**Configure runner environment:**

```bash
# Add to runner's .bashrc or systemd service
export GOMODCACHE=/opt/go-cache/modcache
export GOCACHE=/opt/go-cache/buildcache
export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin
```

**Time Saved:** 3-5 minutes per run

---

#### 4.1.2 Local Docker Registry Cache

Set up a local Docker registry to cache frequently used images:

```bash
# Start local registry
docker run -d \
  -p 5000:5000 \
  --restart=always \
  --name registry \
  -v /opt/docker-registry:/var/lib/registry \
  registry:2

# Configure Docker daemon to use local registry as mirror
cat > /etc/docker/daemon.json <<EOF
{
  "registry-mirrors": ["http://localhost:5000"]
}
EOF

sudo systemctl restart docker
```

**Pre-pull common images:**

```bash
docker pull postgres:15-alpine
docker pull redis:7-alpine
docker pull clamav/clamav:latest
docker pull ipfs/kubo:latest

# Push to local registry
docker tag postgres:15-alpine localhost:5000/postgres:15-alpine
docker push localhost:5000/postgres:15-alpine
# ... repeat for other images
```

**Update workflows to use local registry:**

```yaml
services:
  postgres:
    image: localhost:5000/postgres:15-alpine  # Use local cache
```

**Time Saved:** 1-2 minutes per run (faster image pulls)

---

### 4.2 Test Parallelization Within Go

Optimize individual test execution:

```bash
# Update Makefile
test-unit:
 @PKGS=$$(go list ./... | grep -v repository | grep -v integration); \
 go test -v -race -parallel=16 -short $$PKGS

test-integration-ci:
 @go test -v -short -race -parallel=16 ./...
```

**Key changes:**

- Increase `-parallel` from 8 to 16 (if runner has 16+ cores)
- Use `-short` flag to skip long-running tests in CI

**Time Saved:** 1-2 minutes

---

### 4.3 Build Cache Warming

Pre-populate build cache on self-hosted runners:

```bash
# Create warmup script
cat > scripts/warm-cache.sh <<'EOF'
#!/bin/bash
# Run on runner startup or daily via cron

cd /path/to/athena
git pull origin main

# Build project to populate cache
go build -o /tmp/athena ./cmd/server
rm /tmp/athena

echo "Cache warmed at $(date)"
EOF

chmod +x scripts/warm-cache.sh

# Add to crontab
crontab -e
# Add: 0 1 * * * /path/to/athena/scripts/warm-cache.sh
```

**Time Saved:** 30-60 seconds per build

---

## Verification & Monitoring

### Check Workflow Performance

```bash
# Use GitHub CLI to compare workflow run times
gh run list --workflow=test.yml --limit 10 --json conclusion,createdAt,updatedAt,url

# Calculate average duration
gh run list --workflow=test.yml --limit 100 --json createdAt,updatedAt \
  | jq '.[] | (.updatedAt | fromdateiso8601) - (.createdAt | fromdateiso8601)' \
  | jq -s 'add/length'
```

### Monitor Cache Hit Rates

Add to workflows:

```yaml
- name: Report cache statistics
  run: |
    echo "### Cache Statistics" >> $GITHUB_STEP_SUMMARY
    echo "Go module cache: $(du -sh $(go env GOMODCACHE))" >> $GITHUB_STEP_SUMMARY
    echo "Go build cache: $(du -sh $(go env GOCACHE))" >> $GITHUB_STEP_SUMMARY
```

### Track Metrics Over Time

Create a simple metrics dashboard:

```python
# scripts/analyze-workflow-performance.py
import json
import subprocess
from datetime import datetime
import statistics

def get_workflow_runs(workflow_name, limit=100):
    cmd = [
        'gh', 'run', 'list',
        f'--workflow={workflow_name}',
        f'--limit={limit}',
        '--json', 'conclusion,createdAt,updatedAt,status'
    ]
    result = subprocess.run(cmd, capture_output=True, text=True)
    return json.loads(result.stdout)

def calculate_duration_stats(runs):
    durations = []
    for run in runs:
        if run['conclusion'] == 'success':
            created = datetime.fromisoformat(run['createdAt'].replace('Z', '+00:00'))
            updated = datetime.fromisoformat(run['updatedAt'].replace('Z', '+00:00'))
            duration = (updated - created).total_seconds() / 60  # minutes
            durations.append(duration)

    return {
        'mean': statistics.mean(durations),
        'median': statistics.median(durations),
        'p95': sorted(durations)[int(len(durations) * 0.95)],
        'min': min(durations),
        'max': max(durations)
    }

# Usage
runs = get_workflow_runs('test.yml')
stats = calculate_duration_stats(runs)
print(f"Test Workflow Stats (last 100 runs):")
print(f"  Mean: {stats['mean']:.1f} min")
print(f"  Median: {stats['median']:.1f} min")
print(f"  P95: {stats['p95']:.1f} min")
```

---

## Rollback Plan

If optimizations cause issues:

### Quick Rollback

```bash
# Restore backed up workflows
cp .github/workflows/test-backup.yml .github/workflows/test.yml
cp .github/workflows/security-tests-backup.yml .github/workflows/security-tests.yml

git add .github/workflows/
git commit -m "rollback: Restore original CI workflows"
git push
```

### Gradual Rollout Strategy

1. **Test on feature branch first:**

   ```bash
   git checkout -b optimize/ci-improvements
   # Make changes
   git push -u origin optimize/ci-improvements
   # Verify CI passes
   ```

2. **Enable for specific branches:**

   ```yaml
   on:
     push:
       branches: [ optimize/** ]  # Only run optimized version on optimization branches
   ```

3. **Monitor for one week** before fully replacing original workflows

---

## Success Criteria

After implementing optimizations, verify:

- ✅ **Main test suite:** < 8 minutes (previously 12-18 min)
- ✅ **Security tests:** < 6 minutes (previously 15-20 min)
- ✅ **Overall CI time:** < 30 minutes (previously 45-60 min)
- ✅ **Cache hit rate:** > 80%
- ✅ **Test flakiness:** < 2% (same as before)
- ✅ **All tests passing:** 100%

---

## Next Steps

1. **Week 1:** Implement Phase 1 (Quick Wins)
2. **Week 2:** Monitor results, implement Phase 2 (Parallelization)
3. **Week 3:** Create composite actions (Phase 3)
4. **Week 4:** Advanced optimizations for self-hosted runners (Phase 4)
5. **Week 5:** Fine-tune and document

---

## Support

For questions or issues:

- Review the [full optimization report](./CI_CD_OPTIMIZATION_REPORT.md)
- Check GitHub Actions documentation
- Analyze failed runs using GitHub Actions Timeline view

**Common Issues:**

**Issue:** Jobs fail due to resource contention
**Solution:** Reduce `max-parallel` in matrix strategies

**Issue:** Cache misses are high
**Solution:** Verify cache keys match between runs, check runner disk space

**Issue:** Tests are flaky in parallel
**Solution:** Add test isolation, use `-failfast=false`, investigate race conditions
