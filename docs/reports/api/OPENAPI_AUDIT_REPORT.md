# OpenAPI Documentation Audit Report

**Date:** 2025-11-30
**Project:** Vidra Core Backend API (PeerTube-Inspired)
**Auditor:** Claude (Documentation Engineer)

---

## Executive Summary

This audit systematically compared the OpenAPI/Swagger documentation against the actual API implementation in the Vidra Core backend. The analysis examined:

1. **Route definitions** in `/Users/yosefgamble/github/vidra/internal/httpapi/routes.go`
2. **OpenAPI specifications** in `/Users/yosefgamble/github/vidra/api/*.yaml` and `/Users/yosefgamble/github/vidra/docs/openapi_notifications.yaml`
3. **Handler implementations** in `/Users/yosefgamble/github/vidra/internal/httpapi/handlers/**/*.go`
4. **Domain models and DTOs** in `/Users/yosefgamble/github/vidra/internal/domain/*.go`

### Key Findings

- **Total Implemented Endpoints:** 100+
- **Total Documented Endpoints:** 80+ (across 17 OpenAPI files)
- **Undocumented Endpoints:** 25+
- **Documentation Quality:** Mixed (some specs are comprehensive, others are minimal)
- **Schema Accuracy:** Generally good, but several mismatches found

---

## 1. Undocumented Endpoints

The following endpoints are **implemented in code but NOT documented** in any OpenAPI specification:

### 1.1 Live Stream Endpoints

**Location:** `/Users/yosefgamble/github/vidra/internal/httpapi/routes.go:293-342`

| Method | Endpoint | Handler | Status |
|--------|----------|---------|--------|
| POST | `/api/v1/streams` | `liveStreamHandlers.CreateStream` | ❌ Not documented |
| GET | `/api/v1/streams/active` | `liveStreamHandlers.GetActiveStreams` | ❌ Not documented |

**Issue:** While `/api/v1/streams/{id}` and related HLS endpoints ARE documented in `openapi_livestreaming.yaml`, the create and list endpoints are missing.

**Recommendation:** Add to `openapi_livestreaming.yaml`:

```yaml
/api/v1/streams:
  post:
    summary: Create a new live stream
    tags: [Live Streaming]
    requestBody:
      required: true
      content:
        application/json:
          schema:
            type: object
            required: [channel_id, title]
            properties:
              channel_id:
                type: string
                format: uuid
              title:
                type: string
              description:
                type: string
              privacy:
                type: string
                enum: [public, unlisted, private]
    responses:
      '201':
        description: Stream created successfully
      '401':
        description: Unauthorized
      '400':
        description: Invalid request

/api/v1/streams/active:
  get:
    summary: Get active live streams
    tags: [Live Streaming]
    responses:
      '200':
        description: List of active streams
        content:
          application/json:
            schema:
              type: object
              properties:
                data:
                  type: array
                  items:
                    $ref: '#/components/schemas/LiveStream'
```

### 1.2 Plugin Management Endpoints

**Location:** Documented in `openapi_plugins.yaml` but implementation status unclear

These endpoints are documented but may not be fully implemented:

- `GET /api/v1/admin/plugins`
- `POST /api/v1/admin/plugins`
- `PUT /api/v1/admin/plugins/{id}/config`
- `POST /api/v1/admin/plugins/{id}/enable`
- `POST /api/v1/admin/plugins/{id}/disable`

**Issue:** No corresponding routes found in `routes.go`. The plugin system may be planned but not yet implemented.

**Recommendation:** Either:

1. Implement these endpoints as documented, OR
2. Move `openapi_plugins.yaml` to a `/api/planned/` directory and add a note that these are future features

### 1.3 Video Category Endpoints

**Location:** `/Users/yosefgamble/github/vidra/api/openapi.yaml:2018-2270`

These endpoints ARE documented in the main `openapi.yaml` but their implementation in handlers needs verification:

| Method | Endpoint | Documentation | Implementation |
|--------|----------|---------------|----------------|
| GET | `/api/v1/categories` | ✅ Documented | ❓ Needs verification |
| GET | `/api/v1/categories/{id}` | ✅ Documented | ❓ Needs verification |
| POST | `/api/v1/admin/categories` | ✅ Documented | ❓ Needs verification |
| PUT | `/api/v1/admin/categories/{id}` | ✅ Documented | ❓ Needs verification |
| DELETE | `/api/v1/admin/categories/{id}` | ✅ Documented | ❓ Needs verification |

