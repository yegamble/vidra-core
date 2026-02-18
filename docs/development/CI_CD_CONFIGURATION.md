# CI/CD Configuration Guide

This document describes the CI/CD configuration for the Athena project, including recent fixes and best practices.

## Overview

Athena uses GitHub Actions for CI/CD with self-hosted runners. The workflows are designed to ensure code quality, security, and reliability through comprehensive testing.

## Self-Hosted Runner Configuration

### Runner Setup

The project uses self-hosted GitHub Actions runners with the following configuration:

- **OS**: Ubuntu 24.04 LTS
- **Users**: `runner` and `github-runner` (both configured with passwordless sudo)
- **Docker**: Pre-installed for container-based tests
- **Dependencies**: Go, Make, PostgreSQL client, Docker Compose

### Passwordless Sudo Configuration

The runners are configured with passwordless sudo to allow dynamic dependency installation:

**File**: `/etc/sudoers.d/91-github-runner`

```bash
# GitHub Actions Runner - Passwordless sudo configuration
runner ALL=(ALL) NOPASSWD:ALL
github-runner ALL=(ALL) NOPASSWD:ALL
```

**Security Note**: This is acceptable for dedicated CI/CD runners. For enhanced security, consider restricting to specific commands or pre-installing all dependencies.

## ClamAV Configuration

### Issue and Resolution

**Problem**: ClamAV containers were failing health checks due to incorrect health check command.

**Root Cause**: The health check was using `/usr/local/bin/clamd-ping` which doesn't exist in the official ClamAV Docker image.

**Solution**: Updated all ClamAV health checks to use `/usr/local/bin/clamdcheck.sh`, which is the correct health check script provided by the ClamAV container.

### Files Modified

1. **docker-compose.test.yml** (Line 71)
   - Changed: `test: ["CMD", "/usr/local/bin/clamd-ping"]`
   - To: `test: ["CMD", "/usr/local/bin/clamdcheck.sh"]`

2. **.github/workflows/virus-scanner-tests.yml** (Lines 142, 255, 443)
   - Changed: `--health-cmd "clamdcheck"`
   - To: `--health-cmd "/usr/local/bin/clamdcheck.sh"`
   - Added missing health checks to edge-case-tests and performance-benchmarks jobs

3. **tests/e2e/docker-compose.yml** (Line 64)
   - Already using correct path: `/usr/local/bin/clamdcheck.sh` ✓

### ClamAV Health Check Best Practices

```yaml
# Docker Compose
healthcheck:
  test: ["CMD", "/usr/local/bin/clamdcheck.sh"]
  interval: 10s
  timeout: 5s
  retries: 30
  start_period: 120s  # ClamAV needs time to load signatures
```

```yaml
# GitHub Actions Services
clamav:
  image: clamav/clamav:latest
  ports:
    - 3310:3310
  options: >-
    --health-cmd "/usr/local/bin/clamdcheck.sh"
    --health-interval 30s
    --health-timeout 10s
    --health-retries 10
    --health-start-period 120s
```

### Why ClamAV Takes Time to Start

ClamAV containers need significant startup time because they:

1. Download virus signature databases (daily.cld, main.cvd, bytecode.cvd)
2. Load signatures into memory
3. Start the clamd daemon

**Recommendation**: Use persistent volumes to cache signatures:

```yaml
volumes:
  clamav-signatures:/var/lib/clamav
```

## Workflow Structure

### Test Suite Workflow (.github/workflows/test.yml)

**Jobs:**

- `unit`: Unit tests with Go
- `integration`: Integration tests with PostgreSQL, Redis, IPFS
- `lint`: Code linting with golangci-lint
- `build`: Binary compilation and artifact upload
- `migrations`: Database migration testing
- `docker`: Docker image build and compose testing
- `postman-e2e`: End-to-end API tests with Newman

**Key Features:**

- Retry logic for network operations
- Exponential backoff for dependency downloads
- Proper cleanup of containers before starting tests

### Virus Scanner Security Tests (.github/workflows/virus-scanner-tests.yml)

**Jobs:**

- `unit-tests`: Virus scanner unit tests
- `integration-tests`: ClamAV integration tests
- `edge-case-tests`: Breaking scenarios and attack simulations
- `performance-benchmarks`: Performance testing with large files
- `security-audit`: Security scanning with gosec
- `post-test-report`: Comprehensive test report generation

**Security Features:**

- EICAR test virus detection
- Network interruption handling
- Concurrent upload testing
- Performance threshold validation

### Security Tests (.github/workflows/security-tests.yml)

**Jobs:**

- `ssrf-protection-tests`: SSRF attack prevention validation
- `url-validation-tests`: URL and domain validation
- `activitypub-security-tests`: Federation security
- `dependency-scanning`: Vulnerability scanning with govulncheck
- `static-analysis`: Static security analysis with gosec and staticcheck
- `penetration-testing`: SSRF attack simulation
- `security-report`: Comprehensive security summary

## Common Issues and Solutions

### Issue: ClamAV Container Unhealthy

**Symptoms:**

```
Container athena_test_clamav is unhealthy
dependency failed to start: container athena_test_clamav is unhealthy
```

**Solution:**

1. Verify health check command: `/usr/local/bin/clamdcheck.sh`
2. Increase `start_period` to at least 120s
3. Check ClamAV logs: `docker logs <container-id>`

