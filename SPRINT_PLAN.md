# Sprint Plan: Operation Bedrock & Secure Launch

**Sprint Goal**: Establish a reliable, reproducible test environment AND secure the platform for Phase 1 Launch.

## Context
We are combining "Operation Bedrock" (Reliability) and "Sprint 15" (Phase 1 Launch) into a single execution plan.
- **Reliability**: The current test infrastructure is too slow (missing "Fail Fast" optimization) and repository tests are unverified. This blocks efficient development.
- **Security**: A critical security advisory (Credential Exposure) requires immediate remediation (Credential Rotation, Git History Cleanup).

## Priorities
1.  **Infrastructure Reliability (Blocker)**: Optimize "Fail Fast" logic to unblock local development and CI.
2.  **Security Hardening (Critical)**: Create scripts and guides to rotate credentials and clean git history.
3.  **Codebase Verification (High)**: Run and fix `internal/repository` tests to ensure logic integrity.
4.  **Documentation (Medium)**: Align docs with reality (Stabilization Phase, Setup Instructions).

## Schedule & Assignments

### Step 1: Unblock Development (Builder 🛠️)
*   **Status**: ✅ Complete
*   **Task**: Optimize "Fail Fast" in `internal/testutil/database.go`.
*   **Method**: Verified `checkTCP` implementation in `verifyInfra`.
*   **Result**: `go test` skips instantly if DB is missing (Verified 0.00s skips).

### Step 2: Secure the Platform (Sentinel 🛡️)
*   **Status**: 🔄 In Progress
*   **Task**: Create Credential Rotation Scripts.
*   **Task**: Create Git History Cleanup Guide.
*   **Result**: Scripts exist (`scripts/rotate-credentials.sh`, `scripts/clean-git-history.sh`); pending final verification and `setup-production-env.sh`.

### Step 3: Verify Integrity (Builder 🛠️)
*   **Status**: 🔄 In Progress
*   **Task**: Run `go test ./internal/repository/...` with DB.
*   **Task**: Fix any SQL/Logic errors found.
*   **Result**: Tests to be run with `docker compose -f docker-compose.ci.yml`.

### Step 4: Update Documentation (Scribe 📝)
*   **Status**: 📋 To Do
*   **Task**: Update `README.md` (Project Status, Prerequisites).
*   **Task**: Verify Monitoring Docs.
*   **Result**: Documentation matches "Stabilization Phase" reality.

## Risks
*   **Docker Rate Limits**: CI requires authentication.
*   **Data Loss**: Git history cleanup is destructive (requires force push).
*   **Hidden Regressions**: Repository tests may reveal significant broken logic.

## Definition of Done
*   [x] `make test` runs efficiently (fast skip or fast pass).
*   [ ] Security remediation scripts exist in `scripts/`.
*   [ ] `internal/repository` tests pass.
*   [ ] `README.md` is accurate.
