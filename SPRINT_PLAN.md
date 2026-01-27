# Sprint Plan: Operation Bedrock (Reliability & Verification)

**Sprint Goal**: Establish a reliable, reproducible test environment and verify the codebase integrity to validate the "88% Production Ready" claim.

## Context
The project is in a "Stabilization Phase". While feature completion is high (88%), local verification is hindered by infrastructure dependencies (Postgres, Redis, IPFS) and Docker rate limits. We cannot safely proceed with Phase 2 features until the foundation is solid and verifiable by any developer.

## Priorities
1.  **Infrastructure Reliability**: Enable developers (and CI) to run all tests without hitting rate limits or "connection refused" delays.
2.  **Codebase Verification**: Once infra is reliable, run the full suite (`internal/repository`, `internal/ipfs`) and fix uncovered logic bugs.
3.  **Documentation Accuracy**: Align `README.md` with the verified reality and provide clear developer setup instructions.

## Schedule
*   **Week 1**: Fix Test Infra (Fail Fast logic), Verify Repository Tests.
*   **Week 2**: Verify IPFS Tests, Update Documentation, CI Optimization.

## Risks
*   **Docker Rate Limits**: Persistent blocker for anonymous CI/CD. Solution: Authenticated registry or caching.
*   **Hidden Regressions**: `internal/repository` tests might reveal broken SQL queries once they actually run.

## Definition of Done
*   [ ] `make test` runs successfully in < 5 minutes locally (skipping integration tests instantly if DB unavailable).
*   [ ] `internal/repository` tests pass when DB is available.
*   [ ] `internal/ipfs` tests pass when IPFS is available.
*   [ ] CI pipeline is green and reliable.
*   [ ] `README.md` accurately reflects supported features, known limitations, and setup instructions.
