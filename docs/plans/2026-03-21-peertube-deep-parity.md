# PeerTube Deep Parity Audit Implementation Plan

Created: 2026-03-21
Status: VERIFIED
Approved: Yes
Iterations: 0
Worktree: No
Type: Feature

## Summary

**Goal:** Comprehensive second-pass comparison of PeerTube functionality vs Athena — identify missing capabilities, incomplete user stories, E2E gaps, and test coverage holes across the full feature surface.

**Architecture:** Additive endpoint and test layer on top of existing Chi + SQLX + domain architecture. No schema changes. New handlers follow established handler patterns in `internal/httpapi/handlers/`.

**Tech Stack:** Go 1.24, Chi router, PostgreSQL (SQLX), Redis, testify

## Scope

### In Scope

- Missing PeerTube API endpoints (static enum routes, video metadata, account/subscription paths)
- Admin config endpoints (`/config/custom`)
- Test coverage hardening for handlers with < 18 tests: account, backup, channel-media, livestream, payments, plugin

### Out of Scope

- Frontend changes
- Database schema migrations
- ATProto / ActivityPub changes
- Payment business logic changes

## Context for Implementer

- **Patterns to follow:** `internal/httpapi/handlers/video/` for handler shape; `shared.WriteJSON` / `shared.WriteError` for responses
- **Auth:** `middleware.GetUserIDFromContext(r.Context())` for authenticated user
- **URL params:** `chi.URLParam(r, "paramName")`
- **Test pattern:** table-driven tests with httptest.NewRecorder + chi.RouteContext injection for URL params

## Assumptions

- All new endpoints are additive — existing routes are not modified
- Test targets: ≥18 tests for payments and plugin handler packages, ≥30 for livestream combined

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|-----------|
| Duplicate test function names silently shadow earlier test | Medium | Medium | Verify unique names before commit |
| Permanently-skipped stubs give false coverage confidence | Low | Low | Use `testing.Short()` guard or remove stubs |

## Goal Verification

### Truths

1. All PeerTube gap endpoints from the audit are reachable (return non-405) — verified via route registration in app.go
2. Video stats, retention, contact form, oauth-clients, and watch history endpoints exist and return structured responses
3. Account, backup, channel-media, livestream, payments, and plugin handler packages each have ≥18 test functions
4. Full test suite passes (zero failures) after all changes
5. `go build ./...` succeeds with zero errors

### Artifacts

- `internal/httpapi/handlers/account/handlers_test.go` — 20+ test functions
- `internal/httpapi/handlers/backup/backup_handlers_test.go` — 18+ test functions
- `internal/httpapi/handlers/channel/channel_media_test.go` — 30 test functions
- `internal/httpapi/handlers/livestream/` — 30 combined test functions
- `internal/httpapi/handlers/payments/payment_handlers_test.go` — 18 test functions
- `internal/httpapi/handlers/plugin/` — 22+ combined test functions

## Progress Tracking

- [x] Task 1: Add video metadata static enum endpoints
- [x] Task 2: Add accounts list, me/videos, me/comments, me/videos/imports
- [x] Task 3: Add GET and PUT /config/custom admin endpoints
- [x] Task 4: Add watch history PeerTube paths
- [x] Task 5: Add subscription management by handle
- [x] Task 6: Add video ownership change endpoints
- [x] Task 7: Add video source delete and token endpoints
- [x] Task 8: Add video stats overall and retention endpoints
- [x] Task 9: Add contact form, oauth-clients/local, registration delete
- [x] Task 10: Harden account and channel-media handler tests
- [x] Task 11: Harden livestream and backup handler tests
- [x] Task 12: Harden payments and plugin handler tests

**Total Tasks:** 12 | **Completed:** 12 | **Remaining:** 0

## Implementation Tasks

### Task 1: Video Metadata Static Enum Endpoints

**Objective:** Add `/videos/licences`, `/videos/languages`, `/videos/privacies` static enum endpoints.