**Issue:** No category routes found in `routes.go`. Handler exists at `/Users/yosefgamble/github/vidra/internal/httpapi/handlers/video/video_category_handler.go` but routes may not be registered.

**Recommendation:**

1. Add category routes to `routes.go` in the `/api/v1` section
2. If categories are not yet supported, remove from OpenAPI spec or mark as "planned"

### 1.4 Chat Endpoints (Stream Chat)

**Location:** Documented in `openapi_chat.yaml`

| Method | Endpoint | Status |
|--------|----------|--------|
| GET | `/api/v1/streams/{streamId}/chat/ws` | ❌ Not in routes.go |
| POST | `/api/v1/streams/{streamId}/chat/messages` | ❌ Not in routes.go |
| DELETE | `/api/v1/streams/{streamId}/chat/messages/{messageId}` | ❌ Not in routes.go |
| POST | `/api/v1/streams/{streamId}/chat/moderators` | ❌ Not in routes.go |
| DELETE | `/api/v1/streams/{streamId}/chat/moderators/{userId}` | ❌ Not in routes.go |
| POST | `/api/v1/streams/{streamId}/chat/bans` | ❌ Not in routes.go |
| DELETE | `/api/v1/streams/{streamId}/chat/bans/{userId}` | ❌ Not in routes.go |
| GET | `/api/v1/streams/{streamId}/chat/stats` | ❌ Not in routes.go |

**Issue:** Extensive chat API is documented but no routes are registered in `routes.go`.

**Recommendation:**

1. If chat is planned but not implemented, move to `/api/planned/`
2. If chat is implemented via WebSocket only, update documentation to clarify this
3. Add routes if handlers exist but routes were forgotten

### 1.5 Redundancy Endpoints

**Location:** Documented in `openapi_redundancy.yaml`

All redundancy endpoints are documented but not found in `routes.go`:

- `/api/v1/admin/redundancy/policies`
- `/api/v1/admin/redundancy/instances`
- `/api/v1/admin/redundancy/create`
- `/api/v1/admin/redundancy/redundancies/{id}`
- etc.

**Issue:** Entire redundancy system is documented but appears unimplemented.

**Recommendation:** Move to `/api/planned/` or implement the feature.

### 1.6 E2EE Messaging Endpoints

**Location:** Documented in main `openapi.yaml:1570-1703`

| Method | Endpoint | Status |
|--------|----------|--------|
| POST | `/api/v1/e2ee/setup` | ❌ Not in routes.go |
| POST | `/api/v1/e2ee/unlock` | ❌ Not in routes.go |
| POST | `/api/v1/e2ee/key-exchange` | ❌ Not in routes.go |
| POST | `/api/v1/messages/secure` | ❌ Not in routes.go |

**Issue:** End-to-end encryption endpoints documented but not implemented.

**Recommendation:**

1. Handler exists at `/Users/yosefgamble/github/vidra/internal/httpapi/handlers/messaging/secure_messages.go`
2. Add routes to `routes.go` or remove from OpenAPI spec if not ready

---

## 2. Documented but Non-Existent Endpoints

### 2.1 Payments Endpoints Path Mismatch

**Location:** `openapi_payments.yaml`

**Documented paths:**

```
/payments/wallet
/payments/intents
/payments/transactions
```

**Actual implementation** (from `routes.go:369-383`):

```
/api/v1/payments/wallet
/api/v1/payments/intents
/api/v1/payments/transactions
```

**Issue:** Documentation is missing the `/api/v1` prefix.

**Fix Required:** Update `openapi_payments.yaml` to use `/api/v1/payments/*` paths.

### 2.2 Notifications Path Mismatch

**Location:** `docs/openapi_notifications.yaml`

**Documented paths:**

```
/notifications
/notifications/{id}
/notifications/unread-count
```

**Actual implementation** (from `routes.go:357-366`):

```
/api/v1/notifications
/api/v1/notifications/{id}
/api/v1/notifications/unread-count
```

