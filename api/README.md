# Vidra Core API Documentation

This directory contains the 39 OpenAPI 3.0 specifications for the Vidra Core video platform API, covering the complete API surface with full PeerTube v8.1.0 parity.

The canonical HTTP surface lives in `internal/httpapi/routes.go`. The specs in `api/` cover all domains.

## Documentation Structure

The API documentation is split into modular files by functional domain for better maintainability:

### Primary Specs

1. **`openapi.yaml`** - Main specification
   - Authentication (register, login, refresh, logout)
   - OAuth 2.0 endpoints (token, authorize, revoke, introspect)
   - Admin OAuth client management
   - User messaging (basic endpoints)
   - E2EE encrypted messaging
   - Core video endpoints (CRUD)
   - Notifications, categories, collaborators, runners, and other PeerTube-compatible/admin routes

2. **`openapi_auth_2fa.yaml`** - Canonical Two-Factor Authentication reference
   - TOTP setup and verification (RFC 6238)
   - Backup code generation (10 one-time recovery codes)
   - QR code generation for authenticator apps
   - 2FA disable with password + code verification
   - Backup code regeneration
   - 2FA status endpoint

3. **`openapi_uploads.yaml`** - Video Upload & Encoding
   - Chunked upload workflow (initiate, upload chunks, complete)
   - Upload session management
   - Resume capabilities for interrupted uploads
   - Legacy one-shot upload (backward compatibility)
   - Encoding status tracking (Redis-backed)

4. **`openapi_analytics.yaml`** - Video Analytics & Views
   - View tracking with fingerprint deduplication
   - Video analytics (owner/admin access)
   - Daily statistics for time-series charts
   - Top videos by views
   - Trending algorithm (velocity, engagement, recency)
   - Fingerprint generation for view deduplication

5. **`openapi_livestreaming.yaml`** - Live Streaming
   - Stream management (create, update, end, stats)
   - RTMP ingest configuration
   - HLS transcoding and delivery
   - Stream key rotation
   - Multi-quality variants (360p-1080p)
   - Master and variant playlists
   - Segment delivery
   - Session history
   - Waiting-room, scheduling, and stream-analytics routes exist in code but are not yet fully reflected here

6. **`openapi_imports.yaml`** - Video Imports
   - Import from external URLs
   - Import job management (create, list, status, cancel)
   - SSRF protection documentation
   - Rate limiting (10 imports/minute)
   - Progress tracking

7. **`openapi_ipfs.yaml`** - IPFS Streaming Diagnostics
   - IPFS delivery metrics
   - Gateway health visibility
   - Vidra Core-specific operational endpoints

### Domain Specs

8. **`openapi_comments.yaml`** - Comment System
   - Video comments (CRUD)
   - Comment flagging and moderation
   - Nested comment support

9. **`openapi_channels.yaml`** - Channel Management
   - Channel CRUD operations
   - Channel subscriptions
   - Subscriber listing
   - Channel videos

10. **`openapi_captions.yaml`** - Video Captions/Subtitles
   - Caption upload (VTT, SRT formats)
   - Caption metadata management
   - IPFS storage integration
   - Auto-generated caption support

11. **`openapi_ratings_playlists.yaml`** - Ratings & Playlists
    - Video like/dislike system
    - Playlist management (create, update, delete)
    - Watch Later special playlist
    - Playlist item reordering

12. **`openapi_moderation.yaml`** - Moderation & Instance Config
    - Abuse reports
    - Blocklist management
    - Instance configuration (admin)
    - oEmbed endpoint

13. **`openapi_chat.yaml`** - WebSocket Chat
    - Live chat for streams (10,000+ concurrent connections)
    - Role-based moderation (owner/moderator permissions)
    - User bans (temporary/permanent) and timeouts
    - Message history with soft deletes
    - Rate limiting (5 msg/10s users, 10 msg/10s moderators)
    - Chat statistics and analytics

14. **`openapi_federation.yaml`** - ActivityPub Federation
    - Federation timeline
    - ActivityPub discovery endpoints (.well-known)
    - Actor endpoints (inbox, outbox, followers, following)

15. **`openapi_federation_hardening.yaml`** - Federation Security
    - Dead letter queue management
    - Instance/actor blocklists
    - Abuse reports
    - Dashboard and health metrics

16. **`openapi_plugins.yaml`** - Plugin System
    - Plugin installation and management
    - Enable/disable plugins
    - Plugin configuration
    - Plugin statistics

