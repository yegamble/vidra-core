# Sprint Plan: Operation Bedrock (Reliability & Verification)

**Sprint Goal**: Establish a reliable, reproducible test environment and verify the codebase integrity to validate the "88% Production Ready" claim.

## Context
The project claims high completion but local verification is blocked by infrastructure dependencies (Postgres, Redis, IPFS) and Docker rate limits. We cannot safely proceed with new features (Phase 2) until the foundation is solid.

## Priorities
1.  **Infrastructure**: Enable developers (and CI) to run all tests without hitting rate limits or "connection refused" errors.
2.  **Verification**: Once infra is up, run the full suite (`internal/repository`, `internal/ipfs`) and fix uncovered logic bugs.
3.  **Documentation**: Align `README.md` with the verified reality.

## Schedule
*   **Week 1**: Fix Test Infra, "Fail Fast" logic for tests, CI setup.
*   **Week 2**: Fix logic bugs found by enabled tests, Documentation update.

## Risks
*   **Docker Rate Limits**: Persistent blocker for anonymous CI/CD. Solution: Authenticated registry or caching.
*   **Hidden Regressions**: `internal/repository` tests might reveal broken SQL queries once they run.

## Definition of Done
*   `make test` runs successfully in < 5 minutes locally.
*   CI pipeline is green.
*   `README.md` accurately reflects supported features and verified platforms.