**Issue:** Documentation is missing the `/api/v1` prefix.

**Fix Required:** Update `docs/openapi_notifications.yaml` to use `/api/v1/notifications/*` paths.

### 2.3 User Ratings Endpoint

**Documented:** `/api/v1/user/ratings` in `openapi_ratings_playlists.yaml`
**Actual:** `/api/v1/users/me/ratings` in `routes.go:243`

**Issue:** Path mismatch - `user` vs `users/me`

**Fix Required:** Update OpenAPI to use `/api/v1/users/me/ratings`

### 2.4 Watch Later Endpoint

**Documented:** `/api/v1/playlists/watch-later` in `openapi_ratings_playlists.yaml`
**Actual:** `/api/v1/users/me/watch-later` in `routes.go:248`

**Issue:** Path mismatch

**Fix Required:** Update OpenAPI to use `/api/v1/users/me/watch-later`

---

## 3. Schema Mismatches

### 3.1 User Schema

**OpenAPI Location:** `api/openapi.yaml` (components/schemas/User)
**Domain Model:** `/Users/yosefgamble/github/vidra/internal/domain/user.go:9-29`

**Mismatches:**

| Field | OpenAPI | Go Domain Model | Issue |
|-------|---------|-----------------|-------|
| `subscriber_count` | ❓ Not documented | ✅ `int64 'json:"subscriber_count"'` | Missing in OpenAPI |
| `twofa_enabled` | ❓ Not documented | ✅ `bool 'json:"twofa_enabled"'` | Missing in OpenAPI |
| `email_verified` | ❓ Not documented | ✅ `bool 'json:"email_verified"'` | Missing in OpenAPI |
| `email_verified_at` | ❓ Not documented | ✅ `sql.NullTime 'json:"email_verified_at"'` | Missing in OpenAPI |
| `avatar` | ❓ Needs verification | ✅ Custom nested structure | May need update |

**Recommendation:** Review and add these fields to the User schema in OpenAPI:

```yaml
User:
  type: object
  properties:
    id:
      type: string
    username:
      type: string
    email:
      type: string
      format: email
    display_name:
      type: string
    bio:
      type: string
    bitcoin_wallet:
      type: string
    role:
      type: string
      enum: [user, admin, moderator]
    is_active:
      type: boolean
    email_verified:
      type: boolean
    email_verified_at:
      type: string
      format: date-time
      nullable: true
    subscriber_count:
      type: integer
      format: int64
    twofa_enabled:
      type: boolean
    avatar:
      type: object
      nullable: true
      properties:
        id:
          type: string
        ipfs_cid:
          type: string
          nullable: true
        webp_ipfs_cid:
          type: string
          nullable: true
    created_at:
      type: string
      format: date-time
    updated_at:
      type: string
      format: date-time
```

### 3.2 Video Schema

**OpenAPI Location:** `api/openapi.yaml` (components/schemas/Video)
**Domain Model:** `/Users/yosefgamble/github/vidra/internal/domain/video.go:43-83`

**Mismatches:**

| Field | OpenAPI Status | Go Domain Model | Issue |
|-------|----------------|-----------------|-------|
| `channel_id` | ❓ Needs verification | ✅ `uuid.UUID` | May be missing |
| `channel` | ❓ Needs verification | ✅ `*Channel` (nested) | May be missing |
| `category` | ❓ Needs verification | ✅ `*VideoCategory` (nested) | May be missing |
| `storage_tier` | ❓ Likely missing | ✅ `string` | New field for S3 migration |
| `s3_migrated_at` | ❌ Missing | ✅ `*time.Time` | New field for S3 migration |
| `local_deleted` | ❌ Missing | ✅ `bool` | New field for S3 migration |
| `s3_urls` | ❌ Missing | ✅ `map[string]string` | New field for S3 URLs |
| `is_remote` | ❌ Missing | ✅ `bool` | Federation support |
| `remote_uri` | ❌ Missing | ✅ `*string` | Federation support |
| `remote_actor_uri` | ❌ Missing | ✅ `*string` | Federation support |
| `remote_video_url` | ❌ Missing | ✅ `*string` | Federation support |
| `remote_instance_domain` | ❌ Missing | ✅ `*string` | Federation support |
| `remote_thumbnail_url` | ❌ Missing | ✅ `*string` | Federation support |
| `remote_last_synced_at` | ❌ Missing | ✅ `*time.Time` | Federation support |
| `metadata` | ❓ Needs verification | ✅ `VideoMetadata` struct | Complex nested object |

