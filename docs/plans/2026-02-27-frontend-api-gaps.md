# Frontend API Gaps Implementation Plan

Created: 2026-02-27
Status: VERIFIED
Approved: Yes
Iterations: 0
Worktree: No
Type: Feature

> **Status Lifecycle:** PENDING → COMPLETE → VERIFIED
> **Iterations:** Tracks implement→verify cycles (incremented by verify phase)
>
> - PENDING: Initial state, awaiting implementation
> - COMPLETE: All tasks implemented
> - VERIFIED: All checks passed
>
> **Approval Gate:** Implementation CANNOT proceed until `Approved: Yes`
> **Worktree:** Set at plan creation (from dispatcher). `No` works directly on current branch.
> **Type:** Feature

## Summary

**Goal:** Add missing backend API endpoints required by the Iris frontend, including public user profiles, admin user/video management, view history route wiring, payment response envelope normalization, and notification preferences.

**Architecture:** All new endpoints follow Athena's existing handler patterns — closure-style handlers in `internal/httpapi/handlers/`, using `shared.WriteJSON`/`shared.WriteError` for responses, `middleware.Auth` for JWT validation, and `middleware.RequireRole` for admin access control. Repository layer methods already exist for most operations (`UserRepo.GetByID`, `List`, `Count`, `Update`; `VideoRepo.List`). New domain models needed only for notification preferences.

**Tech Stack:** Go (Chi router), PostgreSQL (SQLX), existing repository layer

## Scope

### In Scope

- `GET /api/v1/users/{id}` — Public user profile (strips sensitive fields)
- `GET /api/v1/admin/users` — Admin user listing with search, pagination
- `PUT /api/v1/admin/users/{id}` — Admin user management (role change, ban/unban)
- `GET /api/v1/admin/videos` — Admin video listing with search, pagination
- Wire `GetViewHistory` handler to `GET /api/v1/views/history` route
- Normalize payment handler response envelope to use `shared.WriteJSON`/`shared.WriteError`
- `GET/PUT /api/v1/users/me/notification-preferences` — Notification preference storage

### Out of Scope

- Frontend changes (those are in the Iris plan)
- New business logic beyond CRUD operations
- WebSocket/real-time notification delivery
- Admin video editing (admin can only list/view)

## Prerequisites

- None — all repository interfaces and domain models needed already exist (except notification preferences)

## Runtime Environment

- **Start command:** `make run` or `go run ./cmd/server/...`
- **Port:** `http://localhost:8080`
- **Infrastructure:** `docker compose up postgres redis` + `make migrate-up`
- **Health check:** `curl http://localhost:8080/health`
- **Minimum env vars:** Copy `.env.example` → `.env` (DB_URL, REDIS_URL, JWT_SECRET)

## Context for Implementer

> This section is critical for cross-session continuity. Write it for an implementer who has never seen the codebase.

- **Patterns to follow:**
  - Handler closure pattern: `func GetUserHandler(repo usecase.UserRepository) http.HandlerFunc` — see `internal/httpapi/handlers/auth/users.go:19`
  - Admin handler struct pattern: `type InstanceHandlers struct` with methods — see `internal/httpapi/handlers/admin/instance.go:22`
  - Response envelope: `shared.WriteJSON(w, http.StatusOK, data)` for success, `shared.WriteError(w, status, err)` for errors — see `internal/httpapi/shared/response.go:32`
  - Pagination: `shared.WriteJSONWithMeta(w, http.StatusOK, data, &shared.Meta{Total: count, Limit: limit, Offset: offset})` — see `internal/httpapi/shared/response.go:71`
  - Admin middleware: `middleware.Auth(cfg.JWTSecret)` + `middleware.RequireRole("admin")` — see `routes.go:438-439`
  - Route registration: all routes in `internal/httpapi/routes.go` function `RegisterRoutesWithDependencies`
- **Conventions:**
  - Context first: `ctx := r.Context()`
  - Error wrapping: `fmt.Errorf("operation: %w", err)`
  - User ID from context: `middleware.UserIDKey` — see `internal/middleware/auth.go`
  - URL params: `chi.URLParam(r, "id")`
  - Query params: `r.URL.Query().Get("param")`
  - Parse int params: `strconv.Atoi(r.URL.Query().Get("limit"))` with sensible defaults
