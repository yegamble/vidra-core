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
*   **Task**: Optimize "Fail Fast" in `internal/testutil/database.go`.
*   **Method**: Replace `connectWithRetry` with `net.DialTimeout` (TCP check).
*   **Result**: `go test` skips instantly if DB is missing.

### Step 2: Secure the Platform (Sentinel 🛡️)
*   **Task**: Create Credential Rotation Scripts.
*   **Task**: Create Git History Cleanup Guide.
*   **Result**: Paths to remediation are scripted/documented.

### Step 3: Verify Integrity (Builder 🛠️)
*   **Task**: Run `go test ./internal/repository/...` with DB.
*   **Task**: Fix any SQL/Logic errors found.
*   **Result**: Repository layer is proven correct.

### Step 4: Update Documentation (Scribe 📝)
*   **Task**: Update `README.md` (Project Status, Prerequisites).
*   **Task**: Verify Monitoring Docs.
*   **Result**: Documentation matches "Stabilization Phase" reality.

## Risks
*   **Docker Rate Limits**: CI requires authentication.
*   **Data Loss**: Git history cleanup is destructive (requires force push).
*   **Hidden Regressions**: Repository tests may reveal significant broken logic.

## Definition of Done
*   [x] `make test` runs efficiently (fast skip or fast pass).
*   [x] Security remediation scripts exist in `scripts/`.
*   [x] `internal/repository` tests pass.
*   [x] `README.md` is accurate.