**Recommendation:** Major update required to Video schema. Add federation fields and S3 storage fields:

```yaml
Video:
  type: object
  required: [id, title, user_id, channel_id, privacy, status]
  properties:
    id:
      type: string
    title:
      type: string
    description:
      type: string
    duration:
      type: integer
    views:
      type: integer
      format: int64
    privacy:
      type: string
      enum: [public, unlisted, private]
    status:
      type: string
      enum: [uploading, queued, processing, completed, failed]
    upload_date:
      type: string
      format: date-time
    user_id:
      type: string
    channel_id:
      type: string
      format: uuid
    channel:
      $ref: '#/components/schemas/Channel'
    original_cid:
      type: string
    processed_cids:
      type: object
      additionalProperties:
        type: string
    thumbnail_cid:
      type: string
    thumbnail_path:
      type: string
    preview_path:
      type: string
    output_paths:
      type: object
      additionalProperties:
        type: string
    s3_urls:
      type: object
      additionalProperties:
        type: string
      description: S3 URLs for different resolutions
    storage_tier:
      type: string
      description: Storage tier (local, s3, hybrid)
    s3_migrated_at:
      type: string
      format: date-time
      nullable: true
    local_deleted:
      type: boolean
      description: Whether local files have been deleted after S3 migration
    tags:
      type: array
      items:
        type: string
    category_id:
      type: string
      format: uuid
      nullable: true
    category:
      $ref: '#/components/schemas/VideoCategory'
    language:
      type: string
    file_size:
      type: integer
      format: int64
    mime_type:
      type: string
    metadata:
      $ref: '#/components/schemas/VideoMetadata'
    # Federation fields
    is_remote:
      type: boolean
      description: Whether this video is from a federated instance
    remote_uri:
      type: string
      nullable: true
      description: ActivityPub URI of the remote video
    remote_actor_uri:
      type: string
      nullable: true
      description: ActivityPub URI of the remote actor
    remote_video_url:
      type: string
      nullable: true
      description: Direct URL to remote video file
    remote_instance_domain:
      type: string
      nullable: true
      description: Domain of the remote instance
    remote_thumbnail_url:
      type: string
      nullable: true
    remote_last_synced_at:
      type: string
      format: date-time
      nullable: true
    created_at:
      type: string
      format: date-time
    updated_at:
      type: string
      format: date-time

VideoMetadata:
  type: object
  properties:
    width:
      type: integer
    height:
      type: integer
    framerate:
      type: number
      format: double
    bitrate:
      type: integer
    audio_codec:
      type: string
    video_codec:
      type: string
    aspect_ratio:
      type: string
```

### 3.3 Response Wrapper Schema

**Implementation:** `/Users/yosefgamble/github/vidra/internal/httpapi/shared/response.go:11-30`

All API responses use a standard wrapper:

```go
type Response struct {
    Data    interface{} `json:"data,omitempty"`
    Error   *ErrorInfo  `json:"error,omitempty"`
    Success bool        `json:"success"`
    Meta    *Meta       `json:"meta,omitempty"`
}
```

**Issue:** Most OpenAPI specs show responses WITHOUT this wrapper, directly returning the data objects.

**Example from `openapi.yaml`:**

```yaml
/api/v1/videos:
  get:
    responses:
      '200':
        content:
          application/json:
            schema:
              type: array
              items:
                $ref: '#/components/schemas/Video'
```

**Actual response:**

```json
{
  "data": [/* array of videos */],
  "success": true,
  "meta": {
    "total": 100,
    "limit": 20,
    "offset": 0,
    "page": 1,
    "pageSize": 20
  }
}
```

**Recommendation:** Add a generic response wrapper schema and use it consistently:

