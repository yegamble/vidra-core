# Postman E2E Test Coverage Audit

**Date:** 2026-02-15

---

## API Surface Inventory

Based on `internal/httpapi/routes.go`, the API has the following endpoint groups:

### Authentication & Users (~15 endpoints)

- `POST /auth/register`
- `POST /auth/login`
- `POST /auth/refresh`
- `POST /auth/logout`
- `POST /auth/2fa/setup`
- `POST /auth/2fa/verify-setup`
- `POST /auth/2fa/disable`
- `POST /auth/2fa/regenerate-backup-codes`
- `GET /auth/2fa/status`
- `POST /oauth/token`
- `GET /oauth/authorize`
- `POST /oauth/revoke`
- `POST /oauth/introspect`
- `GET /api/v1/users/me`
- `PUT /api/v1/users/me`
- `POST /api/v1/users/me/avatar`

### Videos (~12 endpoints)

- `GET /api/v1/videos`
- `GET /api/v1/videos/search`
- `GET /api/v1/videos/qualities`
- `GET /api/v1/videos/top`
- `POST /api/v1/videos`
- `POST /api/v1/videos/upload`
- `GET /api/v1/videos/{id}`
- `PUT /api/v1/videos/{id}`
- `DELETE /api/v1/videos/{id}`
- `GET /api/v1/videos/{id}/stream`
- `POST /api/v1/videos/{id}/upload`
- `POST /api/v1/videos/{id}/complete`

### Uploads (~4 endpoints)

- `POST /api/v1/uploads/initiate`
- `POST /api/v1/uploads/{sessionId}/chunks`
- `POST /api/v1/uploads/{sessionId}/complete`
- `GET /api/v1/uploads/{sessionId}/status`
- `GET /api/v1/uploads/{sessionId}/resume`

### Views & Analytics (~6 endpoints)

- `POST /api/v1/videos/{id}/views`
- `GET /api/v1/videos/{id}/analytics`
- `GET /api/v1/videos/{id}/stats/daily`
- `GET /api/v1/trending`
- `POST /api/v1/views/fingerprint`

### Comments (~7 endpoints)

- `GET /api/v1/videos/{videoId}/comments`
- `POST /api/v1/videos/{videoId}/comments`
- `GET /api/v1/comments/{commentId}`
- `PUT /api/v1/comments/{commentId}`
- `DELETE /api/v1/comments/{commentId}`
- `POST /api/v1/comments/{commentId}/flag`
- `POST /api/v1/comments/{commentId}/moderate`

### Ratings (~3 endpoints)

- `PUT /api/v1/videos/{id}/rating`
- `GET /api/v1/videos/{id}/rating`
- `DELETE /api/v1/videos/{id}/rating`

### Channels (~8 endpoints)

- `GET /api/v1/channels`
- `GET /api/v1/channels/{id}`
- `POST /api/v1/channels`
- `PUT /api/v1/channels/{id}`
- `DELETE /api/v1/channels/{id}`
- `GET /api/v1/channels/{id}/videos`
- `GET /api/v1/channels/{id}/subscribers`
- `POST /api/v1/channels/{id}/subscribe`
- `DELETE /api/v1/channels/{id}/subscribe`

### Playlists (~7 endpoints)

- `GET /api/v1/playlists`
- `GET /api/v1/playlists/{id}`
- `POST /api/v1/playlists`
- `PUT /api/v1/playlists/{id}`
- `DELETE /api/v1/playlists/{id}`
- `POST /api/v1/playlists/{id}/items`
- `DELETE /api/v1/playlists/{id}/items/{itemId}`
- `PUT /api/v1/playlists/{id}/items/{itemId}/reorder`

### Messaging (~4 endpoints)

- `POST /api/v1/messages`
- `GET /api/v1/messages`
- `PUT /api/v1/messages/{messageId}/read`
- `DELETE /api/v1/messages/{messageId}`

### Conversations (~2 endpoints)

- `GET /api/v1/conversations`
- `GET /api/v1/conversations/unread-count`

### Notifications (~6 endpoints)

- `GET /api/v1/notifications`
- `GET /api/v1/notifications/unread-count`
- `GET /api/v1/notifications/stats`
- `PUT /api/v1/notifications/{id}/read`
- `PUT /api/v1/notifications/read-all`
- `DELETE /api/v1/notifications/{id}`

