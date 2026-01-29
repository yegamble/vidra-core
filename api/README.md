# Athena API Documentation

This directory contains comprehensive OpenAPI 3.0 specifications for the Athena video platform API.

## Documentation Structure

The API documentation is split into modular files by functional domain for better maintainability:

### Core API Files (COMPLETE)

1. **`openapi.yaml`** - Main specification
   - Authentication (register, login, refresh, logout)
   - OAuth 2.0 endpoints (token, authorize, revoke, introspect)
   - Admin OAuth client management
   - User messaging (basic endpoints)
   - E2EE encrypted messaging
   - Core video endpoints (CRUD)
   - Subscriptions

2. **`openapi_auth_2fa.yaml`** - Two-Factor Authentication ✅ **NEW**
   - TOTP setup and verification (RFC 6238)
   - Backup code generation (10 one-time recovery codes)
   - QR code generation for authenticator apps
   - 2FA disable with password + code verification
   - Backup code regeneration
   - 2FA status endpoint
   - **Coverage**: 5 endpoints

3. **`openapi_uploads.yaml`** - Video Upload & Encoding ✅ **NEW**
   - Chunked upload workflow (initiate, upload chunks, complete)
   - Upload session management
   - Resume capabilities for interrupted uploads
   - Legacy one-shot upload (backward compatibility)
   - Encoding status tracking (Redis-backed)
   - **Coverage**: 10 endpoints

4. **`openapi_analytics.yaml`** - Video Analytics & Views ✅ **NEW**
   - View tracking with fingerprint deduplication
   - Video analytics (owner/admin access)
   - Daily statistics for time-series charts
   - Top videos by views
   - Trending algorithm (velocity, engagement, recency)
   - Fingerprint generation for view deduplication
   - **Coverage**: 6 endpoints

5. **`openapi_livestreaming.yaml`** - Live Streaming ✅ **COMPLETE**
   - Stream management (create, update, end, stats)
   - RTMP ingest configuration
   - HLS transcoding and delivery
   - Stream key rotation
   - Stream scheduling with waiting rooms
   - Multi-quality variants (360p-1080p)
   - Master and variant playlists
   - Segment delivery
   - Real-time analytics collection
   - **Coverage**: 25 endpoints (12 streaming + 6 scheduling + 7 analytics)

6. **`openapi_imports.yaml`** - Video Imports ✅ **NEW**
   - Import from external URLs
   - Import job management (create, list, status, cancel)
   - SSRF protection documentation
   - Rate limiting (10 imports/minute)
   - Progress tracking
   - **Coverage**: 4 endpoints

### Domain-Specific Files (ALREADY COMPLETE)

7. **`openapi_comments.yaml`** - Comment System ✅
   - Video comments (CRUD)
   - Comment flagging and moderation
   - Nested comment support

8. **`openapi_channels.yaml`** - Channel Management ✅
   - Channel CRUD operations
   - Channel subscriptions
   - Subscriber listing
   - Channel videos

9. **`openapi_captions.yaml`** - Video Captions/Subtitles ✅
   - Caption upload (VTT, SRT formats)
   - Caption metadata management
   - IPFS storage integration
   - Auto-generated caption support

10. **`openapi_ratings_playlists.yaml`** - Ratings & Playlists ✅
   - Video like/dislike system
   - Playlist management (create, update, delete)
   - Watch Later special playlist
   - Playlist item reordering

11. **`openapi_moderation.yaml`** - Moderation & Instance Config ✅
    - Abuse reports
    - Blocklist management
    - Instance configuration (admin)
    - oEmbed endpoint

12. **`openapi_notifications.yaml`** - User Notifications ✅
    - Notification management
    - Unread count tracking
    - Mark as read (single/bulk)
    - Notification statistics
    - Automatic triggers (new video, message, etc.)

13. **`openapi_chat.yaml`** - WebSocket Chat ✅ **COMPLETE (Sprint 7)**
    - Live chat for streams (10,000+ concurrent connections)
    - Role-based moderation (owner/moderator permissions)
    - User bans (temporary/permanent) and timeouts
    - Message history with soft deletes
    - Rate limiting (5 msg/10s users, 10 msg/10s moderators)
    - Chat statistics and analytics
    - **Coverage**: 10 endpoints

14. **`openapi_federation.yaml`** - Federation (ActivityPub + ATProto) ✅
    - ActivityPub: WebFinger, NodeInfo, Inbox/Outbox, Actor Profiles
    - ATProto: DID, Timeline, Config, Peers, Jobs
    - **Coverage**: 100% (ActivityPub & ATProto endpoints)

