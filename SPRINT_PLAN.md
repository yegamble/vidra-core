# Sprint Plan: Operation Bedrock (Reliability & Verification)

**Sprint Goal**: Establish a reliable, reproducible test environment and verify the codebase integrity to validate the "88% Production Ready" claim. The immediate focus is fixing the "Fail Fast" mechanism to prevent test timeouts and verifying the state of `repository` and `ipfs` tests.

## Context
The project claims high completion but local verification is blocked by infrastructure dependencies (Postgres, Redis, IPFS) and Docker rate limits. We cannot safely proceed with new features (Phase 2) until the foundation is solid.
Currently, running tests without a database causes massive delays (400s+) because connection retries are not optimized.

## Priorities
1.  **Infrastructure Optimization**: Implement "Fail Fast" logic in `internal/testutil/database.go` to ensure tests skip immediately if dependencies are missing.
2.  **Verification**: Run the full suite (`internal/repository`, `internal/ipfs`) using the dockerized infrastructure to establish a baseline pass/fail status.
3.  **Documentation**: Update `README.md` to accurately reflect the "Conditional Go" status and Docker prerequisites (Auth/Rate Limits).

## Scope
### In-Scope
*   `internal/testutil/database.go`: Implementation of `sync.Once` and global availability check.
*   `internal/repository`: Verification run and analysis of failures.
*   `internal/ipfs`: Verification run and analysis of failures.
*   `README.md`: Updates for developer setup.

### Out-of-Scope
*   New feature development (Phase 2).
*   Major refactoring of unrelated components.
*   Fixing complex logic bugs in `repository` tests (unless trivial). Focus is on *identification* first.

## Schedule
*   **Day 1**: Fix Test Infra ("Fail Fast"), Update Docs.
*   **Day 2**: Run Verification Suites, Triage Failures.
*   **Day 3**: Plan fixes for verified bugs.

## Risks
*   **Docker Rate Limits**: Persistent blocker for anonymous CI/CD. Solution: Authenticated registry or caching.
*   **Hidden Regressions**: `internal/repository` tests might reveal broken SQL queries once they run.

## Definition of Done
*   `go test ./internal/repository/...` skips immediately (< 1s) when DB is offline.
*   `go test ./internal/repository/...` runs (pass or fail) when DB is online.
*   Failures are logged as GitHub Issues.
*   `README.md` includes "Prerequisites" section.
