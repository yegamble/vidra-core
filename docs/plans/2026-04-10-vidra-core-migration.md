# Vidra Core Production Migration Implementation Plan

Created: 2026-04-10
Author: yegamble@gmail.com
Status: VERIFIED
Approved: Yes
Iterations: 1
Worktree: Yes
Type: Feature

## Summary

**Goal:** Build the migration infrastructure needed to safely move a 1M-user PeerTube instance to Vidra Core: shared JWT auth for split-brain cutover, ETL resume capability, reverse ETL for rollback, and a DB sanitization script for staging.

**Architecture:** Extends the existing Migration ETL service with resume/checkpoint support and a new reverse ETL module. Adds dual-secret JWT validation to the auth middleware with PeerTube-to-Vidra user ID mapping. All changes are additive; no existing behavior is modified.

**Tech Stack:** Go 1.24, PostgreSQL (SQLX), golang-jwt/jwt/v5, Goose migrations

## Scope

### In Scope

1. Dual-secret JWT auth middleware (accept PeerTube-issued JWTs during cutover)
2. PeerTube-to-Vidra user ID mapping table and lookup
3. Migration ETL resume capability (checkpoint per entity type)
4. Reverse ETL service (Vidra Core -> PeerTube schema sync for rollback)
5. DB sanitization SQL script for staging environment
6. Goose migration for new tables (user_id_mapping, migration_checkpoints)

### Out of Scope

- Payment system refactor (BTCPay Server) — deferred to post-migration Phase 2
- Cloudflare Worker routing scripts — operational/infrastructure, not Go code
- Staging VPS provisioning — infrastructure task
- Frontend (vidra-user) changes — separate project
- Media file migration — handled via rsync/manual transfer, not ETL

## Approach

**Chosen:** Additive changes to existing services
**Why:** All four components extend existing code (auth middleware, migration ETL). No new services or packages needed except one new file for reverse ETL. Minimal blast radius.
**Alternatives considered:** New microservice for migration orchestration (rejected: over-engineering for a one-time operation); token translation proxy for JWT (rejected: dual-secret with ID lookup is simpler and the idMap already exists from the forward ETL).

## Context for Implementer

> Write for an implementer who has never seen the codebase.

- **Patterns to follow:**
  - Auth middleware: `internal/middleware/auth.go:129` — `validateJWT()` is the single validation point. All auth functions (`Auth`, `AuthWithUserLookup`, `OptionalAuth`) call it.
  - Migration ETL pipeline: `internal/usecase/migration_etl/service.go:248` — `runPipeline()` calls extract phases sequentially: users → channels → videos → comments → playlists → captions → media → validate.
  - ID mapping: `internal/usecase/migration_etl/service.go:19` — `idMap` struct tracks PeerTube integer IDs → Vidra Core UUIDs in memory. Currently ephemeral (lost when pipeline completes).
  - Migration domain model: `internal/domain/migration.go` — `MigrationJob`, `MigrationStats`, `EntityStats` types.
  - Goose migrations: `migrations/` directory, sequential numbering, `-- +goose Up` / `-- +goose Down` format.

- **Conventions:**
  - Context as first parameter everywhere
  - Error wrapping: `fmt.Errorf("operation: %w", err)`
  - Table-driven tests with testify
  - Repository interfaces in `internal/port/`
  - `testing.Short()` guard for integration tests

- **Key files:**
  - `internal/middleware/auth.go` — JWT validation and auth middleware
  - `internal/middleware/auth_test.go` — auth middleware tests
  - `internal/usecase/migration_etl/service.go` — ETL pipeline orchestration (942 lines)
  - `internal/usecase/migration_etl/mapper.go` — PeerTube → Vidra Core type mapping
  - `internal/domain/migration.go` — Migration domain types
  - `internal/port/migration.go` — MigrationJobRepository interface

- **Gotchas:**
  - PeerTube uses integer user IDs; Vidra Core uses UUID strings. The mapping must persist in DB, not just in-memory idMap.
  - PeerTube JWTs may use different claim names (check actual PeerTube token structure).
  - The ETL `runPipeline()` runs in a background goroutine with `context.Background()`. Resume must handle job restart from a fresh goroutine.
  - The `idMap` is currently ephemeral. For resume + reverse ETL, mappings must be persisted to a new DB table.