15. **`openapi_federation_hardening.yaml`** - Federation Security ✅
    - Dead letter queue management
    - Instance/actor blocklists
    - Abuse reports
    - Dashboard and health metrics

16. **`openapi_plugins.yaml`** - Plugin System ✅
    - Plugin installation and management
    - Enable/disable plugins
    - Plugin configuration
    - Plugin statistics

17. **`openapi_redundancy.yaml`** - Video Redundancy ✅
    - Peer management
    - Redundancy policies
    - Synchronization

---

## Missing Documentation (TODO)

### MEDIUM Priority

1. **HLS Static Serving** (add to `openapi.yaml`)
   ```
   GET    /api/v1/hls/*
   ```
   - Static HLS segment delivery for VOD
   - Privacy gating for private videos
   - Cache headers and range request support

2. **Additional Messaging Endpoints** (add to `openapi.yaml`)
   ```
   GET    /api/v1/conversations/
   GET    /api/v1/conversations/unread-count
   DELETE /api/v1/messages/{messageId}
   ```

3. **ATProto DID Endpoint** (add to `openapi.yaml` or `openapi_federation.yaml`)
   ```
   GET    /.well-known/atproto-did
   ```

---

## Documentation Coverage Statistics

### Overall API Coverage: 99%+

| Category | Endpoints Implemented | Endpoints Documented | Coverage |
|----------|----------------------|---------------------|----------|
| Authentication | 8 | 8 | 100% ✅ |
| **Two-Factor Auth (2FA)** | **5** | **5** | **100% ✅** |
| OAuth 2.0 | 8 | 8 | 100% ✅ |
| Videos (Core) | 12 | 12 | 100% ✅ |
| **Uploads** | **10** | **10** | **100% ✅** |
| **Analytics** | **6** | **6** | **100% ✅** |
| **Live Streaming** | **25** | **25** | **100% ✅** |
| **Imports** | **4** | **4** | **100% ✅** |
| Comments | 7 | 7 | 100% ✅ |
| Channels | 8 | 8 | 100% ✅ |
| Captions | 5 | 5 | 100% ✅ |
| Ratings & Playlists | 10 | 10 | 100% ✅ |
| Notifications | 6 | 6 | 100% ✅ |
| Chat | 10 | 10 | 100% ✅ |
| Moderation | 12 | 12 | 100% ✅ |
| **Federation** | **25+** | **25+** | **100% ✅** |
| Federation Hardening | 12 | 12 | 100% ✅ |
| Plugins | 8 | 8 | 100% ✅ |
| Redundancy | 6 | 6 | 100% ✅ |
| **User Profiles** | **6** | **6** | **100% ✅** |
| HLS Static | 1 | 0 | 0% ⚠️ |
| **TOTAL** | **~200** | **~199** | **~99%** |

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
    {url: '/api/openapi_comments.yaml', name: 'Comments'}, \
    {url: '/api/openapi_channels.yaml', name: 'Channels'}, \
    {url: '/api/openapi_captions.yaml', name: 'Captions'}, \
    {url: '/api/openapi_ratings_playlists.yaml', name: 'Ratings & Playlists'}, \
    {url: '/api/openapi_notifications.yaml', name: 'Notifications'}, \
    {url: '/api/openapi_chat.yaml', name: 'Chat'}, \
    {url: '/api/openapi_moderation.yaml', name: 'Moderation'}, \
    {url: '/api/openapi_federation.yaml', name: 'Federation'}, \
    {url: '/api/openapi_federation_hardening.yaml', name: 'Federation Security'}, \
    {url: '/api/openapi_plugins.yaml', name: 'Plugins'}, \
    {url: '/api/openapi_redundancy.yaml', name: 'Redundancy'} \
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

# Validate all specs
for spec in api/openapi*.yaml; do
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

### 2025-01-26 - Federation Documentation Update

**Updated:**
- ✅ `openapi_federation.yaml` - Added ActivityPub endpoints (WebFinger, NodeInfo, Actor, Inbox, Outbox)
- ✅ Updated `api/README.md` to reflect complete Federation coverage

**Remaining Work:**
- Add HLS static serving endpoint
- Complete conversation/messaging endpoints

---

## Contributing

When adding new API endpoints:

1. Choose the appropriate OpenAPI file (or create new one for new domain)
2. Follow existing patterns for consistency
3. Include comprehensive examples
4. Document security requirements
5. Add rate limiting information if applicable
6. Update this README with new endpoints

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

Same license as the main Athena project.
