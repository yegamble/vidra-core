# Contract Repair & Parity Recovery Plan

Created: 2026-04-14
Author: yegamble@gmail.com
Status: VERIFIED
Approved: Yes
Iterations: 0
Worktree: No
Type: Feature

## Summary

**Goal:** Two-track recovery — (1) fix every frontend-to-backend contract drift so the product works end-to-end, and (2) map the full parity backlog at story level with file-level detail for future /spec plans. Establish the rule: "feature complete" = frontend route + service contract + live backend + integration tests + E2E flow all agree.

**Architecture:** Track 1 (Tasks 1-12) repairs contracts across vidra-core and vidra-user. Track 2 (Roadmap section) documents every missing story with exact file paths for future plans.

**Tech Stack:** Go (Chi), TypeScript (Next.js), Vitest integration tests, BTCPay Server Greenfield API, WebSocket (gorilla/websocket)

## Scope

### In Scope (Track 1 — this plan implements)

- Rewrite vidra-user payments service to match BTCPay invoice-centric backend API
- Add studio individual route wrappers in vidra-core
- Fix channel avatar/banner HTTP method + path mismatch in vidra-user
- Fix stream chat plural route mismatch in vidra-user
- Fix messaging route structure mismatch in vidra-user
- Verify + fix analytics route prefix alignment
- Fix admin jobs state parameter format
- Create E2EE frontend service to call existing backend routes
- Fix integration test harness auth
- Add 3 critical integration test suites (payments, messages, stream-chat)

### In Scope (Track 2 — roadmap reference, not tasks)

- Story-level backlog for all missing parity features
- File-level mapping for both repos
- Acceptance criteria per domain

### Out of Scope

- Implementing Track 2 stories (deferred to separate /spec plans)
- New database migrations
- ATProto route surface gap (needs its own investigation /spec)
- Frontend UI redesign beyond contract alignment
- PeerTube platform migration UI

## Approach

**Chosen:** Frontend adapts to backend for most domains; backend adds thin wrappers only where frontend has superior UX patterns (studio individual routes)

**Why:** vidra-core's 4,879 tests and clean architecture make it the source of truth. Changing Go routes risks breaking existing test coverage. The frontend service layer is a thin API client — adapting it is lower risk and faster.

**Alternatives considered:**
- Backend adapts to frontend: Rejected — would require adding speculative routes that duplicate BTCPay, adding test burden
- New shared contract: Rejected — highest effort, delays shipping; better to align first, then formalize OpenAPI later

## Context for Implementer

> Write for an implementer who has never seen the codebase.

- **Patterns to follow:**
  - vidra-core response envelope: `{success, data, error, meta}` via `shared.WriteJSON/WriteError` (`internal/httpapi/shared/response.go`)
  - vidra-user service pattern: thin async functions calling `api.get/post/put/delete` from `src/lib/api/client.ts`
  - Integration test pattern: `src/lib/api/__tests__/integration/auth.integration.test.ts` for reference
- **Conventions:**
  - Backend routes registered in `internal/httpapi/routes.go` (1300+ lines, split into helper functions)
  - Frontend services in `src/lib/api/services/*.ts`, types in `src/lib/api/types.ts`
  - All backend routes under `/api/v1/` prefix
- **Key files:**
  - `vidra-core/internal/httpapi/routes.go` — master route registration
  - `vidra-core/internal/httpapi/handlers/payments/btcpay_handlers.go` — BTCPay handler (4 endpoints)
  - `vidra-core/internal/httpapi/handlers/video/studio_handlers.go` — studio (single `/edit` endpoint)
  - `vidra-core/internal/chat/websocket_server.go` — WebSocket chat server
  - `vidra-user/src/lib/api/services/` — all frontend service contracts
  - `vidra-user/src/lib/api/__tests__/integration/setup.ts` — integration test auth harness
- **Gotchas:**
  - Backend `/messages/` and `/conversations/` are **separate sibling route groups** (not nested)
  - Backend `/e2ee/` routes exist and work but frontend has no service file for them
  - `paymentService` in vidra-user has `isPaymentsEnabled()` gate — when false, returns mock data. Must be removed entirely.
  - Backend channel avatar uses `POST` (multipart), frontend sends `PUT` with `/pick` suffix — PeerTube compat alias exists for instance media but NOT for channels
  - Admin jobs use `{state}` as URL path param, not query param

## Runtime Environment

- **vidra-core:** `make run` on port 9000, health at `/health`
- **vidra-user:** `npm run dev` on port 3000, calls vidra-core via `NEXT_PUBLIC_API_BASE_URL`
- **Integration tests:** `vitest` with `setup.ts` beforeAll hook, requires running vidra-core

## Contract Drift Matrix

