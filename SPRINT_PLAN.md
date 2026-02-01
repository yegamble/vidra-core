# Sprint Plan: Phase 1 Launch & Stabilization (Sprint 16)

**Sprint Goal**: **Green Build & Launch**. Eliminate local test infrastructure failures to guarantee a reproducible environment, and verify all Phase 1 Launch requirements (Load Tests, Security, Docs).

## Context
The project is in the final stretch of **Phase 1: Stabilization**.
*   **Completion**: 88%
*   **Status**: Features Complete, Security Hardened.
*   **Blocker**: Local integration tests (`make test-local`) are failing/skipping due to database connectivity issues (`dial tcp [::1]:5433: connect: operation not permitted`), preventing reliable local verification.

## Priorities

### 1. 🔴 Fix Test Infrastructure (Highest Priority)
*   **Assignee**: **Gatekeeper 🚦** / **Builder 🛠️**
*   **Problem**: `make test-local` fails to connect to Postgres on some environments (IPv6 `::1` vs IPv4 `127.0.0.1`), causing tests to skip.
*   **Task**:
    *   Investigate `scripts/test-setup.sh` and `docker-compose.test.yml`.
    *   Ensure explicit IPv4 binding (`127.0.0.1`) is used everywhere.
    *   Verify `make test-local` runs all integration tests without skipping.

### 2. 🟡 Verify Load Testing
*   **Assignee**: **QA Guardian 🧪**
*   **Problem**: `make load-test` target exists but hasn't been baselined.
*   **Task**:
    *   Run `make load-test`.
    *   Capture baseline metrics (RPS, Latency).
    *   Document results in `tests/loadtest/baseline.md` (or similar).

### 3. 🟢 Formalize Roadmap
*   **Assignee**: **Product Architect 🧩**
*   **Problem**: `README.md` outlines the vision, but a dedicated architecture document is missing.
*   **Task**:
    *   Create `docs/architecture/roadmap.md`.
    *   Explicitly define Phase 1 (Current), Phase 2 (Growth/Payments), and Phase 3 (Future).

## Definition of Done
*   [ ] `make test-local` passes locally with **0 skipped integration tests**.
*   [ ] Load tests executed and baseline recorded.
*   [ ] `docs/architecture/roadmap.md` exists and aligns with `README.md`.
*   [ ] CI pipeline is green.

## Risks
*   **Environment Differences**: Docker behavior varies between Linux/Mac/WSL. The fix must be robust.