- **Key files:**
  - `internal/httpapi/routes.go` — All route registrations
  - `internal/httpapi/shared/response.go` — Response envelope helpers
  - `internal/port/user.go` — UserRepository interface (GetByID, List, Count, Update, Delete)
  - `internal/port/video.go` — VideoRepository interface (List with VideoSearchRequest)
  - `internal/domain/user.go` — User struct, UserRole, MarshalJSON
  - `internal/domain/notification.go` — Notification types
  - `internal/httpapi/handlers/auth/users.go` — Existing user handlers (GetCurrentUserHandler pattern)
  - `internal/httpapi/handlers/admin/instance.go` — Admin handler pattern (InstanceHandlers struct)
  - `internal/httpapi/handlers/payments/payment_handlers.go` — Payment handlers (to be normalized)
  - `internal/httpapi/handlers/video/views_handlers.go:315` — GetViewHistory (unwired handler)
- **Gotchas:**
  - `domain.User.MarshalJSON()` already strips `TwoFASecret` and `TwoFAConfirmedAt` via `json:"-"` tags, but still exposes `Email`, `EmailVerified`, `BitcoinWallet` — public profile must use a separate response type
  - Payment handlers have their own `successResponse`/`errorResponse` methods that produce `{success, data}` / `{success, error}` instead of the standard envelope — these must be replaced with `shared.WriteJSON`/`shared.WriteError`
  - `GetViewHistory` at `views_handlers.go:315` has full implementation but no route registration
  - Admin routes use double middleware: `middleware.Auth` AND `middleware.RequireRole` — see `routes.go:438-439`

## Progress Tracking

**MANDATORY: Update this checklist as tasks complete. Change `[ ]` to `[x]`.**

- [x] Task 1: Public user profile endpoint
- [x] Task 2: Admin list users endpoint
- [x] Task 3: Admin user management endpoint
- [x] Task 4: Admin video listing endpoint
- [x] Task 5: Wire view history route
- [x] Task 6: Payment response envelope normalization
- [x] Task 7: Notification preferences endpoints
- [x] Task 8: OpenAPI spec updates
- [x] Task 9: Postman E2E test collection

**Total Tasks:** 9 | **Completed:** 9 | **Remaining:** 0

## Implementation Tasks

### Task 1: Public User Profile Endpoint

**Objective:** Add `GET /api/v1/users/{id}` that returns a public-safe subset of user data. This is the critical missing endpoint for Iris Task 5 (user profile page).

**Dependencies:** None

**Files:**

- Create: `internal/httpapi/handlers/auth/public_user.go`
- Create: `internal/httpapi/handlers/auth/public_user_test.go`
- Modify: `internal/httpapi/routes.go` — Add route registration

**Key Decisions / Notes:**

- Create a `PublicUser` response struct that omits `Email`, `EmailVerified`, `EmailVerifiedAt`, `BitcoinWallet`, `TwoFAEnabled`, `IsActive` — only expose: `ID`, `Username`, `DisplayName`, `Bio`, `Avatar`, `Role`, `SubscriberCount`, `CreatedAt`
- **WARNING:** `GetUserHandler` already exists at `users.go:97` but returns raw `domain.User` with sensitive fields. Do NOT reuse it. Create a new `GetPublicUserHandler` that maps to `PublicUser` response type.
- Follow the closure pattern from `GetCurrentUserHandler` at `internal/httpapi/handlers/auth/users.go:19`
- Use `middleware.OptionalAuth` so both authenticated and unauthenticated users can view profiles
- Route: `r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{id}", auth.GetPublicUserHandler(deps.UserRepo))` inside the `/users` route group
- Repository method `GetByID` already exists on `UserRepository` interface

**Definition of Done:**

- [ ] `GET /api/v1/users/{id}` returns public user data with 200 status
- [ ] Response omits sensitive fields (email, wallet, 2FA, active status)
- [ ] Returns 404 for non-existent user IDs
- [ ] Unit tests cover: valid user, not found, invalid ID format
- [ ] No diagnostics errors