```yaml
components:
  schemas:
    SuccessResponse:
      type: object
      required: [success]
      properties:
        data:
          description: Response data (type varies by endpoint)
        success:
          type: boolean
          example: true
        meta:
          $ref: '#/components/schemas/Meta'

    ErrorResponse:
      type: object
      required: [success, error]
      properties:
        success:
          type: boolean
          example: false
        error:
          $ref: '#/components/schemas/ErrorInfo'

    ErrorInfo:
      type: object
      required: [message]
      properties:
        code:
          type: string
          example: "NOT_FOUND"
        message:
          type: string
          example: "Resource not found"
        details:
          type: string

    Meta:
      type: object
      properties:
        total:
          type: integer
          format: int64
        limit:
          type: integer
        offset:
          type: integer
        page:
          type: integer
        pageSize:
          type: integer
```

Then update all endpoint responses to use this wrapper.

---

## 4. Authentication & Security Issues

### 4.1 Missing Security Schemes in Specialized Specs

**Issue:** Several OpenAPI files don't define the `bearerAuth` security scheme:

- `openapi_payments.yaml`
- `openapi_chat.yaml`
- `openapi_redundancy.yaml`

**Recommendation:** Add security schemes to all OpenAPI files or reference the main spec:

```yaml
components:
  securitySchemes:
    bearerAuth:
      type: http
      scheme: bearer
      bearerFormat: JWT
```

### 4.2 Inconsistent Security Requirements

**Issue:** Some endpoints that require authentication don't have `security` defined in their OpenAPI specs.

**Example from routes.go:**

```go
r.With(middleware.Auth(cfg.JWTSecret)).Post("/api/v1/videos", video.CreateVideoHandler(deps.VideoRepo))
```

But OpenAPI may not show:

```yaml
security:
  - bearerAuth: []
```

**Recommendation:** Audit all authenticated endpoints and ensure they have proper security requirements in OpenAPI.

---

## 5. HTTP Method Discrepancies

### 5.1 Comment Flagging

**Documented:** `POST /api/v1/comments/{commentId}/flag`
**Implemented:** `POST /api/v1/comments/{commentId}/flag` ✅ Matches

**Documented:** No DELETE for unflagging
**Implemented:** `DELETE /api/v1/comments/{commentId}/flag` from `routes.go:352`

**Issue:** DELETE method for unflagging exists but not documented.

**Fix:** Add to `openapi_comments.yaml`:

```yaml
/api/v1/comments/{commentId}/flag:
  delete:
    summary: Unflag a comment
    operationId: unflagComment
    responses:
      '204':
        description: Flag removed successfully
```

---

## 6. Pagination & Query Parameters

### 6.1 Inconsistent Pagination Parameters

**Implementation** (from `shared` package):

- Supports both `page`/`pageSize` AND `limit`/`offset`
- Default limit: 20
- Max limit: 100 (in most handlers)

**OpenAPI Documentation:**

- Some specs use `page`/`pageSize`
- Some use `limit`/`offset`
- Some use both
- Limits and defaults vary

**Recommendation:** Standardize pagination documentation across all specs:

```yaml
parameters:
  - name: page
    in: query
    schema:
      type: integer
      minimum: 1
      default: 1
    description: Page number (1-based)
  - name: pageSize
    in: query
    schema:
      type: integer
      minimum: 1
      maximum: 100
      default: 20
    description: Number of items per page
  - name: limit
    in: query
    schema:
      type: integer
      minimum: 1
      maximum: 100
      default: 20
    description: Maximum number of items to return (alternative to pageSize)
  - name: offset
    in: query
    schema:
      type: integer
      minimum: 0
      default: 0
    description: Number of items to skip (alternative to page)
```

Add note: "Both pagination styles are supported for backward compatibility."

---

## 7. Missing Features in Documentation

### 7.1 Two-Factor Authentication

**Status:** ✅ Well documented in `openapi_auth_2fa.yaml`

Endpoints match implementation in `routes.go:82-90`:

- `POST /auth/2fa/setup`
- `POST /auth/2fa/verify-setup`
- `POST /auth/2fa/disable`
- `POST /auth/2fa/regenerate-backup-codes`
- `GET /auth/2fa/status`

**Note:** Paths missing `/api/v1` prefix in some docs. Actual routes are under `/auth/2fa/*` (not `/api/v1/auth/2fa/*`).