| # | Domain | vidra-user calls | vidra-core has | Drift Type |
|---|--------|-----------------|----------------|------------|
| 1 | Payments | 16+ endpoints: `/payments/intent`, `/wallet`, `/transactions`, `/config`, `/stats`, `/subscribers`, `/memberships`, `/tiers`, `/inner-circle/join` + mock fallbacks | 4 BTCPay endpoints: `POST/GET /payments/invoices`, `GET /payments/invoices/{id}`, `POST /payments/webhooks/btcpay` | **Critical** — 70% of frontend routes don't exist |
| 2 | Studio | `POST /videos/{id}/studio/cut`, `/intro`, `/watermark` | `POST /videos/{id}/studio/edit` (type in request body) | **High** — 3 routes vs 1 |
| 3 | Channel Assets | `PUT /channels/{id}/avatar/pick`, `PUT /channels/{id}/banner/pick` | `POST /channels/{id}/avatar`, `POST /channels/{id}/banner` | **High** — wrong method + path |
| 4 | Stream Chat | `POST /streams/{id}/chat/ban`, `POST .../chat/timeout`, `DELETE .../chat/messages/{id}`, `PUT .../chat/slow-mode` | `POST /streams/{id}/chat/bans`, `DELETE .../chat/bans/{id}`, `POST .../chat/moderators`, `GET .../chat/stats` | **Medium** — singular vs plural, missing routes |
| 5 | Messaging | `GET /messages/conversations`, `GET /messages/conversations/{id}`, `GET /messages/unread-count` | `/messages/` and `/conversations/` as **separate sibling routes**; unread-count at `/conversations/unread-count` | **Medium** — nested vs sibling |
| 6 | Analytics | `GET /analytics/channel`, `/analytics/video/{id}`, `.../retention`, `.../demographics`, `.../heatmap`, `/analytics/export/*` | Per-video: `GET /videos/{id}/analytics`, `/videos/{id}/stats/retention`. Stream: `GET /streams/{id}/analytics`. Viewer tracking: `POST /analytics`. **No `/analytics/channel` or `/analytics/video/{id}` path exists.** | **High** — frontend paths are structurally different from every backend analytics route |
| 7 | Admin Jobs | `GET /admin/jobs` with `state` query param | `GET /admin/jobs/{state}` with state in URL path | **Low** — param location |
| 8 | E2EE | No service file (only `src/lib/crypto` dir) | Full `/e2ee/*` routes: `/keys`, `/key-exchange`, `/status`, `/messages` | **Medium** — backend exists, frontend missing |
| 9 | ATProto | 8 endpoints at `/federation/atproto/*` | Only `/.well-known/atproto-did` visible in routes.go | **Unknown** — needs verification |
| 10 | Auth (setup.ts) | `/api/v1/auth/register`, `/api/v1/auth/login` | Same paths | **None** — aligned |

## Assumptions

- BTCPay backend API is the source of truth for payments — assumed from user decision to have frontend adapt. Tasks 1, 10 depend on this.
- Backend `/conversations/` and `/messages/` are correctly registered as sibling groups in routes.go — verified in exploration. Task 5 depends on this.
- **VERIFIED (spec-review):** `analyticsHandler.RegisterRoutes()` registers `/streams/{streamId}/analytics` (stream analytics) and `/analytics` (viewer tracking events). There is NO `/analytics/channel` or `/analytics/video/{id}` path. Per-video analytics lives at `/videos/{id}/analytics` and `/videos/{id}/stats/retention`. Frontend paths need complete restructuring, not just a prefix fix. Task 6 depends on this.
- ATProto federation routes may exist outside routes.go (in federation handler self-registration) — not verified. Deferred to Track 2.
- Integration test setup.ts auth routes match backend — verified `/api/v1/auth/register` and `/api/v1/auth/login` exist. Task 9 still validates runtime behavior.

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Payments rewrite breaks premium page checkout flow | High | High | Keep `isPaymentsEnabled()` flag during transition, only remove after integration test passes |
| Analytics handler registers at unexpected prefix | **Confirmed** | High | Task 6 updated to restructure all frontend analytics paths (no `/analytics/channel` exists in backend) |
| Message service changes break existing conversations page | Medium | Medium | Run existing vidra-user unit tests after each change |
| Studio wrapper routes duplicate request validation | Low | Low | Extract ownership check to shared helper, call from all 4 handlers |
| BTCPay unavailable in CI breaks payments integration tests | High | Medium | Skip guard in test suite checks BTCPay availability before running |

## Goal Verification

### Truths

1. `paymentService` in vidra-user has zero mock data — every function calls a real backend endpoint
2. `studioService.cut()` returns a valid `StudioJob` from `POST /api/v1/videos/{id}/studio/cut`
3. `channelService.uploadAvatar()` succeeds with a real file upload
4. Stream chat ban/unban calls succeed against live backend
5. `messageService.listConversations()` returns real data from `/api/v1/conversations/`
6. Integration test harness authenticates successfully against vidra-core
7. All 3 new integration test suites pass against a running vidra-core instance

### Artifacts

1. `vidra-user/src/lib/api/services/payments.ts` — zero `MOCK_*` constants, zero `isPaymentsEnabled()` gates
2. `vidra-core/internal/httpapi/handlers/video/studio_handlers.go` — 3 new route handler methods
3. `vidra-user/src/lib/api/services/channels.ts` — `POST` method, no `/pick` suffix
4. `vidra-user/src/lib/api/services/streams.ts` — `/chat/bans` (plural), no `/chat/timeout`
5. `vidra-user/src/lib/api/services/messages.ts` — calls `/conversations/` and `/conversations/unread-count`
6. `vidra-user/src/lib/api/services/e2ee.ts` — new file covering all `/e2ee/*` backend routes
7. `vidra-user/src/lib/api/__tests__/integration/payments.integration.test.ts` — exists and passes
8. `vidra-user/src/lib/api/__tests__/integration/messages.integration.test.ts` — exists and passes
9. `vidra-user/src/lib/api/__tests__/integration/stream-chat.integration.test.ts` — exists and passes

## Progress Tracking

- [x] Task 1: Payments service rewrite (vidra-user)
- [x] Task 2: Studio individual route wrappers (vidra-core)
- [x] Task 3: Channel avatar/banner route fix (vidra-user)
- [x] Task 4: Stream chat route alignment (vidra-user)
- [x] Task 5: Messaging route alignment (vidra-user)
- [x] Task 6: Analytics route verification + fix
- [x] Task 7: Admin jobs route fix (vidra-user)
- [x] Task 8: E2EE service creation (vidra-user)
- [x] Task 9: Integration test harness validation (vidra-user)
- [x] Task 10: Payments integration test suite (vidra-user)
- [x] Task 11: Messages integration test suite (vidra-user)
- [x] Task 12: Stream chat integration test suite (vidra-user)

