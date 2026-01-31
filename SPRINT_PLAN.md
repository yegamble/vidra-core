# Sprint Plan: Operation Bedrock (Reliability & Verification)

**Sprint Goal**: Verify the integrity of the Data Access Layer (Repository) and ensure all core tests pass reliably.

## Context
We are in **Phase 1: Stabilization**. "Fail Fast" logic is implemented, allowing tests to skip gracefully when infrastructure is missing. Now we must run the tests with infrastructure available and fix the regressions that were hidden by skipped tests.

## Priorities
1.  **Infrastructure Reliability**: ✅ **DONE** ("Fail Fast" logic implemented).
2.  **Codebase Verification (Repositories)**: Run the full `internal/repository` suite and fix logic bugs.
3.  **Codebase Verification (IPFS)**: Run `internal/ipfs` tests and ensure they pass/skip correctly.
4.  **Documentation Accuracy**: Align `README.md` and dev docs.

## Execution Plan: Repository Verification
We are splitting the repository verification into logical chunks to isolate failures.

### Chunk A: Identity & Auth (Critical Path)
*   **Modules**: `user_repository`, `auth_repository`, `session_repository`
*   **Goal**: Ensure users can sign up, login, and maintain sessions.
*   **Status**: Pending

### Chunk B: Content Management (Core Feature)
*   **Modules**: `video_repository`, `upload_repository`, `encoding_repository`
*   **Goal**: Ensure video metadata is stored, uploads are tracked, and encoding jobs are persisted.
*   **Status**: Pending

### Chunk C: Social & Interaction
*   **Modules**: `comment_repository`, `rating_repository`, `subscription_repository`, `playlist_repository`
*   **Goal**: Ensure users can interact with content and each other.
*   **Status**: Pending

### Chunk D: Federation (ActivityPub)
*   **Modules**: `activitypub_repository`, `federation_repository`
*   **Goal**: Ensure federation keys and actors are correctly managed.
*   **Status**: Pending

## Risks
*   **Hidden Regressions**: We expect to find broken SQL queries (e.g., schema mismatches) now that tests are actually running.
*   **Test Data Pollution**: Tests might interfere with each other if `TruncateTables` isn't used correctly.

## Definition of Done
*   [x] `make test` runs successfully in < 5 minutes locally (skipping integration tests instantly if DB unavailable).
*   [ ] `internal/repository/...` tests pass 100% when DB is available.
*   [ ] `internal/ipfs/...` tests pass when IPFS is available.
*   [ ] `README.md` updated with correct "Local Development" instructions.