**Files:**
- Modify: `internal/httpapi/handlers/video/`
- Modify: `internal/httpapi/router.go`

**Definition of Done:**
- [x] All three endpoints return 200 with enum maps
- [x] Routes registered in router

### Task 2: Accounts and Me Endpoints

**Objective:** Add `/accounts`, `/me/videos`, `/me/comments`, `/me/videos/imports` endpoints.

**Files:**
- Modify: `internal/httpapi/handlers/account/`
- Modify: `internal/httpapi/router.go`

**Definition of Done:**
- [x] Endpoints return paginated responses
- [x] Auth required for `/me/*` routes

### Task 3: Admin Config Custom Endpoints

**Objective:** Add GET and PUT `/config/custom` admin endpoints.

**Files:**
- Modify: `internal/httpapi/handlers/admin/`
- Modify: `internal/httpapi/router.go`

**Definition of Done:**
- [x] GET returns current config
- [x] PUT updates and persists config

### Task 4: Watch History Paths

**Objective:** Add `/me/history/videos` GET and DELETE.

**Definition of Done:**
- [x] GET returns paginated history
- [x] DELETE clears history

### Task 5: Subscription Management by Handle

**Objective:** Add GET/POST/DELETE `/me/subscriptions/{handle}`.

**Definition of Done:**
- [x] All three methods work with username@host handle format

### Task 6: Video Ownership Change

**Objective:** Add give-ownership, accept, refuse endpoints.

**Definition of Done:**
- [x] Ownership transfer workflow endpoints all return correct status codes

### Task 7: Video Source and Token Endpoints

**Objective:** Add DELETE `/videos/{id}/source` and POST `/videos/{id}/token`.

**Definition of Done:**
- [x] Source delete removes original file
- [x] Token endpoint returns signed access token

### Task 8: Video Stats Endpoints

**Objective:** Add overall stats and retention analytics endpoints.

**Files:**
- Modify: `internal/httpapi/handlers/video/`

**Definition of Done:**
- [x] GET `/videos/{id}/stats/overall` returns view/like counts
- [x] GET `/videos/{id}/stats/retention` returns retention curve data

### Task 9: Contact Form, OAuth Clients, Registration Delete

**Objective:** Add `/contact` form submission, `/oauth-clients/local`, `/registrations/:id` DELETE.

**Definition of Done:**
- [x] Contact form endpoint validates required fields
- [x] OAuth clients/local returns client_id + client_secret
- [x] Registration delete removes pending registration

### Task 10: Test Hardening — Account and Channel-Media

**Objective:** Bring account and channel-media handler test counts to ≥18 each.

**Files:**
- Modify: `internal/httpapi/handlers/account/handlers_test.go`
- Modify: `internal/httpapi/handlers/channel/channel_media_test.go`

**Definition of Done:**
- [x] Account handlers: ≥18 test functions (reached 20)
- [x] Channel-media handlers: ≥18 test functions (reached 30)

### Task 11: Test Hardening — Livestream and Backup

**Objective:** Bring livestream combined and backup handler test counts to ≥30 and ≥18.

**Files:**
- Modify: `internal/httpapi/handlers/livestream/livestream_handlers_test.go`
- Modify: `internal/httpapi/handlers/livestream/session_history_test.go`
- Modify: `internal/httpapi/handlers/backup/backup_handlers_test.go`

**Definition of Done:**
- [x] Livestream combined: 30 test functions
- [x] Backup: 18 test functions

### Task 12: Test Hardening — Payments and Plugin

**Objective:** Bring payments to ≥18 tests and plugin combined to ≥22 tests.

**Files:**
- Modify: `internal/httpapi/handlers/payments/payment_handlers_test.go`
- Modify: `internal/httpapi/handlers/plugin/install_test.go`
- Modify: `internal/httpapi/handlers/plugin/plugin_handlers_unit_test.go`

**Definition of Done:**
- [x] Payments: 18 test functions
- [x] Plugin combined: 22 test functions