**Total Tasks:** 12 | **Completed:** 12 | **Remaining:** 0

## Implementation Tasks

### Task 1: Payments Service Rewrite (vidra-user)

**Objective:** Rewrite `payments.ts` to use BTCPay invoice-centric API. Remove all mock data and `isPaymentsEnabled()` gates. The premium page should create BTCPay invoices and redirect to checkout links.

**Dependencies:** None

**Files:**

- Modify: `vidra-user/src/lib/api/services/payments.ts` — complete rewrite
- Modify: `vidra-user/src/lib/api/types.ts` — update payment types to match BTCPay domain
- Modify: `vidra-user/src/lib/payments/feature-flag.ts` — remove or convert to server-config check
- Modify: `vidra-user/src/app/components/pages/premium-page.tsx` — update to invoice-based checkout

**Key Decisions / Notes:**

- Backend has 4 BTCPay endpoints (`routes.go:439-443`):
  - `POST /api/v1/payments/invoices` — CreateInvoice (body: `{amount_sats, currency, metadata}`)
  - `GET /api/v1/payments/invoices` — ListInvoices (query: `start`, `count`, `status`)
  - `GET /api/v1/payments/invoices/{id}` — GetInvoice
  - `POST /api/v1/payments/webhooks/btcpay` — WebhookCallback (server-to-server)
- Response type is `domain.BTCPayInvoice` with fields: `id`, `btcpay_invoice_id`, `user_id`, `amount_sats`, `currency`, `status`, `checkout_link`, `expires_at`
- Premium checkout flow becomes: create invoice → redirect to `checkout_link` → poll invoice status
- Remove ALL `MOCK_*` constants, `mockDelay()`, `mockPaymentIntent()`, and every `if (!isPaymentsEnabled())` branch
- Subscriptions, tiers, memberships, and inner circle features are NOT yet in the backend — these frontend endpoints should be removed and tracked in the roadmap
- Keep `isPaymentsEnabled()` export in feature-flag.ts but change it to check `cfg.EnableBitcoin` from server config endpoint

**Definition of Done:**

- [ ] `payments.ts` has zero mock data, zero `isPaymentsEnabled()` fallback branches
- [ ] All payment service methods call real backend endpoints
- [ ] Types in `types.ts` match `domain.BTCPayInvoice` shape
- [ ] Premium page creates a real BTCPay invoice and shows checkout link
- [ ] `npm run build` succeeds with no type errors

**Verify:**

- `cd ~/github/vidra-user && npm run build`
- `cd ~/github/vidra-user && npx vitest run src/lib/api --reporter=verbose`

---

### Task 2: Studio Individual Route Wrappers (vidra-core)

**Objective:** Add `/studio/cut`, `/studio/intro`, `/studio/watermark` as thin wrappers that construct a `StudioEditRequest` and delegate to `CreateEditJob`. This matches what vidra-user expects.

**Dependencies:** None

**Files:**

- Modify: `vidra-core/internal/httpapi/handlers/video/studio_handlers.go` — add 3 handler methods
- Modify: `vidra-core/internal/httpapi/routes.go` — register 3 new routes (~line 1027-1031)
- Modify: `vidra-core/internal/httpapi/handlers/video/studio_handlers_test.go` — add tests for new routes

**Key Decisions / Notes:**

- Current backend (`studio_handlers.go:39`): `POST /videos/{id}/studio/edit` accepts full `StudioEditRequest` in body
- New routes parse operation-specific params and construct the `StudioEditRequest`:
  - `POST /videos/{id}/studio/cut` — body: `StudioCutParams` → `StudioEditRequest{Tasks: [{Name: "cut", Options: ...}]}`
  - `POST /videos/{id}/studio/intro` — body: `StudioIntroParams` → `StudioEditRequest{Tasks: [{Name: "add-intro", Options: ...}]}`
  - `POST /videos/{id}/studio/watermark` — body: `StudioWatermarkParams` → `StudioEditRequest{Tasks: [{Name: "add-watermark", Options: ...}]}`
- Before implementing wrappers, read `studio_handlers.go` to determine if video ownership check is a shared helper or inline in `CreateEditJob`. If inline, extract to a private function (e.g., `verifyVideoOwnership(ctx, w, r, videoRepo) (*domain.Video, bool)`) in the same file first, then call it from all 4 handlers (CreateEditJob + 3 wrappers). This extraction is part of Task 2 scope.
- Register in routes.go after the existing `/edit` route (line 1029)
- Follow existing table-driven test pattern in `studio_handlers_test.go`

**Definition of Done:**

- [ ] `POST /api/v1/videos/{id}/studio/cut` returns 201 with a StudioJob
- [ ] `POST /api/v1/videos/{id}/studio/intro` returns 201 with a StudioJob
- [ ] `POST /api/v1/videos/{id}/studio/watermark` returns 201 with a StudioJob
- [ ] Unauthenticated request to `/studio/cut` returns 401
- [ ] Request from non-owner to `/studio/cut` returns 403
- [ ] Existing `/edit` endpoint still works unchanged
- [ ] Table-driven tests cover all 3 new routes + auth/ownership error cases
- [ ] `make validate-all` passes

**Verify:**

- `cd ~/github/vidra-core && go test -v ./internal/httpapi/handlers/video/ -run TestStudio`
- `cd ~/github/vidra-core && make validate-all`

---

### Task 3: Channel Avatar/Banner Route Fix (vidra-user)

**Objective:** Fix channel avatar/banner upload to use `POST` method and correct path (no `/pick` suffix), matching vidra-core's actual routes.

**Dependencies:** None

**Files:**

- Modify: `vidra-user/src/lib/api/services/channels.ts` — fix `uploadAvatar` and `uploadBanner` methods

**Key Decisions / Notes:**

