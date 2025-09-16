# PeerTube‑Inspired API Roadmap (Athena)

This roadmap uses PeerTube’s resource model and API patterns as guidance to finish Athena’s API natively (no compatibility facade). We’ll adopt the spirit of PeerTube’s shapes and flows while keeping Athena’s strengths (encoding, IPFS, notifications) and minimizing breaking changes.

Notes:
- We will converge toward consistent shapes, pagination, and naming inline with common PeerTube clients, prioritizing clarity and maintainability.
- Status legend: Done = shipped; Planned = upcoming; Future = longer‑term.

## 1) Current Coverage (by area)

### Videos (list/search/get/stream)
- Athena: `GET /api/v1/videos` → list public videos (Covered/Partial: payload shape likely differs)
- Athena: `GET /api/v1/videos/search` → search videos (Covered/Partial)
- Athena: `GET /api/v1/videos/{id}` → get video (Covered/Partial)
- Athena: `GET /api/v1/videos/{id}/stream` → HLS master/variants (Covered/Partial)
- Athena: `GET /api/v1/hls/*` → HLS static file server with privacy (Covered/Partial)
- Athena: `GET /api/v1/trending` and `GET /api/v1/videos/top` (Covered; not a core PeerTube shape but useful)

Upload & Processing
- Athena: Chunked upload flow: `/api/v1/uploads/{initiate,chunks,complete,status,resume}` (Covered/Partial)
- Athena: Legacy one-shot upload: `POST /api/v1/videos/upload` (Covered/Partial)
- Athena: Post-upload processing/encoding queue + status: `GET /api/v1/encoding/status` (Covered/Partial)

Views & Analytics
- Athena: `POST /api/v1/videos/{videoId}/views` (Covered; tracking)
- Athena: `GET /api/v1/videos/{videoId}/analytics` (Covered)
- Athena: `GET /api/v1/videos/{videoId}/stats/daily` (Covered)
- Athena: `POST /api/v1/views/fingerprint` (Covered; dedup helper)

### Users / Profiles
- Athena: `GET /api/v1/users/{id}`; `GET /api/v1/users/{id}/videos` (Covered/Partial)
- Athena: `GET /api/v1/users/me`, `PUT /api/v1/users/me` (Covered/Partial)
- Athena: `POST /api/v1/users/me/avatar` (Covered, with IPFS pinning)

### Subscriptions
- Athena: `POST/DELETE /api/v1/users/{id}/subscribe` (Covered/Partial)
- Athena: `GET /api/v1/users/me/subscriptions` (Covered/Partial)
- Athena: `GET /api/v1/videos/subscriptions` (Covered/Partial)

### Categories
- Athena: `GET /api/v1/categories`, `GET /api/v1/categories/{id}` (Covered)
- Athena: Admin: `POST/PUT/DELETE /api/v1/admin/categories*` (Covered)

### Notifications (Athena-specific)
- Athena: `GET/PUT/DELETE /api/v1/notifications*` + stats (Covered; not a standard PeerTube REST area)

### Messaging (Athena-specific)
- Athena: `/api/v1/messages*`, `/api/v1/conversations*` (Covered; not PeerTube standard)
- E2EE helpers present (Athena-specific)

### Auth / OAuth2
- Athena (legacy): `/auth/register`, `/auth/login`, `/auth/refresh`, `/auth/logout` (Covered)
- Athena (OAuth2):
  - `POST /oauth/token` with `grant_type=password|refresh_token` (Covered)
  - Admin OAuth client management: `GET/POST/PUT/DELETE /api/v1/admin/oauth/clients*` (Covered)
  - Notes: access tokens are JWTs signed with HS256 (same `JWT_SECRET`); refresh tokens reuse Athena sessions. See `docs/OAUTH2.md`.

### Health/Infra
- Athena: `/health`, `/ready` (Covered)

## 2) Design Principles

- Resources: introduce Channels separate from Accounts (users); videos belong to channels.
- Shapes: consistent JSON naming; pagination (`page`, `pageSize`, `total`); standard sorting/filtering.
- Auth: OAuth2 bearer tokens; refine toward authorization code grant + scopes.
- Errors: standardized codes/messages; consistent 401/403/404/409 behavior.
- Privacy/streaming: keep current privacy gating and HLS; expose shapes akin to PeerTube.

## 3) Roadmap (Phases)

Phase 1 — Core Videos + Users (Planned)
- Videos: align list/get/search shapes, unify pagination/meta, standard filters.
- Uploads: converge one‑shot + chunked flows to stable paths and forms.
- OpenAPI: update schemas/paths to target shapes; deprecate legacy fields where needed.
- Tests: table‑driven handler tests for list/get/search; keep current coverage.

Accounts / Channels Model
- Missing: Distinct Channel and Account resources (Athena equates User as channel). Requires DB and ownership model changes.

Playlists
- Missing: CRUD for playlists, items, privacy, watch-later.

Comments
- Missing: Threaded comments, list/create/delete, moderation, mentions.

Ratings / Interactions
- Missing: Reactions (like/dislike), favorites, watch-later toggles, history aligned to PeerTube data model.

Captions / Subtitles
- Missing: Upload/list/delete caption tracks (VTT/SRT), language metadata.