### Live Streams (~10+ endpoints)

- `POST /api/v1/streams`
- `GET /api/v1/streams/active`
- `GET /api/v1/streams/{id}`
- `PUT /api/v1/streams/{id}`
- `POST /api/v1/streams/{id}/end`
- `GET /api/v1/streams/{id}/stats`
- `POST /api/v1/streams/{id}/rotate-key`
- `GET /api/v1/streams/{id}/hls/master.m3u8`
- `GET /api/v1/streams/{id}/hls/{variant}/index.m3u8`
- `GET /api/v1/streams/{id}/hls/{variant}/{segment}`

### Encoding (~3 endpoints)

- `GET /api/v1/encoding/status`
- `GET /api/v1/encoding/jobs/{jobID}`
- `GET /api/v1/encoding/my-jobs`

### Payments (~5 endpoints)

- `POST /api/v1/payments/wallet`
- `GET /api/v1/payments/wallet`
- `POST /api/v1/payments/intents`
- `GET /api/v1/payments/intents/{id}`
- `GET /api/v1/payments/transactions`

### Captions (~5 endpoints)

- `GET /api/v1/videos/{id}/captions`
- `POST /api/v1/videos/{id}/captions`
- `GET /api/v1/videos/{id}/captions/{captionId}/content`
- `PUT /api/v1/videos/{id}/captions/{captionId}`
- `DELETE /api/v1/videos/{id}/captions/{captionId}`

### Moderation/Admin (~15+ endpoints)

- `POST /api/v1/abuse-reports`
- `GET /api/v1/admin/abuse-reports`
- `GET /api/v1/admin/abuse-reports/{id}`
- `PUT /api/v1/admin/abuse-reports/{id}`
- `DELETE /api/v1/admin/abuse-reports/{id}`
- `POST /api/v1/admin/blocklist`
- `GET /api/v1/admin/blocklist`
- And 8+ more admin endpoints

### Federation (~20+ endpoints)

- ActivityPub well-known endpoints (5)
- Federation hardening (12+)
- Admin federation jobs/actors (8+)
- Federation timeline (1)

### Health (~2 endpoints)

- `GET /health`
- `GET /ready`

**Total: ~135+ unique API endpoints**

---

## Existing Postman Collections

| Collection | File Size | Coverage Area |
|------------|-----------|---------------|
| `vidra-auth.postman_collection.json` | 3,786 lines | Auth, registration, login, OAuth |
| `vidra-virus-scanner-tests.postman_collection.json` | 1,130 lines | Virus scanning edge cases |
| `vidra-edge-cases-security.postman_collection.json` | 997 lines | Security edge cases |
| `vidra-registration-edge-cases.postman_collection.json` | 927 lines | Registration edge cases |
| `vidra-analytics.postman_collection.json` | 807 lines | Analytics endpoints |
| `vidra-uploads.postman_collection.json` | 739 lines | Upload endpoints |
| `vidra-imports.postman_collection.json` | 664 lines | Import endpoints |
| `vidra-encoding-jobs.postman_collection.json` | 456 lines | Encoding job endpoints |

**Total: 8 collections, ~9,506 lines**

---

## Coverage Matrix

| Endpoint Group | Postman Collection? | Coverage Level |
|---------------|-------------------|----------------|
| Auth/Registration | **Yes** (auth, registration-edge-cases) | Good |
| OAuth | **Yes** (auth) | Partial |
| 2FA | **No** | **MISSING** |
| Videos CRUD | **No** | **MISSING** |
| Video Search | **No** | **MISSING** |
| Uploads (chunked) | **Yes** (uploads) | Partial |
| Video Imports | **Yes** (imports) | Partial |
| Encoding Jobs | **Yes** (encoding-jobs) | Partial |
| Analytics/Views | **Yes** (analytics) | Partial |
| Comments | **No** | **MISSING** |
| Ratings | **No** | **MISSING** |
| Channels | **No** | **MISSING** |
| Playlists | **No** | **MISSING** |
| Messaging | **No** | **MISSING** |
| Conversations | **No** | **MISSING** |
| Notifications | **No** | **MISSING** |
| Live Streams | **No** | **MISSING** |
| HLS Streaming | **No** | **MISSING** |
| Captions | **No** | **MISSING** |
| Payments (IOTA) | **No** | **MISSING** |
| Moderation | **No** | **MISSING** |
| Admin | **No** | **MISSING** |
| Federation/AP | **No** | **MISSING** |
| Federation Hardening | **No** | **MISSING** |
| Health | **No** | **MISSING** |
| Virus Scanner | **Yes** (virus-scanner-tests) | Good |
| Security Edge Cases | **Yes** (edge-cases-security) | Good |
| Trending | **No** | **MISSING** |
| IPFS Metrics | **No** | **MISSING** |

