# Current Sprint Backlog: Repository Verification

**Sprint Goal:** Verify the integrity of the Data Access Layer.

---

## 1. Verify Identity & Auth Repositories (Chunk A)
**Assignee:** Builder 🛠️
**Priority:** Critical
**Status:** To Do

**Description:**
Verify that the core identity tables (`users`, `sessions`, `refresh_tokens`, `email_verification`) are working correctly.

**Scope:**
*   `internal/repository/user_repository_test.go`
*   `internal/repository/auth_repository_test.go`
*   `internal/repository/twofa_backup_code_repository_test.go`

**Instructions:**
1.  Run `go test -v ./internal/repository/user_repository_test.go ...` (ensure DB is running).
2.  Fix any SQL errors (e.g., missing columns in `INSERT`, wrong column names).
3.  Ensure `testutil.SetupTestDB` works as expected.

**Acceptance Criteria:**
*   [ ] All tests in scope pass when Postgres is available.

---

## 2. Verify Content Management Repositories (Chunk B)
**Assignee:** Builder 🛠️
**Priority:** High
**Status:** To Do

**Description:**
Verify video metadata, upload sessions, and encoding job persistence. This is the core "PeerTube" functionality.

**Scope:**
*   `internal/repository/video_repository_test.go`
*   `internal/repository/upload_repository_test.go`
*   `internal/repository/encoding_repository_test.go`
*   `internal/repository/video_category_repository_test.go`

**Instructions:**
1.  Run tests for the modules listed above.
2.  Pay attention to `jsonb` columns and array fields (`tags`, `processed_cids`).

**Acceptance Criteria:**
*   [ ] All tests in scope pass when Postgres is available.

---

## 3. Verify Social & Interaction Repositories (Chunk C)
**Assignee:** Builder 🛠️
**Priority:** Medium
**Status:** To Do

**Description:**
Verify social features like comments, ratings, playlists, and subscriptions.

**Scope:**
*   `internal/repository/comment_repository_test.go`
*   `internal/repository/rating_repository_test.go`
*   `internal/repository/subscription_repository_test.go`
*   `internal/repository/playlist_repository_test.go`

**Instructions:**
1.  Run tests.
2.  Verify foreign key constraints (cascading deletes) are tested/working.

**Acceptance Criteria:**
*   [ ] All tests in scope pass.

---

## 4. Verify Federation Repositories (Chunk D)
**Assignee:** Builder 🛠️
**Priority:** Medium
**Status:** To Do

**Description:**
Verify ActivityPub key management and actor persistence.

**Scope:**
*   `internal/repository/activitypub_repository_test.go`
*   `internal/repository/federation_repository_test.go`

**Instructions:**
1.  Run tests.
2.  Ensure `private_key` encryption/decryption (if applicable) logic is working.

**Acceptance Criteria:**
*   [ ] All tests in scope pass.
