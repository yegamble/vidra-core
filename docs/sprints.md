# PeerTube Compatibility Sprint Plan

## Sprint A: Channels (Foundations) ✅ **COMPLETED**

- Scope: Separate Channels from Users; videos belong to channels.
- Deliverables:
    - DB: channels table; add videos.channel_id (nullable → backfill → non‑null).
    - Migration: create a default channel per user; backfill videos.channel_id.
    - Repos/Usecase: Channel CRUD (list/get/create/update for owner).
    - HTTP/Routes: GET/POST/PUT /api/v1/channels, GET /api/v1/channels/{id}, GET /api/v1/channels/{id}/videos.
    - OpenAPI: Channels schemas and routes; deprecate "user as channel" language.
    - Tests: repo + handler unit tests; migration sanity tests.
- Success: All videos mapped to a default channel; channel list/get works with pagination (page/pageSize).
- Risks: Long backfill on large datasets; plan idempotent, chunked migration.
- **Status**: ✅ Completed
    - ✅ Created channels table migration (024_create_channels_table.sql)
    - ✅ Added channel_id to videos table (025_add_channel_id_to_videos.sql)
    - ✅ Implemented Channel domain model
    - ✅ Implemented ChannelRepository with full CRUD operations
    - ✅ Implemented ChannelService with business logic
    - ✅ Added channel HTTP handlers
    - ✅ Added channel API routes at `/api/v1/channels`
    - ✅ Added `/api/v1/users/me/channels` endpoint
    - ✅ Updated Video domain model to include channel_id
    - ✅ Build successful - all compilation errors resolved

## Sprint B: Subscriptions → Channels 🚀 **READY TO START**

- Scope: Move subscriptions from users to channels (with compatibility).
- Deliverables:
    - DB: subscriptions to reference channel_id; migrate old rows; dual‑write/dual‑read if needed.
    - HTTP: Switch GET /api/v1/videos/subscriptions to channel‑based; add POST/DELETE /api/v1/channels/{id}/subscribe.
    - Compatibility: Keep user subscribe endpoints as thin shims for 1 version with deprecation notice.
    - Notifications: Update to use channel_id origin.
    - OpenAPI: new channel subscription endpoints; update docs + examples.
    - Tests: feed correctness; backward compatibility.
- Success: Feed shows channel videos; old user subscribe endpoints still function via shims.
- **Status**: User-based subscriptions exist at `/api/v1/users/{id}/subscribe`. Cannot migrate to channels until Sprint A completes.

## Sprint C: Comments (Threads) + Moderation Basics ❌ **NOT STARTED**

- Scope: Add threaded comments with basic moderation.
- Deliverables:
    - DB: comments table (id, video_id, user_id, parent_id, body, status, timestamps).
    - Repos/Usecase: CRUD with threading (parent_id), soft delete, flagging.
    - HTTP: GET/POST /api/v1/videos/{id}/comments, DELETE /api/v1/comments/{id} (owner/mod), POST /api/v1/comments/{id}/flag.
    - OpenAPI: comment models, endpoints, pagination.
    - Tests: unit + handler; simple abuse flows.
- Success: Threaded comments visible; moderation actions available to owner/admin.
- **Status**: No comments table or implementation found.

## Sprint D: Ratings + Playlists ❌ **NOT STARTED**

- Scope: Like/Dislike and user playlists.
- Deliverables:
    - DB: video_ratings (user_id, video_id, value), playlists + playlist_items with privacy.
    - HTTP: PUT /api/v1/videos/{id}/rating, GET /api/v1/videos/{id}/rating; GET/POST/PUT/DELETE /api/v1/playlists*.
    - OpenAPI: endpoints; include rating summary in video GET; playlists CRUD.
    - Tests: ratings (idempotent), playlist ordering, privacy.
- Success: Ratings reflected in aggregates; playlists list/get/add/remove works.
- **Status**: No ratings or playlists tables/implementation found.

