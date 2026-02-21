## Docker Mock Services for Integration Testing

External service integration tests use Docker Compose profile `test-integration` to spin up mock instances on port range `19xxx` (avoids conflicts with dev services).

### Mock services available

| Service | Port | Purpose |
|---------|------|---------|
| MinIO | 19100 | S3-compatible storage |
| ATProto PDS | 19200 | BlueSky federation mock |
| ActivityPub | 19300 | ActivityPub federation mock |
| Mailpit SMTP | 19401 | Email sending |
| IOTA node | 19500 | Payment mock |
| Postgres | 15432 | Test database |
| Redis | 16379 | Cache |
| IPFS | 15001 | P2P storage |

### Commands

```bash
# One-shot: start services, run tests, tear down
make test-external-integration

# Manual: keep services running across multiple test runs
make test-mock-services-up
go test -tags integration ./tests/... -run TestS3
make test-mock-services-down

# Keep services alive after test run (debugging)
TEST_KEEP_SERVICES=true ./scripts/test-integration.sh
```

### Writing integration tests

Tests gate on `TEST_INTEGRATION=true` env var (set automatically by `test-integration.sh`):

```go
func TestS3Upload(t *testing.T) {
    if os.Getenv("TEST_INTEGRATION") != "true" {
        t.Skip("set TEST_INTEGRATION=true to run")
    }
    // connect to MinIO at localhost:19100
}
```

Or use `testing.Short()` guard for DB-backed tests (standard pattern).

### Script

`scripts/test-integration.sh` handles: start services → wait for readiness → run tests → tear down (even on failure).