- **Domain context:**
  - This migration moves a live PeerTube instance (1M users/month) to Vidra Core.
  - During progressive cutover, BOTH backends serve traffic simultaneously.
  - PeerTube issues JWTs; Vidra Core must accept them and map the user IDs.
  - Reverse ETL is the rollback safety net: if cutover fails after write paths switch, new Vidra Core data must sync back to PeerTube.

## Assumptions

- PeerTube JWTs use HMAC-SHA256 with `sub` claim containing the integer user ID — supported by PeerTube source code convention — Tasks 1, 2 depend on this
- The in-memory `idMap` in the ETL accurately represents all migrated entities — supported by `service.go:19-34` — Tasks 3, 4, 5 depend on this
- The forward ETL completes successfully before reverse ETL is needed — supported by design doc sequencing — Task 5 depends on this
- PeerTube's DB schema for users, videos, channels, comments is compatible with the existing ETL mapper — supported by `mapper.go` and integration tests — Tasks 4, 5 depend on this

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| PeerTube JWT claim structure differs from assumption | Medium | High | Task 1 includes a verification step: parse a real PeerTube token in tests |
| Reverse ETL misses Vidra-only data types (payments, ATProto) | Low | Medium | Reverse ETL only syncs core entities (users, videos, comments). Vidra-only data acknowledged as lossy on rollback — documented in design doc |
| Resume skips entities that were partially migrated (some users in a batch succeeded, some failed) | Medium | Medium | Resume checks per-entity-type completion via stats, not per-row. Partially completed types are re-run (idempotent via UPSERT) |
| ID mapping table grows large for 1M+ users | Low | Low | Indexed on both PeerTube ID and Vidra Core ID. Lookup is O(log n) |

## Goal Verification

### Truths

1. A PeerTube-issued JWT (HMAC-SHA256 with integer user ID) is accepted by Vidra Core's auth middleware and resolves to the correct Vidra Core UUID user
2. A failed migration job can be resumed from its last completed entity type without re-processing already-migrated entities
3. Data written to Vidra Core during cutover can be synced back to PeerTube's schema via the reverse ETL
4. A sanitized copy of a PeerTube database (PII stripped) can be created via the provided SQL script
5. The idMap is persisted to the database and survives across process restarts

### Artifacts

1. `internal/middleware/auth.go` — dual-secret validation with PeerTube JWT support
2. `internal/usecase/migration_etl/service.go` — resume logic in `runPipeline()`
3. `internal/usecase/migration_etl/reverse_etl.go` — reverse ETL service
4. `migrations/NNN_add_migration_id_mapping.sql` — ID mapping and checkpoint tables
5. `scripts/sanitize-peertube-db.sql` — DB sanitization script

## Progress Tracking

- [x] Task 1: ID Mapping Table & Migration
- [x] Task 2: Dual-Secret JWT Auth Middleware
- [x] Task 3: Persist ID Mappings During ETL
- [x] Task 4: ETL Resume Capability
- [x] Task 5: Reverse ETL Service
- [x] Task 6: DB Sanitization Script

**Total Tasks:** 6 | **Completed:** 6 | **Remaining:** 0

## Implementation Tasks

### Task 1: ID Mapping Table & Goose Migration

**Objective:** Create a persistent table for PeerTube integer ID → Vidra Core UUID mappings and a checkpoint table for ETL resume.
**Dependencies:** None
**Mapped Scenarios:** None

**Files:**

- Create: `migrations/NNN_add_migration_id_mapping.sql`
- Modify: `internal/domain/migration.go` (add IDMapping and Checkpoint types)
- Modify: `internal/port/migration.go` (add IDMappingRepository interface)
- Create: `internal/repository/migration_id_mapping_repository.go`
- Test: `internal/repository/migration_id_mapping_repository_test.go`

**Key Decisions / Notes:**

- Two new tables: `migration_id_mappings` (entity_type TEXT, peertube_id INTEGER, vidra_id TEXT, job_id UUID) and `migration_checkpoints` (job_id UUID, entity_type TEXT, completed_at TIMESTAMPTZ)
- `vidra_id` is TEXT (not UUID) because the existing idMap uses mixed types: users/videos are `map[int]string`, channels/comments are `map[int]uuid.UUID`. TEXT accommodates both via `.String()` conversion.
- `migration_id_mappings` indexed on (entity_type, peertube_id) for forward lookup and (entity_type, vidra_id) for reverse lookup
- entity_type values: 'user', 'channel', 'video', 'comment', 'playlist', 'caption'
- Follow existing repository pattern: `internal/repository/iota_repository.go` for SQLX conventions