## Sprint E: Captions/Subtitles ❌ **NOT STARTED**

- Scope: VTT/SRT tracks per video.
- Deliverables:
    - DB: captions (video_id, lang, path|cid, label).
    - Storage: store/pin caption files.
    - HTTP: POST/GET/DELETE /api/v1/videos/{id}/captions, include in video GET.
    - OpenAPI: caption endpoints and schema.
    - Tests: upload validation (lang, type), list/remove, GET payload updated.
- Success: Clients can add/list captions; video responses include caption tracks.
- **Status**: No captions table or implementation found.

## Sprint F: OAuth2 (Auth Code + Scopes) ✅ **PARTIALLY COMPLETE**

- Scope: Round out OAuth2 toward client parity.
- Deliverables:
    - Flow: Authorization Code (+PKCE), token revocation, basic introspection.
    - Scopes: define basic, upload, moderation, etc.; enforce on protected routes.
    - Error: standardized OAuth error responses (WWW-Authenticate) where applicable.
    - OpenAPI: OAuth flows; client registration note; examples.
    - Tests: happy paths + failure modes; scope gating.
- Success: Third‑party clients authenticate via auth code; scopes enforced.
- **Status**:
    - ✅ Basic OAuth2 password grant implemented at `/oauth/token`
    - ✅ OAuth client management at `/api/v1/admin/oauth/clients`
    - ❌ Authorization Code flow NOT implemented
    - ❌ PKCE NOT implemented
    - ❌ Token revocation/introspection NOT implemented
    - ❌ Scopes NOT implemented

## Sprint G: Admin + Instance Info + oEmbed ❌ **NOT STARTED**

- Scope: Admin endpoints + instance metadata.
- Deliverables:
    - HTTP: abuse reports CRUD, basic blocklist/blacklist endpoints, instance "about/config".
    - oEmbed: /oembed returning basic video embed info.
    - OpenAPI: admin routes; instance info; oEmbed schema.
    - Tests: admin gating + basic flows; oEmbed smoke tests.
- Success: Instance introspection documented; mod tooling available.
- **Status**: Only OAuth client admin endpoints exist. No instance info, oEmbed, or moderation tools.

## Sprint H: Federation I — Foundations ❌ **NOT STARTED**

- Scope: ActivityPub groundwork.
- Deliverables:
    - Discovery: WebFinger, NodeInfo.
    - Actors: server/channel/user actors and their JSON‑LD representations.
    - Crypto: HTTP Signatures (incoming/outgoing), verification middleware.
    - Infrastructure: inbox/outbox endpoints; queue + retry scaffolding.
    - OpenAPI/Docs: document non‑REST surfaces + configuration.
- Success: Instance can serve identity metadata; accept signed requests to inbox (validate + store).
- **Status**: No federation implementation found.

## Sprint I: Federation II — Publish/Consume Videos ❌ **NOT STARTED**

- Scope: Basic activities for videos.
- Deliverables:
    - Outgoing: Create/Update/Delete for videos (public).
    - Incoming: consume remote Create activities; store foreign videos and minimal metadata.
    - Timeline: lightweight "federated feed" to surface remote content.
    - Tests: signature, persistence, dedupe, simple conflict handling.
- Success: Remote-to-local video propagation works for public content.
- **Status**: Depends on Sprint H.

## Sprint J: Federation III — Social Actions ❌ **NOT STARTED**

- Scope: Follows/Announce; comments/likes federation.
- Deliverables:
    - Follows: follow/unfollow channels; process Accept/Reject; announce activity propagation.
    - Likes/Comments: federate likes and comments; map to local models.
    - Moderation: ignore/block logic for remote spam/abuse.
    - Tests: end‑to‑end flows with mock remote.
- Success: Follows and basic interactions roundtrip across instances.
- **Status**: Depends on Sprints H & I.