**Coverage: 8 out of ~30 endpoint groups covered (~27%)**

---

## Missing Edge Case Tests (Priority Order)

### Priority 1: Authentication & Authorization

**2FA Endpoint Edge Cases (No collection exists):**

- Setup 2FA with invalid TOTP token
- Verify setup with expired code
- Disable 2FA without valid backup code
- Regenerate codes when 2FA not enabled
- Race condition: two concurrent setup requests
- Status check with expired session

**OAuth Edge Cases:**

- Token request with revoked client
- Authorization with invalid redirect_uri
- Token introspection with expired token
- Concurrent token refresh (race condition)

### Priority 2: Data Mutation Endpoints

**Video CRUD (No collection exists):**

- Create video with missing required fields
- Create video with excessively long title (>200 chars)
- Update video owned by different user (403)
- Delete video that's currently processing
- Get video with non-existent UUID format ID
- Get video with SQL injection in ID parameter
- List videos with negative page number
- List videos with page size > 1000
- Search with XSS payload in query

**Comments (No collection exists):**

- Create comment on non-existent video
- Create comment with empty body
- Update comment owned by different user
- Delete comment as moderator vs. owner
- Flag own comment (should fail?)
- Nested comment depth limit test

**Channels (No collection exists):**

- Create channel with duplicate name
- Delete channel with active videos
- Subscribe to own channel
- Subscribe twice (idempotency)

### Priority 3: Complex Workflows

**Notifications (No collection exists):**

- Get notifications without auth
- Mark non-existent notification as read
- Delete already-deleted notification
- Mark all as read with empty list

**Playlists (No collection exists):**

- Add non-existent video to playlist
- Reorder item to invalid position
- Delete item from non-owned playlist
- Watch Later concurrent additions

**Messaging (No collection exists):**

- Send message to self
- Send message to non-existent user
- Read message from another conversation
- Delete message from non-owned conversation

### Priority 4: Live Streaming & Media

**Live Streams (No collection exists):**

- Create stream without channel
- End already-ended stream
- Access HLS for non-live stream
- Stream key rotation while live
- Get stats for non-existent stream

**Captions (No collection exists):**

- Upload caption with invalid format
- Upload duplicate language caption
- Get caption content with invalid captionId

### Priority 5: Admin & Federation

**Admin (No collection exists):**

- Access admin endpoints as regular user (403)
- Abuse report lifecycle (create -> list -> update -> delete)
- Blocklist management

**Federation (No collection exists):**

- WebFinger for non-existent user
- NodeInfo endpoint validation
- Invalid HTTP signature

---

## Recommendations

### Immediate Actions

1. **Create `vidra-videos.postman_collection.json`** - Video CRUD is the core feature and has zero Postman coverage
2. **Create `vidra-channels.postman_collection.json`** - Second most important CRUD collection
3. **Create `vidra-social.postman_collection.json`** - Comments, ratings, playlists in one collection
4. **Create `vidra-2fa.postman_collection.json`** - Security-critical, no coverage

### Medium-Term

5. Create collections for: notifications, messaging, live streams, captions
6. Add environment files for different configurations (dev, CI, with-IPFS, without-IPFS)
7. Add response schema validation to existing collections (not just status code checks)

### Long-Term

8. Create federation endpoint collection (requires multi-instance setup)
9. Create admin endpoint collection
10. Create health/monitoring collection
11. Add performance assertions (response time < 500ms for reads)

---

## Summary

| Metric | Value |
|--------|-------|
| Total API endpoints | ~135+ |
| Endpoint groups | ~30 |
| Postman collections | 8 |
| Groups with coverage | 8 (~27%) |
| Groups with NO coverage | 22 (~73%) |
| Priority gaps | Videos, Channels, Comments, 2FA |