- Backend (`routes.go:381-384`):
  - `POST /api/v1/channels/{id}/avatar` — UploadAvatar (multipart form)
  - `DELETE /api/v1/channels/{id}/avatar` — DeleteAvatar
  - `POST /api/v1/channels/{id}/banner` — UploadBanner (multipart form)
  - `DELETE /api/v1/channels/{id}/banner` — DeleteBanner
- Frontend currently uses:
  - `PUT /api/v1/channels/{id}/avatar/pick` — wrong method AND path
  - `PUT /api/v1/channels/{id}/banner/pick` — wrong method AND path
- Fix: change `method: "PUT"` → `method: "POST"`, remove `/pick` from URL
- Form field names (`avatarfile`, `bannerfile`) MUST be verified against backend handler before changing the service. Read `channel_media_handlers.go` to confirm the expected multipart field name. If the backend expects a different field name (e.g., `avatar` instead of `avatarfile`), update accordingly.

**Definition of Done:**

- [ ] `uploadAvatar` sends `POST` to `/api/v1/channels/{id}/avatar`
- [ ] `uploadBanner` sends `POST` to `/api/v1/channels/{id}/banner`
- [ ] Multipart field name in `uploadAvatar` matches the field name expected by `channel_media_handlers.go`
- [ ] No `/pick` suffix in any channel media URL
- [ ] `deleteAvatar` and `deleteBanner` paths match backend (already correct with DELETE)

**Verify:**

- `cd ~/github/vidra-user && npx vitest run src/lib/api --reporter=verbose`

---

### Task 4: Stream Chat Route Alignment (vidra-user)

**Objective:** Fix stream chat service routes to match backend's actual route structure.

**Dependencies:** None

**Files:**

- Modify: `vidra-user/src/lib/api/services/streams.ts` — fix chat-related methods

**Key Decisions / Notes:**

- Backend chat routes (`chat_handlers.go:47-66`, registered at `/streams/{streamId}/chat`):
  - `GET /messages` — GetChatMessages
  - `DELETE /messages/{messageId}` — DeleteMessage (auth required)
  - `POST /moderators` — AddModerator
  - `DELETE /moderators/{userId}` — RemoveModerator
  - `GET /moderators` — GetModerators
  - `POST /bans` — BanUser (NOT `/ban`)
  - `DELETE /bans/{userId}` — UnbanUser
  - `GET /bans` — GetBans
  - `GET /stats` — GetChatStats
- Frontend drift:
  - `banUser`: `POST /chat/ban` → should be `POST /chat/bans`
  - `timeoutUser`: `POST /chat/timeout` → backend has NO timeout route; remove or map to ban with duration in metadata
  - `deleteMessage`: correct path, just verify
  - `setSlowMode`: `PUT /chat/slow-mode` → backend has NO slow-mode route; remove
- Add missing methods: `getMessages`, `getModerators`, `addModerator`, `removeModerator`, `getBans`, `unbanUser`, `getStats`

**Definition of Done:**

