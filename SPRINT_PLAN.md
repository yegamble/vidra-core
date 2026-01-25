# Sprint Plan: Operation Bedrock (Reliability & Verification)

**Sprint Goal**: Establish a reliable, reproducible test environment and verify the codebase integrity to validate the "88% Production Ready" claim.

## Context
The project claims high completion but local verification is blocked by infrastructure dependencies (Postgres, Redis, IPFS) and Docker rate limits. We cannot safely proceed with new features (Phase 2) until the foundation is solid.

## Team Assignments

### 🛠️ Builder
*   **Focus**: Test Infrastructure & "Fail Fast" Logic.
*   **Primary Task**: Modify `internal/testutil/database.go` to implement immediate failure if DB is unreachable (avoid 400s timeouts).
*   **Secondary Task**: Fix flaky tests in `internal/database` if time permits.

### 🧪 QA Guardian
*   **Focus**: Repository Layer Verification.
*   **Primary Task**: Execute `internal/repository` tests once Builder unblocks infrastructure.
*   **Secondary Task**: Identify and report any logic bugs or SQL regressions found during verification.

### 📝 Scribe
*   **Focus**: Documentation Truth.
*   **Primary Task**: Update `README.md` to include "Development Prerequisites" (Docker Hub auth, required ports).
*   **Secondary Task**: Ensure `docs/` reflect the reality of "Conditional Go" (Phase 2 features are present but require config).

### 🚦 Gatekeeper
*   **Focus**: CI Pipeline Integrity.
*   **Primary Task**: Ensure CI workflows are using cached dependencies where possible to mitigate rate limits.

## Schedule
*   **Week 1**: Fix Test Infra (Builder), "Fail Fast" logic (Builder), CI setup (Gatekeeper).
*   **Week 2**: Fix logic bugs found by enabled tests (QA/Builder), Documentation update (Scribe).

## Risks & Dependencies
*   **Docker Rate Limits**: Major blocker. Requires developer/CI authentication.
*   **Hidden Regressions**: `internal/repository` tests likely contain regressions masked by previous timeouts.
*   **Scope Creep**: Strictly NO new features until `make test` passes reliably.

## Definition of Done
*   [ ] `make test-unit` runs successfully in < 5 minutes locally (skips integration tests instantly if infra missing).
*   [ ] `make test-integration` runs successfully when infra is provided.
*   [ ] CI pipeline is green.
*   [ ] `README.md` accurately lists prerequisites for running tests.
*   [ ] `docs/architecture/roadmap.md` exists and clearly defines Phase 2.