**Verify:**

- `go test ./internal/httpapi/handlers/auth/... -run TestGetPublicUser -v`

### Task 2: Admin List Users Endpoint

**Objective:** Add `GET /api/v1/admin/users` that returns paginated user list with search. Required for Iris Task 11 admin user management table.

**Dependencies:** None

**Files:**

- Create: `internal/httpapi/handlers/admin/user_handlers.go`
- Create: `internal/httpapi/handlers/admin/user_handlers_test.go`
- Modify: `internal/httpapi/routes.go` — Add route registration in admin group

**Key Decisions / Notes:**

- Create `AdminUserHandlers` struct with `userRepo usecase.UserRepository` dependency
- Support query params: `?limit=20&offset=0&search=term`
- `UserRepo.List(ctx, limit, offset)` has NO search parameter. Use in-memory filter: call `List` with high limit, filter by username/email substring match, then slice for pagination. Set `Meta.Total` to the filtered count (not `UserRepo.Count()`) so pagination is correct.
- Use `shared.WriteJSONWithMeta` for paginated response with `Meta{Total, Limit, Offset}`
- Route: inside `/admin` group (already has `Auth` + `RequireRole("admin", "moderator")` middleware)
- Follow admin handler pattern from `internal/httpapi/handlers/admin/instance.go`
- Constructor: `NewAdminUserHandlers(userRepo usecase.UserRepository)`

**Definition of Done:**

- [ ] `GET /api/v1/admin/users` returns paginated user list with meta
- [ ] Supports `limit`, `offset` query params with sensible defaults (limit=20, offset=0)
- [ ] `?search=term` filters results by username or email (in-memory filter over full list for MVP)
- [ ] Admin/mod role required (enforced by existing middleware)
- [ ] Unit tests cover: list users, pagination, search filter, empty result
- [ ] No diagnostics errors

**Verify:**

- `go test ./internal/httpapi/handlers/admin/... -run TestAdminListUsers -v`

### Task 3: Admin User Management Endpoint

**Objective:** Add `PUT /api/v1/admin/users/{id}` for admin to change user role and ban/unban users. Required for Iris Task 11 DoD: "change role, ban/unban".

**Dependencies:** Task 2 (uses same handler struct)

**Files:**

- Modify: `internal/httpapi/handlers/admin/user_handlers.go` — Add UpdateUser method
- Modify: `internal/httpapi/handlers/admin/user_handlers_test.go` — Add tests
- Modify: `internal/httpapi/routes.go` — Add route

**Key Decisions / Notes:**

- Request body: `{ "role": "admin"|"moderator"|"user", "is_active": true|false }` — both fields optional (partial update)
- Validate role is one of the three valid `domain.UserRole` values
- Prevent admin from demoting themselves (check `userID != targetID` when changing role)
- Also prevent last-admin scenario: before demoting any admin, count remaining admins; reject if count would reach zero
- Use `UserRepo.GetByID` to fetch, modify fields, then `UserRepo.Update` to save
- Ban = set `IsActive = false`, Unban = set `IsActive = true`
- Route: `r.Put("/users/{id}", adminUserHandlers.UpdateUser)` inside `/admin` group

**Definition of Done:**