**Definition of Done:**

- [ ] All tests pass
- [ ] No diagnostics errors
- [ ] `make migrate-up` and `make migrate-down` both succeed (reversible migration)
- [ ] Repository can UPSERT mappings (idempotent for resume scenarios)
- [ ] Repository can look up Vidra UUID by PeerTube ID and vice versa

**Verify:**

- `go test ./internal/repository/ -run TestMigrationIDMapping -short -count=1`

---

### Task 2: Dual-Secret JWT Auth Middleware

**Objective:** Modify the auth middleware to accept both Vidra Core and PeerTube JWTs. PeerTube tokens have integer user IDs that need mapping to Vidra Core UUIDs.
**Dependencies:** Task 1 (needs IDMappingRepository for user ID lookup)
**Mapped Scenarios:** None

**Files:**

- Modify: `internal/middleware/auth.go` (add new `DualAuth` / `DualAuthWithUserLookup` / `DualOptionalAuth` wrapper functions)
- Modify: `internal/middleware/auth_test.go` (add tests for PeerTube JWT validation)
- Modify: `internal/config/config.go` (add `PeerTubeJWTSecret` field)
- Modify: `internal/httpapi/routes.go` (use DualAuth functions where PeerTube tokens are expected during cutover)

**Key Decisions / Notes:**