### Issue: Stale Containers Blocking Tests

**Symptoms:**

```
Container already exists
dependency failed to start
```

**Solution:**
Add cleanup step in Makefile:

```makefile
postman-e2e:
 @echo "Cleaning up any existing test containers..."
 COMPOSE_PROJECT_NAME=athena-test $(DOCKER_COMPOSE) -f docker-compose.test.yml down -v 2>/dev/null || true
 # ... rest of target
```

### Issue: Sudo Permission Denied

**Symptoms:**

```
sudo: a terminal is required to read the password
sudo: a password is required
```

**Solution:**
Configure passwordless sudo as described in the "Self-Hosted Runner Configuration" section above.

## Docker Compose Configuration

### Test Environment (docker-compose.test.yml)

Services:

- **postgres-test**: PostgreSQL 15 with test database
- **redis-test**: Redis 7 for caching
- **ipfs-test**: IPFS Kubo for decentralized storage
- **clamav-test**: ClamAV for virus scanning (with proper health check)
- **app-test**: Athena application under test
- **newman**: Postman/Newman for E2E API tests

### Health Check Configuration

All services use health checks to ensure they're ready before dependent services start:

```yaml
depends_on:
  postgres-test:
    condition: service_healthy
  redis-test:
    condition: service_healthy
  ipfs-test:
    condition: service_started  # IPFS doesn't have reliable health check
  clamav-test:
    condition: service_healthy
```

## Best Practices

### 1. Always Use Health Checks

Ensure all critical services have health checks to prevent race conditions:

```yaml
healthcheck:
  test: ["CMD", "appropriate-health-check-command"]
  interval: 10s
  timeout: 5s
  retries: 10
  start_period: 30s  # Adjust based on service startup time
```

### 2. Implement Retry Logic

For network operations, always implement retry logic with exponential backoff:

```bash
max_attempts=5
for i in $(seq 1 $max_attempts); do
  if command; then
    echo "Success on attempt $i"
    break
  else
    if [ $i -lt $max_attempts ]; then
      delay=$((2 ** (i - 1) * 2))  # Exponential backoff
      echo "Attempt $i failed, retrying in ${delay}s..."
      sleep $delay
    else
      echo "All attempts failed"
      exit 1
    fi
  fi
done
```

### 3. Clean Up Before Starting

Always clean up existing containers before starting new ones:

```bash
docker compose down -v 2>/dev/null || true
docker compose up -d
```

### 4. Use Persistent Volumes for Caching

Persist ClamAV signatures and other large downloads to speed up CI runs:

```yaml
volumes:
  clamav-signatures:
    driver: local
```

### 5. Set Appropriate Timeouts

Configure timeouts based on expected operation duration:

- Unit tests: 5-15 minutes
- Integration tests: 15-30 minutes
- E2E tests: 20-45 minutes
- Security scans: 10-20 minutes

## Monitoring and Debugging

### View Workflow Runs

```bash
# List recent runs
gh run list --limit 10

# View specific run
gh run view <run-id>

# View logs
gh run view <run-id> --log
```

### Check Container Logs

```bash
# Check ClamAV logs
docker logs athena_test_clamav

# Check all compose logs
COMPOSE_PROJECT_NAME=athena-test docker compose -f docker-compose.test.yml logs
```

### Debug Health Checks

```bash
# Manually test health check
docker exec <container-id> /usr/local/bin/clamdcheck.sh

# Check container health status
docker inspect <container-id> | jq '.[0].State.Health'
```

## Maintenance

### Regular Tasks

1. **Update Dependencies**: Keep Go, Docker, and other tools up to date
2. **Review Logs**: Periodically check workflow logs for warnings
3. **Monitor Disk Usage**: CI runners can accumulate Docker images and caches
4. **Test Locally**: Test workflow changes locally before pushing

### Cleanup Commands

```bash
# Remove all stopped containers
docker container prune -f

# Remove unused images
docker image prune -a -f

# Remove unused volumes
docker volume prune -f

# Clean Go caches
go clean -cache -testcache -modcache
```

## Related Documentation

- [Test Execution Guide](TEST_EXECUTION_GUIDE.md)
- [Virus Scanner Test Report](VIRUS_SCANNER_TEST_REPORT.md)
- [Security Documentation](../security/README.md)
- [Deployment Guide](../deployment/README.md)

## Recent Changes

### 2025-11-18: ClamAV Health Check Fixes

- **Commits**:
  - `1ff1de3`: Fixed docker-compose.test.yml ClamAV health check
  - `1ac73f9`: Fixed virus-scanner-tests.yml ClamAV health checks

- **Impact**: Resolved all ClamAV container startup failures in CI/CD
- **Files Modified**:
  - `docker-compose.test.yml`
  - `.github/workflows/test.yml`
  - `.github/workflows/virus-scanner-tests.yml`
  - `Makefile`

### 2025-11-18: Passwordless Sudo Configuration

- **File**: `/etc/sudoers.d/91-github-runner`
- **Impact**: Resolved all sudo permission errors in GitHub Actions
- **Security**: Acceptable for dedicated CI/CD runners

## Support

For issues with CI/CD configuration:

1. Check this documentation first
2. Review workflow logs with `gh run view --log <run-id>`
3. Check container logs if tests are failing
4. Create an issue in the repository with relevant logs
