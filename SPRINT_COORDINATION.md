# Sprint Coordination: Operation Bedrock & Secure Launch

This document outlines the execution flow and agent handoffs for the current sprint.

## 🔄 Execution Sequence

### Phase 1: Unblock & Secure (Parallel)
*   **Builder 🛠️**: **IMMEDIATELY** pick up "Optimize Fail Fast" (#1).
    *   *Goal*: Allow tests to be run locally without massive delays.
    *   *Handoff*: Once done, signal QA/Sentinel that CI is faster.
*   **Sentinel 🛡️**: **IMMEDIATELY** pick up "Credential Rotation" (#2) and "Git Cleanup" (#3).
    *   *Goal*: Prepare the security scripts.
    *   *Note*: These tasks can happen in parallel with Builder's work.

### Phase 2: Verification (Sequential)
*   **Builder 🛠️**: Once "Fail Fast" is done, verify `internal/repository` tests (#4).
    *   *Goal*: Ensure SQL logic is sound.
    *   *Dependency*: Requires "Fail Fast" to be fixed so running tests isn't painful.

### Phase 3: Documentation & Final Polish
*   **Scribe 📝**: Update `README.md` and check Monitoring docs (#5).
    *   *Goal*: Reflect the true state of the project.
    *   *Trigger*: Can start anytime, but best after Phase 1.

## 🚦 Handoff Protocols

*   **Builder -> Sentinel**: When `internal/testutil` is updated, ensure it doesn't break any security scanners (unlikely, but good to check).
*   **Sentinel -> All**: When Credential Scripts are ready, broadcast that `.env` setup might change for production.
*   **QA -> Builder**: If Load Tests (from Sprint 15 scope) are added, coordinate with Builder on where to run them.

## ⚠️ Critical Flags
*   **Do NOT** force push the Git History Cleanup changes without explicit human approval. Sentinel should only provide the *script/guide*.
*   **Do NOT** merge broken repository tests. Fix the tests or the code.