## Sprint K: Federation IV — Hardening ❌ **NOT STARTED**

- Scope: Reliability, moderation, operator UX.
- Deliverables:
    - Queue: exponential backoff, DLQ, idempotency.
    - Security: stricter signature/time window checks; request size limits.
    - Moderation: instance and actor blocklists, abuse workflows across federation.
    - Observability: metrics + dashboards for federation health.
- Success: Robust, observable federation with operational controls.
- **Status**: No federation implementation to harden.

---

## Current Implementation Status Summary

### ✅ Completed Features (Not in original sprints)
- **Notifications API**: Full CRUD, stats, unread counts at `/api/v1/notifications`
- **Messaging System**: Messages and conversations with E2EE support at `/api/v1/messages` and `/api/v1/conversations`
- **Views/Analytics**: Video views tracking, analytics, and daily stats
- **User Avatars**: Avatar upload with IPFS pinning
- **Basic OAuth2**: Password grant type with client management

### ⚠️ Partially Completed
- **OAuth2 (Sprint F)**: Basic password grant implemented, missing Authorization Code flow and scopes
- **Subscriptions**: User-based subscriptions exist, but not channel-based as PeerTube requires
- **Categories**: Database support exists (migration 018), but no API routes visible

### ❌ Not Started (Core PeerTube Requirements)
- **Sprint A**: Channels - Critical foundation missing
- **Sprint C**: Comments - Essential for user engagement
- **Sprint D**: Ratings & Playlists - Core interaction features
- **Sprint E**: Captions/Subtitles - Accessibility requirement
- **Sprint G-K**: Admin tools, instance info, federation

## Recommended Next Steps

### 🎯 Priority 1: Foundation (Sprint A - Channels)
Channels are the foundation of PeerTube's content model. Without channels:
- Cannot properly implement subscriptions (Sprint B)
- Videos lack proper ownership model
- Federation cannot work correctly (Sprints H-K)

**Action Items**:
1. Create channels table migration
2. Implement channel repository and usecase
3. Add channel API routes
4. Migrate existing videos to default channels
5. Update video creation to require channel_id

### 🎯 Priority 2: Core Interactions (Sprints C & D)
**Comments** and **Ratings** are essential for user engagement:
- Implement comments with threading support
- Add like/dislike functionality
- Create playlist management

### 🎯 Priority 3: Complete OAuth2 (Sprint F)
**Authorization Code flow** is required for third-party clients:
- Implement Authorization Code grant with PKCE
- Add scope definitions and enforcement
- Implement token revocation and introspection

### 🎯 Priority 4: Accessibility (Sprint E)
**Captions/Subtitles** for video accessibility:
- VTT/SRT file upload and storage
- Language metadata management
- Include caption tracks in video responses

### 💡 Recommendations

1. **Focus on Core PeerTube Compatibility**: The project has implemented useful features (notifications, messaging) but lacks core PeerTube requirements. Consider prioritizing PeerTube compatibility features.

2. **Sequential Sprint Execution**: Sprint A (Channels) must be completed first as it blocks Sprint B and affects federation. Don't skip ahead.

3. **Testing Strategy**:
   - Add PeerTube client compatibility tests
   - Test with actual PeerTube clients (web UI, mobile apps)
   - Ensure API response shapes match PeerTube exactly

4. **Categories API**: The database support exists but needs API routes implemented. This could be a quick win.

5. **Consider Scope Reduction**: Federation (Sprints H-K) is complex and could be deferred until core features are solid.

## Testing Status
- ✅ Unit tests are passing (`make test-unit`)
- ⚠️ Need integration tests for Sprint implementations
- ⚠️ Need PeerTube client compatibility tests

## Next Immediate Actions
1. Start Sprint A (Channels) implementation
2. Complete Categories API (quick win - DB already exists)
3. Finish OAuth2 Authorization Code flow
4. Then proceed to Comments (Sprint C)