17. **`openapi_redundancy.yaml`** - Video Redundancy
    - Peer management
    - Redundancy policies
    - Synchronization

18. **`openapi_social.yaml`** - Social & ATProto
    - ATProto actor resolution and stats
    - Follow graph endpoints
    - Likes, comments, labels, and ingest routes

19. **`openapi_payments.yaml`** - IOTA Payments (beta / feature-flagged)
    - Wallet creation and retrieval
    - Payment intents
    - Transaction history

20. **`openapi_admin.yaml`** - Admin Management
    - User management, server config, instance admin endpoints

21. **`openapi_backup.yaml`** - Backup & Restore
    - Backup creation, listing, restore operations

22. **`openapi_extensions.yaml`** - Vidra Core Extensions
    - Video ownership change, storyboards, custom routes

23. **`openapi_messaging.yaml`** - User Messaging
    - Direct messages, conversations, E2EE messaging endpoints

24. **`openapi_runners.yaml`** - Remote Runners
    - Runner registration, job lifecycle, file transfer

25. **`openapi_notifications.yaml`** - Notifications
    - Notification listing, read status, batch operations, settings

26. **`openapi_watched_words.yaml`** - Watched Words
    - Server and account-level word filters, CRUD operations

27. **`openapi_auto_tags.yaml`** - Automatic Tags
    - Available tags, compute tags, auto-tag policies

28. **`openapi_video_passwords.yaml`** - Video Passwords
    - Password-protected video access, set/verify/list/delete

29. **`openapi_video_storyboards.yaml`** - Video Storyboards
    - Storyboard sprite generation and retrieval

30. **`openapi_video_embed_privacy.yaml`** - Video Embed Privacy
    - Embed allow-listing configuration

31. **`openapi_video_files.yaml`** - Video File Management
    - Delete HLS files, delete web-video files

32. **`openapi_compat_aliases.yaml`** - PeerTube Compatibility Aliases
    - PeerTube-compatible URL path aliases

33. **`openapi_static.yaml`** - Static File Serving
    - Static web-video and streaming-playlist file serving

34. **`openapi_channel_sync.yaml`** - Channel Sync
    - Channel synchronization with external feeds

35. **`openapi_player_settings.yaml`** - Player Settings
    - Per-user video player configuration

36. **`openapi_server_debug.yaml`** - Server Debug
    - Admin debug info and diagnostic endpoints

37. **`openapi_user_archives.yaml`** - User Data Archives
    - User data export and import operations

38. **`openapi_video_studio.yaml`** - Video Studio Editing
    - Server-side video editing jobs (cut, intro, outro, watermark)
    - Job creation, listing, and status tracking

39. **`openapi_migration.yaml`** - Migration ETL
    - PeerTube dump import pipeline
    - Import, list jobs, get job, cancel, dry-run

### Legacy / Compatibility Specs

- **`../docs/openapi_notifications.yaml`** - Older standalone notifications spec; canonical notification routes live in `openapi.yaml`

---

## Current Sync Status

- All 39 specs cover the complete Vidra Core API surface.
- All operationIds are unique across all specs (65 duplicates were resolved with domain prefixes).
- All specs standardized to OpenAPI 3.0.3.
- The router in `internal/httpapi/routes.go` remains the implementation source of truth.

---

## Using the Documentation

### View in Swagger UI

You can view the API documentation using any OpenAPI-compatible tool:

