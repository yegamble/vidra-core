# Sprint Plan: Operation Bedrock (Reliability & Verification)

**Sprint Goal**: Establish a reliable, reproducible test environment and verify the codebase integrity to validate the "88% Production Ready" claim.

## Context
The project is in a "Stabilization Phase". While feature completion is high (88%), local verification is hindered by infrastructure dependencies (Postgres, Redis, IPFS) and Docker rate limits. We cannot safely proceed with Phase 2 features until the foundation is solid and verifiable by any developer.

## Priorities
1.  **Infrastructure Reliability**: Enable developers (and CI) to run all tests quickly without hitting rate limits or "connection refused" delays. Specifically, optimize "Fail Fast" logic using TCP dialing.
2.  **Codebase Verification**: Once infra is reliable, run the full suite (`internal/repository`) and fix uncovered logic bugs or schema drift.
3.  **Documentation Accuracy**: Align `README.md` with the verified reality and provide clear developer setup instructions (including Docker Hub login).

## Schedule
*   **Step 1**: Optimize "Fail Fast" in Test Helpers (Builder).
*   **Step 2**: Verify and Fix Repository Tests (Builder).
*   **Step 3**: Update Documentation (Scribe).

## Risks
*   **Docker Rate Limits**: Persistent blocker for anonymous CI/CD. Solution: Authenticated registry or caching.
*   **Hidden Regressions**: `internal/repository` tests might reveal broken SQL queries once they actually run.

## Definition of Done
*   [ ] `make test` runs successfully in < 5 minutes locally (skipping integration tests instantly if DB unavailable).
*   [ ] `internal/repository` tests pass when DB is available.
*   [ ] CI pipeline is green and reliable.
*   [ ] `README.md` accurately reflects supported features, known limitations, and setup instructions.