- [ ] `banUser` calls `POST /api/v1/streams/{id}/chat/bans`
- [ ] `timeoutUser` removed (backend doesn't support it)
- [ ] `setSlowMode` removed (backend doesn't support it)
- [ ] New methods added: `getMessages`, `getModerators`, `addModerator`, `removeModerator`, `getBans`, `unbanUser`, `getStats`
- [ ] TypeScript types updated for chat ban/moderator/stats responses

**Verify:**

- `cd ~/github/vidra-user && npm run build`

---

### Task 5: Messaging Route Alignment (vidra-user)

**Objective:** Fix message service to match backend's separate `/messages/` and `/conversations/` route groups (they are sibling routes, not nested).

**Dependencies:** None

**Files:**

- Modify: `vidra-user/src/lib/api/services/messages.ts` — fix all route paths

**Key Decisions / Notes:**

- Backend routes (`routes.go:787-798`, within `registerCommunicationsAPIRoutes`):
  - `/messages/` group: `POST /` (send), `GET /` (list), `PUT /{messageId}/read`, `DELETE /{messageId}`
  - `/conversations/` group: `GET /` (list), `GET /unread-count`
- Frontend drift:
  - `listConversations`: `/messages/conversations` → should be `/conversations/`
  - `getMessages`: `/messages/conversations/{id}` → should be `/messages/` with conversation filter (or GET /messages?conversation_id=X)
  - `getUnreadCount`: `/messages/unread-count` → should be `/conversations/unread-count`
  - `getConversationWithUser`: `POST /messages/conversations` → need to verify if backend has conversation creation
  - `send`, `markAsRead`, `remove` — paths look correct (already under `/messages/`)
- **MUST verify before implementing:** Read `internal/httpapi/handlers/messaging/messages_handlers.go` to check if `GET /messages/` accepts a `conversation_id` query param. If it does NOT, two options: (a) add `conversation_id` query param support to `GET /messages/` in vidra-core (small backend change within scope since plan already touches vidra-core for studio), or (b) remove `getMessages(conversationId)` from frontend service and add to Track 2 as a backend gap. Decision should be made by implementer based on handler complexity.

**Definition of Done:**

- [ ] `listConversations` calls `GET /api/v1/conversations/`
- [ ] `getUnreadCount` calls `GET /api/v1/conversations/unread-count`
- [ ] `getMessages` either uses verified `conversation_id` filter on `GET /messages/` or is explicitly deferred with comment
- [ ] `send`, `markAsRead`, `remove` paths verified against backend
- [ ] Types match backend response shapes

**Verify:**

- `cd ~/github/vidra-user && npm run build`

---

### Task 6: Analytics Route Restructuring

**Objective:** Restructure vidra-user's analytics service to match vidra-core's actual route topology. The backend has NO unified `/analytics/` prefix — analytics are scattered across video routes, stream routes, and a viewer tracking endpoint.

**Dependencies:** None

**Files:**

- Read: `vidra-core/internal/httpapi/handlers/analytics/export_handler.go` — verify export route prefix
- Modify: `vidra-user/src/lib/api/services/analytics.ts` — restructure all route paths

**Key Decisions / Notes:**

- **VERIFIED backend route topology (spec-review confirmed):**
  - Per-video analytics: `GET /api/v1/videos/{id}/analytics` (line 607 of routes.go)
  - Retention: `GET /api/v1/videos/{id}/stats/retention` (line 610)
  - Stream analytics: `GET /api/v1/streams/{streamId}/analytics` (registered by `analyticsHandler.RegisterRoutes`)
  - Viewer tracking: `POST /api/v1/analytics` (join/leave/engagement events)
  - **No `/api/v1/analytics/channel` path exists in the backend**
  - **No `/api/v1/analytics/video/{id}` path exists** — it's `/api/v1/videos/{id}/analytics`
- Frontend path restructuring needed:
  - `getVideoAnalytics(videoId)`: `/analytics/video/${videoId}` → `/videos/${videoId}/analytics`
  - `getRetention(videoId)`: `/analytics/video/${videoId}/retention` → `/videos/${videoId}/stats/retention`
  - `getChannelAnalytics()`: `/analytics/channel` → **backend gap** — either needs a backend endpoint or must aggregate from video analytics. Flag as deferred if no backend equivalent exists.
  - `getDemographics(videoId)`: `/analytics/video/${videoId}/demographics` → verify if backend has this; likely does not exist
  - `getHeatmap(videoId)`: `/analytics/video/${videoId}/heatmap` → verify if backend has this; likely does not exist
  - Export routes: check `exportHandler.RegisterRoutes()` in `internal/httpapi/handlers/analytics/export_handler.go` for actual paths
- Demographics, heatmap, and channel analytics may be backend gaps — if so, remove from frontend service and add to Track 2 roadmap

**Definition of Done:**

- [ ] `getVideoAnalytics` calls `GET /api/v1/videos/{id}/analytics`
- [ ] `getRetention` calls `GET /api/v1/videos/{id}/stats/retention`
- [ ] `getChannelAnalytics` either calls a verified backend route or is removed with a comment pointing to Track 2
- [ ] Demographics and heatmap either call verified backend routes or are removed with comments
- [ ] Export paths verified against `export_handler.go` and aligned
- [ ] `npm run build` succeeds

**Verify:**

- `cd ~/github/vidra-user && npm run build`

---

### Task 7: Admin Jobs Route Fix (vidra-user)

**Objective:** Fix admin service to send job state as URL path parameter instead of query parameter.

**Dependencies:** None

**Files:**

- Modify: `vidra-user/src/lib/api/services/admin.ts` — fix `getJobs` method

**Key Decisions / Notes:**

- Backend (`routes.go:line ~3`): `GET /admin/jobs/{state}` and `GET /admin/jobs` (without state, returns all)
- Frontend: `GET /admin/jobs` with `state` as query param in the params object
- Fix: when state is provided, interpolate into URL path: `/admin/jobs/${state}`
- Also available: `POST /admin/jobs/pause`, `POST /admin/jobs/resume` — verify frontend has these

**Definition of Done:**

- [ ] `getJobs({state: "active"})` calls `GET /api/v1/admin/jobs/active`
- [ ] `getJobs()` (no state) calls `GET /api/v1/admin/jobs`
- [ ] Pause/resume methods exist in admin service

**Verify:**

- `cd ~/github/vidra-user && npm run build`

---

### Task 8: E2EE Service Creation (vidra-user)

**Objective:** Create a new `e2ee.ts` service file that exposes all backend `/e2ee/*` routes to the frontend.

**Dependencies:** None

**Files:**

- Create: `vidra-user/src/lib/api/services/e2ee.ts` — new service file
- Modify: `vidra-user/src/lib/api/services/index.ts` — export new service
- Modify: `vidra-user/src/lib/api/types.ts` — add E2EE types

**Key Decisions / Notes:**

- Backend E2EE routes (`routes.go:803-812`):
  - `POST /api/v1/e2ee/keys` — RegisterIdentityKey
  - `GET /api/v1/e2ee/keys/{userId}` — GetPublicKeys
  - `GET /api/v1/e2ee/status` — GetE2EEStatus
  - `POST /api/v1/e2ee/key-exchange` — InitiateKeyExchange
  - `POST /api/v1/e2ee/key-exchange/accept` — AcceptKeyExchange
  - `GET /api/v1/e2ee/key-exchange/pending` — GetPendingKeyExchanges
  - `POST /api/v1/e2ee/messages` — StoreEncryptedMessage
  - `GET /api/v1/e2ee/messages/{conversationId}` — GetEncryptedMessages
- `src/lib/crypto` directory already exists — may contain client-side crypto utils. Check for reuse.
- Follow same service pattern as `messages.ts`: thin async functions calling `api.get/post`
- Types should mirror backend handler request/response shapes

**Definition of Done:**

- [ ] `e2ee.ts` exposes all 8 backend endpoints
- [ ] Types added to `types.ts` for E2EE key, key exchange, encrypted message
- [ ] Exported from `services/index.ts`
- [ ] `npm run build` succeeds

**Verify:**

- `cd ~/github/vidra-user && npm run build`

---

### Task 9: Integration Test Harness Validation (vidra-user)

**Objective:** Verify and fix the integration test `setup.ts` so it authenticates against current vidra-core. Ensure the test helpers are correct.

**Dependencies:** None

**Files:**

- Modify (if needed): `vidra-user/src/lib/api/__tests__/integration/setup.ts`
- Modify (if needed): `vidra-user/src/lib/api/__tests__/integration/helpers.ts`

**Key Decisions / Notes:**

- setup.ts calls `POST /api/v1/auth/register` and `POST /api/v1/auth/login` — routes confirmed in vidra-core (`routes.go:line ~2`)
- Expected response shape: `{user: {id, username}, access_token, refresh_token}` for register, `{access_token, refresh_token}` for login
- Need to verify: does vidra-core's register/login return tokens in the response body directly, or wrapped in the standard `{success, data}` envelope?
- If envelope is used, `setup.ts` needs to unwrap: `response.data.access_token` instead of `response.access_token`
- The health check at `/health` should be correct

**Definition of Done:**

- [ ] `setup.ts` successfully authenticates against a running vidra-core instance
- [ ] Test tokens are correctly stored for subsequent test use
- [ ] Existing integration tests (auth, channels, comments, etc.) still pass

**Verify:**

- Start vidra-core: `cd ~/github/vidra-core && make run`
- Run: `cd ~/github/vidra-user && npx vitest run src/lib/api/__tests__/integration/auth.integration.test.ts`

---

### Task 10: Payments Integration Test Suite (vidra-user)

**Objective:** Add live integration tests for the rewritten payments service against a running vidra-core with BTCPay enabled.

**Dependencies:** Task 1 (payments rewrite), Task 9 (harness fix)

**Files:**

- Create: `vidra-user/src/lib/api/__tests__/integration/payments.integration.test.ts`

**Key Decisions / Notes:**

- Tests require vidra-core running with `EnableBitcoin=true` and a BTCPay mock or test instance
- Test cases:
  - Create invoice → verify response has `checkout_link`, `amount_sats`, `status: "new"`
  - List invoices → verify pagination with `start`/`count`
  - Get invoice by ID → verify same invoice returned
  - Create invoice with invalid amount → verify 400 error
  - Create invoice without auth → verify 401
- Use `describe("payments service integration")` with `beforeAll` from setup.ts
- **BTCPay availability guard:** Before the test suite runs, call `GET /api/v1/payments/invoices`. If it returns 503 (BTCPay unreachable), 404 (route not registered because `EnableBitcoin=false`), or network error, call `test.skip('BTCPay unavailable — set EnableBitcoin=true and configure BTCPayServerURL in vidra-core .env')`. This prevents network errors from masking real test failures in CI.

**Definition of Done:**

- [ ] 5+ test cases covering CRUD + error cases
- [ ] Tests pass against running vidra-core with BTCPay
- [ ] Tests emit a clear skip message when BTCPay is not configured, not an error
- [ ] Test file header documents required environment variables

**Verify:**

- `cd ~/github/vidra-user && npx vitest run src/lib/api/__tests__/integration/payments.integration.test.ts`

---

### Task 11: Messages Integration Test Suite (vidra-user)

**Objective:** Add live integration tests for the corrected messaging service.

**Dependencies:** Task 5 (messaging alignment), Task 9 (harness fix)

**Files:**

- Create: `vidra-user/src/lib/api/__tests__/integration/messages.integration.test.ts`

**Key Decisions / Notes:**

- Tests require two authenticated users (sender and recipient)
- Test cases:
  - Send message → verify message returned with correct fields
  - List conversations → verify sender's conversation appears
  - Get unread count → verify count increments
  - Mark as read → verify read status updates
  - Delete message → verify 200 response
  - Send to self → verify 400 error
- Need a second test user — either register two users in setup or create one in-test

**Definition of Done:**

- [ ] 6+ test cases covering send, list, read, delete, errors
- [ ] Tests pass against running vidra-core
- [ ] Two-user flow tested (sender + recipient)

**Verify:**

- `cd ~/github/vidra-user && npx vitest run src/lib/api/__tests__/integration/messages.integration.test.ts`

---

### Task 12: Stream Chat Integration Test Suite (vidra-user)

**Objective:** Add live integration tests for the corrected stream chat service.

**Dependencies:** Task 4 (chat alignment), Task 9 (harness fix)

**Files:**

- Create: `vidra-user/src/lib/api/__tests__/integration/stream-chat.integration.test.ts`

**Key Decisions / Notes:**

- Chat operations require an active live stream — tests need to create one first
- Test cases:
  - Ban user in stream chat → verify 200
  - Get bans list → verify banned user appears
  - Unban user → verify removed from bans list
  - Add moderator → verify 200
  - Get moderators → verify moderator appears
  - Get chat stats → verify response shape
  - Delete chat message → verify 200 (requires a message to exist)
- WebSocket chat (send/receive messages) may be better as E2E test — REST endpoints sufficient for integration
- If no live stream infrastructure in test env, tests should skip

**Definition of Done:**

- [ ] 6+ test cases covering bans, moderators, stats
- [ ] Tests pass against running vidra-core with live stream support
- [ ] Tests skip gracefully when streaming unavailable

**Verify:**

- `cd ~/github/vidra-user && npx vitest run src/lib/api/__tests__/integration/stream-chat.integration.test.ts`

---

## Track 2: Parity Completion Roadmap

> This section is a **reference backlog** for future /spec plans. It documents every missing story with file-level mapping. Each domain should become its own /spec plan when prioritized.

### 2.1 User Stories — Account Import/Export Archive

**PeerTube reference:** [Setup your account](https://docs.joinpeertube.org/use/setup-account) — user-facing archive (history, playlists, follows, channels, optional video files)

**Backend status:** `vidra-core/internal/httpapi/handlers/user/archive_handlers.go` exists with `RequestExport`, `ListExports`, `DeleteExport`, `InitImportResumable`, `UploadImportChunk`, `CancelImportResumable`, `GetLatestImport`. Routes at `/api/v1/users/{userId}/exports/*` and `/api/v1/users/{userId}/imports/*`.

**Frontend status:** No archive UI exists. No page at `/settings/import-export` or similar.

**Missing files (vidra-user):**
- `src/app/[locale]/(main)/settings/import-export/page.tsx` — archive management page
- `src/lib/api/services/archive.ts` — service calling `/users/{userId}/exports/*` and `/imports/*`
- `src/lib/api/__tests__/integration/archive.integration.test.ts`

**Missing backend work:**
- Verify `ArchiveRepository` interface is implemented (not just defined)
- Archive should include: watch history, playlists, follows, channel metadata, optional video files
- Background job for archive generation (large exports)

**Acceptance criteria:** User can request export → download archive → import on another instance, including history, playlists, follows, channels

---

### 2.2 User Stories — Library Parity

**PeerTube reference:** [User library](https://docs.joinpeertube.org/use/library)

**Frontend status:** `src/app/[locale]/(main)/library/[section]/page.tsx` exists but scope TBD.

**Missing features:**
| Feature | vidra-core | vidra-user | Files needed |
|---------|-----------|-----------|--------------|
| Channel collaboration | `channel/collaborator_handlers.go` exists with Invite/Accept/Revoke/List | No UI | `src/components/channel/collaborators-panel.tsx` |
| Ownership transfer | Unknown | No UI | Backend handler + frontend panel |
| Watched words | `autotags/` handlers exist | No UI | `src/app/[locale]/(main)/admin/watched-words/page.tsx` |
| Auto-tag policies | `autotags/` handlers exist | No UI | Admin settings panel |
| Playlist management parity | `social/playlist_handlers.go` exists | Partial UI at `playlist/[id]/page.tsx` | Full CRUD panel, reorder, privacy settings |
| Ownership changes inbox | Unknown | No UI | Backend + frontend notification flow |

---

### 2.3 User Stories — Publish/Import/Live Parity

**PeerTube reference:** [Publish a video or a live](https://docs.joinpeertube.org/use/create-upload-video)

**Missing features:**
| Feature | vidra-core | vidra-user | Files needed |
|---------|-----------|-----------|--------------|
| Import by URL | `importer/` package exists | No UI | Upload page import tab |
| Import by torrent | `importer/` may support | No UI | Upload page import tab |
| Recurring live | `livestream/scheduler.go` exists | No scheduling UI | Stream creation form |
| RTMP key flow | `livestream/streamkey_test.go` tested | Partial (rotateKey exists) | Full key management UI |
| Password-protected videos | Deferred in registry | No backend | Migration + handler + UI |
| Privacy/download/comment settings | Partial backend | Partial UI | Video edit form fields |
| Source replacement | Deferred in registry | No backend | Upload handler + UI |

---

### 2.4 User Stories — Watch/Share/Embed/Privacy Parity

**PeerTube reference:** [Watch, share, download a video](https://docs.joinpeertube.org/use/watch-video)

**Frontend pages:** `embed/[id]/page.tsx`, `embed/playlist/[id]/page.tsx` exist.

**Missing features:**
| Feature | vidra-core | vidra-user | Files needed |
|---------|-----------|-----------|--------------|
| Embed customization | Basic oEmbed | No customization UI | Embed config panel |
| Password/embed privacy | Not implemented | No UI | Backend + embed handler |
| oEmbed endpoint parity | May exist | No consumer | Verify backend oEmbed handler |
| Player settings consistency | Backend has config | Frontend may not read it | Config integration |

---

### 2.5 User Stories — Studio & Analytics Parity

**PeerTube reference:** [Studio](https://docs.joinpeertube.org/use/studio), [Video statistics](https://docs.joinpeertube.org/use/video-stats)

**Frontend pages:** `analytics/page.tsx`, `analytics/video/[id]/page.tsx` exist.

**Missing features:**
| Feature | vidra-core | vidra-user | Files needed |
|---------|-----------|-----------|--------------|
| Studio job status polling | `studio_handlers.go` GetJob exists | No polling UI | Studio page with job status component |
| Caption generation UI | `whisper/` package exists, `/captions/generate` route exists | `captionService.generate()` exists | Caption generation panel on video edit |
| Analytics retention curve | Backend handler exists | `analyticsService.getRetention()` exists | Verify route alignment (Task 6) |
| Analytics export | `export_handler.go` exists | `analyticsService.exportCsv/Json/Pdf()` exists | Verify route alignment (Task 6) |
| Real-time analytics | Unknown | No real-time UI | WebSocket or polling analytics |

---

### 2.6 Moderator Stories — Abuse & Moderation Workflow

**PeerTube reference:** [Moderate your PeerTube platform](https://docs.joinpeertube.org/admin/moderation)

**Frontend pages:** `admin/moderation/page.tsx` exists.

**Missing features:**
| Feature | vidra-core | vidra-user | Files needed |
|---------|-----------|-----------|--------------|
| Full abuse workflow | `admin/abuse-reports` CRUD exists | Partial UI | Complete abuse review panel |
| Comment moderation | `social/comment_handlers.go` exists | Basic comments UI | Moderation queue component |
| Watched words management | `autotags/` handlers exist | No UI | `admin/watched-words/page.tsx` |
| Auto-tag policies | `autotags/` handlers exist | No UI | Auto-tag settings panel |
| Quarantine/auto-block review | Unknown | No UI | Moderation review queue |
| Platform/account mute | `blocklist` routes exist | Partial blocklist UI | Full mute management panel |
| Audit trail | Audit logger exists in backend | No UI | `admin/audit-log/page.tsx` |

---

### 2.7 Admin Stories — Plugins, Runners, Migration

**PeerTube references:** [Plugins & Themes](https://docs.joinpeertube.org/contribute/plugins), [Remote Runners](https://docs.joinpeertube.org/admin/remote-runners), [Platform migration](https://docs.joinpeertube.org/maintain/migration)

**Missing features:**
| Feature | vidra-core | vidra-user | Files needed |
|---------|-----------|-----------|--------------|
| Plugin management UI | `plugin/manager.go`, `plugin_handlers.go` exist | No UI | `admin/plugins/page.tsx`, plugin install/configure panel |
| Theme management | Plugin system supports themes | No UI | Theme picker in admin settings |
| Remote runners UI | `runners/` handlers exist (some `runnersNotImplemented`) | No UI | `admin/runners/page.tsx` |
| Migration dry-run/resume/cancel | `importer/migration*.go` exists | No UI | `admin/migration/page.tsx` |
| Backup/restore UI | `backup/` package exists with CLI | No UI | `admin/backups/page.tsx` |
| Federation actors/hardening | `activitypub/` package exists | Partial at `admin/federation/page.tsx` | Federation settings panel |
| OAuth client management | `authHandlers.AdminListOAuthClients/Create/Rotate` exist | No UI | `admin/oauth/page.tsx` |
| Deeper logs/jobs tooling | Job/log handlers exist | Basic at `admin/jobs/page.tsx`, `admin/logs/page.tsx` | Enhanced filtering, job detail view |

---

### 2.8 Vidra-Specific Stories

| Feature | vidra-core status | vidra-user status | Next step |
|---------|------------------|------------------|-----------|
| BTCPay subscriptions/tips/memberships | Only invoices exist | Mock-based premium page | Backend: subscription model + recurring invoice logic |
| Inner Circle gating | Not implemented | Mock tiers/memberships | Backend: membership domain + gating middleware |
| ATProto account connect/status | `atproto_service.go` PublishVideo exists | `atproto.ts` service file exists with 8 endpoints | Verify backend routes exist; add missing ones |
| ATProto syndication/feed | `atproto_features.go` exists | `atproto.ts` has syndicate/feed methods | Verify end-to-end flow |
| E2EE key registration/exchange | Full `/e2ee/*` routes exist | No service file (Task 8 creates it) | After Task 8: build key management UI |
| E2EE message encryption | Backend stores/retrieves encrypted messages | `src/lib/crypto` dir exists | Build encryption UI component |
| Live chat with moderation | `chat/websocket_server.go` + `chat_handlers.go` exist | `streams.ts` has partial chat methods | After Task 4: build chat UI component |
| Live chat bans/moderators/stats | Backend routes exist | Being fixed in Task 4 | After Task 4: moderation UI panel |
| Video studio with real jobs | Backend handlers exist | `studio.ts` exists | After Task 2: build studio UI with job polling |
| Captions with Whisper generation | Backend `/captions/generate` exists | `captionService.generate()` exists | Build caption generation UI |
| Advanced analytics with exports | Backend export handler exists | `analyticsService.export*()` exists | After Task 6: verify export routes work |

---

### 2.9 Integration Test Gap (Future Plans)

**Currently existing suites (7):** auth, channels, comments, notifications, playlists, search, videos

**Missing suites mapped to domains:**
| Suite | Depends on Track 1 task | Key endpoints to test | Priority |
|-------|------------------------|----------------------|----------|
| `payments.integration.test.ts` | Task 1, 10 | invoices CRUD | **P0** (in this plan) |
| `messages.integration.test.ts` | Task 5, 11 | send, list conversations, unread count | **P0** (in this plan) |
| `stream-chat.integration.test.ts` | Task 4, 12 | bans, moderators, stats | **P0** (in this plan) |
| `e2ee.integration.test.ts` | Task 8 | key register, key exchange, encrypted messages | P1 |
| `studio.integration.test.ts` | Task 2 | cut/intro/watermark job creation, job polling | P1 |
| `captions.integration.test.ts` | None | CRUD, generate | P1 |
| `analytics.integration.test.ts` | Task 6 | channel/video analytics, export | P1 |
| `admin-ops.integration.test.ts` | Task 7 | jobs, registrations, config, logs | P1 |
| `channel-assets.integration.test.ts` | Task 3 | avatar/banner upload/delete | P2 |
| `migrations.integration.test.ts` | None | dry-run, status, cancel | P2 |

**E2E flows (Playwright, future plans):**
| Flow | Priority | Preconditions |
|------|----------|---------------|
| Bitcoin checkout + invoice status | P1 | BTCPay test instance |
| DM send/read + E2EE handshake fallback | P1 | Two test users |
| Live chat send/delete/ban | P1 | Active stream |
| ATProto connect + syndicate | P2 | ATProto PDS mock |
| Admin migration dry-run | P2 | Migration ETL infrastructure |
| Plugin install/uninstall | P2 | Plugin manager |
| Backup/restore | P2 | Backup infrastructure |

---

## Open Questions

1. **Analytics handler route prefix:** What path does `analyticsHandler.RegisterRoutes(r, jwtSecret)` use? Needs runtime verification in Task 6.
2. **ATProto federation routes:** The frontend expects 8 endpoints at `/api/v1/federation/atproto/*` but only `/.well-known/atproto-did` is visible in routes.go. Are these registered in a federation handler self-registration, or is this a backend gap?
3. **Message conversation retrieval:** Does `GET /api/v1/messages/` accept a `conversation_id` query param to filter messages by conversation? If not, how does the frontend fetch messages for a specific conversation?
4. **BTCPay test environment:** Is a BTCPay mock/test instance available for integration tests, or should tests skip when unavailable?

## Deferred Ideas

- **Unified OpenAPI contract:** After Track 1 repairs are done, generate a single OpenAPI spec from vidra-core routes and use it to type-check vidra-user services at build time.
- **Contract drift CI check:** Add a CI job that compares frontend service URLs against backend route registration to prevent future drift.
- **Feature flag service:** Replace per-feature `isPaymentsEnabled()` flags with a single server-config-driven feature flag system.