```bash
# Using Swagger UI Docker
docker run -p 8080:8080 \
  -e URLS="[ \
    {url: '/api/openapi.yaml', name: 'Main API'}, \
    {url: '/api/openapi_auth_2fa.yaml', name: 'Two-Factor Auth (2FA)'}, \
    {url: '/api/openapi_uploads.yaml', name: 'Uploads & Encoding'}, \
    {url: '/api/openapi_analytics.yaml', name: 'Analytics & Views'}, \
    {url: '/api/openapi_livestreaming.yaml', name: 'Live Streaming'}, \
    {url: '/api/openapi_imports.yaml', name: 'Video Imports'}, \
    {url: '/api/openapi_ipfs.yaml', name: 'IPFS Streaming'}, \
    {url: '/api/openapi_comments.yaml', name: 'Comments'}, \
    {url: '/api/openapi_channels.yaml', name: 'Channels'}, \
    {url: '/api/openapi_captions.yaml', name: 'Captions'}, \
    {url: '/api/openapi_ratings_playlists.yaml', name: 'Ratings & Playlists'}, \
    {url: '/api/openapi_social.yaml', name: 'Social & ATProto'}, \
    {url: '/api/openapi_chat.yaml', name: 'Chat'}, \
    {url: '/api/openapi_moderation.yaml', name: 'Moderation'}, \
    {url: '/api/openapi_federation.yaml', name: 'Federation'}, \
    {url: '/api/openapi_federation_hardening.yaml', name: 'Federation Security'}, \
    {url: '/api/openapi_plugins.yaml', name: 'Plugins'}, \
    {url: '/api/openapi_redundancy.yaml', name: 'Redundancy'}, \
    {url: '/api/openapi_payments.yaml', name: 'Payments'}, \
    {url: '/api/openapi_admin.yaml', name: 'Admin'}, \
    {url: '/api/openapi_backup.yaml', name: 'Backup & Restore'}, \
    {url: '/api/openapi_extensions.yaml', name: 'Vidra Core Extensions'}, \
    {url: '/api/openapi_messaging.yaml', name: 'Messaging'}, \
    {url: '/api/openapi_runners.yaml', name: 'Runners'}, \
    {url: '/api/openapi_notifications.yaml', name: 'Notifications'}, \
    {url: '/api/openapi_watched_words.yaml', name: 'Watched Words'}, \
    {url: '/api/openapi_auto_tags.yaml', name: 'Auto Tags'}, \
    {url: '/api/openapi_video_passwords.yaml', name: 'Video Passwords'}, \
    {url: '/api/openapi_video_storyboards.yaml', name: 'Video Storyboards'}, \
    {url: '/api/openapi_video_embed_privacy.yaml', name: 'Video Embed Privacy'}, \
    {url: '/api/openapi_video_files.yaml', name: 'Video Files'}, \
    {url: '/api/openapi_compat_aliases.yaml', name: 'Compat Aliases'}, \
    {url: '/api/openapi_static.yaml', name: 'Static Files'}, \
    {url: '/api/openapi_channel_sync.yaml', name: 'Channel Sync'}, \
    {url: '/api/openapi_player_settings.yaml', name: 'Player Settings'}, \
    {url: '/api/openapi_server_debug.yaml', name: 'Server Debug'}, \
    {url: '/api/openapi_user_archives.yaml', name: 'User Archives'}, \
    {url: '/api/openapi_video_studio.yaml', name: 'Video Studio'}, \
    {url: '/api/openapi_migration.yaml', name: 'Migration ETL'} \
  ]" \
  -v $(pwd)/api:/usr/share/nginx/html/api \
  swaggerapi/swagger-ui
```

### Generate Client SDKs

Use OpenAPI Generator to create client libraries:

```bash
# JavaScript/TypeScript
openapi-generator-cli generate \
  -i api/openapi.yaml \
  -g typescript-fetch \
  -o clients/typescript

# Python
openapi-generator-cli generate \
  -i api/openapi.yaml \
  -g python \
  -o clients/python

# Go
openapi-generator-cli generate \
  -i api/openapi.yaml \
  -g go \
  -o clients/go
```

### Validate Specifications

```bash
# Install validator
npm install -g @apidevtools/swagger-cli

# Validate all primary specs plus the legacy notifications spec
for spec in api/openapi*.yaml docs/openapi*.yaml; do
  [ -f "$spec" ] || continue
  echo "Validating $spec..."
  swagger-cli validate "$spec"
done
```

---

## API Design Principles

### 1. **Modular Structure**

- Each domain has its own OpenAPI file
- Main spec contains core functionality
- Easy to navigate and maintain

### 2. **Security First**

- JWT Bearer authentication for protected endpoints
- OAuth 2.0 for third-party applications
- Rate limiting documented for critical endpoints
- SSRF protection on import endpoints

### 3. **Consistency**

- Standard error response format across all endpoints
- Consistent pagination (limit/offset or page/pageSize)
- Uniform date-time format (ISO 8601)
- UUIDs for resource identifiers

### 4. **Comprehensive Examples**

- Request examples for common use cases
- Response examples for success and error cases
- Real-world scenario documentation

### 5. **Privacy & Access Control**

- Public endpoints support both authenticated and anonymous access
- Private resources require authentication
- Owner-only operations clearly marked

---

## Common Patterns

### Authentication

```http
Authorization: Bearer <jwt_access_token>
```

### Standard Response Format

**Success:**

```json
{
  "data": { /* resource or collection */ },
  "success": true
}
```

**Error:**

