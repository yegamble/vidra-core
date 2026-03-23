# End-to-End (E2E) Testing for Athena

This directory contains the end-to-end testing framework for the Athena video platform. E2E tests validate complete user workflows from start to finish, testing the entire application stack.

## Overview

The E2E testing framework tests:

- Complete user workflows (register, login, upload, view, delete)
- API endpoint interactions
- Database persistence
- File storage (S3/MinIO)
- Virus scanning (ClamAV)
- Video encoding workflows
- Federation features (ActivityPub)
- Search functionality
- Social interactions (likes, comments)

## Architecture

```
tests/e2e/
├── docker-compose.yml       # Test environment setup
├── helpers.go                # Reusable test utilities
├── scenarios/                # Test scenario implementations
│   └── video_workflow_test.go
├── fixtures/                 # Test data files
│   ├── README.md
│   └── data/
│       └── users.json
└── config/                   # Test configuration
    └── e2e_config.yaml
```

## Prerequisites

### Required Tools

1. **Docker & Docker Compose** (v2.0+)

   ```bash
   docker --version
   docker-compose --version
   ```

2. **Go** (1.24+)

   ```bash
   go version
   ```

3. **FFmpeg** (for generating test video files)

   ```bash
   ffmpeg -version
   ```

4. **Make** (optional, for convenience commands)

   ```bash
   make --version
   ```

## Quick Start

### 1. Start Test Environment

```bash
# Start all services (Postgres, Redis, MinIO, ClamAV, Athena API)
docker-compose -f tests/e2e/docker-compose.yml up -d

# Check service health
docker-compose -f tests/e2e/docker-compose.yml ps

# View logs
docker-compose -f tests/e2e/docker-compose.yml logs -f athena-api-e2e
```

### 2. Generate Test Fixtures

```bash
# Generate test video files
cd tests/e2e/fixtures
ffmpeg -f lavfi -i testsrc=duration=5:size=640x480:rate=24 \
  -c:v libx264 -preset ultrafast -pix_fmt yuv420p test_video_480p.mp4
```

### 3. Run E2E Tests

```bash
# Run all E2E tests
E2E_BASE_URL=http://localhost:8080 go test -v ./tests/e2e/scenarios/...

# Run specific test
E2E_BASE_URL=http://localhost:8080 go test -v ./tests/e2e/scenarios/ -run TestVideoUploadWorkflow

# Run with race detection
E2E_BASE_URL=http://localhost:8080 go test -v -race ./tests/e2e/scenarios/...

# Run with timeout
E2E_BASE_URL=http://localhost:8080 go test -v -timeout 30m ./tests/e2e/scenarios/...
```

### 4. Cleanup

```bash
# Stop and remove all containers
docker-compose -f tests/e2e/docker-compose.yml down

# Remove volumes (clean slate)
docker-compose -f tests/e2e/docker-compose.yml down -v
```

## Test Scenarios

### Implemented Scenarios

1. **TestVideoUploadWorkflow** - Complete video lifecycle
   - User registration
   - Video upload
   - Video retrieval
   - Video listing
   - Video search
   - Video deletion

2. **TestUserAuthenticationFlow** - Authentication
   - User registration
   - User login
   - Token generation
   - Protected endpoint access

3. **TestVideoSearchFunctionality** - Search
   - Video upload
   - Full-text search
   - Result verification

### Planned Scenarios (To Be Implemented)

- [ ] Federation workflows (ActivityPub publish/subscribe)
- [ ] Video encoding and quality variants
- [ ] Social interactions (likes, comments, shares)
- [ ] Channel management
- [ ] Playlist creation and management
- [ ] Live streaming workflows
- [ ] Video transcoding status tracking
- [ ] Privacy settings (public, unlisted, private)
- [ ] User blocking and moderation
- [ ] Payment workflows (IOTA)
- [ ] OAuth2 third-party app authorization
- [ ] Video download and offline viewing

## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `E2E_BASE_URL` | `http://localhost:8080` | Base URL for API |
| `E2E_TIMEOUT` | `30m` | Test timeout |
| `E2E_ADMIN_EMAIL` | `admin@example.com` | Admin user email |
| `E2E_ADMIN_PASSWORD` | `admin123` | Admin user password |

### Docker Compose Configuration

The `docker-compose.yml` file sets up a complete test environment:

- **PostgreSQL** (port 5433) - Test database
- **Redis** (port 6380) - Session storage
- **MinIO** (ports 9000, 9001) - S3-compatible object storage
- **ClamAV** (port 3311) - Virus scanning
- **Athena API** (port 8080) - Application server

All services include health checks to ensure proper startup order.

## Writing E2E Tests

### Basic Test Structure

```go
package scenarios

import (
    "testing"
    "github.com/yegamble/athena/tests/e2e"
)

func TestMyWorkflow(t *testing.T) {
    if testing.Short() {
        t.Skip("Skipping E2E test in short mode")
    }

    cfg := e2e.DefaultConfig()
    client := e2e.NewTestClient(cfg.BaseURL)

    // 1. Setup - Register user
    userID, token := client.RegisterUser(t, "testuser", "test@example.com", "password")

    // 2. Execute workflow
    videoID := client.UploadVideo(t, "fixtures/test_video.mp4", "Title", "Description")

    // 3. Verify results
    video := client.GetVideo(t, videoID)
    assert.Equal(t, "Title", video["title"])

    // 4. Cleanup
    client.DeleteVideo(t, videoID)
}
```