- [ ] `PUT /api/v1/admin/users/{id}` updates user role and/or active status
- [ ] Validates role is valid enum value
- [ ] Prevents self-demotion (admin can't change own role)
- [ ] Returns 404 for non-existent users
- [ ] Unit tests cover: change role, ban, unban, self-demotion prevention, invalid role, not found
- [ ] No diagnostics errors

**Verify:**

- `go test ./internal/httpapi/handlers/admin/... -run TestAdminUpdateUser -v`

### Task 4: Admin Video Listing Endpoint

**Objective:** Add `GET /api/v1/admin/videos` for admin video management table. Required by Iris Task 11.

**Dependencies:** None

**Files:**

- Create: `internal/httpapi/handlers/admin/video_handlers.go`
- Create: `internal/httpapi/handlers/admin/video_handlers_test.go`
- Modify: `internal/httpapi/routes.go` — Add route in admin group

**Key Decisions / Notes:**

- Create `AdminVideoHandlers` struct with `videoRepo usecase.VideoRepository` dependency
- Support query params: `?limit=20&offset=0&search=term&sort=created_at`
- Use `VideoRepo.List(ctx, &domain.VideoSearchRequest{...})` — already supports search and pagination
- Use `shared.WriteJSONWithMeta` for paginated response
- Route: `r.Get("/videos", adminVideoHandlers.ListVideos)` inside `/admin` group
- Constructor: `NewAdminVideoHandlers(videoRepo usecase.VideoRepository)`

**Definition of Done:**

- [ ] `GET /api/v1/admin/videos` returns paginated video list with meta
- [ ] Supports `limit`, `offset`, `search` query params
- [ ] Admin/mod role required (existing middleware)
- [ ] Unit tests cover: list videos, pagination, search filter, empty result
- [ ] No diagnostics errors

**Verify:**

- `go test ./internal/httpapi/handlers/admin/... -run TestAdminListVideos -v`

### Task 5: Wire View History Route

**Objective:** Register the existing `GetViewHistory` handler to `GET /api/v1/views/history`. The handler at `internal/httpapi/handlers/video/views_handlers.go:315` is fully implemented but has no route.

**Dependencies:** None

**Files:**

- Modify: `internal/httpapi/routes.go` — Add one route registration

**Key Decisions / Notes:**

- The `viewsHandler` variable is already created at the beginning of the v1 route group
- Add: `r.With(middleware.Auth(cfg.JWTSecret)).Get("/views/history", viewsHandler.GetViewHistory)` at an appropriate position in the route group (near the other views-related routes like `/views/fingerprint` at line 290)
- Handler already handles auth, authorization, filtering, and pagination internally
- **Privacy note:** When called without `user_id` query param, handler may return all users' history. Verify the handler defaults to the authenticated user's history when no `user_id` is provided, or add that guard.
- No new handler code needed — just one line in routes.go (plus privacy check if needed)

**Definition of Done:**

- [ ] `GET /api/v1/views/history` is accessible and returns view history
- [ ] Existing `GetViewHistory` handler tests still pass
- [ ] Route is protected by auth middleware
- [ ] No diagnostics errors

**Verify:**

- `go test ./internal/httpapi/handlers/video/... -run TestViewHistory -v`
- `go build ./cmd/server/...` — confirms route wiring compiles

### Task 6: Payment Response Envelope Normalization

**Objective:** Replace payment handler's custom `successResponse`/`errorResponse` methods with `shared.WriteJSON`/`shared.WriteError` to match the standard response envelope used by all other endpoints.

**Dependencies:** None

**Files:**

- Modify: `internal/httpapi/handlers/payments/payment_handlers.go` — Replace response methods
- Modify: `internal/httpapi/handlers/payments/payment_handlers_test.go` — Update expected response shapes

**Key Decisions / Notes:**

- Current payment responses: `{success: true, data: T}` and `{success: false, error: "message"}`
- Standard responses: `{success: true, data: T}` and `{success: false, error: {code: "...", message: "..."}}`
- Change all `h.successResponse(w, data, status)` calls to `shared.WriteJSON(w, status, data)`
- Change all `h.errorResponse(w, msg, status)` calls to `shared.WriteError(w, status, domain.NewDomainError("CODE", msg))` with appropriate error codes
- Delete the `successResponse` and `errorResponse` helper methods
- Add `"athena/internal/httpapi/shared"` and `"athena/internal/domain"` imports
- **CRITICAL:** Tests at `payment_handlers_test.go` use `resp["error"].(string)` type assertions. After this change, `error` becomes `map[string]interface{}` (ErrorInfo object). These assertions will PANIC (not fail gracefully). Must change to `resp["error"].(map[string]interface{})["message"].(string)` or use a helper.

**Definition of Done:**

- [ ] All payment handlers use `shared.WriteJSON` and `shared.WriteError`
- [ ] Custom `successResponse` and `errorResponse` methods removed
- [ ] Response envelope matches standard format: `{success, data, error: {code, message}}`
- [ ] All existing payment handler tests pass with updated assertions
- [ ] No diagnostics errors

**Verify:**

- `go test ./internal/httpapi/handlers/payments/... -v`

### Task 7: Notification Preferences Endpoints

**Objective:** Add `GET/PUT /api/v1/users/me/notification-preferences` for per-type notification toggle storage. Required by Iris Task 8 (Settings > Notifications tab).

**Dependencies:** None

**Files:**

- Modify: `internal/domain/notification.go` — Add `NotificationPreferences` struct
- Create: `migrations/NNNN_add_notification_preferences.sql` — New table
- Modify: `internal/port/notification.go` — Add preference methods to interface (or create if absent)
- Modify: `internal/repository/notification_repository.go` — Add preference methods
- Create: `internal/httpapi/handlers/auth/notification_preferences.go` — Handlers (in auth package since route is under `/users`)
- Create: `internal/httpapi/handlers/auth/notification_preferences_test.go` — Tests
- Modify: `internal/httpapi/routes.go` — Add routes
- Modify: `internal/httpapi/shared/dependencies.go` — Add NotificationPreferenceRepo field
- Modify: `internal/app/app.go` — Wire NotificationPreferenceRepo into HandlerDependencies

**Key Decisions / Notes:**

- Domain model: `NotificationPreferences` with per-type boolean fields:
  ```go
  type NotificationPreferences struct {
      UserID    uuid.UUID `json:"user_id" db:"user_id"`
      Comment   bool      `json:"comment" db:"comment_enabled"`
      Like      bool      `json:"like" db:"like_enabled"`
      Subscribe bool      `json:"subscribe" db:"subscribe_enabled"`
      Mention   bool      `json:"mention" db:"mention_enabled"`
      Reply     bool      `json:"reply" db:"reply_enabled"`
      Upload    bool      `json:"upload" db:"upload_enabled"`
      System    bool      `json:"system" db:"system_enabled"`
      EmailEnabled bool   `json:"email_enabled" db:"email_enabled"`
  }
  ```
- Migration: `CREATE TABLE notification_preferences (user_id UUID PRIMARY KEY REFERENCES users(id), ...)` with all booleans defaulting to `true`
- GET: Returns current preferences (creates default row if none exists)
- PUT: Upsert preferences
- Routes: inside `/users` group with auth middleware:
  - `r.With(middleware.Auth(cfg.JWTSecret)).Get("/me/notification-preferences", ...)`
  - `r.With(middleware.Auth(cfg.JWTSecret)).Put("/me/notification-preferences", ...)`
- Follow Goose migration pattern — see `migrations/CLAUDE.md`
- Use next available migration number

**Definition of Done:**

- [ ] `GET /api/v1/users/me/notification-preferences` returns current preferences (defaults if none set)
- [ ] `PUT /api/v1/users/me/notification-preferences` upserts preferences
- [ ] Migration creates `notification_preferences` table with proper defaults
- [ ] Unit tests cover: get defaults, update preferences, partial update
- [ ] No diagnostics errors

**Verify:**

- `go test ./internal/httpapi/handlers/messaging/... -run TestNotificationPreference -v`
- `make migrate-dev` — confirms migration applies cleanly to dev database

### Task 8: OpenAPI Spec Updates

**Objective:** Add all new endpoints to the OpenAPI spec files in `api/` so they are documented and can be used for code generation. Run `make generate-openapi` and `make verify-openapi` to ensure no drift.

**Dependencies:** Tasks 1-7

**Files:**

- Modify: `api/openapi.yaml` — Add user profile, admin users, admin videos, view history, notification preferences endpoints
- Modify: `api/openapi_payments.yaml` — Update payment response schemas to match normalized envelope
- Modify: `internal/generated/` — Regenerated (do not edit directly)

**Key Decisions / Notes:**

- Follow existing OpenAPI 3.0 patterns in the spec files
- Each new endpoint needs: path, method, parameters, request body (if any), response schemas, auth requirements
- Payment response schemas must be updated to reflect the new `{success, data, error: {code, message}}` envelope
- Run `make generate-openapi` after spec changes, then `make verify-openapi` to confirm no drift
- See `.claude/rules/openapi-codegen.md` for the workflow

**Definition of Done:**

- [ ] All 7 new endpoints documented in OpenAPI spec
- [ ] Payment response schemas updated to match normalized envelope
- [ ] `make generate-openapi` succeeds
- [ ] `make verify-openapi` passes (no drift)
- [ ] No diagnostics errors

**Verify:**

- `make verify-openapi`

### Task 9: Postman E2E Test Collection

**Objective:** Add Postman collection tests for all new endpoints to maintain E2E testing coverage. Tests should cover happy path, error cases, and authorization requirements.

**Dependencies:** Tasks 1-7

**Files:**

- Modify: `postman/` — Add or update collection files with new endpoint tests

**Key Decisions / Notes:**

- Check existing Postman collections in `postman/` directory for patterns and conventions
- Each new endpoint needs: happy path test, 404/validation error test, auth required test (401 without token)
- Admin endpoints need: forbidden test (403 for non-admin user)
- Payment envelope tests should verify the new standardized response shape
- Include pre-request scripts for auth token setup if following existing patterns

**Definition of Done:**

- [ ] Postman tests cover all 7 new endpoints (happy path + error cases)
- [ ] Admin endpoints tested for role-based access (403 for non-admin)
- [ ] Payment tests verify normalized response envelope
- [ ] Collection runs successfully against local dev server
- [ ] No diagnostics errors

**Verify:**

- `newman run postman/*.json` (or equivalent collection runner)

## Testing Strategy

- Unit tests: Each handler tested in isolation with mock repositories (table-driven tests)
- Integration tests: Not needed — all new endpoints are thin CRUD wrappers over existing repo methods
- Manual verification: Build compiles, routes register correctly

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Public user profile exposes sensitive data | Med | High | Create dedicated `PublicUser` response struct; never return raw `domain.User` |
| Payment test failures after envelope change | Low | Med | Update test assertions to match new ErrorInfo format; run full payment test suite |
| Migration conflicts with other branches | Low | Low | Use next available migration number; check `migrations/` before creating |
| Admin self-demotion leaves no admins | Low | High | Prevent admin from changing own role in UpdateUser handler |
| In-memory user search doesn't scale | Low | Low | Acceptable for MVP; replace with UserRepo.Search when user count exceeds 10k — tracked in Deferred Ideas |

## Goal Verification

> Derived from the plan's goal using goal-backward methodology.

### Truths (what must be TRUE for the goal to be achieved)

- Iris can fetch any user's public profile by ID via `GET /api/v1/users/{id}`
- Admins can list all users with pagination via `GET /api/v1/admin/users`
- Admins can change user roles and ban/unban via `PUT /api/v1/admin/users/{id}`
- Admins can list all videos with search via `GET /api/v1/admin/videos`
- View history is accessible via `GET /api/v1/views/history`
- Payment API responses use the same envelope as all other endpoints
- Users can get and update notification preferences

### Artifacts (what must EXIST to support those truths)

- `internal/httpapi/handlers/auth/public_user.go` — Public user profile handler
- `internal/httpapi/handlers/admin/user_handlers.go` — Admin user list + management handlers
- `internal/httpapi/handlers/admin/video_handlers.go` — Admin video list handler
- `internal/domain/notification.go` — NotificationPreferences struct
- Migration file for `notification_preferences` table
- Route registrations in `routes.go` for all new endpoints

### Key Links (critical connections that must be WIRED)

- `routes.go` registers `GET /api/v1/users/{id}` → `auth.GetPublicUserHandler`
- `routes.go` admin group registers user + video admin routes
- `routes.go` registers `GET /api/v1/views/history` → `viewsHandler.GetViewHistory`
- Payment handlers import and use `shared.WriteJSON`/`shared.WriteError` instead of custom methods
- Notification preference handlers wired to routes inside `/users` group

## Open Questions

- None — all gaps are clearly defined CRUD operations with existing infrastructure.

### Deferred Ideas

- Admin user search (full-text search over username/email) — can be added later if `UserRepo.List` proves insufficient
- Admin video moderation actions (hide, delete, flag) — Iris plan defers these
- Notification delivery preferences (email vs push vs in-app routing) — current plan only stores toggles