```json
{
  "error": {
    "code": "ERROR_CODE",
    "message": "Human-readable message",
    "details": { /* optional additional context */ }
  },
  "success": false
}
```

### Pagination

**Query Parameters:**

- `limit`: Number of items (1-100, default 20)
- `offset`: Offset for pagination (default 0)

**Response:**

```json
{
  "data": {
    "items": [...],
    "total": 500,
    "limit": 20,
    "offset": 0
  },
  "success": true
}
```

---

## Changelog

### 2026-03-23 - PeerTube 100% Parity: 39 Spec Files

**Added (WS1-WS9):**
- 20 new OpenAPI spec files covering all remaining PeerTube categories
- Video Studio editing, Migration ETL, watched words, auto-tags, video passwords, storyboards, embed privacy, file management, channel sync, player settings, server debug, user archives, static files, compatibility aliases, admin, backup, extensions, messaging, runners, notifications

**Fixed:**
- 65 duplicate operationIds resolved with domain prefixes across 14 specs
- Deleted duplicate `openapi_2fa.yaml` (consolidated into `openapi_auth_2fa.yaml`)
- Fixed `openapi_channels.yaml` from fragment to proper standalone spec

**Result:**
- 39 spec files with all unique operationIds
- Full PeerTube v8.1.0 API parity documented

### 2025-12-04 - Sprint 7 Complete: Enhanced Live Streaming Features

**Updated:**

- ✅ `openapi_livestreaming.yaml` - Added scheduling and analytics endpoints
  - 6 scheduling endpoints (waiting rooms, scheduled streams, upcoming streams)
  - 7 analytics endpoints (real-time metrics, session tracking, engagement)
  - Total: 25 endpoints (12 streaming + 6 scheduling + 7 analytics)
- ✅ `openapi_chat.yaml` - Updated with Sprint 7 enhancements
  - 10 endpoints for WebSocket chat (up from 8)
  - 10,000+ concurrent connection support
  - Role-based moderation, bans, and rate limiting

**Result:**

- API coverage maintained at 98%+
- All Sprint 7 features (chat, scheduling, analytics) fully documented
- Total endpoints increased from ~155 to ~173

### 2025-01-06 - Two-Factor Authentication Documentation

**Added:**

- ✅ `openapi_auth_2fa.yaml` - Complete two-factor authentication documentation (5 endpoints)
  - TOTP setup and verification (RFC 6238 compliant)
  - Backup code generation and management
  - QR code generation for authenticator apps
  - 2FA disable with dual verification (password + code)
  - 2FA status endpoint

**Result:**

- API coverage increased from ~85% to 98%+
- All authentication features now fully documented
- Comprehensive security documentation complete

### 2025-01-25 - Major Documentation Update

**Added:**

- ✅ `openapi_uploads.yaml` - Complete upload and encoding documentation (10 endpoints)
- ✅ `openapi_analytics.yaml` - Views, analytics, and trending (6 endpoints)
- ✅ `openapi_livestreaming.yaml` - Live streaming with HLS (12 endpoints)
- ✅ `openapi_imports.yaml` - Video import system (4 endpoints)
- ✅ This README with comprehensive overview

**Result:**

- API coverage increased from ~60% to ~85%
- 32 critical endpoints now fully documented
- All major features have OpenAPI specs

### 2025-02-06 - Documentation Audit

**Audit:**

- Confirmed 100% API coverage
- Verified User Profile, HLS Static, and Messaging endpoints are fully documented in `openapi.yaml`
- Verified ATProto DID endpoint is documented in `openapi_federation.yaml`
- Removed deprecated references to `openapi_notifications.yaml` (endpoints are in `openapi.yaml`)

**Result:**

- API coverage updated to 100%
- Documentation structure consolidated

---

## Contributing

When adding new API endpoints:

1. Choose the appropriate OpenAPI file (or create new one for new domain)
2. Follow existing patterns for consistency
3. Include comprehensive examples
4. Document security requirements
5. Add rate limiting information if applicable
6. Update this README and keep the sync-status notes honest when code lands ahead of spec coverage

### File Naming Convention

```
openapi_<domain>.yaml
```

Examples: `openapi_uploads.yaml`, `openapi_analytics.yaml`

---

## Support & Contact

For API documentation issues or questions:

- File an issue in the repository
- Check existing endpoint implementations in `/internal/httpapi/`
- Refer to integration tests for usage examples

---

## License

Same license as the main Vidra Core project.