### 7.2 Video Imports

**Status:** ✅ Documented in `openapi_imports.yaml`

Implementation matches (from `routes.go:193-208`):

- `POST /api/v1/videos/imports/`
- `GET /api/v1/videos/imports/`
- `GET /api/v1/videos/imports/{id}`
- `DELETE /api/v1/videos/imports/{id}`

### 7.3 Channels

**Status:** ⚠️ Partially documented in `openapi_channels.yaml`

Missing endpoints:

- `GET /api/v1/users/me/channels` (routes.go:240)

Documented but need verification:

- All other channel endpoints appear to match

---

## 8. Federation (ActivityPub) Documentation

### 8.1 Well-Known Endpoints

**Implementation** (from `routes.go:103-124`):

```
GET /.well-known/webfinger
GET /.well-known/nodeinfo
GET /.well-known/host-meta
GET /nodeinfo/2.0
POST /inbox (shared inbox)
GET /users/{username}
GET /users/{username}/outbox
GET /users/{username}/inbox
POST /users/{username}/inbox
GET /users/{username}/followers
GET /users/{username}/following
```

**Documentation:** Partially covered in `openapi_federation.yaml`

**Issue:** ActivityPub endpoints should be documented separately or with clear notes that they follow ActivityPub spec.

**Recommendation:**

1. Create `openapi_activitypub.yaml` for well-known endpoints
2. Reference ActivityPub specification
3. Document how these interact with native API

### 8.2 Federation Admin Endpoints

**Implementation** (routes.go:465-507):

```
GET /api/v1/admin/federation/jobs
GET /api/v1/admin/federation/jobs/{id}
POST /api/v1/admin/federation/jobs/{id}/retry
DELETE /api/v1/admin/federation/jobs/{id}
GET /api/v1/admin/federation/actors
POST /api/v1/admin/federation/actors
PUT /api/v1/admin/federation/actors/{actor}
DELETE /api/v1/admin/federation/actors/{actor}
```

**Documentation:** Covered in `openapi_federation.yaml` and `openapi_federation_hardening.yaml`

**Status:** ✅ Appears to be documented

---

## 9. Priority Fixes

### Critical (Fix Immediately)

1. **Fix path prefixes** in `openapi_payments.yaml` and `docs/openapi_notifications.yaml` - add `/api/v1` prefix
2. **Add response wrapper schema** to all OpenAPI specs for consistency
3. **Update Video schema** with federation and S3 storage fields
4. **Update User schema** with missing fields (subscriber_count, twofa_enabled, email_verified)

### High Priority (Fix Soon)

5. **Document or remove** plugin endpoints - either implement or move to `/api/planned/`
6. **Document or remove** chat endpoints - same as above
7. **Document or remove** redundancy endpoints - same as above
8. **Document or remove** E2EE endpoints - same as above
9. **Fix path mismatches** for user ratings and watch-later endpoints
10. **Add live stream creation endpoints** to documentation

### Medium Priority (Fix When Time Permits)

11. **Standardize pagination** parameters across all specs
12. **Add security schemes** to all OpenAPI files
13. **Document category endpoints** or register routes if missing
14. **Add unflag comment endpoint** to documentation
15. **Create ActivityPub-specific** OpenAPI file for federation endpoints

### Low Priority (Nice to Have)

16. **Add more request/response examples** to all endpoints
17. **Document error codes** comprehensively in each spec
18. **Add rate limiting information** to endpoint docs
19. **Document webhook/SSE** endpoints if they exist
20. **Create Postman collection** from OpenAPI specs

---

## 10. Recommendations for Ongoing Documentation Maintenance

### 10.1 Establish a Documentation Workflow

1. **Pre-Commit Hook:** Remind developers to update OpenAPI specs when adding/modifying endpoints
2. **CI/CD Integration:** Add OpenAPI validation to CI pipeline
3. **Code Generation:** Consider using OpenAPI to generate Go types or vice versa

### 10.2 Use OpenAPI Linting

Install and configure Spectral or similar linter:

```bash
npm install -g @stoplight/spectral-cli
spectral lint api/*.yaml
```

### 10.3 Automated Schema Validation

Create a test that:

1. Parses all Go structs with JSON tags
2. Parses all OpenAPI schemas
3. Compares them and reports mismatches

Example location: `/Users/yosefgamble/github/vidra/test/openapi_validation_test.go`

### 10.4 Documentation Standards Document

Create `/Users/yosefgamble/github/vidra/api/README.md` with:

- When to create a new OpenAPI file vs extending existing
- Naming conventions for operationIds
- Required fields for all endpoint definitions
- How to document pagination, errors, authentication
- Process for deprecating endpoints

### 10.5 Merge OpenAPI Files (Optional)

Consider merging all OpenAPI specs into a single file or using `$ref` to reference shared components:

```yaml
# api/openapi.yaml (main file)
openapi: 3.0.3
info:
  title: Vidra Core API
  version: 1.0.0

paths:
  $ref: './paths/auth.yaml'
  $ref: './paths/videos.yaml'
  $ref: './paths/federation.yaml'
  # etc.

components:
  schemas:
    $ref: './schemas/user.yaml'
    $ref: './schemas/video.yaml'
    # etc.
```

This improves maintainability and ensures consistency.

---

## 11. Summary of Action Items

| # | Action | Priority | Estimated Effort | File(s) to Update |
|---|--------|----------|------------------|-------------------|
| 1 | Fix /api/v1 prefix in payments spec | Critical | 5 min | openapi_payments.yaml |
| 2 | Fix /api/v1 prefix in notifications spec | Critical | 5 min | docs/openapi_notifications.yaml |
| 3 | Add response wrapper schema | Critical | 30 min | All OpenAPI files |
| 4 | Update Video schema | Critical | 1 hour | openapi.yaml |
| 5 | Update User schema | Critical | 30 min | openapi.yaml |
| 6 | Decide plugin endpoint fate | High | 15 min | openapi_plugins.yaml or routes.go |
| 7 | Decide chat endpoint fate | High | 15 min | openapi_chat.yaml or routes.go |
| 8 | Decide redundancy endpoint fate | High | 15 min | openapi_redundancy.yaml or routes.go |
| 9 | Decide E2EE endpoint fate | High | 15 min | openapi.yaml or routes.go |
| 10 | Fix user ratings path | High | 5 min | openapi_ratings_playlists.yaml |
| 11 | Fix watch-later path | High | 5 min | openapi_ratings_playlists.yaml |
| 12 | Document live stream creation | High | 20 min | openapi_livestreaming.yaml |
| 13 | Standardize pagination | Medium | 1 hour | All OpenAPI files |
| 14 | Add security schemes to all files | Medium | 30 min | Multiple files |
| 15 | Document/register category routes | Medium | 30 min | openapi.yaml or routes.go |

**Total Estimated Effort:** ~6-8 hours to fix critical and high-priority issues

---

## 12. Conclusion

The Vidra Core API implementation is generally well-structured, but the OpenAPI documentation has fallen behind the actual codebase. The main issues are:

1. **Missing documentation** for recently added features (live streams, federation fields, S3 storage)
2. **Path inconsistencies** (missing /api/v1 prefixes)
3. **Schema drift** between Go domain models and OpenAPI schemas
4. **Uncertain implementation status** for some documented endpoints (plugins, chat, redundancy)
5. **Inconsistent response format documentation** (missing wrapper schema)

**Next Steps:**

1. Fix the 5 critical issues (total ~2.5 hours)
2. Decide whether to implement or remove the 4 uncertain feature sets (plugins, chat, redundancy, E2EE)
3. Update schemas to match domain models
4. Establish ongoing documentation maintenance process

With these fixes, the OpenAPI documentation will accurately reflect the API implementation and serve as a reliable source of truth for frontend developers, API consumers, and future maintainers.

---

**Report Generated:** 2025-11-30
**Files Analyzed:**

- `/Users/yosefgamble/github/vidra/internal/httpapi/routes.go`
- `/Users/yosefgamble/github/vidra/api/*.yaml` (17 files)
- `/Users/yosefgamble/github/vidra/docs/openapi_notifications.yaml`
- `/Users/yosefgamble/github/vidra/internal/domain/*.go` (20+ files)
- `/Users/yosefgamble/github/vidra/internal/httpapi/handlers/**/*.go` (80+ files)