### Using Test Helpers

The `helpers.go` file provides reusable test utilities:

```go
// Create authenticated client
client := e2e.NewTestClient(baseURL)

// Register user
userID, token := client.RegisterUser(t, username, email, password)

// Login
userID, token := client.Login(t, username, password)

// Upload video
videoID := client.UploadVideo(t, videoPath, title, description)

// Get video details
video := client.GetVideo(t, videoID)

// List videos
videos := client.ListVideos(t)

// Search videos
results := client.SearchVideos(t, query)

// Delete video
client.DeleteVideo(t, videoID)

// Wait for service
err := e2e.WaitForService(ctx, url, timeout)
```

## CI/CD Integration

### GitHub Actions

```yaml
name: E2E Tests

on:
  push:
    branches: [main, develop]
  pull_request:
    branches: [main, develop]

jobs:
  e2e:
    runs-on: ubuntu-latest
    timeout-minutes: 30

    steps:
      - uses: actions/checkout@v3

      - name: Set up Go
        uses: actions/setup-go@v4
        with:
          go-version: '1.24'

      - name: Install FFmpeg
        run: sudo apt-get update && sudo apt-get install -y ffmpeg

      - name: Generate test fixtures
        run: |
          cd tests/e2e/fixtures
          ffmpeg -f lavfi -i testsrc=duration=5:size=640x480:rate=24 \
            -c:v libx264 -preset ultrafast -pix_fmt yuv420p test_video_480p.mp4

      - name: Start test environment
        run: docker-compose -f tests/e2e/docker-compose.yml up -d

      - name: Wait for services
        run: |
          timeout 120 bash -c 'until curl -f http://localhost:8080/health; do sleep 5; done'

      - name: Run E2E tests
        run: |
          E2E_BASE_URL=http://localhost:8080 go test -v -timeout 20m ./tests/e2e/scenarios/...

      - name: Collect logs on failure
        if: failure()
        run: |
          docker-compose -f tests/e2e/docker-compose.yml logs

      - name: Cleanup
        if: always()
        run: docker-compose -f tests/e2e/docker-compose.yml down -v
```

## Debugging

### View Service Logs

```bash
# All services
docker-compose -f tests/e2e/docker-compose.yml logs -f

# Specific service
docker-compose -f tests/e2e/docker-compose.yml logs -f athena-api-e2e

# Database logs
docker-compose -f tests/e2e/docker-compose.yml logs -f postgres-e2e
```

### Access Services Directly

```bash
# Access Postgres
docker exec -it athena-e2e-postgres psql -U athena_test -d athena_e2e

# Access Redis
docker exec -it athena-e2e-redis redis-cli

# Access MinIO console
open http://localhost:9001  # user: minioadmin, pass: minioadmin
```

### Run Tests with Verbose Output

```bash
# Maximum verbosity
E2E_BASE_URL=http://localhost:8080 go test -v -count=1 ./tests/e2e/scenarios/... 2>&1 | tee e2e_output.log
```

### Inspect Database State

```bash
# Check uploaded videos
docker exec athena-e2e-postgres psql -U athena_test -d athena_e2e -c "SELECT id, title, state FROM videos;"

# Check users
docker exec athena-e2e-postgres psql -U athena_test -d athena_e2e -c "SELECT id, username, email FROM users;"
```

## Performance Testing Integration

E2E tests can be combined with load testing:

```bash
# Run E2E tests to verify functionality
E2E_BASE_URL=http://localhost:8080 go test -v ./tests/e2e/scenarios/...

# Then run load tests
k6 run -e BASE_URL=http://localhost:8080 tests/loadtest/k6-video-platform.js
```

## Troubleshooting

### Common Issues

**Services not starting:**

```bash
# Check Docker resources
docker system df

# Clean up old containers/volumes
docker system prune -a --volumes
```

**ClamAV taking too long:**

- ClamAV requires 2-3 minutes to initialize on first start (downloading virus definitions)
- Increase `start_period` in docker-compose health check if needed

**Tests timing out:**

- Increase test timeout: `go test -timeout 60m`
- Check service logs for errors
- Ensure adequate system resources (4GB+ RAM recommended)

**Database migration errors:**

- Ensure migrations are run before tests
- Check database connectivity
- Verify DATABASE_URL environment variable

## Best Practices

1. **Isolation**: Each test should be independent and clean up after itself
2. **Idempotency**: Tests should produce the same result when run multiple times
3. **Speed**: Use smaller test files (480p videos) to reduce test time
4. **Clarity**: Use descriptive test names and log important steps
5. **Cleanup**: Always clean up resources (delete uploaded videos, users)
6. **Fixtures**: Reuse test data from fixtures/ directory
7. **Health Checks**: Wait for services to be ready before running tests

## Resources

- [Go Testing Documentation](https://pkg.go.dev/testing)
- [Testify Framework](https://github.com/stretchr/testify)
- [Docker Compose](https://docs.docker.com/compose/)
- [FFmpeg Documentation](https://ffmpeg.org/documentation.html)

## Contributing

When adding new E2E tests:

1. Create test in `scenarios/` directory
2. Use helpers from `helpers.go` when possible
3. Add test fixtures to `fixtures/` if needed
4. Update this README with new test scenarios
5. Ensure tests pass in CI before merging