- **Zero blast radius on existing call sites:** Create NEW `DualAuth(vidraSecret, ptSecret string, idLookup IDMappingLookup)` functions that wrap the existing `Auth`/`AuthWithUserLookup`/`OptionalAuth`. Existing ~50 call sites in routes.go remain unchanged. Only endpoints served during cutover (Phase 1/2 read endpoints) use the new DualAuth variants.
- `IDMappingLookup` is a function type `func(ctx context.Context, entityType string, peertubeID int) (string, error)` — injected via closure, avoids adding repository dependency to middleware package.
- `validateJWT` is extended internally to handle PeerTube `sub` claims: try `string` assertion first, then `float64` assertion with `strconv.FormatInt` conversion (Go JSON unmarshals integers as float64 in `MapClaims`).
- When a PeerTube JWT validates (integer `sub` claim), call the `IDMappingLookup` function to get the Vidra Core UUID. If no mapping exists, return 401 (user wasn't migrated).
- Config: add `PeerTubeJWTSecret` field to config struct, loaded from `PEERTUBE_JWT_SECRET` env var. Empty = disabled (no PeerTube token acceptance). Validated to be non-empty only when explicitly configured.
- This is a temporary feature. After full cutover, the PeerTube secret env var is removed.

**Definition of Done:**

- [ ] All tests pass (existing + new)
- [ ] No diagnostics errors
- [ ] Vidra Core JWT (UUID sub) validates and returns correct user ID
- [ ] PeerTube JWT (integer sub) validates and maps to Vidra Core UUID via ID mapping table
- [ ] PeerTube JWT with unmapped user ID returns 401
- [ ] Invalid/expired tokens still rejected correctly
- [ ] When peertubeSecret is empty, only Vidra Core tokens accepted (no behavior change)

**Verify:**

- `go test ./internal/middleware/ -run TestAuth -count=1`

---

### Task 3: Persist ID Mappings During Forward ETL

**Objective:** Modify the forward ETL pipeline to persist PeerTube-to-Vidra ID mappings to the database as entities are migrated. Currently the `idMap` is ephemeral (in-memory only, lost when pipeline completes).
**Dependencies:** Task 1 (needs IDMappingRepository and migration tables)
**Mapped Scenarios:** None

**Files:**

- Modify: `internal/usecase/migration_etl/service.go` (inject IDMappingRepository, persist mappings in each extract phase)
- Modify: `internal/usecase/migration_etl/service_test.go` (add mock for IDMappingRepository)
- Modify: `internal/app/app.go` (wire IDMappingRepository into ETLService constructor)

**Key Decisions / Notes:**

- Add `idMappingRepo port.IDMappingRepository` to `ETLService` constructor
- After each entity is successfully migrated (e.g., after `s.userRepo.Create(ctx, user, placeholderHash)` at line 409), also call `s.idMappingRepo.Upsert(ctx, mapping)` to persist the mapping
- UPSERT (not INSERT) to handle resume scenarios where some mappings already exist
- Keep the in-memory `idMap` for pipeline-internal cross-entity resolution (channels need user mappings, videos need channel mappings). The DB persistence is for cross-process use (resume, reverse ETL, JWT auth).
- Follow pattern at `service.go:396-417` (extractUsers loop)

**Definition of Done:**

- [ ] All tests pass (existing + new)
- [ ] No diagnostics errors
- [ ] After ETL completes, all migrated entity IDs are persisted in `migration_id_mappings` table
- [ ] Mappings include correct entity_type, peertube_id, vidra_id, and job_id
- [ ] Existing ETL behavior unchanged (in-memory idMap still used for cross-entity resolution)

**Verify:**

- `go test ./internal/usecase/migration_etl/ -run TestStartMigration -count=1`

---

### Task 4: ETL Resume Capability

**Objective:** Add checkpoint support to the ETL pipeline so a failed/interrupted migration can resume from the last completed entity type.
**Dependencies:** Task 1 (checkpoint table), Task 3 (persisted ID mappings)
**Mapped Scenarios:** None

**Files:**

- Modify: `internal/usecase/migration_etl/service.go` (add `ResumeMigration` method, modify `runPipeline` to check checkpoints)
- Modify: `internal/domain/migration.go` (add `MigrationStatusResuming` status)
- Modify: `internal/port/migration.go` (add checkpoint methods to repository interface)
- Modify: `internal/repository/migration_id_mapping_repository.go` (add checkpoint CRUD)
- Modify: `internal/httpapi/handlers/migration/handlers.go` (add resume endpoint)
- Test: `internal/usecase/migration_etl/service_test.go` (resume test cases)

**Key Decisions / Notes:**

- New public method: `ResumeMigration(ctx, jobID) (*MigrationJob, error)` — loads the job, checks it's in `failed` status, loads checkpoints, restarts pipeline from first incomplete entity type
- **CanTransition fix:** `MigrationStatus.IsTerminal()` currently returns true for `failed`, blocking all transitions from `failed`. Must either: (a) add `failed` → `resuming` to the `validTransitions` map in `CanTransition`, or (b) modify `IsTerminal` to not treat `failed` as terminal. Choose (a) to minimize blast radius.
- Checkpoint written after each entity type completes (e.g., after all users extracted, write checkpoint "users"). Follow order: users → channels → videos → comments → playlists → captions
- On resume: load checkpoints, skip completed types, reload idMap via `rebuildIDMap(ctx, jobID)` method
- **`rebuildIDMap(ctx, jobID)`:** Queries all mappings for the job_id from `migration_id_mappings`, groups by entity_type, populates users/videos maps with string values, channels/comments maps by parsing UUID from string via `uuid.Parse()`. This restores cross-entity resolution state.
- **Panic recovery:** Add `defer func() { if r := recover()... }` to `runPipeline` goroutine (currently missing per go-concurrency guardrails). Since we're already modifying this function, fix it now.
- New API endpoint: `POST /api/v1/admin/migrations/{id}/resume`
- New status: `MigrationStatusResuming = "resuming"` — transitions from `failed` to `resuming` to `running`
- **OpenAPI + Postman:** Update `api/openapi*.yaml` for the resume endpoint. Add Postman collection entry in `postman/`.
- **Feature registry:** Update `.claude/rules/feature-parity-registry.md` with ETL resume feature.

**Definition of Done:**

- [ ] All tests pass
- [ ] No diagnostics errors
- [ ] A migration that failed after users+channels can resume and complete videos+comments+playlists+captions
- [ ] Resumed migration produces correct final stats (sum of original + resumed counts)
- [ ] In-memory idMap correctly rebuilt from DB on resume
- [ ] Resume of a completed or cancelled job returns appropriate error
- [ ] API endpoint returns the resumed job with updated status

**Verify:**

- `go test ./internal/usecase/migration_etl/ -run TestResumeMigration -count=1`

---

### Task 5: Reverse ETL Service

**Objective:** Build a reverse ETL that maps Vidra Core UUIDs back to PeerTube integer IDs and writes new Vidra Core data back to PeerTube's schema. This enables rollback after the write path is switched.
**Dependencies:** Task 1 (ID mapping table), Task 3 (persisted mappings)
**Mapped Scenarios:** None

**Files:**

- Create: `internal/usecase/migration_etl/reverse_etl.go`
- Create: `internal/usecase/migration_etl/reverse_etl_test.go`
- Modify: `internal/port/migration.go` (add ReverseETLService interface)
- Modify: `internal/httpapi/handlers/migration/handlers.go` (add reverse sync endpoint)

**Key Decisions / Notes:**

- Reverse ETL handles core entities only: users, channels, videos, comments. Vidra-only data (payments, ATProto, IPFS metadata) is acknowledged as lossy on rollback.
- Uses the `migration_id_mappings` table to convert Vidra Core UUIDs back to PeerTube integer IDs
- Connects to the PeerTube source DB (same connection config as forward ETL) and performs INSERT/UPDATE operations. **Note:** PeerTube DB credentials must have WRITE access (forward ETL only needs read). Document this requirement.
- For entities created in Vidra Core post-migration (no PeerTube ID): INSERT without specifying the integer ID column, letting PostgreSQL auto-increment assign the value. Use `RETURNING id` to capture the generated PeerTube ID and store the reverse mapping in `migration_id_mappings`.
- Follow the reverse mapper pattern: `mapper.go` but in reverse direction
- API: `POST /api/v1/admin/migrations/{id}/reverse-sync` — takes a job ID, syncs all Vidra Core data created after the migration started
- Filter: only sync entities with `created_at > migration.started_at` (new data since cutover)
- **OpenAPI + Postman:** Update `api/openapi*.yaml` for the reverse-sync endpoint. Add Postman collection entry.
- **Feature registry:** Update `.claude/rules/feature-parity-registry.md` with reverse ETL feature.

**Definition of Done:**

- [ ] All tests pass
- [ ] No diagnostics errors
- [ ] Users created in Vidra Core after migration appear in PeerTube DB with correct integer IDs
- [ ] Videos created in Vidra Core after migration appear in PeerTube DB with correct foreign keys
- [ ] Comments with thread relationships are preserved in reverse direction
- [ ] Entities that existed before migration (have PeerTube IDs) are updated, not duplicated
- [ ] New entities (no PeerTube ID) get fresh integer IDs from PeerTube sequences

**Verify:**

- `go test ./internal/usecase/migration_etl/ -run TestReverseETL -count=1`

---

### Task 6: DB Sanitization SQL Script

**Objective:** Create a SQL script that sanitizes a PeerTube production database dump for use in staging. Strips PII while preserving data structure for ETL testing.
**Dependencies:** None
**Mapped Scenarios:** None

**Files:**

- Create: `scripts/sanitize-peertube-db.sql`

**Key Decisions / Notes:**

- Operates on a PeerTube-schema PostgreSQL database (not Vidra Core schema)
- Sanitizes: emails → synthetic (`user_{row_number}@sanitized.local`, prevents rainbow-table de-anonymization), passwords → removed (set to placeholder), OAuth tokens → deleted, 2FA secrets → deleted, notification preferences → reset to defaults
- Preserves: usernames, video metadata, channel info, comments, playlists, timestamps, user roles, blocked status
- Script should be idempotent (safe to run multiple times)
- Usage: `pg_dump peertube_prod | psql staging_db && psql staging_db -f scripts/sanitize-peertube-db.sql`

**Definition of Done:**

- [ ] Script runs without errors on a PeerTube-schema database
- [ ] No real email addresses remain after sanitization
- [ ] No password hashes remain
- [ ] No OAuth/2FA secrets remain
- [ ] Data structure preserved (ETL can consume the sanitized DB)
- [ ] Script is idempotent

**Verify:**

- Manual verification against a test PeerTube schema (or integration test with Docker mock)

## Open Questions

1. **PeerTube JWT claim structure:** Need to verify actual claim names and format from a real PeerTube token. Assumption is `sub` contains integer user ID, but PeerTube may use a different claim structure.
2. **PeerTube sequence behavior:** When reverse ETL creates new entities in PeerTube's DB, do the sequences auto-increment correctly, or do we need to manually manage integer IDs?

## Deferred Ideas

- **Incremental reverse sync:** Instead of syncing all new data at once, run reverse ETL on a schedule (e.g., every 5 minutes) during the cutover window. Would reduce data loss window on rollback.
- **Bidirectional conflict resolution:** If both PeerTube and Vidra Core accept writes during Phase 3/4, edits to the same entity could conflict. Current plan avoids this by routing all writes to one backend at a time per endpoint group.