Video Import / Redundancy / Live
- Missing: Import by URL/magnet, redundancy controls, live stream lifecycle & ingest keys.

Instance / Config / oEmbed / Plugins / Moderation
- Missing: Public server config & info endpoints, oEmbed, plugin management, abuse reports and moderation actions.

Federation (non-REST, but essential)
- Missing: ActivityPub, WebFinger, NodeInfo endpoints.

## 3) Shim Route Proposals (Phase 1 scope)

Goal: Provide PeerTube-shaped routes that wrap Athena’s existing handlers, translating requests/responses to expected payloads. Exact PeerTube paths/payloads will be verified against the spec before implementation.

Videos
- Add compatibility layer for list/search/get: map Athena responses to PeerTube field names, pagination meta, and embed-friendly structures.
- Streaming: ensure `GET /api/v1/videos/{id}` includes streaming URLs in PeerTube’s structure (or expose `/api/v1/videos/{id}/streaming-playlists` where expected), while continuing to serve `/{id}/stream` and `/hls/*` for continuity.

Uploads
- Expose PeerTube’s upload endpoints (resumable/one-shot) as thin proxies to Athena’s `/uploads` or `/videos/{id}/upload` flows; align form fields and response contracts.

Users / Channels (interim)
- Until channels are formalized, expose channel-shaped responses derived from `User` and mark as a single default channel; provide IDs and slugs consistent with channel expectations.

Subscriptions
- Adapt payloads (e.g., `accounts`/`channels` schemas) while reusing Athena’s subscription repositories.

Categories
- Align naming/fields to PeerTube’s taxonomy where they differ.

OpenAPI
- Add a `compat` tag set and corresponding routes in `api/openapi.yaml` that reflect PeerTube method/paths while documenting Athena’s native routes. Keep both during transition.

Phase 2 — Channels Model (Planned)
- DB: add `channels` table; backfill default channel per user; add `videos.channel_id`.
- API: list/get channels; update create/update video to accept `channel_id`.
- Subscriptions: migrate to channels (keep user subscribe aliases temporarily).

Phase 3 — OAuth2 Refinement (Planned)
- Authorization Code (+PKCE), token revocation, basic introspection.
- Enforce scopes and improve standardized OAuth error responses.

Phase 4 — Community Features (Future)
- Comments (threaded), Ratings (like/dislike), Playlists, Captions.
- Instance info/config endpoints; oEmbed; moderation endpoints.

Phase 5 — Advanced (Future)
- Live streams; import by URL/magnet; redundancy/mirroring.

## 5) Channels Model Refactor

Rationale
- PeerTube distinguishes Accounts (users) from Channels (publishers). Videos belong to Channels; users manage one or more channels.

DB Changes (Incremental, safe)
1) New `channels` table:
   - id (UUID), account_id (UUID → users.id), handle (unique), display_name, description, avatar_id (nullable), banner fields, created_at, updated_at, is_public, etc.
2) `videos` table: add `channel_id` (nullable initially), backfill default channel per user, then make `channel_id` required; keep `user_id` during migration window.
3) Indexes: `channels(handle)`, `videos(channel_id)`.

API Changes
- New endpoints to list/get channels, create/update my channels, list channel videos.
- Update video create/update to accept `channel_id`; default to user’s default channel if omitted.
- Subscriptions switch to channel-based where relevant.

Migration Plan
1) Create default channel for each existing user (handle = username; display_name = existing display_name).
2) Backfill `videos.channel_id` for all rows using the owner’s default channel.
3) Update repositories/services to prefer `channel_id` but keep `user_id` fallback during transition.
4) Update subscriptions to be against channels; keep compatibility routes for user-subscribe until all consumers migrate.

## 4) Endpoint Checklist (Initial)

Core (Phase 1)
- [x] OAuth2 token endpoint (password + refresh)
- [x] Minimal OAuth client registration (admin endpoints)
- [ ] Videos list/search/get shape and pagination alignment
- [ ] Uploads form/contracts convergence
- [ ] Users payload alignment and pagination

Community (Phase 2)
- [ ] Comments: list/create/delete, thread model, moderation hooks
- [ ] Ratings: like/dislike; favorites; watch later
- [ ] Playlists: CRUD + items + privacy
- [ ] Captions: upload/list/delete; language tags
- [ ] Instance config/about endpoints; oEmbed

Admin/Moderation (Phase 3)
- [ ] Abuse reports endpoints and admin actions
- [ ] Blacklist/blocklist endpoints
- [ ] Server stats/config endpoints

Advanced (Phase 4)
- [ ] Live streaming lifecycle + ingest keys
- [ ] Import by URL/magnet + status
- [ ] Redundancy/mirroring endpoints
- [ ] Plugins API exposure (list/config)

## 5) Next Steps

1) Confirm target shapes and pagination for videos/users; update OpenAPI and handlers accordingly.
2) Expand OAuth2 support: authorization code grant (+PKCE), token revocation/introspection, scope enforcement.
3) Begin channels table/migration scaffolding (default channel per user) and ship read‑only channel endpoints.

Once the above lands, most PeerTube clients should be able to browse, authenticate (OAuth2), and upload/watch via familiar contracts while we layer on community and admin features.
