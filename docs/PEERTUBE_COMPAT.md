# PeerTube REST Compatibility Plan (Athena)

This document maps Athena’s current API to the PeerTube REST API surface, identifies gaps, and proposes shims and refactors to reach practical compatibility.

Notes:
- Network access to the official PeerTube REST docs is disabled in this environment, so endpoint names below for “PeerTube side” are based on common/typical PeerTube groupings. We’ll refine exact paths and payloads once the spec is synced locally.
- Status legend: Covered = Athena provides equivalent capability; Partial = feature exists but path/payload differ; Missing = not implemented.

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

### Auth
- Athena: `/auth/register`, `/auth/login`, `/auth/refresh`, `/auth/logout` (Covered; Not OAuth2-compatible)

### Health/Infra
- Athena: `/health`, `/ready` (Covered)

## 2) Gaps vs PeerTube (high-level)

Authentication / OAuth2
- Missing: OAuth2 flows (client registration, token endpoint with grant types, token revocation). Athena currently uses custom JWT.

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

## 4) OAuth2 Integration Outline

Objectives
- Implement OAuth2-compatible endpoints to support common PeerTube clients: token issuance via password/refresh (initial), with a path to authorization code later.

Proposed Endpoints
- Token: `POST` token endpoint supporting `grant_type=password|refresh_token` mapped to Athena’s users and session storage.
- Client registration (local): a minimal endpoint to create/register OAuth clients and store secrets securely.
- Token revocation & introspection (basic): optional for Phase 1; else emulate via session blacklist.

Implementation Plan
1) Storage: Add `oauth_clients` table (client_id, name, secret hash, grant_types, redirect_uris, scopes, created_at). Reuse Redis + DB for sessions/refresh.
2) Token issuance: Implement grant handlers that authenticate users (password) and issue access + refresh tokens with scope claims; rotate refresh.
3) Middleware: Accept OAuth2 Bearer tokens as first-class, align error responses (`WWW-Authenticate`) where appropriate.
4) Backward compatibility: Keep `/auth/login` etc. for now; gradually migrate clients to OAuth2.

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

## 6) Endpoint Checklist (initial)

Core (Phase 1)
- [ ] OAuth2 token endpoint (password + refresh)
- [ ] Minimal OAuth client registration
- [ ] Shim: videos list/search/get payload mapping
- [ ] Shim: upload endpoints to PeerTube forms/contracts
- [ ] Shim: user/channel payloads for listings
- [ ] OpenAPI: add `compat` routes matching PeerTube spec

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

## 7) Next Steps

1) Confirm exact PeerTube endpoint paths/payloads (sync docs locally or provide a copy).
2) Add `compat` OpenAPI paths for videos/users/uploads first (low risk, high value).
3) Implement OAuth2 token endpoint + minimal clients storage.
4) Begin channels table/migration scaffolding (default channel per user) and ship read-only channel endpoints.

Once the above lands, most PeerTube clients should be able to browse, authenticate (OAuth2), and upload/watch via familiar contracts while we layer on community and admin features.

